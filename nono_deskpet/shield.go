//go:build windows

package main

import (
	"image"
	"os"
	"path/filepath"
	"sync"

	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

// 超能屏障（700012）的可见效果，用游戏原资源而不是程绘。
//
// 出处：`resource/eff/shotEffect.swf` 的导出符号 `pk_nono_shield`（characterId 284）。
// 游戏端 `BasePeoleModel.showNonoShield()` 拿到这个 MC 后只做 `gotoAndStop(1)` 再
// addChild，也就是说**实机上是一张静止的首帧**，36 帧的补间不会播。所以这里同样
// 只渲染 default/frame0，不做帧动画——多播一帧都不算还原。
//
// 画布 133.334×128.18、锚点 (72.35, 89.985)，球罩直径约 100 单位，正好套住
// NONO 99px 的球壳（1 单位 = 1 屏幕像素）。
type shieldTex struct {
	img *image.RGBA
	k   float64 // 源图像素 / 画布单位（渲染倍率）
	ax  float64 // 锚点（源图像素）
	ay  float64
	w   float64 // 画布宽（单位）
	h   float64
	// ballDia 球罩外径（画布单位）。由 shape_282 层实测：导出后只有 58 单位，
	// 直接按画布缩放会比 NONO 的 99 单位球小一号，必须按球径归一再上屏。
	ballDia float64
}

var (
	shieldOnce     sync.Once
	shieldImg      *shieldTex
	shieldLogOnce  sync.Once
)

// loadShieldTex 懒加载游戏护盾首帧。素材缺失时返回 nil，调用方回退程绘椭圆。
func loadShieldTex() *shieldTex {
	shieldOnce.Do(func() {
		dir := filepath.Join(assetsRoot(), "sprites", "shield")
		sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
		if err != nil {
			return
		}
		lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
		if err != nil {
			return
		}
		const k, ss = 2.0, 2
		r := sprite.New(sp, lib)
		img, err := r.RenderFullCanvas("default", 0, k, ss)
		if err != nil || img == nil {
			return
		}
		// 量球罩外径：只渲染球壳层（shape_282），取不透明像素包围盒宽度。
		ballDia := 0.0
		if layer, lerr := r.RenderLayerFullCanvas("default", 0, 1, 1, "shape_282"); lerr == nil && layer != nil {
			bb := layer.Bounds()
			minX, maxX := bb.Max.X, bb.Min.X-1
			for y := bb.Min.Y; y < bb.Max.Y; y++ {
				for x := bb.Min.X; x < bb.Max.X; x++ {
					if _, _, _, a := layer.At(x, y).RGBA(); a > 8000 {
						if x < minX {
							minX = x
						}
						if x > maxX {
							maxX = x
						}
					}
				}
			}
			if maxX >= minX {
				ballDia = float64(maxX - minX + 1)
			}
		}
		if ballDia <= 0 {
			ballDia = sp.Canvas.W * 0.44 // 兜底：经验比例
		}
		shieldImg = &shieldTex{
			img: img, k: k,
			ax: sp.Canvas.Anchor[0] * k,
			ay: sp.Canvas.Anchor[1] * k,
			w:  sp.Canvas.W, h: sp.Canvas.H, ballDia: ballDia,
		}
	})
	return shieldImg
}

// drawShieldSprite 把护盾首帧按「画布锚点 = 本体球心」贴到 canvas 上。
//
// cx/cy：本体球心在 canvas 里的像素坐标（与 drawShield 同一口径）。
// targetBallPx：本体球在屏幕上的直径（像素）。护盾球罩按它归一，保证
// 「护盾球 ≈ 本体球 × 1.06」，不随素材导出倍率漂移。
// alpha：整体透明度，供淡入淡出用。
//
// 返回 false 表示素材不可用，调用方应当回退到程绘椭圆（保证功能不因缺图而消失）。
func drawShieldSprite(canvas *image.RGBA, cx, cy, targetBallPx, alpha float64) bool {
	t := loadShieldTex()
	if t == nil || t.img == nil || t.k <= 0 || targetBallPx <= 0 || t.ballDia <= 0 {
		return false
	}
	// 1 单位显示成多少像素：让球罩外径 = 本体球径 × 1.06
	scale := targetBallPx * 1.06 / t.ballDia
	if os.Getenv("NONO_LOG") != "" {
		shieldLogOnce.Do(func() {
			logf("shield: ballDia=%.1fu targetBall=%.1fpx scale=%.4f drawW=%.1fpx anchor=(%.1f,%.1f)u",
				t.ballDia, targetBallPx, scale, t.w*scale, t.ax/t.k, t.ay/t.k)
		})
	}
	src := t.img
	sb := src.Bounds()
	// 目标矩形：画布单位 × 缩放系数；左上角由锚点反推。
	dw := t.w * scale
	dh := t.h * scale
	ox := cx - t.ax/t.k*scale
	oy := cy - t.ay/t.k*scale

	x0 := int(ox)
	y0 := int(oy)
	x1 := int(ox + dw + 0.999)
	y1 := int(oy + dh + 0.999)
	cb := canvas.Bounds()
	if x0 < cb.Min.X {
		x0 = cb.Min.X
	}
	if y0 < cb.Min.Y {
		y0 = cb.Min.Y
	}
	if x1 > cb.Max.X {
		x1 = cb.Max.X
	}
	if y1 > cb.Max.Y {
		y1 = cb.Max.Y
	}
	if x1 <= x0 || y1 <= y0 {
		return true // 素材在，只是被窗口裁没了
	}

	// 源→目标步长（单位：源像素/目标像素）
	stepX := t.k / scale
	stepY := t.k / scale
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}

	for y := y0; y < y1; y++ {
		sy := (float64(y) + 0.5 - oy) * stepY
		if sy < 0 {
			sy = 0
		}
		if sy > float64(sb.Dy()-1) {
			sy = float64(sb.Dy() - 1)
		}
		y0i := int(sy)
		y1i := y0i + 1
		if y1i >= sb.Dy() {
			y1i = sb.Dy() - 1
		}
		fy := sy - float64(y0i)
		rowDst := canvas.Pix[y*canvas.Stride:]

		for x := x0; x < x1; x++ {
			sx := (float64(x) + 0.5 - ox) * stepX
			if sx < 0 {
				sx = 0
			}
			if sx > float64(sb.Dx()-1) {
				sx = float64(sb.Dx() - 1)
			}
			x0i := int(sx)
			x1i := x0i + 1
			if x1i >= sb.Dx() {
				x1i = sb.Dx() - 1
			}
			fx := sx - float64(x0i)

			// 四邻双线性
			i00 := (y0i-sb.Min.Y)*src.Stride + (x0i-sb.Min.X)*4
			i10 := (y0i-sb.Min.Y)*src.Stride + (x1i-sb.Min.X)*4
			i01 := (y1i-sb.Min.Y)*src.Stride + (x0i-sb.Min.X)*4
			i11 := (y1i-sb.Min.Y)*src.Stride + (x1i-sb.Min.X)*4
			w00 := (1 - fx) * (1 - fy)
			w10 := fx * (1 - fy)
			w01 := (1 - fx) * fy
			w11 := fx * fy

			sa := (float64(src.Pix[i00+3])*w00 + float64(src.Pix[i10+3])*w10 +
				float64(src.Pix[i01+3])*w01 + float64(src.Pix[i11+3])*w11) / 255
			if sa <= 0.002 {
				continue
			}
			sr := float64(src.Pix[i00])*w00 + float64(src.Pix[i10])*w10 +
				float64(src.Pix[i01])*w01 + float64(src.Pix[i11])*w11
			sg := float64(src.Pix[i00+1])*w00 + float64(src.Pix[i10+1])*w10 +
				float64(src.Pix[i01+1])*w01 + float64(src.Pix[i11+1])*w11
			sbl := float64(src.Pix[i00+2])*w00 + float64(src.Pix[i10+2])*w10 +
				float64(src.Pix[i01+2])*w01 + float64(src.Pix[i11+2])*w11

			a := sa * alpha
			di := (x - cb.Min.X) * 4
			da := float64(rowDst[di+3]) / 255
			outA := a + da*(1-a)
			if outA <= 0 {
				continue
			}
			// 源是直通 alpha（非预乘），按 over 公式合成
			rowDst[di] = clamp8((sr*a + float64(rowDst[di])*da*(1-a)) / outA)
			rowDst[di+1] = clamp8((sg*a + float64(rowDst[di+1])*da*(1-a)) / outA)
			rowDst[di+2] = clamp8((sbl*a + float64(rowDst[di+2])*da*(1-a)) / outA)
			rowDst[di+3] = clamp8(outA * 255)
		}
	}
	return true
}
