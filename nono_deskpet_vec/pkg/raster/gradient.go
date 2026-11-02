package raster

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/vector"

	"nonovec/pkg/pathd"
)

// FillShaded 用逐像素回调着色填充多边形。
// 先把覆盖率画进 alpha mask，再对 mask 覆盖到的像素求色。
// shade 收到的是「目标位图坐标系」的像素中心坐标。
func FillShaded(dst *image.RGBA, polys [][]pathd.Point, shade func(x, y float64) color.NRGBA) {
	if len(polys) == 0 {
		return
	}
	b := dst.Bounds()
	mask := image.NewAlpha(b)
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
	r.Draw(mask, b, image.NewUniform(color.Alpha{A: 255}), image.Point{})

	for y := b.Min.Y; y < b.Max.Y; y++ {
		mi := (y - b.Min.Y) * mask.Stride
		di := (y - b.Min.Y) * dst.Stride
		for x := b.Min.X; x < b.Max.X; x++ {
			ma := uint32(mask.Pix[mi+(x-b.Min.X)])
			if ma == 0 {
				continue
			}
			c := shade(float64(x)+0.5, float64(y)+0.5)
			sa := uint32(c.A) * ma / 255
			if sa == 0 {
				continue
			}
			i := di + (x-b.Min.X)*4
			da := uint32(dst.Pix[i+3])
			// Go 的 image.RGBA 是「预乘 alpha」：存的是 color*alpha/255。
			// 源色 c 是非预乘（NRGBA 语义），所以 Over 必须先转预乘再相加：
			// 本项目的中间缓冲一律是「非预乘 alpha」（见 Downsample 的按 alpha 加权求色），
			// 所以这里也按非预乘 Over 算：
			//   outA = sa + da*(1-sa)
			//   outC = (c*sa + d*da*(1-sa)) / outA
			// 定点写法（outA 已是 0..255）：
			//   outA = sa + da*(255-sa)/255
			//   outC = (c*sa + d*da*(255-sa)/255) / outA
			// 旧版写的是 (...)/outA/255，多除了一个 255，
			// 颜色被压到 1/255 量级，49 处渐变全部渲染成近黑色。
			outA := sa + da*(255-sa)/255
			if outA == 0 {
				continue
			}
			nr := uint8((uint32(c.R)*sa + uint32(dst.Pix[i])*da*(255-sa)/255) / outA)
			ng := uint8((uint32(c.G)*sa + uint32(dst.Pix[i+1])*da*(255-sa)/255) / outA)
			nb := uint8((uint32(c.B)*sa + uint32(dst.Pix[i+2])*da*(255-sa)/255) / outA)
			dst.Pix[i] = nr
			dst.Pix[i+1] = ng
			dst.Pix[i+2] = nb
			dst.Pix[i+3] = uint8(outA)
		}
	}
}

// Mat3 2x3 仿射矩阵 [a b c d e f]：x' = a*x + c*y + e，y' = b*x + d*y + f。
type Mat3 [6]float64

// Invert 求逆，不可逆时返回 false。
func (m Mat3) Invert() (Mat3, bool) {
	det := m[0]*m[3] - m[1]*m[2]
	if math.Abs(det) < 1e-12 {
		return Mat3{}, false
	}
	inv := Mat3{
		m[3] / det, -m[1] / det,
		-m[2] / det, m[0] / det,
		0, 0,
	}
	inv[4] = -(inv[0]*m[4] + inv[2]*m[5])
	inv[5] = -(inv[1]*m[4] + inv[3]*m[5])
	return inv, true
}

// Apply 变换一个点。
func (m Mat3) Apply(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}
