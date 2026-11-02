//go:build windows

package main

import (
	"fmt"
	"image"
	"os"
	"syscall"
	"unsafe"
)

const (
	// 20261027-10：旧 GDI 信息面板（panelInfo）已删除，NONO 面板只保留设置这一种。
	panelSet = 2
)

type Hit struct {
	RC     RECT
	Action string
}

var panel = &PanelState{}

type PanelState struct {
	Hwnd HWND
	Kind int
	Hits []Hit
}

func (p *PanelState) ensureWindow() {
	if p.Hwnd != 0 {
		return
	}
	hInst, _, _ := pGetModuleHandleW.Call(0)
	cls := utf16ptr("NonoPanelWnd")
	wc := WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEX{})),
		LpfnWndProc:   syscall.NewCallback(panelProc),
		HInstance:     HINSTANCE(hInst),
		LpszClassName: cls,
	}
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	hwnd, _, _ := pCreateWindowExW.Call(
		WS_EX_TOOLWINDOW|WS_EX_TOPMOST,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(utf16ptr("NONO"))),
		WS_OVERLAPPED|WS_CAPTION|WS_SYSMENU,
		420, 60, 460, 500, 0, 0, hInst, 0)
	p.Hwnd = HWND(hwnd)
	logf("panel hwnd=%d", hwnd)
}

// 20261027-10：旧 GDI 信息面板 showInfo() 已删除（子渊：「我有正常 NONO 主面板去显示信息，
// 这些没必要」）。资料信息统一走 panel_info.go 的 showInfoPanel()（1:1 复刻 NewNonoInfoPanel）。
//
func (g *Game) showSettings() {
	panel.ensureWindow()
	panel.Kind = panelSet
	pSetWindowTextW.Call(uintptr(panel.Hwnd), uintptr(unsafe.Pointer(utf16ptr("NONO 设置"))))
	pShowWindow.Call(uintptr(panel.Hwnd), SW_SHOW)
	pSetWindowPos.Call(uintptr(panel.Hwnd), 0, 0, 0, 460, 500, SWP_NOMOVE|SWP_NOZORDER|SWP_SHOWWINDOW)
	logf("panel show hwnd=%d", panel.Hwnd)
	pSetForegroundWindow.Call(uintptr(panel.Hwnd))
	pInvalidateRect.Call(uintptr(panel.Hwnd), 0, 1)
}

func panelProc(hwnd HWND, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case WM_PAINT:
		panel.paint(hwnd)
		return 0
	case WM_LBUTTONUP:
		x := GET_X_LPARAM(lp)
		y := GET_Y_LPARAM(lp)
		for _, h := range panel.Hits {
			if x >= int(h.RC.Left) && x <= int(h.RC.Right) &&
				y >= int(h.RC.Top) && y <= int(h.RC.Bottom) {
				doPanelAction(h.Action)
				return 0
			}
		}
		return 0
	case WM_CLOSE:
		pShowWindow.Call(uintptr(hwnd), SW_HIDE)
		return 0
	case WM_DESTROY:
		panel.Hwnd = 0
		return 0
	}
	ret, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wp, lp)
	return ret
}

func doPanelAction(a string) {
	switch a {
	case "close":
		pShowWindow.Call(uintptr(panel.Hwnd), SW_HIDE)
		return
	case "reset":
		g.resetAppearance()
	case "scale-":
		s := g.Save.Scale - 0.5
		if s < 1 {
			s = 1
		}
		g.SetScale(s)
	case "scale+":
		s := g.Save.Scale + 0.5
		if s > 4 {
			s = 4
		}
		g.SetScale(s)
	case "top":
		g.Save.Topmost = !g.Save.Topmost
		idx := uintptr(1) // HWND_NOTOPMOST
		if g.Save.Topmost {
			idx = 0 // HWND_TOPMOST = -1，用 SetWindowPos 的 hWndInsertAfter
		}
		var after uintptr = 1
		if g.Save.Topmost {
			after = ^uintptr(0) // -1
		}
		pSetWindowPos.Call(uintptr(g.Hwnd), after, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE)
		_ = idx
	case "walk":
		g.Save.AutoWalk = !g.Save.AutoWalk
	case "bubble":
		g.Save.Bubbles = !g.Save.Bubbles
	case "follow":
		g.Save.Follow = !g.Save.Follow
	case "fly":
		g.Save.Fly = !g.Save.Fly
	case "shield":
		g.Save.Shield = !g.Save.Shield
	case "glow":
		g.Save.Glow = !g.Save.Glow
	case "annual":
		// 开关式：开 = 超能 13 级（至尊 annual），关 = 回到普通形态。
		// 走 setSuperLevel 而不是直接写字段：它负责 Super 标志、落盘、
		// applyForm 换素材和 refreshPanelsAfterForm 刷新面板，别绕过去。
		if g.Save.SuperLevel >= 13 {
			g.setSuperLevel(0)
		} else {
			g.setSuperLevel(13)
		}
		return
	case "sound":
		g.Save.Sound = !g.Save.Sound
	case "autostart":
		setAutoStart(!autoStartEnabled())
	default:
		// 2026-10-27 子渊：帧间隔面板已移除，这里不再处理 idle-*/move-* 动作。
		// 存档字段 IdleMs/MoveMs 保留（旧档兼容），但没有任何 UI 入口可改。
	}
	g.Save.write()
	g.draw()
	pInvalidateRect.Call(uintptr(panel.Hwnd), 0, 1)
}

// ---- 绘制 ----

func setFont(hdc HDC, size int, bold bool) HFONT {
	w := uintptr(FW_NORMAL)
	if bold {
		w = FW_BOLD
	}
	// 第 12 个参数是 fdwQuality：用 ANTIALIASED_QUALITY(4) 让文字边缘带灰度过渡，
	// 否则在分层窗口的 DIB 上画出的是硬边点阵，放大后明显发毛。
	f, _, _ := pCreateFontW.Call(uintptr(size), 0, 0, 0, w, 0, 0, 0,
		DEFAULT_CHARSET, 0, 0, 4, 0, uintptr(unsafe.Pointer(utf16ptr("Microsoft YaHei"))))
	return HFONT(f)
}

func text(hdc HDC, s string, x, y, size int, bold bool, color uint32) {
	f := setFont(hdc, size, bold)
	old, _, _ := pSelectObject.Call(uintptr(hdc), uintptr(f))
	pSetBkMode.Call(uintptr(hdc), TRANSPARENT)
	pSetTextColor.Call(uintptr(hdc), uintptr(color))
	wt, _ := syscall.UTF16FromString(s)
	pTextOutW.Call(uintptr(hdc), uintptr(x), uintptr(y),
		uintptr(unsafe.Pointer(&wt[0])), uintptr(len(wt)-1))
	pSelectObject.Call(uintptr(hdc), old)
	pDeleteObject.Call(uintptr(f))
}

func fill(hdc HDC, x, y, w, h int, color uint32) {
	b, _, _ := pCreateSolidBrush.Call(uintptr(color))
	rc := RECT{int32(x), int32(y), int32(x + w), int32(y + h)}
	pFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(&rc)), b)
	pDeleteObject.Call(b)
}

func box(hdc HDC, x, y, w, h int, color uint32) {
	fill(hdc, x, y, w, h, color)
}

// button 画一个淡蓝按钮并登记点击区域。
func button(hdc HDC, p *PanelState, label, action string, x, y, w, h int) {
	fill(hdc, x, y, w, h, 0x00F0D8B0) // COLORREF: 淡蓝
	box(hdc, x, y, w, 2, 0x00C8A070)
	text(hdc, label, x+10, y+h/2-9, 15, false, 0x00402010)
	p.Hits = append(p.Hits, Hit{RC: RECT{int32(x), int32(y), int32(x + w), int32(y + h)}, Action: action})
}

func (p *PanelState) paint(hwnd HWND) {
	var ps PAINTSTRUCT
	hdcRaw, _, _ := pBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	hdc := HDC(hdcRaw)
	p.Hits = nil
	var rc RECT
	pGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rc)))
	cw := int(rc.Right)
	fill(hdc, 0, 0, cw, int(rc.Bottom), 0x00FBF0E4) // 淡蓝背景

	// 20261027-10：旧信息面板分支已删除，这里只剩设置面板一种布局。
	p.paintSettings(hdc, cw)
	pEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
}

func (p *PanelState) paintSettings(hdc HDC, cw int) {
	s := g.Save
	text(hdc, "NONO 设置", 20, 14, 20, true, 0x00603010)
	text(hdc, fmt.Sprintf("缩放：%.1fx", s.Scale), 20, 50, 14, false, 0x00504030)
	button(hdc, p, "－", "scale-", 130, 44, 40, 26)
	button(hdc, p, "＋", "scale+", 180, 44, 40, 26)
	text(hdc, "（缩放会改变外观，可用下方按钮一键恢复）", 20, 76, 12, false, 0x00706050)

	items := []struct {
		label  string
		action string
		on     bool
	}{
		{"窗口置顶", "top", s.Topmost},
		{"自动走动", "walk", s.AutoWalk},
		{"表情气泡", "bubble", s.Bubbles},
		{"跟随鼠标", "follow", s.Follow},
		{"悬浮模式", "fly", s.Fly},
		{"超能屏障", "shield", s.Shield},
		{"超能炫光", "glow", s.Glow},
		// 至尊形态 = 超能 13 级（游戏客户端 annual 分支，服务端未开放）
		{"至尊形态", "annual", s.SuperLevel >= 13},
		{"音效", "sound", s.Sound},
		// 「移动帧率」已移除：原版 NonoModel 对每个子 MC 只做 gotoAndStop(方向)，
		// 待机与移动都没有帧循环，没有可调的帧率档位（2026-10-24 严格原版）。
		{"开机自启", "autostart", autoStartEnabled()},
	}
	y := 104
	for _, it := range items {
		box(hdc, 22, y+3, 14, 14, 0x00C8A070)
		if it.on {
			fill(hdc, 25, y+6, 8, 8, 0x002080D0)
		}
		text(hdc, it.label, 46, y, 14, false, 0x00504030)
		text(hdc, onOff(it.on), 160, y, 14, false, 0x002060A0)
		p.Hits = append(p.Hits, Hit{RC: RECT{20, int32(y - 4), 220, int32(y + 20)}, Action: it.action})
		y += 28
	}

	y += 6
	// 2026-10-27 子渊：「这个面板可以取消了，使命完成」——待机/移动帧间隔
	// 整块（7 形态 × 2 列加减控件）已删除。存档里的 IdleMs/MoveMs 字段保留，
	// frameMsFor 仍会读取（旧档不会失效），但界面上不再有任何入口修改它们。
	y += 0

	button(hdc, p, "恢复默认外观", "reset", 20, y+10, 130, 30)
	button(hdc, p, "关闭", "close", cw-100, y+10, 80, 30)
}

// snapshotSettingsPanel 把设置面板离屏渲染成 BMP，用于无图形环境下的自测取证。
// 20261027-10：旧信息面板快照随面板一起删除，只剩设置一种布局，故去掉 kind 参数。
func snapshotSettingsPanel(path string) {
	// 设置面板「动画速度」7 行（含至尊）+ 至尊形态开关，需要 700 高，否则按钮被截。
	w, h := 460, 500
	r := newRenderer(w, h)
	defer r.release()
	fill(r.Hdc, 0, 0, w, h, 0x00FBF0E4)
	panel.paintSettings(r.Hdc, w)
	saveBMP(r, path)
}

// saveBMP 写 32 位 BMP（自上而下），alpha 统一补 255。
func saveBMP(r *Renderer, path string) {
	if r.Bits == nil {
		return
	}
	src := unsafe.Slice((*byte)(r.Bits), r.W*r.H*4)
	rowSize := r.W * 4
	imgSize := rowSize * r.H
	hdr := make([]byte, 54)
	hdr[0], hdr[1] = 'B', 'M'
	putU32(hdr[2:], uint32(54+imgSize))
	putU32(hdr[10:], 54)
	putU32(hdr[14:], 40)
	putU32(hdr[18:], uint32(r.W))
	putI32(hdr[22:], -int32(r.H))
	putU16(hdr[26:], 1)
	putU16(hdr[28:], 32)
	putU32(hdr[34:], uint32(imgSize))
	buf := make([]byte, 54+imgSize)
	copy(buf, hdr)
	for i := 0; i < r.W*r.H; i++ {
		buf[54+i*4] = src[i*4]
		buf[54+i*4+1] = src[i*4+1]
		buf[54+i*4+2] = src[i*4+2]
		buf[54+i*4+3] = 255
	}
	os.WriteFile(path, buf, 0644)
	logf("snapshot %s", path)
}

func putU32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}
func putI32(b []byte, v int32) { putU32(b, uint32(v)) }
func putU16(b []byte, v uint16) {
	b[0], b[1] = byte(v), byte(v>>8)
}

// saveRGBA 把合成帧写成 32 位 BMP（自上而下，BGRA）。
func saveRGBA(img *image.RGBA, path string) {
	if img == nil {
		return
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	imgSize := w * h * 4
	buf := make([]byte, 54+imgSize)
	buf[0], buf[1] = 'B', 'M'
	putU32(buf[2:], uint32(54+imgSize))
	putU32(buf[10:], 54)
	putU32(buf[14:], 40)
	putU32(buf[18:], uint32(w))
	putI32(buf[22:], -int32(h))
	putU16(buf[26:], 1)
	putU16(buf[28:], 32)
	putU32(buf[34:], uint32(imgSize))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(img.Bounds().Min.X+x, img.Bounds().Min.Y+y)
			i := 54 + (y*w+x)*4
			buf[i], buf[i+1], buf[i+2], buf[i+3] = c.B, c.G, c.R, c.A
		}
	}
	os.WriteFile(path, buf, 0644)
}


// defaultMsFor 返回某形态动画帧时长的出厂值（0=plain，1..5=super1..5）。
// 与 Game.frameMsFor 的回落值保持一致，避免面板显示与实际播放对不上。
func defaultMsFor(idx int) int {
	if idx == 0 {
		return int(plainDefaultMs)
	}
	return int(idleFrameMs)
}

// formIdxOfLevel 把存档里的 SuperLevel 映射成动画速度表的下标（0=plain，1..5=super1..5）。
// 口径与 assets.go 的 SuperLevel→形态映射一致：1-3→super1、4-6→super2、
// 7-8→super3、9-11→super4、12→super5。
func formIdxOfLevel(lv int) int {
	switch {
	case lv <= 0:
		return 0
	case lv <= 3:
		return 1
	case lv <= 6:
		return 2
	case lv <= 8:
		return 3
	case lv <= 11:
		return 4
	case lv <= 12:
		return 5
	}
	return 6 // 13 级 = 至尊 annual
}
