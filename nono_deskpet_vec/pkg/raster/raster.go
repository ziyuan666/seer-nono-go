// Package raster 把折线路径光栅化成 RGBA，支持填充、描边和超采样。
//
// 填充用 golang.org/x/image/vector（解析覆盖率，抗锯齿质量好）。
// 描边自己实现：折线按线宽扩成四边形 + 关节圆，再用同一光栅化器填充。
// 超采样：先按 scale 渲染，再由调用方下采样；也可用 RenderSuper 一步到位。
package raster

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/vector"

	"nonovec/pkg/pathd"
)

// Style 一个填充/描边样式。
type Style struct {
	Fill   color.NRGBA
	Stroke color.NRGBA
	Width  float64
	Cap    string // round | butt | square
	Join   string // round | miter
}

// FillPolys 用非零环绕规则填充多条闭合折线。
func FillPolys(dst *image.RGBA, polys [][]pathd.Point, c color.NRGBA) {
	if c.A == 0 || len(polys) == 0 {
		return
	}
	b := dst.Bounds()
	r := vector.NewRasterizer(b.Dx(), b.Dy())
	for _, poly := range polys {
		if len(poly) < 2 {
			continue
		}
		r.MoveTo(float32(poly[0].X), float32(poly[0].Y))
		for _, p := range poly[1:] {
			r.LineTo(float32(p.X), float32(p.Y))
		}
		r.ClosePath()
	}
	r.Draw(dst, b, image.NewUniform(c), image.Point{})
}

// StrokePolys 用「每段矩形 + 每顶点圆」的方式描边。
// 重叠区域靠非零环绕合并，所以不用做线段求交。
func StrokePolys(dst *image.RGBA, polys [][]pathd.Point, c color.NRGBA, width float64) {
	if c.A == 0 || width <= 0 || len(polys) == 0 {
		return
	}
	b := dst.Bounds()
	r := vector.NewRasterizer(b.Dx(), b.Dy())
	hw := width / 2
	for _, poly := range polys {
		if len(poly) < 2 {
			continue
		}
		for i := 0; i+1 < len(poly); i++ {
			addQuad(r, poly[i], poly[i+1], hw)
		}
		// 关节与端点用圆补齐（cap=round / join=round 的近似）
		for _, p := range poly {
			addCircle(r, p, hw, 10)
		}
	}
	r.Draw(dst, b, image.NewUniform(c), image.Point{})
}

func addQuad(r *vector.Rasterizer, a, b pathd.Point, hw float64) {
	dx, dy := b.X-a.X, b.Y-a.Y
	l := math.Hypot(dx, dy)
	if l < 1e-9 {
		return
	}
	nx, ny := -dy/l*hw, dx/l*hw
	r.MoveTo(float32(a.X+nx), float32(a.Y+ny))
	r.LineTo(float32(b.X+nx), float32(b.Y+ny))
	r.LineTo(float32(b.X-nx), float32(b.Y-ny))
	r.LineTo(float32(a.X-nx), float32(a.Y-ny))
	r.ClosePath()
}

func addCircle(r *vector.Rasterizer, p pathd.Point, rad float64, n int) {
	if rad <= 0 {
		return
	}
	for i := 0; i <= n; i++ {
		t := 2 * math.Pi * float64(i) / float64(n)
		x := p.X + rad*math.Cos(t)
		y := p.Y + rad*math.Sin(t)
		if i == 0 {
			r.MoveTo(float32(x), float32(y))
		} else {
			r.LineTo(float32(x), float32(y))
		}
	}
	r.ClosePath()
}

// RenderSuper 按 ss 倍超采样渲染，再 box 下采样到 w×h。
// drawFn 在超采样坐标系里绘制（坐标需乘以 ss）。
func RenderSuper(w, h, ss int, drawFn func(dst *image.RGBA)) *image.RGBA {
	if ss < 1 {
		ss = 1
	}
	big := image.NewRGBA(image.Rect(0, 0, w*ss, h*ss))
	drawFn(big)
	return Downsample(big, w, h, ss)
}

// Downsample 把 ss 倍图用 box 平均缩回原尺寸。
// box 在超采样场景比 lanczos 更稳（不会在细描边上产生振铃）。
//
// 像素数据是非预乘 alpha（blendOver 写的就是非预乘），所以不能直接对 RGB 求平均：
// 完全透明的邻居 RGB=0 会把边缘拉黑，alpha<255 的实色边缘会整体发暗。
// 这里按 alpha 加权求颜色，alpha 本身仍旧算术平均。
func Downsample(src *image.RGBA, w, h, ss int) *image.RGBA {
	if ss <= 1 {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	n := ss * ss
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sr, sg, sb, sa uint32
			for dy := 0; dy < ss; dy++ {
				row := (y*ss + dy) * src.Stride
				for dx := 0; dx < ss; dx++ {
					i := row + (x*ss+dx)*4
					a := uint32(src.Pix[i+3])
					if a == 0 {
						continue
					}
					sr += uint32(src.Pix[i]) * a
					sg += uint32(src.Pix[i+1]) * a
					sb += uint32(src.Pix[i+2]) * a
					sa += a
				}
			}
			i := y*dst.Stride + x*4
			if sa == 0 {
				continue
			}
			dst.Pix[i] = uint8(sr / sa)
			dst.Pix[i+1] = uint8(sg / sa)
			dst.Pix[i+2] = uint8(sb / sa)
			dst.Pix[i+3] = uint8(sa / uint32(n))
		}
	}
	return dst
}

// OverNonPremul 非预乘 alpha 的 Over 合成（把 src 叠到 dst 上）。
// 为什么不用 draw.Over：PNG 解码出来是预乘，但我们的矢量中间结果是直通 RGBA，
// 逐层叠加时必须按非预乘公式算，否则半透明边缘会偏暗。
func OverNonPremul(dst *image.RGBA, src *image.RGBA, ox, oy int) {
	sb := src.Bounds()
	db := dst.Bounds()
	for y := 0; y < sb.Dy(); y++ {
		dy := oy + y
		if dy < db.Min.Y || dy >= db.Max.Y {
			continue
		}
		for x := 0; x < sb.Dx(); x++ {
			dx := ox + x
			if dx < db.Min.X || dx >= db.Max.X {
				continue
			}
			si := y*src.Stride + x*4
			sa := uint32(src.Pix[si+3])
			if sa == 0 {
				continue
			}
			di := (dy-db.Min.Y)*dst.Stride + (dx-db.Min.X)*4
			da := uint32(dst.Pix[di+3])
			// 非预乘 Over：outA = sa + da*(1-sa)，outC = (sC*sa + dC*da*(1-sa))/outA。
			// 这里 sa/da 都用 0..255 表示，先按 255 归一化再除，别把 outA 也乘 255
			// （乘了会把每一层都压成 1/255 灰，发光层叠上去会变成近黑色）。
			outA := sa + da*(255-sa)/255
			if outA == 0 {
				continue
			}
			for k := 0; k < 3; k++ {
				s := uint32(src.Pix[si+k])
				d := uint32(dst.Pix[di+k])
				num := s*sa*255 + d*da*(255-sa)
				v := num / (outA * 255)
				// outA 用整数除法预先把小数部分截掉了，分母比精确值略小，
				// 商可能是 256/257。直接 uint8() 会回绕成 0/1：发光层出现大片
				// 黑色麻点，再被模糊就变成深色网纹（2026-10-27 fly1 dirMc 实测
				// 9235 个黑像素，辉光整圈发暗）。这里显式夹到 255。
				if v > 255 {
					v = 255
				}
				dst.Pix[di+k] = uint8(v)
			}
			dst.Pix[di+3] = uint8(outA)
		}
	}
}
