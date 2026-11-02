//go:build windows

package main

// 本地新增：自定义颜色芯片 / 自定义渐变芯片用的系统取色器。
//
// 为什么不自绘调色面板：Win32 的 ChooseColorW 是系统标准对话框，交互、放大镜、
// 自定义色槽都是用户熟悉的；自绘一套「色相条 + 明度方块」在 133px 的芯片环里
// 体验反而更差。渐变芯片只是连弹两次取色器（起色、止色）。
//
// 注意 COLORREF 是 0x00BBGGRR，而存档里的 Color 是 0xRRGGBB，进出都要换字节序。

import (
	"syscall"
	"unsafe"
)

const (
	ccRGBInit  = 0x00000001
	ccFullOpen = 0x00000002
	ccAnyColor = 0x00000100
)

type chooseColorW struct {
	lStructSize    uint32
	hwndOwner      HWND
	hInstance      HINSTANCE
	rgbResult      uint32
	lpCustColors   *uint32
	flags          uint32
	lCustData      uintptr
	lpfnHook       uintptr
	lpTemplateName uintptr
}

var (
	comdlg32      = syscall.NewLazyDLL("comdlg32.dll")
	pChooseColorW = comdlg32.NewProc("ChooseColorW")
	// 16 个自定义色槽跨次保留，用户在对话框里存过的颜色不会丢。
	ccCustomSlots [16]uint32
)

// toColorRef / fromColorRef 在 0xRRGGBB 与 Win32 的 COLORREF 之间互转。
func toColorRef(c uint32) uint32 {
	return (c >> 16 & 0xFF) | (c >> 8 & 0xFF00) | (c&0xFF)<<16
}

func fromColorRef(c uint32) uint32 {
	return toColorRef(c)
}

// pickColor 弹出系统取色器。用户取消时返回 ok=false，调用方必须保持原色不变。
func pickColor(initial uint32) (uint32, bool) {
	cc := chooseColorW{
		lStructSize:  uint32(unsafe.Sizeof(chooseColorW{})),
		rgbResult:    toColorRef(initial),
		lpCustColors: &ccCustomSlots[0],
		flags:        ccRGBInit | ccFullOpen | ccAnyColor,
	}
	if mainPanelWin != nil && mainPanelWin.Visible {
		cc.hwndOwner = mainPanelWin.Hwnd
	}
	r, _, _ := pChooseColorW.Call(uintptr(unsafe.Pointer(&cc)))
	if r == 0 {
		return initial, false
	}
	return fromColorRef(cc.rgbResult), true
}
