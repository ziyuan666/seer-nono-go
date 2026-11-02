// Package pathd 解析 SVG path 的 d 属性，输出可直接喂给光栅化器的段序列。
//
// 只实现 NONO 素材实际用到的子集：M/m L/l H/h V/v Q/q T/t C/c S/s Z/z。
// 圆弧 A/a 不实现（FFDec 导出的 shape 已把弧转成 Q/C）；遇到时报错，避免静默画错。
//
// 为什么自己写：几何来源是矢量工具/FFDec 的 SVG，语法有限，引第三方库收益不大，
// 而依赖越少，以后改格式越自由。
package pathd

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// SegKind 段类型。
type SegKind int

const (
	Move SegKind = iota // 起新子路径
	Line
	Quad // 二次贝塞尔
	Cube // 三次贝塞尔
	Close
)

// Seg 一段路径。Pts 按类型解释：
// Move/Line: [x,y]；Quad: [cx,cy,x,y]；Cube: [c1x,c1y,c2x,c2y,x,y]；Close: 空。
type Seg struct {
	Kind SegKind
	Pts  []float64
}

// Parse 把 d 字符串解析成段序列。
func Parse(d string) ([]Seg, error) {
	p := &parser{s: d}
	var out []Seg
	var cx, cy float64 // 当前点
	var sx, sy float64 // 子路径起点
	var lastCtrlX, lastCtrlY float64
	var lastKind SegKind = -1
	for {
		cmd, ok := p.nextCmd()
		if !ok {
			break
		}
		rel := cmd >= 'a' && cmd <= 'z'
		up := cmd
		if rel {
			up = cmd - 32
		}
		switch up {
		case 'M':
			x, y, err := p.pair()
			if err != nil {
				return nil, err
			}
			if rel {
				x += cx
				y += cy
			}
			out = append(out, Seg{Move, []float64{x, y}})
			cx, cy, sx, sy = x, y, x, y
			lastKind = Move
			for p.hasNum() {
				x, y, err := p.pair()
				if err != nil {
					return nil, err
				}
				if rel {
					x += cx
					y += cy
				}
				out = append(out, Seg{Line, []float64{x, y}})
				cx, cy = x, y
				lastKind = Line
			}
		case 'L':
			for p.hasNum() {
				x, y, err := p.pair()
				if err != nil {
					return nil, err
				}
				if rel {
					x += cx
					y += cy
				}
				out = append(out, Seg{Line, []float64{x, y}})
				cx, cy = x, y
				lastKind = Line
			}
		case 'H':
			for p.hasNum() {
				v, err := p.num()
				if err != nil {
					return nil, err
				}
				x := v
				if rel {
					x += cx
				}
				out = append(out, Seg{Line, []float64{x, cy}})
				cx = x
				lastKind = Line
			}
		case 'V':
			for p.hasNum() {
				v, err := p.num()
				if err != nil {
					return nil, err
				}
				y := v
				if rel {
					y += cy
				}
				out = append(out, Seg{Line, []float64{cx, y}})
				cy = y
				lastKind = Line
			}
		case 'Q':
			for p.hasNum() {
				qx, qy, err := p.pair()
				if err != nil {
					return nil, err
				}
				x, y, err := p.pair()
				if err != nil {
					return nil, err
				}
				if rel {
					qx += cx
					qy += cy
					x += cx
					y += cy
				}
				out = append(out, Seg{Quad, []float64{qx, qy, x, y}})
				lastCtrlX, lastCtrlY = qx, qy
				cx, cy = x, y
				lastKind = Quad
			}
		case 'T':
			for p.hasNum() {
				x, y, err := p.pair()
				if err != nil {
					return nil, err
				}
				if rel {
					x += cx
					y += cy
				}
				qx, qy := cx, cy
				if lastKind == Quad {
					qx = 2*cx - lastCtrlX
					qy = 2*cy - lastCtrlY
				}
				out = append(out, Seg{Quad, []float64{qx, qy, x, y}})
				lastCtrlX, lastCtrlY = qx, qy
				cx, cy = x, y
				lastKind = Quad
			}
		case 'C':
			for p.hasNum() {
				c1x, c1y, err := p.pair()
				if err != nil {
					return nil, err
				}
				c2x, c2y, err := p.pair()
				if err != nil {
					return nil, err
				}
				x, y, err := p.pair()
				if err != nil {
					return nil, err
				}
				if rel {
					c1x += cx
					c1y += cy
					c2x += cx
					c2y += cy
					x += cx
					y += cy
				}
				out = append(out, Seg{Cube, []float64{c1x, c1y, c2x, c2y, x, y}})
				cx, cy = x, y
				lastCtrlX, lastCtrlY = c2x, c2y
				lastKind = Cube
			}
		case 'S':
			for p.hasNum() {
				c2x, c2y, err := p.pair()
				if err != nil {
					return nil, err
				}
				x, y, err := p.pair()
				if err != nil {
					return nil, err
				}
				if rel {
					c2x += cx
					c2y += cy
					x += cx
					y += cy
				}
				c1x, c1y := cx, cy
				if lastKind == Cube {
					c1x = 2*cx - lastCtrlX
					c1y = 2*cy - lastCtrlY
				}
				out = append(out, Seg{Cube, []float64{c1x, c1y, c2x, c2y, x, y}})
				cx, cy = x, y
				lastCtrlX, lastCtrlY = c2x, c2y
				lastKind = Cube
			}
		case 'Z':
			out = append(out, Seg{Close, nil})
			cx, cy = sx, sy
			lastKind = Close
		case 'A':
			return nil, fmt.Errorf("pathd: 圆弧命令 A 未实现（应在上游转成 Q/C）")
		default:
			return nil, fmt.Errorf("pathd: 不支持的命令 %q", string(cmd))
		}
	}
	return out, nil
}

type parser struct {
	s string
	i int
}

func (p *parser) skip() {
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == ' ' || c == ',' || c == '\n' || c == '\r' || c == '\t' {
			p.i++
			continue
		}
		break
	}
}

func (p *parser) nextCmd() (byte, bool) {
	p.skip()
	if p.i >= len(p.s) {
		return 0, false
	}
	c := p.s[p.i]
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
		p.i++
		return c, true
	}
	return 0, false
}

func (p *parser) hasNum() bool {
	p.skip()
	if p.i >= len(p.s) {
		return false
	}
	c := p.s[p.i]
	return c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9')
}

func (p *parser) num() (float64, error) {
	p.skip()
	start := p.i
	if p.i < len(p.s) && (p.s[p.i] == '-' || p.s[p.i] == '+') {
		p.i++
	}
	for p.i < len(p.s) {
		c := p.s[p.i]
		if (c >= '0' && c <= '9') || c == '.' {
			p.i++
			continue
		}
		if (c == 'e' || c == 'E') && p.i+1 < len(p.s) {
			p.i++
			if p.s[p.i] == '-' || p.s[p.i] == '+' {
				p.i++
			}
			continue
		}
		break
	}
	if start == p.i {
		return 0, fmt.Errorf("pathd: 位置 %d 处期望数字", p.i)
	}
	v, err := strconv.ParseFloat(p.s[start:p.i], 64)
	if err != nil {
		return 0, fmt.Errorf("pathd: 解析数字 %q 失败: %w", p.s[start:p.i], err)
	}
	return v, nil
}

func (p *parser) pair() (float64, float64, error) {
	x, err := p.num()
	if err != nil {
		return 0, 0, err
	}
	y, err := p.num()
	if err != nil {
		return 0, 0, err
	}
	return x, y, nil
}

// Point 二维点。
type Point struct{ X, Y float64 }

// Polyline 把段序列拍平成折线（每条子路径一个 []Point）。
// 贝塞尔按曲率自适应细分：先用二阶差分估算最大偏差，再定步数。
func Polyline(segs []Seg, tol float64) [][]Point {
	if tol <= 0 {
		tol = 0.15
	}
	var polys [][]Point
	var cur []Point
	var cx, cy float64
	for _, s := range segs {
		switch s.Kind {
		case Move:
			if len(cur) > 1 {
				polys = append(polys, cur)
			}
			cx, cy = s.Pts[0], s.Pts[1]
			cur = []Point{{cx, cy}}
		case Line:
			cx, cy = s.Pts[0], s.Pts[1]
			cur = append(cur, Point{cx, cy})
		case Quad:
			qx, qy, x, y := s.Pts[0], s.Pts[1], s.Pts[2], s.Pts[3]
			n := quadSteps(cx, cy, qx, qy, x, y, tol)
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				mt := 1 - t
				px := mt*mt*cx + 2*mt*t*qx + t*t*x
				py := mt*mt*cy + 2*mt*t*qy + t*t*y
				cur = append(cur, Point{px, py})
			}
			cx, cy = x, y
		case Cube:
			c1x, c1y, c2x, c2y, x, y := s.Pts[0], s.Pts[1], s.Pts[2], s.Pts[3], s.Pts[4], s.Pts[5]
			n := cubeSteps(cx, cy, c1x, c1y, c2x, c2y, x, y, tol)
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				mt := 1 - t
				a := mt * mt * mt
				b := 3 * mt * mt * t
				c := 3 * mt * t * t
				d := t * t * t
				px := a*cx + b*c1x + c*c2x + d*x
				py := a*cy + b*c1y + c*c2y + d*y
				cur = append(cur, Point{px, py})
			}
			cx, cy = x, y
		case Close:
			if len(cur) > 1 {
				polys = append(polys, cur)
			}
			cur = nil
		}
	}
	if len(cur) > 1 {
		polys = append(polys, cur)
	}
	return polys
}

func quadSteps(x0, y0, qx, qy, x1, y1, tol float64) int {
	dev := math.Hypot(x0-2*qx+x1, y0-2*qy+y1) / 4
	n := int(math.Ceil(math.Sqrt(dev / (8 * tol))))
	if n < 2 {
		n = 2
	}
	if n > 64 {
		n = 64
	}
	return n
}

func cubeSteps(x0, y0, c1x, c1y, c2x, c2y, x1, y1, tol float64) int {
	d1 := math.Hypot(x0-2*c1x+c2x, y0-2*c1y+c2y)
	d2 := math.Hypot(c1x-2*c2x+x1, c1y-2*c2y+y1)
	dev := math.Max(d1, d2) * 3 / 4
	n := int(math.Ceil(math.Sqrt(dev / (8 * tol))))
	if n < 2 {
		n = 2
	}
	if n > 64 {
		n = 64
	}
	return n
}

// Transform 应用 2x3 矩阵 [a b c d e f]（SVG 语义：x' = a*x + c*y + e）。
func Transform(segs []Seg, m [6]float64) []Seg {
	a, b, c, d, e, f := m[0], m[1], m[2], m[3], m[4], m[5]
	out := make([]Seg, len(segs))
	for i, s := range segs {
		pts := make([]float64, len(s.Pts))
		for j := 0; j < len(s.Pts); j += 2 {
			x, y := s.Pts[j], s.Pts[j+1]
			pts[j] = a*x + c*y + e
			pts[j+1] = b*x + d*y + f
		}
		out[i] = Seg{s.Kind, pts}
	}
	return out
}

// ParseMatrix 解析 SVG transform="matrix(a,b,c,d,e,f)"。
func ParseMatrix(s string) (m [6]float64, ok bool) {
	s = strings.TrimSpace(s)
	const pre = "matrix("
	if !strings.HasPrefix(s, pre) || !strings.HasSuffix(s, ")") {
		return m, false
	}
	body := s[len(pre) : len(s)-1]
	parts := strings.Split(body, ",")
	if len(parts) != 6 {
		return m, false
	}
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return m, false
		}
		m[i] = v
	}
	return m, true
}
