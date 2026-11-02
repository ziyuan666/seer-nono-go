// Package vec 把 FFDec 导出的 SWF shape SVG 光栅化成 image.RGBA。
//
// 为什么不用 GDI+：
//   - 桌面宠全流程都在 Go 侧合成 image.RGBA 再 blit 到分层窗口，纯 Go 光栅化可以
//     直接产出同一格式，不引入 cgo、不依赖 GDI+ 的运行时行为；
//   - 可离线（Linux）跑单元测试，把渲染结果和 cairosvg / FFDec 官方图做像素比对。
//
// 解析范围（只覆盖 FFDec 63+ 的 shape 导出格式，不做通用 SVG 支持）：
//   - 元素：svg / defs / g / path / linearGradient / radialGradient / stop
//   - 属性：transform=matrix()|translate()|scale()，path d（M L Q C Z 及相对形式），
//     fill / fill-rule / fill-opacity / stroke / stroke-width / stop-color / stop-opacity
//   - 不支持：mask、filter、text（shape 导出里本来就没有）
package vec

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

// Mat 是 2D 仿射矩阵 [a b c d e f]，与 SVG matrix(a,b,c,d,e,f) 一致。
type Mat struct{ A, B, C, D, E, F float64 }

// Mul 返回 m∘n（先应用 n，再应用 m）。
func (m Mat) Mul(n Mat) Mat {
	return Mat{
		A: m.A*n.A + m.C*n.B,
		B: m.B*n.A + m.D*n.B,
		C: m.A*n.C + m.C*n.D,
		D: m.B*n.C + m.D*n.D,
		E: m.A*n.E + m.C*n.F + m.E,
		F: m.B*n.E + m.D*n.F + m.F,
	}
}

// Pt 把点 (x,y) 变换到 m 之后的坐标。
func (m Mat) Pt(x, y float64) (float64, float64) {
	return m.A*x + m.C*y + m.E, m.B*x + m.D*y + m.F
}

// Det 返回行列式，用于判断退化矩阵。
func (m Mat) Det() float64 { return m.A*m.D - m.B*m.C }

// Stop 是渐变的一个色标。
type Stop struct {
	Offset float64
	R, G, B, A float64 // 0..1
}

// Grad 描述线性/径向渐变。
//
// 为什么存「文档→渐变空间」的逆矩阵：FFDec 的 radial 渐变几乎都带非均匀
// gradientTransform（例如 matrix(0.15,0,0,0.2812,...)），它是椭圆而不是圆；
// 只有把像素点用逆矩阵映射回渐变空间再算 t，才能得到正确的椭圆渐变。
// rawGrad 是 SVG 里渐变元素的原始参数（渐变自身空间）。
type rawGrad struct {
	Radial         bool
	X0, Y0         float64 // 线性起点 / 径向圆心
	X1, Y1         float64 // 线性终点
	R              float64 // 径向半径
	FX, FY         float64 // 径向焦点（缺省等于圆心）
	Stops          []Stop
}

type Grad struct {
	Radial bool
	Inv    Mat     // 文档坐标 → 渐变空间坐标
	CX, CY float64 // 线性起点 / 径向圆心（渐变空间）
	FX, FY float64 // 径向焦点（渐变空间）
	DX, DY float64 // 线性方向向量（渐变空间）
	R      float64 // 径向半径（渐变空间）
	Stops  []Stop
}

// Paint 是纯色或渐变。
type Paint struct {
	Solid    bool
	R, G, B, A float64
	Grad     *Grad
}

// Shape 是一个已变换到文档坐标的填充/描边单元。
type Shape struct {
	Polys   [][]Pt // 轮廓（可能是多个子路径）
	EvenOdd bool
	Fill    *Paint
	// Stroke 若非 0，则把折线按该宽度扩成四边形，用 StrokePaint 填充（nonzero）。
	StrokeW     float64
	StrokePaint *Paint
}

// Pt 是文档坐标点。
type Pt struct{ X, Y float64 }

// Doc 是解析后的矢量文档。
type Doc struct {
	VBx, VBy, VBw, VBh float64
	Shapes             []Shape
}

// ---------------------------------------------------------------- XML 解析

type xmlNode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Nodes   []xmlNode  `xml:",any"`
}

var stopCache = map[string][]Stop{}

// ParseFile 读取并解析 SVG 文件。
func ParseFile(path string) (*Doc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse 从 reader 解析 SVG。
func Parse(r io.Reader) (*Doc, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	var root xmlNode
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	if root.XMLName.Local != "svg" {
		return nil, fmt.Errorf("vec: 根节点不是 svg 而是 %q", root.XMLName.Local)
	}
	d := &Doc{VBw: 100, VBh: 100}
	if vb := attr(root, "viewBox"); vb != "" {
		f := nums(vb)
		if len(f) == 4 && f[2] > 0 && f[3] > 0 {
			d.VBx, d.VBy, d.VBw, d.VBh = f[0], f[1], f[2], f[3]
		}
	}
	// 收集 defs 里的渐变（id → rawGrad，坐标仍是渐变自身空间，使用处再变换）
	grads := map[string]*rawGrad{}
	gradMats := map[string]Mat{}
	var collect func(n xmlNode)
	collect = func(n xmlNode) {
		switch n.XMLName.Local {
		case "linearGradient", "radialGradient":
			id := attr(n, "id")
			if id == "" {
				return
			}
			g := &rawGrad{Radial: n.XMLName.Local == "radialGradient"}
			g.X0 = numDefault(attr(n, "x1"), 0)
			g.Y0 = numDefault(attr(n, "y1"), 0)
			g.X1 = numDefault(attr(n, "x2"), 1)
			g.Y1 = numDefault(attr(n, "y2"), 0)
			if g.Radial {
				g.X0 = numDefault(attr(n, "cx"), 0)
				g.Y0 = numDefault(attr(n, "cy"), 0)
				g.R = numDefault(attr(n, "r"), 1)
				// fx/fy 是偏心焦点：FFDec 导出的径向渐变普遍带
				// fx="-22.4"，只读 cx/cy 会让整条色带位置平移
				// （实测面板环色差最大 84/255）。
				g.FX = numDefault(attr(n, "fx"), g.X0)
				g.FY = numDefault(attr(n, "fy"), g.Y0)
			}
			for _, c := range n.Nodes {
				if c.XMLName.Local != "stop" {
					continue
				}
				s := Stop{Offset: numDefault(attr(c, "offset"), 0)}
				s.R, s.G, s.B, s.A = colorOf(attr(c, "stop-color"))
				s.A *= numDefault(attr(c, "stop-opacity"), 1)
				g.Stops = append(g.Stops, s)
			}
			grads[id] = g
			if gt := attr(n, "gradientTransform"); gt != "" {
				gradMats[id] = parseTransform(gt)
			}
		}
		for _, c := range n.Nodes {
			collect(c)
		}
	}
	for _, n := range root.Nodes {
		collect(n)
	}

	// 遍历可见节点，累积矩阵
	var walk func(n xmlNode, m Mat)
	walk = func(n xmlNode, m Mat) {
		switch n.XMLName.Local {
		case "g", "svg", "defs":
			mm := m
			if t := attr(n, "transform"); t != "" {
				mm = m.Mul(parseTransform(t))
			}
			if n.XMLName.Local == "defs" {
				return // defs 内容不渲染
			}
			for _, c := range n.Nodes {
				walk(c, mm)
			}
		case "path":
			if s, ok := buildShape(n, m, grads, gradMats); ok {
				d.Shapes = append(d.Shapes, s)
			}
		}
	}
	for _, n := range root.Nodes {
		walk(n, Mat{1, 0, 0, 1, 0, 0})
	}
	return d, nil
}

func buildShape(n xmlNode, m Mat, grads map[string]*rawGrad, gradMats map[string]Mat) (Shape, bool) {
	var s Shape
	dAttr := attr(n, "d")
	if dAttr == "" {
		return s, false
	}
	subs, ok := parsePath(dAttr)
	if !ok {
		return s, false
	}
	// 应用到文档坐标
	for _, sub := range subs {
		out := make([]Pt, 0, len(sub))
		for _, p := range sub {
			x, y := m.Pt(p.X, p.Y)
			out = append(out, Pt{x, y})
		}
		if len(out) >= 2 {
			s.Polys = append(s.Polys, out)
		}
	}
	if len(s.Polys) == 0 {
		return s, false
	}
	s.EvenOdd = strings.Contains(attr(n, "fill-rule"), "evenodd")

	fillAttr := attr(n, "fill")
	switch {
	case fillAttr == "" || fillAttr == "none":
		// 无填充
	case strings.HasPrefix(fillAttr, "url(#"):
		id := strings.TrimSuffix(strings.TrimPrefix(fillAttr, "url(#"), ")")
		if g, ok := grads[id]; ok {
			s.Fill = &Paint{Grad: makeGrad(g, m, gradMats[id])}
		} else {
			s.Fill = &Paint{Solid: true, A: 1}
		}
	default:
		r, g, b, a := colorOf(fillAttr)
		a *= numDefault(attr(n, "fill-opacity"), 1)
		if a > 0 {
			s.Fill = &Paint{Solid: true, R: r, G: g, B: b, A: a}
		}
	}

	st := attr(n, "stroke")
	if st != "" && st != "none" {
		w := numDefault(attr(n, "stroke-width"), 1)
		if w > 0 {
			s.StrokeW = w
			if strings.HasPrefix(st, "url(#") {
				id := strings.TrimSuffix(strings.TrimPrefix(st, "url(#"), ")")
				if g, ok := grads[id]; ok {
					s.StrokePaint = &Paint{Grad: makeGrad(g, m, gradMats[id])}
				}
			}
			if s.StrokePaint == nil {
				r, g, b, a := colorOf(st)
				a *= numDefault(attr(n, "stroke-opacity"), 1)
				s.StrokePaint = &Paint{Solid: true, R: r, G: g, B: b, A: a}
			}
		}
	}
	return s, len(s.Polys) > 0
}

func attr(n xmlNode, name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func nums(s string) []float64 {
	f := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\n' || r == '\t' || r == '\r'
	})
	out := make([]float64, 0, len(f))
	for _, v := range f {
		x, err := strconv.ParseFloat(v, 64)
		if err == nil {
			out = append(out, x)
		}
	}
	return out
}

func numDefault(s string, def float64) float64 {
	if s == "" {
		return def
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return v
	}
	return def
}

// colorOf 支持 #rgb / #rrggbb / none / 常见命名色。
func colorOf(s string) (r, g, b, a float64) {
	a = 1
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "none" || s == "transparent" {
		return 0, 0, 0, 0
	}
	if strings.HasPrefix(s, "#") {
		h := s[1:]
		switch len(h) {
		case 3:
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		case 8: // #rrggbbaa
			a = hex2(h[6:]) / 255
			h = h[:6]
		case 6:
		default:
			return 0, 0, 0, 0
		}
		if len(h) == 6 {
			return hex2(h[0:2]) / 255, hex2(h[2:4]) / 255, hex2(h[4:6]) / 255, a
		}
		return 0, 0, 0, 0
	}
	if c, ok := namedColors[s]; ok {
		return c[0] / 255, c[1] / 255, c[2] / 255, 1
	}
	return 0, 0, 0, 0
}

func hex2(s string) float64 {
	v, err := strconv.ParseUint(s, 16, 8)
	if err != nil {
		return 0
	}
	return float64(v)
}

var namedColors = map[string][3]float64{
	"white": {255, 255, 255}, "black": {0, 0, 0}, "red": {255, 0, 0},
	"green": {0, 128, 0}, "blue": {0, 0, 255}, "gray": {128, 128, 128},
	"grey": {128, 128, 128}, "silver": {192, 192, 192}, "none": {0, 0, 0},
}

// parseTransform 解析 matrix / translate / scale / rotate 组合。
func parseTransform(s string) Mat {
	m := Mat{1, 0, 0, 1, 0, 0}
	s = strings.TrimSpace(s)
	for len(s) > 0 {
		i := strings.IndexByte(s, '(')
		if i < 0 {
			break
		}
		name := strings.TrimSpace(s[:i])
		j := strings.IndexByte(s[i:], ')')
		if j < 0 {
			break
		}
		args := nums(s[i+1 : i+j])
		s = strings.TrimSpace(s[i+j+1:])
		var t Mat
		switch name {
		case "matrix":
			if len(args) >= 6 {
				t = Mat{args[0], args[1], args[2], args[3], args[4], args[5]}
			} else {
				continue
			}
		case "translate":
			t = Mat{1, 0, 0, 1, args[0], 0}
			if len(args) > 1 {
				t.F = args[1]
			}
		case "scale":
			t = Mat{args[0], 0, 0, args[0], 0, 0}
			if len(args) > 1 {
				t.D = args[1]
			}
		case "rotate":
			r := args[0] * math.Pi / 180
			cos, sin := math.Cos(r), math.Sin(r)
			t = Mat{cos, sin, -sin, cos, 0, 0}
			if len(args) >= 3 {
				// rotate(a, cx, cy)
				t = Mat{1, 0, 0, 1, args[1], args[2]}.Mul(t).Mul(Mat{1, 0, 0, 1, -args[1], -args[2]})
			}
		default:
			continue
		}
		m = m.Mul(t)
	}
	return m
}

// ---------------------------------------------------------------- path 解析

// parsePath 把 d 拆成若干条折线（曲线已按固定容差细分）。
func parsePath(d string) ([][]Pt, bool) {
	var subs [][]Pt
	var cur []Pt
	var cx, cy float64
	var sx, sy float64 // 子路径起点
	cmd := byte(0)
	i := 0
	n := len(d)
	readNum := func() (float64, bool) {
		for i < n && (d[i] == ' ' || d[i] == ',' || d[i] == '\n' || d[i] == '\t' || d[i] == '\r') {
			i++
		}
		if i >= n {
			return 0, false
		}
		st := i
		if d[i] == '-' || d[i] == '+' {
			i++
		}
		for i < n && d[i] >= '0' && d[i] <= '9' {
			i++
		}
		if i < n && d[i] == '.' {
			i++
			for i < n && d[i] >= '0' && d[i] <= '9' {
				i++
			}
		}
		if i < n && (d[i] == 'e' || d[i] == 'E') {
			i++
			if i < n && (d[i] == '-' || d[i] == '+') {
				i++
			}
			for i < n && d[i] >= '0' && d[i] <= '9' {
				i++
			}
		}
		v, err := strconv.ParseFloat(d[st:i], 64)
		return v, err == nil
	}
	flush := func() {
		if len(cur) >= 2 {
			subs = append(subs, cur)
		}
		cur = nil
	}
	for i < n {
		c := d[i]
		if c == ' ' || c == ',' || c == '\n' || c == '\t' || c == '\r' {
			i++
			continue
		}
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			cmd = c
			i++
			if cmd == 'Z' || cmd == 'z' {
				if len(cur) > 0 {
					cur = append(cur, Pt{sx, sy})
				}
				flush()
				cx, cy = sx, sy
				continue
			}
		} else if cmd == 0 {
			return nil, false
		}
		rel := cmd >= 'a' && cmd <= 'z'
		up := cmd
		if rel {
			up = cmd - 32
		}
		switch up {
		case 'M':
			x, ok1 := readNum()
			y, ok2 := readNum()
			if !ok1 || !ok2 {
				return subs, true
			}
			if rel {
				x += cx
				y += cy
			}
			flush()
			cx, cy = x, y
			sx, sy = x, y
			cur = append(cur, Pt{x, y})
			cmd = 'L'
			if rel {
				cmd = 'l'
			}
		case 'L':
			x, ok1 := readNum()
			y, ok2 := readNum()
			if !ok1 || !ok2 {
				return subs, true
			}
			if rel {
				x += cx
				y += cy
			}
			cx, cy = x, y
			cur = append(cur, Pt{x, y})
		case 'H':
			x, ok := readNum()
			if !ok {
				return subs, true
			}
			if rel {
				x += cx
			}
			cx = x
			cur = append(cur, Pt{cx, cy})
		case 'V':
			y, ok := readNum()
			if !ok {
				return subs, true
			}
			if rel {
				y += cy
			}
			cy = y
			cur = append(cur, Pt{cx, cy})
		case 'Q':
			x1, o1 := readNum()
			y1, o2 := readNum()
			x, o3 := readNum()
			y, o4 := readNum()
			if !o1 || !o2 || !o3 || !o4 {
				return subs, true
			}
			if rel {
				x1 += cx
				y1 += cy
				x += cx
				y += cy
			}
			step := quadSteps(cx, cy, x1, y1, x, y)
			for k := 1; k <= step; k++ {
				t := float64(k) / float64(step)
				mt := 1 - t
				px := mt*mt*cx + 2*mt*t*x1 + t*t*x
				py := mt*mt*cy + 2*mt*t*y1 + t*t*y
				cur = append(cur, Pt{px, py})
			}
			cx, cy = x, y
		case 'C':
			x1, o1 := readNum()
			y1, o2 := readNum()
			x2, o3 := readNum()
			y2, o4 := readNum()
			x, o5 := readNum()
			y, o6 := readNum()
			if !o1 || !o2 || !o3 || !o4 || !o5 || !o6 {
				return subs, true
			}
			if rel {
				x1 += cx
				y1 += cy
				x2 += cx
				y2 += cy
				x += cx
				y += cy
			}
			step := cubicSteps(cx, cy, x1, y1, x2, y2, x, y)
			for k := 1; k <= step; k++ {
				t := float64(k) / float64(step)
				mt := 1 - t
				a := mt * mt * mt
				b := 3 * mt * mt * t
				c2 := 3 * mt * t * t
				dd := t * t * t
				px := a*cx + b*x1 + c2*x2 + dd*x
				py := a*cy + b*y1 + c2*y2 + dd*y
				cur = append(cur, Pt{px, py})
			}
			cx, cy = x, y
		default:
			// S/T/A 等不常见于 shape 导出，跳过两个数字避免死循环
			if _, ok := readNum(); !ok {
				return subs, true
			}
			readNum()
			_ = cx
		}
	}
	flush()
	return subs, true
}

// 曲线细分容差（文档单位，即 1x 像素）。0.12px 意味着折线与真实曲线的
// 最大偏差小于 1/8 像素，配合 16×16 超采样后肉眼看不到多边形棱角。
const curveTol = 0.06

// quadSteps 按「控制点到弦的距离」估算二次贝塞尔所需段数。
//
// 为什么不用段长估算：旧实现按边长/2 且上限 24，直径 300px 的圆弧只有 20 多段，
// 折线拐角直接表现为锯齿。按偏差估算能让大曲线自动细分到足够段数。
func quadSteps(x0, y0, x1, y1, x2, y2 float64) int {
	// 二次贝塞尔到弦的最大偏差 = |控制点 - 弦中点| / 2
	mx, my := (x0+x2)/2, (y0+y2)/2
	dev := math.Hypot(x1-mx, y1-my) / 2
	n := int(math.Sqrt(dev/curveTol)) + 1
	if n < 2 {
		n = 2
	}
	if n > 256 {
		n = 256
	}
	return n
}

// cubicSteps 按三次贝塞尔的控制点偏移估算段数（与渲染无关的上限保护）。
func cubicSteps(x0, y0, x1, y1, x2, y2, x3, y3 float64) int {
	// 用控制多边形长度与弦长之差估算弯曲程度
	chord := math.Hypot(x3-x0, y3-y0)
	poly := math.Hypot(x1-x0, y1-y0) + math.Hypot(x2-x1, y2-y1) + math.Hypot(x3-x2, y3-y2)
	dev := (poly - chord) / 2
	if dev < 0 {
		dev = 0
	}
	n := int(math.Sqrt(dev/curveTol*3)) + 1
	if n < 2 {
		n = 2
	}
	if n > 320 {
		n = 320
	}
	return n
}

// makeGrad 计算渐变的「文档→渐变空间」逆矩阵。
//
// gm 是 使用元素矩阵 ∘ gradientTransform，即 渐变空间 → 文档空间；
// colorAt 里用它的逆把文档像素映射回渐变空间求 t。
func makeGrad(g *rawGrad, m Mat, gt Mat) *Grad {
	gm := m.Mul(gt)
	inv, ok := invert(gm)
	if !ok {
		inv = Mat{1, 0, 0, 1, 0, 0}
	}
	out := &Grad{Radial: g.Radial, Inv: inv, CX: g.X0, CY: g.Y0,
		FX: g.FX, FY: g.FY, R: g.R, Stops: g.Stops}
	if !g.Radial {
		out.DX = g.X1 - g.X0
		out.DY = g.Y1 - g.Y0
	}
	return out
}

// invert 求仿射矩阵的逆；退化时返回 false。
func invert(m Mat) (Mat, bool) {
	det := m.Det()
	if math.Abs(det) < 1e-12 {
		return Mat{}, false
	}
	return Mat{
		A: m.D / det,
		B: -m.B / det,
		C: -m.C / det,
		D: m.A / det,
		E: (m.C*m.F - m.D*m.E) / det,
		F: (m.B*m.E - m.A*m.F) / det,
	}, true
}
