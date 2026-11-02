//go:build windows

package main

// 飞行模式选择面板：点快捷栏「飞行」按钮后弹出，横向 4 个飞行器图标 + 底部「取消飞行模式」。
//
// 依据原版 nonoFlyModel/choicePanel.swf（DefineSprite 61，逻辑 250×134）+ 它的控制器
// com/robot/app/nono/NonoController.as：
//   - mc_1..mc_4  → onFlyClick → nonoFlyAction(i) + hidePanel（选飞行器并起飞）
//   - down        → onDownHandler → nonoFlyAction(0) + hidePanel（**取消飞行模式**，
//                   就是面板底部中间那个橙色小机器人；over 态会弹出黑色胶囊提示条
//                   「取消飞行模式」，比面板底边还低 29px——Flash 的 sprite 不裁剪）
//   - close       → onCloseClick → 只 hidePanel，不改飞行状态
//   - 入口 App_700017 只调 NonoController.show()，**没有 toggle**：飞行中再点快捷栏
//     「飞行」图标不会关闭面板，取消飞行只能按面板里的 down 按钮（子渊 2026-10-27 指出）
//
// 底图由 tools/mk_flychoice.py 从官方 SWF 以 4x 重导（带 alpha），运行时 1:1 缩到
// 250×134 —— 与其他面板同口径，不做 1.5 倍放大。窗口画布比底图高 30px，用来放
// 「取消飞行模式」按钮 over 态的提示条（见 flyChoiceWinH）。
//
// 选中态用官方按钮的 over 态（图标外一圈红描边 + 底部黑横条，fly_N_over.png）常驻显示，
// 不自绘高亮框：4 个飞行器本体宽度相差近一倍（26~43px），按统一格子画的框必然错位。

const (
	// 官方可见区 1000×536 ÷ 4 = 250×134，与 main/info/storage 等面板同一口径（4x 素材 1:1 缩）。
	flyChoiceW = 250
	flyChoiceH = 134

	// 画布/窗口高 = 底图 134 + 30。多出来的 30px 平时全透明（点击穿透），只在 hover
	// 「取消飞行模式」按钮时显示官方 over 态里的黑色胶囊提示条（官方 SWF 里它超出
	// 面板底边 29px，面板底边之下就是场景）。
	flyChoiceWinH = 164

	// fly_N_*.png 是 56×56 的 1.5x 素材，1x 下 37×37（保持原比例，uiFit 不会把图标压扁）。
	flyChoiceCellW = 37
	flyChoiceCellH = 37
)

// flyChoiceCells 是 4 个按钮矩形的左上角（1x，相对面板左上），顺序即 FlyStyle 1..4。
// 数值来自 4x 底图与 fly_N_up.png 的 alpha 掩码模板匹配（score ≥0.90）再 ÷1.5；
// 4 个图标外形差异大，间距不均（53/63/59）是原版排布本身如此，不是取整误差。
var flyChoiceCells = [4][2]int{
	{13, 63},  // 1 头盔
	{73, 62},  // 2 圆球
	{136, 61}, // 3 侧球
	{195, 61}, // 4 冲锋舟
}

var flyChoiceWin *uiWin

func ensureFlyChoicePanel() *uiWin {
	if flyChoiceWin != nil {
		return flyChoiceWin
	}
	win := newUIWin(flyChoiceW, flyChoiceWinH)
	if win == nil {
		return nil
	}
	// 底图只占画布顶部 134（逻辑），下面 30px 留给 over 态提示条。
	sc := win.S
	win.Bg = uiPanel("flychoice.png",
		int(float64(flyChoiceW)*sc+0.5), int(float64(flyChoiceH)*sc+0.5))
	win.OnAction = flyChoiceAction
	flyChoiceWin = win
	rebuildFlyChoiceButtons()
	return win
}

// rebuildFlyChoiceButtons 登记 4 个图标格 + 右上角关闭。
//
// 图片只在需要「多画一层」时才挂：
//   - 底图本身已经烘焙了 4 个 unselected 图标和关闭按钮的 up 态，压上去只会重影；
//   - 选中格常驻 over 态（官方红圈 + 底部黑横条）。uiWin.paint 的取图顺序是
//     Disabled → Down → Hover+Over → Up，没有「选中」这一档，所以这里把选中格的
//     Up 直接换成 over 图；未选中格的 Up 留 nil（不画），hover 时再走 Over。
func rebuildFlyChoiceButtons() {
	win := flyChoiceWin
	if win == nil {
		return
	}
	sel := 1
	if g != nil && g.Save.FlyStyle >= 1 && g.Save.FlyStyle <= 4 {
		sel = g.Save.FlyStyle
	}
	win.Btns = win.Btns[:0]
	for i := 0; i < 4; i++ {
		n := i + 1
		over := uiLoad("fly_" + itoa(n) + "_over.png")
		b := uiBtn{
			Action: "pick" + itoa(n),
			X:      flyChoiceCells[i][0], Y: flyChoiceCells[i][1],
			W: flyChoiceCellW, H: flyChoiceCellH,
			Over: over,
			Down: uiLoad("fly_" + itoa(n) + "_down.png"),
		}
		if n == sel {
			b.Up = over
		}
		win.Btns = append(win.Btns, b)
	}
	// 关闭按钮官方 twips (4529,238) → 1x (226.5,11.9)，素材 38px@1.5x → 25px。
	win.Btns = append(win.Btns, uiBtn{
		Action: "close", X: 225, Y: 13, W: 25, H: 25,
		Over: uiLoad("fly_close_over.png"),
		Down: uiLoad("fly_close_down.png"),
	})
	// 底部「取消飞行模式」按钮（官方 down）：nonoFlyAction(0) + hidePanel。
	// 图矩形比图标宽得多——over 态自带一条 96px 宽的黑胶囊提示条（「取消飞行模式」），
	// 所以绘制按 97×53，命中只认图标本体（官方 hittest 26×28，这里取 30×32 留 1px 余量）。
	win.Btns = append(win.Btns, uiBtn{
		Action: "cancel", X: 77, Y: 100, W: 97, H: 53,
		HitX: 34, HitY: 0, HitW: 30, HitH: 33,
		Over: uiLoad("fly_cancel_over.png"),
		Down: uiLoad("fly_cancel_down.png"),
	})
}

// 面板没有 Draw 钩子：底图是官方成品，选中态由 over 态按钮图负责。
func flyChoiceAction(action string) {
	if action == "close" {
		// 官方 onCloseClick：只收面板，不改飞行状态。
		if flyChoiceWin != nil {
			flyChoiceWin.hide()
		}
		return
	}
	if action == "cancel" {
		// 官方 onDownHandler → NonoManager.nonoFlyAction(0) + hidePanel：取消飞行模式。
		if g != nil {
			g.Save.Fly = false
			g.Save.write()
			logf("fly off (cancel button)")
			g.say("NONO 关闭飞行模式")
		}
		if flyChoiceWin != nil {
			flyChoiceWin.hide()
		}
		return
	}
	if len(action) == 5 && action[:4] == "pick" {
		style := int(action[4] - '0')
		if style < 1 || style > 4 {
			return
		}
		if g == nil {
			return
		}
		g.Save.FlyStyle = style
		g.Save.Fly = true
		g.Save.write()
		logf("fly style -> %d (from choice panel)", style)
		g.say("换好飞行模式啦")
		// 原版 NonoFlyModel 点一下就生效并收起面板（没有"确认"按钮）。
		// 之前这里只 paint()，面板留着不走，看着像还要再点一次（子渊 2026-10-27 反馈）。
		if flyChoiceWin != nil {
			flyChoiceWin.hide()
		}
	}
}

// showFlyChoice 快捷栏「飞行」按钮的入口。
//
// 官方 App_700017 只有一个动作：NonoController.show()——**不是 toggle**。
// 面板已经开着时再点快捷栏图标，行为等同于官方 show() 里的 configPannel()：
// 重新定位、不关闭。取消飞行只能按面板底部的「取消飞行模式」按钮（NonoController.as
// 的 onDownHandler），或右上角 X 只收面板（onCloseClick，不改飞行状态）。
func showFlyChoice() {
	win := ensureFlyChoicePanel()
	if win == nil {
		return
	}
	hideAllSubPanels()
	// 每次打开都重挂按钮：选中态是 over 图常驻，必须跟着当前 Save.FlyStyle 走。
	rebuildFlyChoiceButtons()
	x, y := flyChoiceAnchor()
	win.showAt(x, y)
	if g != nil {
		logf("flychoice show at %d,%d style=%d fly=%v", x, y, g.Save.FlyStyle, g.Save.Fly)
	}
}

// flyChoiceAnchor 把选择面板放在快捷栏正上方，快捷栏不可见时放在本体上方。
func flyChoiceAnchor() (int, int) {
	if g == nil {
		return 100, 100
	}
	var cx, top int
	if shortcutWin != nil && shortcutWin.Visible {
		cx = shortcutWin.X + shortcutWin.W/2
		top = shortcutWin.Y
	} else {
		cx = g.WinX + g.CurW/2
		top = g.WinY0
	}
	x := cx - flyChoiceW/2
	y := top - flyChoiceH - 8
	if y < 0 {
		y = 0
	}
	return x, y
}
