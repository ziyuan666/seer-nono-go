//go:build windows

package main

// 昵称就地编辑。
//
// 原版行为（NewNonoInfoPanel.as）：
//   - 点扳手 onChangeHandler：_changeMc.visible=false、_sucMc.visible=true、
//     _nickTxt.selectable=true、_nickBg.alpha=0.6 —— 昵称原地变成可编辑输入框，
//     原图标位置换成 √。
//   - 点 √ onSucHandler / 输入框里回车 onKeyDown：提交改名。
//   - 改名成功 onChangeSuccessHandler：扳手显示回来、√ 隐藏、取消可编辑。
//
// 面板是分层窗口（UpdateLayeredWindow），Wine 下子控件不参与分层合成，所以
// 输入框仍单独开窗；但改成**无边框 + 精确覆盖昵称框**，视觉上就是就地编辑。
// 昵称框几何取自 SWF：nickBgMc = DefineSprite_135（84×20）@ 面板 (159,39)。
//
// 2026-10-26 子渊：「点击这个扳手就进入修改模式，同时这个扳手变成一个√，
// 现在你是√和扳手同时叠加起来了」——底图烘焙的扳手已在 tools/mk_info_ui.py
// 擦掉，图标完全由代码按状态画（infoRenameBtn）。

import (
	"strings"
	"syscall"
	"unsafe"
)

const (
	WM_CTLCOLOREDIT = 0x0133
	WM_SETFONT      = 0x0030
	WM_KEYDOWN      = 0x0100
	EM_SETSEL       = 0x00B1
	VK_RETURN       = 0x0D
	VK_ESCAPE       = 0x1B
	VK_TAB          = 0x09
	// GWLP_WNDPROC = -4；Go 里 uintptr 常量不能写负数，用 ^uintptr(3) 表达
	gwlpWndProc = ^uintptr(3)
	// EDIT 样式（旧版 panel_input.go 里定义过，重写时丢了）
	esAutoHScroll = 0x0080
	esLeft        = 0x0000
)

// 昵称框（面板逻辑坐标，来自 nickBgMc）
var nickBox = struct{ X, Y, W, H int }{159, 39, 84, 20}

var (
	nickHwnd   HWND
	nickEdit   HWND
	nickCls    bool
	nickBrush  uintptr
	nickOldPrc uintptr
	nickFont   uintptr
	// 编辑态：三行（昵称/主人/生日）就地编辑。
	// editIdx 是当前正在输入的行，editDraft 是三行的草稿（初始值 = 存档现值，
	// 子渊 2026-10-27：「编辑框里要有现有内容，不是空白」）。
	editIdx   int
	editDraft [3]string
)

// editRows 三行可编辑项：值原点 + 高亮条矩形（条 = 值原点左上各退 2/4）。
// 昵称条的几何来自 nickBgMc（DefineSprite_135，84×20 @ 159,39）；
// 主人/生日没有官方高亮条，沿用同一个条贴图，宽度按内容放宽。
var editRows = [3]struct {
	Name       string
	X, Y       int // 值原点（uiLabel 墨迹左上）
	BX, BY, BW int // 高亮条
	MaxChars   int
}{
	{"昵称", 161, 50, 159, 46, 84, 12},
	{"主人", 164, 106, 162, 102, 96, 12},
	{"生日", 164, 124, 162, 120, 130, 20},
}

// editValue 取某行当前显示值（编辑态取草稿，否则取存档）。
func editValue(i int) string {
	if nickHwnd != 0 && i >= 0 && i < 3 {
		return editDraft[i]
	}
	if g == nil {
		return ""
	}
	switch i {
	case 0:
		if g.Save.Nick != "" {
			return g.Save.Nick
		}
		return "NONO"
	case 1:
		if g.Save.Owner != "" {
			return g.Save.Owner
		}
		return "赛尔"
	default:
		if g.Save.Birth != "" {
			return g.Save.Birth
		}
		return "赛尔历1年1月1日"
	}
}

// switchEditIdx 切到另一行继续编辑：先把当前 EDIT 的内容落到草稿，再换文本。
func switchEditIdx(i int) {
	if nickHwnd == 0 || i < 0 || i >= 3 || i == editIdx {
		return
	}
	pullEditText()
	editIdx = i
	if nickEdit != 0 {
		pSendMessageW.Call(uintptr(nickEdit), 0x00C5, uintptr(editRows[i].MaxChars), 0)
		pSetWindowTextW.Call(uintptr(nickEdit), uintptr(unsafe.Pointer(utf16ptr(editDraft[i]))))
		pSendMessageW.Call(uintptr(nickEdit), EM_SETSEL, 0, ^uintptr(0))
		pSetFocus.Call(uintptr(nickEdit))
	}
	if infoPanelWin != nil {
		infoPanelWin.Labels = infoPanelLabels()
		infoPanelWin.paint()
	}
	logf("edit switched to %s", editRows[i].Name)
}

// pullEditText 把隐形 EDIT 的当前内容同步进草稿。
func pullEditText() {
	if nickEdit == 0 || editIdx < 0 || editIdx >= 3 {
		return
	}
	buf := make([]uint16, 128)
	pGetWindowTextW.Call(uintptr(nickEdit), uintptr(unsafe.Pointer(&buf[0])), 127)
	editDraft[editIdx] = syscall.UTF16ToString(buf)
}

var (
	pSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	pCallWindowProcW   = user32.NewProc("CallWindowProcW")
)

// nickEditProc 子类化输入框，只抓回车/ESC（单行 EDIT 不会把回车转成通知）。
func nickEditProc(hwnd HWND, msg uint32, wp, lp uintptr) uintptr {
	if msg == WM_KEYDOWN {
		switch wp {
		case VK_RETURN:
			commitNickInput()
			return 0
		case VK_ESCAPE:
			cancelNickInput()
			return 0
		case VK_TAB:
			// Tab 在三行之间轮转（昵称 → 主人 → 生日 → 昵称）
			switchEditIdx((editIdx + 1) % 3)
			return 0
		}
	}
	if nickOldPrc == 0 {
		ret, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wp, lp)
		return ret
	}
	ret, _, _ := pCallWindowProcW.Call(nickOldPrc, uintptr(hwnd), uintptr(msg), wp, lp)
	return ret
}

func nickWndProc(hwnd HWND, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case WM_CTLCOLOREDIT:
		if nickBrush == 0 {
			// 原版改名态：_nickBg.alpha = 0.6 的淡蓝高亮，EditText 本身透明。
			// Windows 的 EDIT 控件无法真透明，用一个等效的淡色底代替：
			// #C9E8FA 以 30% 叠在面板白底上 ⇒ #EFF8FD（BGR 0x00FDF8EF）。
			nickBrush, _, _ = pCreateSolidBrush.Call(0x00FDF8EF)
		}
		// 文字色/底色都必须显式给：DC 是从面板那边继承来的，
		// 只返回 brush 时 Wine 下会出现「有内容但不显示」。
		pSetBkColor.Call(wp, 0x00FDF8EF)
		pSetTextColor.Call(wp, 0x00202020)
		return nickBrush
	case WM_CLOSE:
		cancelNickInput()
		return 0
	}
	ret, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wp, lp)
	return ret
}

// moveNickInput 把输入框重新贴到面板的昵称位上。
// 面板拖动走的是 SetWindowPos 挪分层窗口，输入框是独立顶层窗口，
// 不跟着挪就会脱位——表现成「点了扳手，输入框跑到面板外面/更上面去了」。
func moveNickInput() {
	if nickHwnd == 0 || infoPanelWin == nil {
		return
	}
	// 只是那个 8x8 键盘缓冲窗口，跟着面板原点走即可，不参与显示
	pSetWindowPos.Call(uintptr(nickHwnd), 0,
		uintptr(infoPanelWin.X+4), uintptr(infoPanelWin.Y+4), 8, 8,
		SWP_NOACTIVATE|SWP_NOZORDER)
}

// showNickInput 进入改名模式：把输入框盖在昵称位置上。
func showNickInput() {
	if nickHwnd != 0 {
		pSetForegroundWindow.Call(uintptr(nickHwnd))
		if nickEdit != 0 {
			pSetFocus.Call(uintptr(nickEdit))
		}
		return
	}
	if !nickCls {
		hInst, _, _ := pGetModuleHandleW.Call(0)
		wc := WNDCLASSEX{
			CbSize:        uint32(unsafe.Sizeof(WNDCLASSEX{})),
			LpfnWndProc:   syscall.NewCallback(nickWndProc),
			HInstance:     HINSTANCE(hInst),
			LpszClassName: utf16ptr("NonoNickWnd"),
		}
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		nickCls = true
	}
	hInst, _, _ := pGetModuleHandleW.Call(0)

	// 输入框要严丝合缝盖在昵称框上：位置按面板窗口偏移 + DPI 缩放换算
	s, px, py := 1.0, 0, 0
	if infoPanelWin != nil {
		if infoPanelWin.S > 0 {
			s = infoPanelWin.S
		}
		px, py = infoPanelWin.X, infoPanelWin.Y
	}
	// 改名输入不再用「盖在昵称上的独立 popup」：Wine 下 EDIT 在 TOPMOST 分层
	// 窗口旁边经常画不出文字，不透明底色还会把昵称框描边压掉。改成 8x8 隐形
	// 窗口只负责收键盘，输入内容由面板自己画（等同原版在 TextField 内就地编辑）。
	x, y := px+4, py+4
	w, h := 8, 8

	hwnd, _, _ := pCreateWindowExW.Call(
		WS_EX_TOPMOST,
		uintptr(unsafe.Pointer(utf16ptr("NonoNickWnd"))),
		uintptr(unsafe.Pointer(utf16ptr("改名"))),
		WS_POPUP,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, hInst, 0)
	if hwnd == 0 {
		logf("nick window create failed")
		return
	}
	// 草稿初值 = 存档现值（子渊：编辑框要带现有内容，不能是空白）。
	// 必须赶在赋 nickHwnd 之前算，否则 editValue 会走「编辑态」分支读到空草稿。
	editIdx = 0
	for i := 0; i < 3; i++ {
		editDraft[i] = editValue(i)
	}
	nickHwnd = HWND(hwnd)
	nick := editDraft[0]
	edit, _, _ := pCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(utf16ptr("EDIT"))),
		uintptr(unsafe.Pointer(utf16ptr(nick))),
		WS_CHILD|WS_VISIBLE|esAutoHScroll|esLeft,
		0, 0, uintptr(w), uintptr(h), hwnd, 0, hInst, 0)
	nickEdit = HWND(edit)
	if nickEdit != 0 {
		if nickFont == 0 {
			nickFont = uintptr(setFont(0, 15, false)) // 字号与面板标签一致
		}
		if nickFont != 0 {
			pSendMessageW.Call(uintptr(nickEdit), WM_SETFONT, nickFont, 1)
		}
		old, _, _ := pSetWindowLongPtrW.Call(uintptr(nickEdit), gwlpWndProc,
			syscall.NewCallback(nickEditProc))
		nickOldPrc = old
	}
	pShowWindow.Call(hwnd, 5) // SW_SHOW
	hideInfoTip()            // 编辑态不显示悬停提示
	pSetForegroundWindow.Call(hwnd)
	if nickEdit != 0 {
		// 原版 _nickTxt.maxChars = 12：EDIT 控件用 EM_SETLIMITTEXT 做到同样的输入上限。
		// 生日给 20（"赛尔历1年1月1日" 已 9 字，留余量）。
		pSendMessageW.Call(uintptr(nickEdit), 0x00C5, uintptr(editRows[0].MaxChars), 0)
		pSetFocus.Call(uintptr(nickEdit))
		pSendMessageW.Call(uintptr(nickEdit), EM_SETSEL, 0, ^uintptr(0)) // 全选
	}
	// 输入框内容由面板自绘：这里立刻重画一次，把昵称旧文字换成输入串
	if infoPanelWin != nil {
		infoPanelWin.Labels = infoPanelLabels()
		infoPanelWin.paint()
	}
	logf("nick input shown at %d,%d %dx%d s=%.2f", x, y, w, h, s)
}

// syncNickEditText 轮询那个隐形 EDIT 的内容，变了就让面板重画。
// EDIT 只在视觉之外当键盘缓冲用，真正的显示由 infoPanelLabels 画。
func syncNickEditText() {
	if nickEdit == 0 {
		return
	}
	buf := make([]uint16, 128)
	pGetWindowTextW.Call(uintptr(nickEdit), uintptr(unsafe.Pointer(&buf[0])), 127)
	s := syscall.UTF16ToString(buf)
	if editIdx < 0 || editIdx >= 3 || s == editDraft[editIdx] {
		return
	}
	editDraft[editIdx] = s
	if infoPanelWin != nil {
		infoPanelWin.Labels = infoPanelLabels()
		infoPanelWin.paint()
	}
}

// commitNickInput 提交（点 √ 或回车）：三行一起写回存档。
// 昵称是唯一必填项，空昵称按原版提示后留在编辑态；主人/生日为空则回落默认值。
func commitNickInput() {
	if nickEdit == 0 {
		return
	}
	pullEditText()
	vals := editDraft
	vals[0] = strings.TrimSpace(vals[0])
	vals[1] = strings.TrimSpace(vals[1])
	vals[2] = strings.TrimSpace(vals[2])
	if vals[0] == "" {
		if g != nil {
			g.say("你还没有输入昵称呢！")
		}
		switchEditIdx(0)
		return
	}
	// 原版 NewNonoInfoPanel.as：_nickTxt.maxChars = 12，超出部分 Flash 直接不接收。
	// EDIT 在输入时就限长，这里再按字符（不是字节）截一次兜底。
	for i, n := range []int{editRows[0].MaxChars, editRows[1].MaxChars, editRows[2].MaxChars} {
		if r := []rune(vals[i]); len(r) > n {
			vals[i] = string(r[:n])
		}
	}
	if vals[1] == "" {
		vals[1] = "赛尔"
	}
	if vals[2] == "" {
		vals[2] = "赛尔历1年1月1日"
	}
	if g != nil {
		g.Save.Nick = vals[0]
		g.Save.Owner = vals[1]
		g.Save.Birth = vals[2]
		g.Save.write()
	}
	closeNickInput()
	if infoPanelWin != nil {
		infoPanelWin.Labels = infoPanelLabels()
		infoPanelWin.paint()
	}
	if g != nil {
		g.say("资料已更新")
	}
	logf("info committed: nick=%s owner=%s birth=%s", vals[0], vals[1], vals[2])
}

func cancelNickInput() {
	closeNickInput()
	logf("nick edit cancelled")
}

func closeNickInput() {
	editIdx = 0
	editDraft = [3]string{}
	if nickHwnd != 0 {
		h := nickHwnd
		nickHwnd = 0
		nickEdit = 0
		nickOldPrc = 0
		pDestroyWindow.Call(uintptr(h))
	}
	setInfoRenameEditing(false)
	// 退出编辑态后把昵称文字放回面板（nickHwnd==0 才会画）
	if infoPanelWin != nil {
		infoPanelWin.Labels = infoPanelLabels()
		infoPanelWin.paint()
	}
}
