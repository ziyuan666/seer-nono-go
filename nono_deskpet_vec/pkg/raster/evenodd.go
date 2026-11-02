package raster

import (
	"image"
	"image/color"
	"math"
	"sort"

	"nonovec/pkg/pathd"
)

// FillPolysEvenOdd 用 SVG 的 even-odd 规则填充多边形。
//
// 为什么需要它：FFDec 导出的 shape 大量使用 fill-rule="evenodd"，且同一条路径
// 里既有外轮廓又有内挖空（自交、同心环）。golang.org/x/image/vector 只有非零
// 环绕（nonzero），遇到这类图形会把挖空填实——super4 动作的触角因此从细弧变成
// 粗月牙。
//
// 像素覆盖率：一行里 xa..xb 可能只有 0.5px 宽（细臂、尖刺），早期版本要求
// 「整个像素都在区间内」才填充，结果细窄部位整行被丢掉（shape_5 的十字只剩
// 一条横线）。这里按重叠长度折算 alpha，既保留细条也顺带做水平抗锯齿。
func FillPolysEvenOdd(dst *image.RGBA, polys [][]pathd.Point, c color.NRGBA) {
	if c.A == 0 {
		return
	}
	fillEvenOdd(dst, polys, func(x, y float64) color.NRGBA { return c })
}

// FillShadedEvenOdd 与 FillPolysEvenOdd 同规则，颜色由 shade 逐像素给出。
// 用于渐变染色：同一个色块里不同高度要用不同的目标色。
func FillShadedEvenOdd(dst *image.RGBA, polys [][]pathd.Point, shade func(x, y float64) color.NRGBA) {
	fillEvenOdd(dst, polys, shade)
}

func fillEvenOdd(dst *image.RGBA, polys [][]pathd.Point, shade func(x, y float64) color.NRGBA) {
	if len(polys) == 0 {
		return
	}
	type seg struct{ ax, ay, bx, by float64 }
	var segs []seg
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, poly := range polys {
		n := len(poly)
		if n < 3 {
			continue
		}
		for i := 0; i < n; i++ {
			a := poly[i]
			b := poly[(i+1)%n]
			if a.Y == b.Y {
				continue // 水平边不产生交点
			}
			segs = append(segs, seg{a.X, a.Y, b.X, b.Y})
			if a.Y < minY {
				minY = a.Y
			}
			if a.Y > maxY {
				maxY = a.Y
			}
		}
	}
	if len(segs) == 0 {
		return
	}
	bd := dst.Bounds()
	y0 := int(math.Floor(minY))
	y1 := int(math.Ceil(maxY))
	if y0 < bd.Min.Y {
		y0 = bd.Min.Y
	}
	if y1 > bd.Max.Y {
		y1 = bd.Max.Y
	}
	xs := make([]float64, 0, 64)
	for y := y0; y < y1; y++ {
		sy := float64(y) + 0.5 // 取像素中心
		xs = xs[:0]
		for _, s := range segs {
			if (s.ay <= sy && s.by > sy) || (s.by <= sy && s.ay > sy) {
				t := (sy - s.ay) / (s.by - s.ay)
				xs = append(xs, s.ax+t*(s.bx-s.ax))
			}
		}
		if len(xs) < 2 {
			continue
		}
		sort.Float64s(xs)
		for i := 0; i+1 < len(xs); i += 2 {
			xa, xb := xs[i], xs[i+1]
			if xb <= xa {
				continue
			}
			px0 := int(math.Floor(xa))
			px1 := int(math.Ceil(xb))
			if px0 < bd.Min.X {
				px0 = bd.Min.X
			}
			if px1 > bd.Max.X {
				px1 = bd.Max.X
			}
			for x := px0; x < px1; x++ {
				cov := math.Min(float64(x+1), xb) - math.Max(float64(x), xa)
				if cov <= 0 {
					continue
				}
				if cov > 1 {
					cov = 1
				}
				cc := shade(float64(x)+0.5, float64(y)+0.5)
				if cov < 1 {
					cc.A = uint8(float64(cc.A) * cov)
				}
				blendOver(dst, x, y, cc)
			}
		}
	}
}

// blendOver 把一个像素按 source-over 叠加到 dst（非预乘 RGBA）。
func blendOver(dst *image.RGBA, x, y int, c color.NRGBA) {
	if c.A == 0 {
		return
	}
	i := dst.PixOffset(x, y)
	if c.A == 255 {
		dst.Pix[i] = c.R
		dst.Pix[i+1] = c.G
		dst.Pix[i+2] = c.B
		dst.Pix[i+3] = 255
		return
	}
	sa := float64(c.A) / 255
	da := float64(dst.Pix[i+3]) / 255
	oa := sa + da*(1-sa)
	if oa <= 0 {
		return
	}
	dst.Pix[i] = uint8((float64(c.R)*sa + float64(dst.Pix[i])*da*(1-sa)) / oa)
	dst.Pix[i+1] = uint8((float64(c.G)*sa + float64(dst.Pix[i+1])*da*(1-sa)) / oa)
	dst.Pix[i+2] = uint8((float64(c.B)*sa + float64(dst.Pix[i+2])*da*(1-sa)) / oa)
	dst.Pix[i+3] = uint8(oa*255 + 0.5)
}
