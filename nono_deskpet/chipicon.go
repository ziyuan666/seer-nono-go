//go:build windows

package main

// 自定义芯片的图标合成。
//
// 原版每种变色芯片都有一张独立 PNG（assets/ui/chips/700301.png …），
// 内部色块已经是成品色。本地新增的两枚芯片颜色是用户随时改的，不可能预出图，
// 所以拆成「外壳」+「可上色蒙版」两张底图，运行时按当前颜色合成：
//   _custom_base.png = 原版 700301 整张图（外壳、描边、高光原样）
//   _custom_mask.png = 内部色块区域（白=可上色，黑=保留外壳）
// 上色时按原图的亮度比例缩放目标色，保留内部原有的明暗与条纹，
// 不会糊成一块死色。

import (
	"fmt"
	"image"
)

var customIconCache = map[string]*image.RGBA{}

// renderCustomChipIcon 合成 700313/700314 的图标。
// grad 为 true 时按竖直方向从 c1 过渡到 c2（与本体渐变方向一致）。
func renderCustomChipIcon(grad bool, c1, c2 uint32, mode int) *image.RGBA {
	key := fmt.Sprintf("%v:%06X:%06X:%d", grad, c1, c2, mode)
	if im, ok := customIconCache[key]; ok {
		return im
	}
	base := uiLoad("chips/_custom_base.png")
	mask := uiLoad("chips/_custom_mask.png")
	if base == nil || mask == nil {
		return nil
	}
	dst := image.NewRGBA(base.Bounds())
	copy(dst.Pix, base.Pix)
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()

	// 原色块的平均亮度，用来把目标色还原到「同样的明暗档位」。
	const ref = 190.0
	ar, ag, ab := uint8(c1>>16), uint8(c1>>8), uint8(c1)
	br, bg, bb := uint8(c2>>16), uint8(c2>>8), uint8(c2)

	mix := func(x, y int) (uint8, uint8, uint8) {
		if !grad {
			return ar, ag, ab
		}
		var t float64
		switch mode {
		case 1:
			t = float64(x) / float64(w)
		case 2:
			t = (float64(x)/float64(w) + float64(y)/float64(h)) / 2
		default:
			t = float64(y) / float64(h)
		}
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
		f := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }
		return f(ar, br), f(ag, bg), f(ab, bb)
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*dst.Stride + x*4
			if mask.Pix[i+3] == 0 || mask.Pix[i] < 128 {
				continue
			}
			// 原像素亮度决定明暗倍率（内部有条纹与高光，直接覆盖会变平）。
			r0, g0, b0 := base.Pix[i], base.Pix[i+1], base.Pix[i+2]
			lum := r0
			if g0 > lum {
				lum = g0
			}
			if b0 > lum {
				lum = b0
			}
			k := float64(lum) / ref
			if k > 1.25 {
				k = 1.25
			}
			tr, tg, tb := mix(x, y)
			scale := func(c uint8) uint8 {
				v := float64(c) * k
				if v > 255 {
					v = 255
				}
				return uint8(v + 0.5)
			}
			dst.Pix[i] = scale(tr)
			dst.Pix[i+1] = scale(tg)
			dst.Pix[i+2] = scale(tb)
		}
	}
	customIconCache[key] = dst
	return dst
}

// chipIconImage 返回储藏/仓库格子里要显示的芯片图标。
// 自定义两枚走运行时合成，其余仍读 assets/ui/chips/<id>.png。
func chipIconImage(id int) *image.RGBA {
	switch id {
	case 700313:
		c := uint32(0x33CCFF)
		if g != nil && g.Save != nil {
			c = g.Save.ColorCustom
		}
		return renderCustomChipIcon(false, c, c, 0)
	case 700314:
		a, b, m := uint32(0x33CCFF), uint32(0x8F31E1), 0
		if g != nil && g.Save != nil {
			a, b, m = g.Save.GradA, g.Save.GradB, g.Save.GradMode
		}
		return renderCustomChipIcon(true, a, b, m)
	}
	return uiLoad("chips/" + itoa(id) + ".png")
}
