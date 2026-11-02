//go:build windows

package main

import (
	"image"
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	NIM_ADD    = 0
	NIM_MODIFY = 1
	NIM_DELETE = 2

	NIF_MESSAGE = 0x01
	NIF_ICON    = 0x02
	NIF_TIP     = 0x04

	WM_TRAY         = WM_APP + 21
	IDI_APPLICATION = 32512

	menuTrayShow = 1100
	menuTrayHide = 1101
)

type NOTIFYICONDATA struct {
	CbSize            uint32
	_                 uint32
	HWnd              HWND
	UID               uint32
	UFlags            uint32
	UCallbackMessage  uint32
	_                 uint32
	HIcon             HANDLE
	SzTip             [128]uint16
	DwState           uint32
	DwStateMask       uint32
	SzInfo            [256]uint16
	UTimeoutOrVersion uint32
	SzInfoTitle       [64]uint16
	DwInfoFlags       uint32
	GuidItem          [16]byte
	HBalloonIcon      HANDLE
}

type ICONINFO struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  HBITMAP
	HbmColor HBITMAP
}

var (
	pShellNotifyIcon               = shell32.NewProc("Shell_NotifyIconW")
	pCreateBitmap                  = gdi32.NewProc("CreateBitmap")
	pCreateIconIndirect            = user32.NewProc("CreateIconIndirect")
	pDestroyIcon                   = user32.NewProc("DestroyIcon")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pRegisterWindowMessageW        = user32.NewProc("RegisterWindowMessageW")
)

var (
	trayIcon       HANDLE
	trayCreatedMsg uintptr
)

// initDPI 声明 Per-Monitor V2 DPI 感知。
// 不加这个，Windows 在 125%/150% 缩放下会把窗口当位图拉伸，画面必糊。
func initDPI() {
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	pSetProcessDpiAwarenessContext.Call(^uintptr(3))
}

// iconFromFrame 用当前帧生成托盘图标（32bpp + 全透明 mask）。
func iconFromFrame(img *image.RGBA, size int) HANDLE {
	if img == nil {
		h, _, _ := pLoadIconW.Call(0, IDI_APPLICATION)
		return HANDLE(h)
	}
	scaled := resizeFrame(img, size, size)

	hdc, _, _ := pCreateCompatibleDC.Call(0)
	defer pDeleteDC.Call(hdc)
	bi := BITMAPINFO{}
	bi.BmiHeader.BiSize = uint32(unsafe.Sizeof(bi.BmiHeader))
	bi.BmiHeader.BiWidth = int32(size)
	bi.BmiHeader.BiHeight = -int32(size)
	bi.BmiHeader.BiPlanes = 1
	bi.BmiHeader.BiBitCount = 32
	var bits unsafe.Pointer
	hbm, _, _ := pCreateDIBSection.Call(hdc, uintptr(unsafe.Pointer(&bi)), DIB_RGB_COLORS,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 || bits == nil {
		return 0
	}
	dst := unsafe.Slice((*byte)(bits), size*size*4)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			c := scaled.RGBAAt(x, y)
			a := uint32(c.A)
			i := (y*size + x) * 4
			dst[i] = uint8(uint32(c.B) * a / 255)
			dst[i+1] = uint8(uint32(c.G) * a / 255)
			dst[i+2] = uint8(uint32(c.R) * a / 255)
			dst[i+3] = c.A
		}
	}
	hmask, _, _ := pCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1, 0)
	ii := ICONINFO{FIcon: 1, HbmMask: HBITMAP(hmask), HbmColor: HBITMAP(hbm)}
	hicon, _, _ := pCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	pDeleteObject.Call(hbm)
	pDeleteObject.Call(hmask)
	return HANDLE(hicon)
}

func (g *Game) addTray() {
	g.trayAdd()
}

func (g *Game) trayAdd() {
	if trayIcon == 0 {
		trayIcon = iconFromFrame(g.currentFrame(), 32)
	}
	var nid NOTIFYICONDATA
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = g.Hwnd
	nid.UID = 1
	nid.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	nid.UCallbackMessage = WM_TRAY
	nid.HIcon = trayIcon
	tip := syscall.StringToUTF16("NONO 桌宠")
	copy(nid.SzTip[:], tip)
	pShellNotifyIcon.Call(NIM_ADD, uintptr(unsafe.Pointer(&nid)))
}

func (g *Game) removeTray() {
	var nid NOTIFYICONDATA
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = g.Hwnd
	nid.UID = 1
	pShellNotifyIcon.Call(NIM_DELETE, uintptr(unsafe.Pointer(&nid)))
	if trayIcon != 0 {
		pDestroyIcon.Call(uintptr(trayIcon))
		trayIcon = 0
	}
}

func (g *Game) showTrayMenu() {
	pt := POINT{}
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	h, _, _ := pCreatePopupMenu.Call()
	hm := HMENU(h)
	// 关机态留开机入口（20261029-01 起本体窗口不再隐藏，这条不再是唯一路径，
	// 但托盘是最稳的一条：主面板要先把本体点开才够得着）。
	if g != nil && !g.Save.Open {
		appendStr(hm, menuOpenNono, "开机 NONO", false)
		appendStr(hm, 0, "", true)
	}
	appendStr(hm, menuInfo, "NONO 信息面板", false)
	appendStr(hm, menuSetting, "设置…", false)
	appendStr(hm, 0, "", true)
	appendStr(hm, menuReset, "恢复默认外观", false)
	appendStr(hm, menuQuit, "退出", false)
	pSetForegroundWindow.Call(uintptr(g.Hwnd))
	cmd, _, _ := pTrackPopupMenu.Call(uintptr(hm), TPM_RETURNCMD|TPM_RIGHTBUTTON,
		uintptr(pt.X), uintptr(pt.Y), 0, uintptr(g.Hwnd), 0)
	pDestroyMenu.Call(uintptr(hm))
	switch int(cmd) {
	case menuOpenNono:
		if g != nil {
			g.turnOn()
		}
	case menuInfo:
		// 20261027-10：旧 GDI 信息面板已删除，统一打开复刻版基本资料面板。
		showInfoPanel()
	case menuSetting:
		g.showSettings()
	case menuReset:
		g.resetAppearance()
	case menuQuit:
		g.Save.write()
		pDestroyWindow.Call(uintptr(g.Hwnd))
	}
}

// ---- 音效 ----

const (
	SND_ASYNC     = 0x0001
	SND_NODEFAULT = 0x0002
	SND_FILENAME  = 0x00020000
)

var (
	winmm       = syscall.NewLazyDLL("winmm.dll")
	pPlaySoundW = winmm.NewProc("PlaySoundW")
	pTimeBeginPeriod = winmm.NewProc("timeBeginPeriod")
)

// playSFX 异步播放 assets/sfx 下的 wav；Sound 关闭时直接返回。
// PlaySound 是单通道，新音效会打断旧音效，对桌宠足够。
func (g *Game) playSFX(name string) {
	if g.Save == nil || !g.Save.Sound || name == "" {
		return
	}
	p := filepath.Join(assetsRoot(), "sfx", name)
	pPlaySoundW.Call(uintptr(unsafe.Pointer(utf16ptr(p))), 0,
		SND_FILENAME|SND_ASYNC|SND_NODEFAULT)
}
