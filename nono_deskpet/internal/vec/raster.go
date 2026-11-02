package vec

import (
	"image"
	"math"
	"sort"
)

// Render 按目标像素尺寸渲染文档，返回 RGBA（预乘无关，普通 alpha）。
//
// 为什么用 4×4 超采样而不是解析覆盖率：shape 导出里曲线已被细分成折线，
// 解析覆盖率需要处理自交/多重子路径，实现复杂且容易出错；4×4 对面板这种
// 中低细节图像足够（与 FFDec 官方渲染的差异在抗锯齿层面）。
func (d *Doc) Render(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if w <= 0 || h <= 0 || d.VBw <= 0 || d.VBh <= 0 {
		return img
	}
	sx := float64(w) / d.VBw
	sy := float64(h) / d.VBh
	cov := make([]float32, w*h)

	for _, s := range d.Shapes {
		polys := transformPolys(s.Polys, d.VBx, d.VBy, sx, sy)
		if s.Fill != nil && len(polys) > 0 {
			fillShape(img, cov, polys, s.EvenOdd, s.Fill, d, sx, sy, w, h)
		}
		if s.StrokeW > 0 && s.StrokePaint != nil {
			sp := strokePolys(polys, s.StrokeW*math.Sqrt(math.Abs(sx*sy)))
			if len(sp) > 0 {
				fillShape(img, cov, sp, false, s.StrokePaint, d, sx, sy, w, h)
			}
		}
	}
	return img
}

func transformPolys(ps [][]Pt, vbx, vby, sx, sy float64) [][]Pt {
	out := make([][]Pt, 0, len(ps))
	for _, p := range ps {
		q := make([]Pt, len(p))
		for i, pt := range p {
			q[i] = Pt{(pt.X - vbx) * sx, (pt.Y - vby) * sy}
		}
		out = append(out, q)
	}
	return out
}

// strokePolys 把折线扩成四边形集合（宽度 w，目标坐标）。
func strokePolys(polys [][]Pt, w float64) [][]Pt {
	if w <= 0 {
		return nil
	}
	hw := w / 2
	var out [][]Pt
	for _, p := range polys {
		for i := 0; i+1 < len(p); i++ {
			x0, y0 := p[i].X, p[i].Y
			x1, y1 := p[i+1].X, p[i+1].Y
			dx, dy := x1-x0, y1-y0
			l := math.Hypot(dx, dy)
			if l < 1e-9 {
				continue
			}
			nx, ny := -dy/l*hw, dx/l*hw
			out = append(out, []Pt{
				{x0 + nx, y0 + ny},
				{x1 + nx, y1 + ny},
				{x1 - nx, y1 - ny},
				{x0 - nx, y0 - ny},
				{x0 + nx, y0 + ny},
			})
		}
	}
	return out
}

type edge struct {
	x0, y0, x1, y1 float64
	dir            int
}

// fillShape 用 4×4 超采样把多边形集合填充到 img。cov 是复用的覆盖率缓冲。
func fillShape(img *image.RGBA, cov []float32, polys [][]Pt, evenOdd bool, p *Paint, d *Doc, sx, sy float64, w, h int) {
	// bbox
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	var edges []edge
	for _, poly := range polys {
		n := len(poly)
		if n < 2 {
			continue
		}
		for i := 0; i < n; i++ {
			a := poly[i]
			b := poly[(i+1)%n]
			if a.X < minX {
				minX = a.X
			}
			if a.X > maxX {
				maxX = a.X
			}
			if a.Y < minY {
				minY = a.Y
			}
			if a.Y > maxY {
				maxY = a.Y
			}
			if a.Y == b.Y {
				continue
			}
			dir := 1
			if b.Y < a.Y {
				dir = -1
			}
			edges = append(edges, edge{a.X, a.Y, b.X, b.Y, dir})
		}
	}
	if len(edges) == 0 {
		return
	}
	x0 := int(math.Floor(minX)) - 1
	x1 := int(math.Ceil(maxX)) + 1
	y0 := int(math.Floor(minY)) - 1
	y1 := int(math.Ceil(maxY)) + 1
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > w {
		x1 = w
	}
	if y1 > h {
		y1 = h
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}

	// 清空并计算覆盖率
	for y := y0; y < y1; y++ {
		row := cov[y*w : y*w+w]
		for x := x0; x < x1; x++ {
			row[x] = 0
		}
	}
	// 纵向超采样数。横向用解析重叠（addSpanCoverage），纵向 16 等分后
	// 单像素纵向误差 ≤ 1/16，配合更细的曲线细分即可消除可见锯齿。
	const SUB = 64          // 每像素纵向子扫描线；32 → 1px 边缘仍有可见台阶
	span := make([]float64, 0, 64)
	dirs := make([]int, 0, 64)
	for y := y0; y < y1; y++ {
		for k := 0; k < SUB; k++ {
			yy := float64(y) + (float64(k)+0.5)/SUB
			span = span[:0]
			for _, e := range edges {
				lo, hi := e.y0, e.y1
				if lo > hi {
					lo, hi = hi, lo
				}
				if yy < lo || yy >= hi {
					continue
				}
				t := (yy - e.y0) / (e.y1 - e.y0)
				span = append(span, e.x0+(e.x1-e.x0)*t)
				dirs = append(dirs, e.dir)
			}
			if len(span) < 2 {
				continue
			}
			// 按 x 排序（同步 dirs）
			idx := make([]int, len(span))
			for i := range idx {
				idx[i] = i
			}
			sort.Slice(idx, func(a, b int) bool { return span[idx[a]] < span[idx[b]] })
			sorted := make([]float64, len(span))
			ds := make([]int, len(span))
			for i, j := range idx {
				sorted[i] = span[j]
				ds[i] = dirs[j]
			}
			// evenodd 只看「交叉次数」的奇偶，与边的方向无关；nonzero 才累加方向。
			// 早期实现两者都用带符号的 wind，导致同一子路径的两条同向边互相抵消，
			// 表现为形状被挖空或边缘多出一条「尾巴」。
			wind, cross := 0, 0
			start := 0.0
			for i := 0; i < len(sorted); i++ {
				wasIn := inside(wind, cross, evenOdd)
				wind += ds[i]
				cross++
				isIn := inside(wind, cross, evenOdd)
				if !wasIn && isIn {
					start = sorted[i]
				} else if wasIn && !isIn {
					addSpanCoverage(cov, y, w, start, sorted[i], x0, x1)
				}
			}
		}
	}

	// 上色
	row := img.Pix
	for y := y0; y < y1; y++ {
		base := y * w
		for x := x0; x < x1; x++ {
			c := cov[base+x]
			if c <= 0.002 {
				continue
			}
			// 像素中心映射回文档坐标求渐变颜色
			dx := float64(x)/sx + d.VBx + 0.5/sx
			dy := float64(y)/sy + d.VBy + 0.5/sy
			r, g, b, a := p.colorAt(dx, dy)
			a *= float64(c)
			if a <= 0 {
				continue
			}
			srcOver(row, (base+x)*4, r, g, b, a)
		}
	}
}

// inside 判断当前是否在多边形内部。
func inside(wind, cross int, evenOdd bool) bool {
	if evenOdd {
		return cross%2 != 0
	}
	return wind != 0
}

// addSpanCoverage 把 [xa,xb) 的横向覆盖率加到 cov 行上（纵向权重 1/SUB）。
func addSpanCoverage(cov []float32, y, w int, xa, xb float64, clampX0, clampX1 int) {
	if xb <= xa {
		return
	}
	const SUBW = 1.0 / 64.0
	base := y * w
	ia := int(math.Floor(xa))
	ib := int(math.Floor(xb))
	if ia < clampX0 {
		ia = clampX0
	}
	if ib >= clampX1 {
		ib = clampX1 - 1
	}
	for x := ia; x <= ib; x++ {
		l := xa
		if float64(x) > l {
			l = float64(x)
		}
		r := xb
		if float64(x+1) < r {
			r = float64(x + 1)
		}
		if r > l {
			cov[base+x] += float32((r - l) * SUBW)
		}
	}
}

// colorAt 返回该文档坐标处的颜色（非预乘，0..1）。
func (p *Paint) colorAt(x, y float64) (r, g, b, a float64) {
	if p.Grad == nil {
		return p.R, p.G, p.B, p.A
	}
	gr := p.Grad
	// 文档坐标 → 渐变空间，再算 t（radial 因此能正确支持椭圆变换）
	qx, qy := gr.Inv.Pt(x, y)
	var t float64
	if gr.Radial {
		// SVG 1.1：焦点 F 不在圆心时，色带参数 t = |P-F| / |E-F|，
		// E 是「从 F 过 P 的射线」与圆(cx,cy,r)的交点。只按 |P-C|/R 算
		// 等于把焦点强行当圆心，偏心渐变的色带位置会整体平移。
		dx, dy := qx-gr.FX, qy-gr.FY
		if gr.R > 1e-9 {
			dd := dx*dx + dy*dy
			if dd < 1e-12 {
				t = 0
			} else {
				vx, vy := gr.FX-gr.CX, gr.FY-gr.CY
				bq := vx*dx + vy*dy
				cq := vx*vx + vy*vy - gr.R*gr.R
				disc := bq*bq - dd*cq
				if disc <= 0 {
					t = 1
				} else {
					se := (-bq + math.Sqrt(disc)) / dd
					if se > 1e-9 {
						t = 1.0 / se
					} else {
						t = 1
					}
				}
			}
		}
	} else {
		l2 := gr.DX*gr.DX + gr.DY*gr.DY
		if l2 > 1e-12 {
			t = ((qx-gr.CX)*gr.DX + (qy-gr.CY)*gr.DY) / l2
		}
	}
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return sampleStops(gr.Stops, t)
}

// sampleStops 在色标间线性插值。
func sampleStops(stops []Stop, t float64) (r, g, b, a float64) {
	if len(stops) == 0 {
		return 0, 0, 0, 1
	}
	if t <= stops[0].Offset {
		s := stops[0]
		return s.R, s.G, s.B, s.A
	}
	for i := 0; i+1 < len(stops); i++ {
		s0, s1 := stops[i], stops[i+1]
		if t >= s0.Offset && t <= s1.Offset {
			span := s1.Offset - s0.Offset
			f := 1.0
			if span > 1e-9 {
				f = (t - s0.Offset) / span
			}
			return s0.R + (s1.R-s0.R)*f,
				s0.G + (s1.G-s0.G)*f,
				s0.B + (s1.B-s0.B)*f,
				s0.A + (s1.A-s0.A)*f
		}
	}
	s := stops[len(stops)-1]
	return s.R, s.G, s.B, s.A
}

// srcOver 把颜色以 src-over 合成到 dst（dst 为非预乘 RGBA）。
func srcOver(dst []byte, i int, r, g, b, a float64) {
	if a <= 0 {
		return
	}
	if a > 1 {
		a = 1
	}
	da := float64(dst[i+3]) / 255
	outA := a + da*(1-a)
	if outA <= 1e-6 {
		dst[i], dst[i+1], dst[i+2], dst[i+3] = 0, 0, 0, 0
		return
	}
	dst[i] = clampToByte((r*a + float64(dst[i])/255*da*(1-a)) / outA)
	dst[i+1] = clampToByte((g*a + float64(dst[i+1])/255*da*(1-a)) / outA)
	dst[i+2] = clampToByte((b*a + float64(dst[i+2])/255*da*(1-a)) / outA)
	dst[i+3] = clampToByte(outA)
}

func clampToByte(v float64) byte {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return byte(v*255 + 0.5)
}
