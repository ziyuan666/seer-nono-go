//go:build windows

package main

// 面板 tooltip：原版用 ToolTipManager.add(目标, 文案) 在鼠标旁弹出白底小框
// （NewNonoInfoPanel.as:235 "修改昵称"、:309 "N级超能NoNo"、:344 "曾经超能N级"）。
//
// 实现：一个常驻的分层小窗（WS_EX_NOACTIVATE|WS_EX_TRANSPARENT，不抢焦点、
// 鼠标完全穿透），背景按文案宽度现画白底圆角框，超出部分保持全透明。

import (
	"image"
	"unsafe"
)

var (
	tipWin     *uiWin
	tipText    string
	gwlExStyle = ^uintptr(19) // GWL_EXSTYLE = -20
)

// 鼠标穿透 + 不激活 + 置顶 + 分层
const tipExStyle = 0x00080000 | 0x00000080 | 0x00000008 | 0x08000000 | 0x00000020

// makeTipBg 画一个白底、灰色细边、圆角的提示框，宽度外全透明。
func makeTipBg(w, h, boxW int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := 3
	if boxW > w {
		boxW = w
	}
	inside := func(x, y, x0, y0, x1, y1, rad int) bool {
		if x < x0 || x >= x1 || y < y0 || y >= y1 {
			return false
		}
		cx := x0 + rad
		cy := y0 + rad
		if x < cx && y < cy {
			return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rad*rad
		}
		cx = x1 - 1 - rad
		if x > cx && y < cy {
			return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rad*rad
		}
		cy = y1 - 1 - rad
		if x < x0+rad && y > cy {
			return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rad*rad
		}
		cx = x0 + rad
		if x < cx && y > cy {
			return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rad*rad
		}
		cx = x1 - 1 - rad
		if x > cx && y > cy {
			return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rad*rad
		}
		return true
	}
	for y := 0; y < h; y++ {
		for x := 0; x < boxW; x++ {
			i := img.PixOffset(x, y)
			// 外圈 1px 灰边，里面白底
			if inside(x, y, 0, 0, boxW, h, r) {
				if inside(x, y, 1, 1, boxW-1, h-1, r) {
					img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 255, 255, 255, 255
				} else {
					img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 140, 140, 140, 255
				}
			}
		}
	}
	return img
}

// showInfoTip 在鼠标右下方弹出提示；text 为空或与当前相同则不动。
func showInfoTip(text string) {
	if text == "" {
		hideInfoTip()
		return
	}
	if tipWin == nil {
		tipWin = newUIWin(140, 24)
		if tipWin == nil {
			return
		}
		pSetWindowLongPtrW.Call(uintptr(tipWin.Hwnd), gwlExStyle, tipExStyle)
	}
	s := tipWin.S
	if s <= 0 {
		s = 1
	}
	dw := int(float64(uiTextWidth(tipWin.R.Hdc, text, 12, false))*s + 0.5) + int(14*s)
	if dw > tipWin.W {
		dw = tipWin.W
	}
	tipWin.Bg = makeTipBg(tipWin.W, tipWin.H, dw)
	tipWin.Labels = []uiLabel{{Text: text, X: 6, Y: 4, Size: 12, Color: 0x00000000}}
	tipText = text
	pt := POINT{}
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	tipWin.X, tipWin.Y = int(pt.X)+14, int(pt.Y)+16
	pShowWindow.Call(uintptr(tipWin.Hwnd), 4) // SW_SHOWNOACTIVATE
	tipWin.paint()                            // 位置靠 UpdateLayeredWindow 的 dstPoint 生效
}

func hideInfoTip() {
	if tipWin != nil && tipWin.Hwnd != 0 {
		pShowWindow.Call(uintptr(tipWin.Hwnd), 0) // SW_HIDE
	}
	tipText = ""
}
