//go:build windows

package main

import "time"

// NONO 主面板：1:1 复刻游戏 MachineDogPanel。
// 布局依据：MachineDogMainInfo_MC 导出图（535×395 原生）+ 模板匹配标定的按钮坐标。
// 交互依据：MachineDogPanel.as（ToolTip 文案、开机/关机/充电/芯片/资料/仓库 分支）。

const (
	panelContentW  = 280 // 面板可见圆形内容的直径（main.png 内 alpha>16 的 bbox）
	panelContentH  = 280
	panelContentCX = 265 // 可见内容中心在 main.png（1x）中的坐标
	panelContentCY = 249
	mainPanelW = 535
	mainPanelH = 395
)

var mainPanelWin *uiWin

// 拖动过主面板后记住位置，下次打开不再强制居中（会话内有效）。
var (
	mainPanelPinned bool
	mainPanelPX     int
	mainPanelPY     int
)

func notePanelMoved(w *uiWin) {
	if w == nil || w != mainPanelWin {
		return
	}
	mainPanelPinned = true
	mainPanelPX, mainPanelPY = w.X, w.Y
	// 跟随窗口（功能芯片环）在拖动过程中只做了 SetWindowPos，Wine 下分层窗口会留残影，
	// 松手后补一次完整重绘（UpdateLayeredWindow 带 dstPoint 定位）。
	syncChipPanelToMain()
	logf("main panel moved to %d,%d", w.X, w.Y)
}

// mainPanelOnMove 是 uiWin.OnMove 回调：主面板拖动时带着功能芯片环一起平移。
// 环的圆心是按主面板可见圆算的（showChipPanel），主面板一挪环必须同步挪，
// 否则环留在原地、与主面板按钮脱开（子渊 2026-10-27 反馈）。
func mainPanelOnMove(dx, dy int) {
	mainPanelPinned = true
	mainPanelPX, mainPanelPY = mainPanelWin.X, mainPanelWin.Y
	if chipPanelWin == nil || !chipPanelWin.Visible || chipPanelWin.Hwnd == 0 {
		return
	}
	chipPanelWin.X += dx
	chipPanelWin.Y += dy
	// 拖动中每 15ms 重绘一次 560×560 的环太重，这里只挪窗口
	// （内容位图跟着窗口走）；松手后 notePanelMoved -> syncChipPanelToMain 补画。
	pSetWindowPos.Call(uintptr(chipPanelWin.Hwnd), ^uintptr(0),
		uintptr(chipPanelWin.X), uintptr(chipPanelWin.Y),
		uintptr(chipPanelWin.W), uintptr(chipPanelWin.H), SWP_NOACTIVATE)
	logf("chip ring follow d=%d,%d -> %d,%d", dx, dy, chipPanelWin.X, chipPanelWin.Y)
}

// syncChipPanelToMain 拖动结束后把芯片环完整重绘一次，清掉 SetWindowPos 的残影。
func syncChipPanelToMain() {
	if chipPanelWin == nil || !chipPanelWin.Visible || chipPanelWin.Hwnd == 0 {
		return
	}
	chipPanelWin.paint()
}

// panelSideGap：子面板（基本资料 / 储藏空间）与主面板可见圆之间的间距（逻辑像素）。
// 子渊 2026-10-27：弹出时默认落在主面板左侧/右侧 20px 左右。
const panelSideGap = 20

// mainPanelCircle 返回主面板「可见圆形内容」的屏幕几何（物理像素）。
// 主面板窗口是 535×395，但 main.png 留白极大，真正可见的圆形内容只有 280×280
// 且不在窗口正中（中心 = panelContentCX/CY）。子面板若按窗口边定位会离本体 100+px，
// 所以统一按可见圆算。
func mainPanelCircle() (cx, cy, r int, s float64, ok bool) {
	if mainPanelWin == nil || mainPanelWin.Hwnd == 0 || !mainPanelWin.Visible {
		return 0, 0, 0, 1, false
	}
	s = mainPanelWin.S
	if s <= 0 {
		s = 1
	}
	cx = mainPanelWin.X + int(float64(panelContentCX)*s+0.5)
	cy = mainPanelWin.Y + int(float64(panelContentCY)*s+0.5)
	r = int(float64(panelContentW)*s/2 + 0.5)
	return cx, cy, r, s, true
}

// placeBesideMainPanel 计算子面板的弹出坐标：side=-1 左侧、side=+1 右侧，
// dyLogical 是相对主面板顶边的纵向偏移（逻辑像素）。
// 返回 ok=false 表示主面板没开着，调用方退回自己的兜底位置。
func placeBesideMainPanel(w, h int, side int, dyLogical int) (int, int, bool) {
	cx, _, r, s, ok := mainPanelCircle()
	if !ok {
		return 0, 0, false
	}
	gap := int(float64(panelSideGap)*s + 0.5)
	x := cx + r + gap
	if side < 0 {
		x = cx - r - gap - w
	}
	y := mainPanelWin.Y + int(float64(dyLogical)*s+0.5)
	// 贴屏边时夹回可视区，避免半块面板落到屏幕外拖不回来
	vx, vy, vw, vh := virtualScreen()
	x = clamp(x, vx, vx+vw-w)
	y = clamp(y, vy, vy+vh-h)
	return x, y, true
}

// mainPanelBtnSpec 是模板匹配得到的按钮矩形（素材为 2x 导出，坐标已除 2）。
type mainPanelBtnSpec struct {
	Action string
	X, Y   int
	W, H   int
	Prefix string
}

var mainPanelBtns = []mainPanelBtnSpec{
	// 电池（充电）：导出 DefineButton2_42，2x 位置 (366,522) 80×80
	{"charge", 183, 261, 40, 40, "main_charg"},
	// 电源（开机/关机）：DefineButton2_28 (490,522) 80×80
	{"power", 245, 261, 40, 40, "main_power"},
	// 关闭：DefineButton2_57 (622,530) 66×66
	{"close", 311, 265, 33, 33, "main_close"},
	// 功能芯片：DefineButton2_11 (484,620) 93×93
	{"chip", 242, 310, 47, 47, "main_chip"},
	// 小箭头左右：背景图已画好箭头，仅登记命中区（b16/b173 是左箭头，b176 是 ⊙，不能混用）
	{"pageL", 222, 324, 24, 24, ""},
	{"pageR", 291, 324, 26, 24, ""},
	// 两侧大箭头：面板图上无独立按钮图，用透明命中区。
	// 热区放大到 44×44：30×34 在 Wine 下经常差几像素就落空，表现为"点了没反应"。
	{"info", 141, 295, 44, 44, ""},
	{"storage", 345, 295, 44, 44, ""},
}

func ensureMainPanel() *uiWin {
	if mainPanelWin != nil {
		return mainPanelWin
	}
	win := newUIWin(mainPanelW, mainPanelH)
	if win == nil {
		return nil
	}
	win.Bg = uiPanel("main.png", win.W, win.H)
	if win.Bg == nil {
		logf("main panel bg missing")
	}
	for _, b := range mainPanelBtns {
		btn := uiBtn{Action: b.Action, X: b.X, Y: b.Y, W: b.W, H: b.H}
		if b.Prefix != "" {
			btn.Up = uiFit(uiLoad(b.Prefix+"_up.png"), b.W, b.H)
			btn.Over = uiFit(uiLoad(b.Prefix+"_over.png"), b.W, b.H)
			btn.Down = uiFit(uiLoad(b.Prefix+"_down.png"), b.W, b.H)
		}
		win.Btns = append(win.Btns, btn)
	}
	win.Labels = mainPanelLabels()
	win.Draw = faceDraw // 黑屏 faceMc 动画层
	win.OnAction = mainPanelAction
	win.OnHover = mainPanelHover
	win.OnMove = mainPanelOnMove // 拖动时带着功能芯片环走
	mainPanelWin = win
	return win
}

// mainPanelLabels 生成随状态变化的文字（页码等）。
func mainPanelLabels() []uiLabel {
	return nil
}

func (g *Game) showMainPanel() {
	win := ensureMainPanel()
	if win == nil {
		return
	}
	win.Labels = mainPanelLabels()
	facePlay("open")
	vx, vy, vw, vh := virtualScreen()
	x := vx + (vw-win.W)/2
	y := vy + (vh-win.H)/2
	if mainPanelPinned {
		x, y = mainPanelPX, mainPanelPY
		x = clamp(x, vx, vx+vw-win.W)
		y = clamp(y, vy, vy+vh-win.H)
	}
	win.showAt(x, y)
	win.paint()
	logf("main panel shown at %d,%d", x, y)
}

func mainPanelHover(idx int) {
	if g == nil || idx < 0 {
		return
	}
	// 悬停只出文字提示，绝不执行任何动作。
	// 这里曾经把 mainPanelAction 的 chip/info/storage 分支复制过来，结果鼠标滑过
	// 按钮就直接开关面板——子渊 2026-10-24 反馈「主面板鼠标滑过去就打开了一些功能」。
	// 所有真实动作统一由 mainPanelAction（WM_LBUTTONUP 校验按下/抬起同一按钮）承担。
	switch mainPanelBtns[idx].Action {
	case "charge":
		g.say("充电")
	case "power":
		if g.Save.Open {
			g.say("关机")
		} else {
			g.say("开机")
		}
	case "close":
		g.say("关闭")
	case "chip":
		g.say("功能")
	case "info":
		g.say("基本资料")
	case "storage":
		g.say("储藏空间")
	case "pageL", "pageR":
		g.say("这一页没有更多内容了")
	}
}

func mainPanelAction(a string) {
	switch a {
	case "close":
		if mainPanelWin != nil {
			mainPanelWin.hide()
		}
		hideAllSubPanels()
	case "charge":
		if g != nil {
			// 游戏 MachineDogPanel.onChargBtnClickHandler：先关面板再发 NONO_CHARGE，
			// 协议回来后重开面板并播 CHARGING_FACE。
			hideAllSubPanels()
			if mainPanelWin != nil {
				mainPanelWin.hide()
			}
			g.say("正在给NONO充电…")
			time.AfterFunc(700*time.Millisecond, func() {
				g.Save.Power = 100
				g.Save.write()
				g.draw()
				g.showMainPanel()
				facePlay("charging")
				if mainPanelWin != nil && mainPanelWin.Visible {
					mainPanelWin.paint()
				}
				g.say("充电完成！能量已满满~")
				time.AfterFunc(3*time.Second, func() {
					if face.name == "charging" {
						facePlay("normal")
						if mainPanelWin != nil && mainPanelWin.Visible {
							mainPanelWin.paint()
						}
					}
				})
			})
		}
	case "power":
		if g == nil {
			return
		}
		if g.Save.Open {
			// 关机（20261029-01）：本体播 action 3 并循环保持（子渊要求「持续播放
			// 关机动画」），面板黑屏同时播 close face，900ms 后收面板。
			// 官方 action 表 id=3 是 autoEnd=0（停住等指令），循环是本工程的取舍。
			facePlay("close")
			if mainPanelWin != nil && mainPanelWin.Visible {
				mainPanelWin.paint()
			}
			g.powerOff()
			time.AfterFunc(900*time.Millisecond, func() {
				hideAllSubPanels()
				if mainPanelWin != nil {
					mainPanelWin.hide()
				}
				facePlay("normal")
			})
			return
		}
		// 开机：播当前形态的 action 2（带 boot.wav），面板黑屏播 open face。
		// 能量检查与气泡在 powerOn 内部统一处理（托盘开机走同一条路）。
		facePlay("open")
		g.powerOn()
		if mainPanelWin != nil && mainPanelWin.Visible {
			mainPanelWin.paint()
		}
	case "info":
		if infoPanelWin != nil && infoPanelWin.Visible {
			infoPanelWin.hide()
			hideInfoTip() // 面板收起后鼠标不再移动，提示窗会一直挂在桌面上
		} else {
			showInfoPanel()
		}
	case "storage":
		if storagePanelWin != nil && storagePanelWin.Visible {
			storagePanelWin.hide()
		} else {
			showStoragePanel()
		}
	case "chip":
		if chipPanelWin != nil && chipPanelWin.Visible {
			chipPanelWin.hide()
			updatePageTxt()
		} else if g != nil && !g.Save.Open {
			// 游戏：_info.state[0] 为 0 时不允许打开功能
			g.say("NoNo的功能只有在开机状态下才可以使用。")
		} else {
			showChipPanel()
		}
	case "pageL", "pageR":
		// 主面板箭头在主面板上翻芯片页（游戏里 pageTxt 显示在主面板）
		if chipPanelWin != nil && chipPanelWin.Visible {
			chipAction(a)
		}
	}
}

// say 显示气泡提示（复用主窗口气泡通道）。
func (g *Game) say(s string) {
	g.Bubble = s
	g.BubbleTicks = 60
	logf("say: %s", s)
}
// hideAllSubPanels 关闭主面板时一并收起子面板，避免残留孤窗。
// chipShellID 功能芯片 -> 原版功能面板（App_7000xx 打开的游戏模块）
var chipShellID = map[int]string{
	700002: "molecule",
	700003: "expadm",
	700004: "train",
	700011: "petstorage",
	700015: "track",
	700016: "petlist",
	700018: "soul",
}

func hideAllSubPanels() {
	if shellWin != nil {
		shellWin.hide()
	}
	if infoPanelWin != nil {
		infoPanelWin.hide()
		hideInfoTip()
		closeNickInput() // 改名输入框是独立 popup，面板收起时必须一起收
	}
	if storagePanelWin != nil {
		storagePanelWin.hide()
	}
	if chipPanelWin != nil {
		chipPanelWin.hide()
	}
	// 发明室面板之前漏在这里，主面板按 × 后它会留在屏幕上关不掉
	// （子渊 2026-10-23「有些打开后无法关闭」）。菜单是原生 TrackPopupMenu，
	// 模态弹出，不需要单独隐藏。
	if labWin != nil {
		labWin.hide()
	}
	if flyChoiceWin != nil {
		flyChoiceWin.hide()
	}
}

// ghostBlockedByPanel 面板占用屏幕时不画残影。注意不能复用 anyPanelVisible：
// 快捷栏常驻可见，会把残影永久关掉。
func ghostBlockedByPanel() bool {
	for _, w := range []*uiWin{mainPanelWin, infoPanelWin, storagePanelWin, chipPanelWin, labWin, shellWin} {
		if w != nil && w.Visible {
			return true
		}
	}
	return false
}

// anyPanelVisible 任一 NONO 面板可见时不再弹快捷栏（游戏里面板打开后快捷栏已收起）。
func anyPanelVisible() bool {
	for _, w := range []*uiWin{mainPanelWin, infoPanelWin, storagePanelWin, chipPanelWin,
		shortcutWin, labWin, shellWin} {
		if w != nil && w.Visible {
			return true
		}
	}
	return false
}
