package sprite

import (
	"image/color"
	"math"

	"nonovec/pkg/raster"
	"nonovec/pkg/spec"
)

type stopRGBA struct {
	off float64
	c   color.NRGBA
}

// shadeFor 把 JSON 里的渐变定义变成「像素坐标 → 颜色」的函数。
// k 是当前渲染的像素/画布坐标比（scale*ss），渐变矩阵定义在画布坐标系。
// shadeFor 生成渐变的采样函数。cmul/cadd 是当前 part 的 cxform，
// 必须对每个色标单独作用——cxform 是 per-place 的，直接乘在最终像素上
// 会把渐变整体压暗（叠加了背景）。
func (r *Renderer) shadeFor(g *spec.Gradient, k, alpha float64, tint *color.NRGBA, cmul, cadd []float64, replace bool) func(x, y float64) color.NRGBA {
	stops := make([]stopRGBA, 0, len(g.Stops))
	for _, s := range g.Stops {
		c, ok := r.color(s.Color, alpha*s.Opacity, tint, cmul, cadd, replace)
		if !ok {
			continue
		}
		stops = append(stops, stopRGBA{s.Offset, c})
	}
	if len(stops) == 0 {
		return nil
	}
	// 按 offset 排序（生成端一般已有序，这里兜底）
	for i := 1; i < len(stops); i++ {
		for j := i; j > 0 && stops[j].off < stops[j-1].off; j-- {
			stops[j], stops[j-1] = stops[j-1], stops[j]
		}
	}
	inv, ok := raster.Mat3(g.Matrix).Invert()
	if !ok {
		return nil
	}
	if g.Type == "linear" {
		dx, dy := g.X2-g.X1, g.Y2-g.Y1
		den := dx*dx + dy*dy
		if den < 1e-12 {
			return nil
		}
		return func(x, y float64) color.NRGBA {
			qx, qy := inv.Apply(x/k, y/k)
			t := ((qx-g.X1)*dx + (qy-g.Y1)*dy) / den
			return sampleStops(stops, t)
		}
	}
	// radial：从焦点出发的射线与圆求交
	return func(x, y float64) color.NRGBA {
		qx, qy := inv.Apply(x/k, y/k)
		t := radialT(qx, qy, g.CX, g.CY, g.R, g.FX, g.FY)
		return sampleStops(stops, t)
	}
}

func radialT(px, py, cx, cy, r, fx, fy float64) float64 {
	if r <= 0 {
		return 0
	}
	dx, dy := px-fx, py-fy
	a := dx*dx + dy*dy
	if a < 1e-12 {
		return 0
	}
	// |f + s*d - c|^2 = r^2
	bx, by := fx-cx, fy-cy
	b := 2 * (dx*bx + dy*by)
	c := bx*bx + by*by - r*r
	disc := b*b - 4*a*c
	if disc < 0 {
		return 1
	}
	s := (-b + math.Sqrt(disc)) / (2 * a)
	if s <= 1e-9 {
		return 1
	}
	t := 1 / s
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t
}

func sampleStops(stops []stopRGBA, t float64) color.NRGBA {
	if t <= stops[0].off {
		return stops[0].c
	}
	last := stops[len(stops)-1]
	if t >= last.off {
		return last.c
	}
	for i := 1; i < len(stops); i++ {
		if t <= stops[i].off {
			a, b := stops[i-1], stops[i]
			span := b.off - a.off
			if span <= 1e-9 {
				return b.c
			}
			f := (t - a.off) / span
			return color.NRGBA{
				R: lerp8(a.c.R, b.c.R, f),
				G: lerp8(a.c.G, b.c.G, f),
				B: lerp8(a.c.B, b.c.B, f),
				A: lerp8(a.c.A, b.c.A, f),
			}
		}
	}
	return last.c
}

func lerp8(a, b uint8, f float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*f + 0.5)
}
