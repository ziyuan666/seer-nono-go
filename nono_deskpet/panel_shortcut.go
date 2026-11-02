//go:build windows

package main

// 悬浮快捷栏：1:1 复刻游戏 NonoShortcut。
// 依据：
//   - 底板 UI_NonoShortcut（taskIcon.swf chid 488），8 个 key 容器坐标经 SWF 显示列表解析：
//     key1(17.25,-44.25) key2(30,-13.5) key3(17.25,17.75) key4(-13.5,30)
//     key5(-13.5,-57) key6(-44.75,-44.25) key7(-57,-13.5) key8(-44.75,16.75)（twips=px）
//   - 底板原点在中心 (57,57)，故画布坐标 = 值 + 57；
//   - 图标来自 resource/nono/shortcutKey/{id}.swf，原生 29×29；
//   - 提示与行为来自 NonoShortcut.as 的 _tipA / _handlerFunA。

// 8 个图标等距环绕 NONO 头部（每个图标 PNG 自带白色圆底，不做整体底板），
// 与游戏内 ROLL_OVER 后的表现一致。
import (
	"math"
	"unsafe"
)

const (
	shortcutW    = 268 // 基准（NONO 2 倍大小时），实际尺寸由 shortcutMetrics 按本体宽度算
	shortcutH    = 268
	shortcutIcon = 46
	shortcutR    = 100.0 // 基准半径，实际由 shortcutMetrics 算
)

var shortcutWin *uiWin

type shortcutSlot struct {
	Key    string
	Tip    string
	Action string
	CX, CY float64 // 图标中心（画布坐标）
}

// 坐标 = SWF 值 + 57，顺序与 key1..key8 一致。
var shortcutSlots = []shortcutSlot{
	{"1", "精灵治疗", "cure", 74.25, 12.75},       // key1
	{"2", "跟随主人", "follow", 87.0, 43.5},       // key2
	{"3", "经验分配", "exp", 74.25, 74.75},        // key3
	{"4", "给NoNo充电", "power", 43.5, 87.0},     // key4
	{"700011", "精灵仓库", "storage", 43.5, 0},    // key5
	{"700015", "精灵追踪", "track", 12.25, 12.75}, // key6
	{"700017", "飞行模式", "fly", 0, 43.5},        // key7
	// key8 为保留空槽
}

// shortcutMetrics 按 NONO 当前显示尺寸算快捷栏尺寸、环绕半径、图标边长。
// 之前写死 268/R100/46 是按 2 倍本体设计的，用户把缩放调成 1 倍后
// 圈还是那么大，图标比本体还大，所以改为跟随本体宽度等比。
// 返回值：窗口宽、窗口高、横向半径 rx、纵向半径 ry、图标边长。
// rx/ry 分开是为了「椭圆环绕」——飞行器 style3 可见轮廓 74×156，细长；
// 用正圆半径要么上下贴脸要么左右散开（子渊 2026-10-27：「要么太近要么太远」）。
func shortcutMetrics() (winW, winH, rx, ry, icon int) {
	// 基准宽取「形态画布」而不是 g.BodyW：飞行时 BodyW 会被换成飞行器宽
	// （4 个 style 各不相同），导致鼠标悬停出现的快捷栏图标跟着忽大忽小
	// （子渊 2026-10-24："每个模式大小不统一，悬浮图标大小也不统一"）。
	bw := 0
	if g != nil && g.FS.Meta.Ball > 0 {
		// 用本体球径而不是画布宽：画布宽会被长鳍/长耳拉大（super4 画布 579 宽，
		// 球径只有 98），按画布算会让那几级的快捷栏图标忽然变大。
		sc := g.Save.Scale
		if sc < 1 {
			sc = 1
		}
		zoom := g.FS.Meta.Zoom
		if zoom < 1 {
			zoom = 1
		}
		bw = int(float64(g.FS.Meta.Ball) * float64(sc) / float64(zoom))
	}
	if bw <= 0 && g != nil && g.FS.Meta.Canvas.W > 0 {
		sc := g.Save.Scale
		if sc < 1 {
			sc = 1
		}
		zoom := g.FS.Meta.Zoom
		if zoom < 1 {
			zoom = 1
		}
		bw = int(float64(g.FS.Meta.Canvas.W) * float64(sc) / float64(zoom))
	}
	if bw <= 0 {
		bw = 126 // 兜底按 2 倍本体
	}
	icon = int(float64(bw)*0.36 + 0.5)
	if icon < 24 {
		icon = 24
	}
	if icon > 56 {
		icon = 56
	}
	// 环半径必须让图标完全落在「视觉本体」外面：Meta.Ball 只是球体直径，
	// 外壳/裙边/耳朵让本体看起来约 1.8 倍球径（plain 球 25px、可视宽 45px）。
	// 原来按 bw/2（=球半径）算，图标内缘直接压到球面上（子渊 2026-10-25：
	// 「周围这一圈功能图标，也没定位好」）。改成按 0.9×球径当本体半宽。
	radius := int(float64(bw)*0.9) + icon/2 + 10
	rx, ry = radius, radius
	// 飞行时环绕对象是飞行器，不是本体球，而且轮廓不是圆的：
	// 横向半径 = 可见宽/2 + 图标半径 + 8，纵向半径 = 可见高/2 + 图标半径 + 8，
	// 得到贴着轮廓的椭圆（style3 可见 74×156 → rx 57 / ry 98）。
	// 用实测 alpha 轮廓而不是素材整帧（整帧 157×223 含大量透明边，按它算会散开），
	// 也不用素材声明的 body bbox（那是单方向测量值，转身就偏）。
	// 取「跨方向最大值」保证同一形态内窗口尺寸恒定，转身不重建、不闪。
	if g != nil && g.Save.Fly {
		fsc := g.Save.Scale
		if fsc < 1 {
			fsc = 1
		}
		fh := flyScreenH(g.Save.FlyStyle, fsc)
		vw, vh := flyVisFor(g.Save.FlyStyle, fh, dirIndex(g.Dir))
		if vw <= 0 || vh <= 0 {
			// 该方向还没渲染过：先用同 style 任意方向的实测值，
			// 再不行才按整帧宽兜底（draw 跑起来后会被重建）
			vw, vh = flyVisAny(g.Save.FlyStyle, fh)
		}
		if vw <= 0 || vh <= 0 {
			vw = flyScreenW(g.Save.FlyStyle, fsc)
			vh = fh
		}
		rx = vw/2 + icon/2 + 8
		ry = vh/2 + icon/2 + 8
	}
	winW = 2*rx + icon + 8
	winH = 2*ry + icon + 8
	return
}

// ensureShortcut 确保快捷栏窗口存在且尺寸与当前几何一致。
// 返回值 rebuilt=true 表示窗口被重建（旧窗已 hide），调用方需要重新 showAt。
func ensureShortcut() (win *uiWin, rebuilt bool) {
	wW, wH, _, _, _ := shortcutMetrics()
	if shortcutWin != nil {
		// shortcutMetrics 给的是逻辑尺寸，窗口 W/H 是 DPI 放大后的物理像素，
		// 比较前换算，否则高 DPI 下会每个 tick 都判定"尺寸变了"而重建窗口。
		pw := int(float64(wW)*shortcutWin.S + 0.5)
		ph := int(float64(wH)*shortcutWin.S + 0.5)
		if shortcutWin.W == pw && shortcutWin.H == ph {
			return shortcutWin, false
		}
		logf("shortcut rebuild %dx%d -> %dx%d", shortcutWin.W, shortcutWin.H, pw, ph)
		// 本体缩放/飞行状态变了，环绕半径跟着变，重建窗口。
		// 注意：重建后 Visible 已经 false，showShortcut 必须重新 showAt，
		// 否则「起飞后第一次 hover」会拿到一个隐藏窗口，快捷栏就此消失。
		// 2026-11-02 E-01：旧窗口必须 destroy（释放句柄/GDI），只 hide 会泄漏。
		shortcutWin.destroy()
		shortcutWin = nil
		rebuilt = true
	}
	win = newUIWin(wW, wH)
	if win == nil {
		return nil, rebuilt
	}
	wW, wH, rx, ry, icon := shortcutMetrics()
	win.Bg = solidRGBA(win.W, win.H, 0)
	// 图标按钮放在前面（hitAt 取第一个命中），中心命中区放最后。
	for i, s := range shortcutSlots {
		ang := -math.Pi/2 + float64(i)*math.Pi/4
		cx := float64(wW)/2 + float64(rx)*math.Cos(ang)
		cy := float64(wH)/2 + float64(ry)*math.Sin(ang)
		x := int(cx) - icon/2
		y := int(cy) - icon/2
		btn := uiBtn{
			Action: s.Action,
			X:      x, Y: y,
			W: icon, H: icon,
			Up:   uiFit(uiLoad("key"+s.Key+"_up.png"), icon, icon),
			Over: uiFit(uiLoad("key"+s.Key+"_over.png"), icon, icon),
			Down: uiFit(uiLoad("key"+s.Key+"_down.png"), icon, icon),
		}
		if g != nil && !g.shortcutEnabled(s.Key) {
			// 不用 Disabled：Disabled 在 hitAt 里被跳过，点击等于石沉大海（还会
			// 触发面板拖动）。改成灰图 + locked 动作，点下去至少给一句提示。
			// （子渊 2026-10-23：没功能可以，但按钮不能点不了。）
			btn.Down = uiGray(btn.Up)
			btn.Over = uiGray(btn.Up)
			btn.Action = "locked:" + s.Key
		}
		win.Btns = append(win.Btns, btn)
	}
	// 中心命中区：点击本体区域即打开主面板，尺寸跟随本体
	cw := 2 * rx / 3
	if 2*ry/3 < cw {
		cw = 2 * ry / 3
	}
	if cw < 40 {
		cw = 40
	}
	win.Btns = append(win.Btns, uiBtn{Action: "main", X: wW/2 - cw/2, Y: wH/2 - cw/2, W: cw, H: cw})
	win.OnAction = shortcutAction
	win.OnHover = func(idx int) {
		if idx < 0 || idx >= len(shortcutSlots) || g == nil {
			return
		}
		g.say(shortcutSlots[idx].Tip)
	}
	shortcutWin = win
	return win, rebuilt
}

// shortcutEnabled 复刻 NonoShortcut.makeAry 的显示条件。
// key1/3/4 恒显示；key2「跟随主人」需已装 700005（func[4]，超能放行）；
// 追加位 700011/700015/700017 需已装对应芯片且 AI ≥ UseAI。
// 20261023-05：原实现恒 return true，门禁形同虚设，未装芯片也能点。
func (g *Game) shortcutEnabled(key string) bool {
	if g == nil || g.Save == nil {
		return true
	}
	switch key {
	case "700011", "700015", "700017":
		if g.Save.Super {
			return true // VipOnly 芯片：超能形态放行
		}
		return g.Save.canUseChip(atoiSafe(key))
	case "2":
		return g.Save.Super || g.Save.hasChip(700005)
	}
	return true
}

// shortcutAnchor 算快捷栏应处的位置（左上角）。环心对准本体球心：
// 优先用 draw() 实测出的 BallSX/BallSY（跨形态统一锚点 + meta.ballc，
// 见 assets.go / tools/measure_ballc.py）；飞行或旧素材包没有球心时
// 退回旧的「窗口水平中心 + 高度 60%」估算。
func shortcutAnchor() (int, int) {
	win := shortcutWin
	if win == nil {
		win, _ = ensureShortcut()
	}
	if win == nil || g == nil {
		return 0, 0
	}
	if g.BallSX != 0 || g.BallSY != 0 {
		// 球心即环心。之前用 CurW/2 与 0.60*BodyH 估，实测各形态偏 7~22px
		// （super5 球心在画布高度 28.8%，按 60% 算环就沉到肚子下面）。
		return clampShortcut(g.BallSX-win.W/2, g.BallSY-win.H/2, win)
	}
	cw := g.CurW
	if cw <= 0 {
		cw = shortcutW
	}
	bh := g.BodyH
	if bh <= 0 {
		bh = cw
	}
	x := g.BaseX + cw/2 - win.W/2
	// 用窗口真实原点 WinY0（不含呼吸浮动）：飞行模式下窗口比 BaseY 高一个
	// 飞行器，拿 BaseY 定位会让快捷栏整体沉到 NONO 下方；用 WinY 又会跟着
	// ±6px 浮动抖，点不中图标（子渊 2026-10-23 反馈）。
	// 用「WinY0 + 气泡高」= 本体/飞行器的屏幕顶部。气泡高从 CurH 反推
	// （CurH = 本体高 + 气泡高），不读 bubbleH 全局变量——它在 showShortcut
	// 时刻可能与窗口实际尺寸不同步，会让环形图标整体偏一个气泡高度（约 27px），
	// 表现就是"图标看得见但点不中"。
	top := g.WinY0 + (g.CurH - bh)
	y := top + int(float64(bh)*0.60) - win.H/2
	return clampShortcut(x, y, win)
}

// clampShortcut 把快捷栏窗口整体夹进虚拟屏幕。
// 飞行器比本体大得多，环绕半径 100+ 时窗口 232px 见方；NONO 贴着屏幕上沿飞行
// 时环会有一半在屏幕外（实测 shown at y=-7，顶部图标直接看不见）。
// 只夹窗口不夹环心：宁可环心下移几像素，也不能让图标跑到屏幕外。
func clampShortcut(x, y int, win *uiWin) (int, int) {
	vx, vy, vw, vh := virtualScreen()
	if vw <= 0 || vh <= 0 || win == nil {
		return x, y
	}
	if x+win.W > vx+vw {
		x = vx + vw - win.W
	}
	if y+win.H > vy+vh {
		y = vy + vh - win.H
	}
	if x < vx {
		x = vx
	}
	if y < vy {
		y = vy
	}
	return x, y
}

// updateShortcutPos 本体走动时让快捷栏跟着走（只在坐标变化时重绘）。
func updateShortcutPos() {
	if shortcutWin == nil || !shortcutWin.Visible || g == nil {
		return
	}
	x, y := shortcutAnchor()
	if x != shortcutWin.X || y != shortcutWin.Y {
		logf("shortcut follow %d,%d -> %d,%d body=%d,%d", shortcutWin.X, shortcutWin.Y, x, y, g.BaseX, g.BaseY)
		shortcutWin.showAt(x, y)
	}
}

func (g *Game) showShortcut() {
	if g == nil {
		return
	}
	win, rebuilt := ensureShortcut()
	if win == nil {
		return
	}
	// rebuilt 说明窗口刚按新半径（起飞/落地、本体缩放变化）重建过，旧的
	// Visible 状态已经随 hide() 清零，这里必须继续往下走到 showAt，
	// 否则「起飞后第一次 hover」会静默返回，快捷栏再也不出现。
	if win.Visible && !rebuilt {
		return
	}
	var cp POINT
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&cp)))
	g.SCMouseX, g.SCMouseY = int(cp.X), int(cp.Y)
	x, y := shortcutAnchor()
	logf("sc metrics body=%d,%d winY0=%d curW/H=%d,%d bodyW/H=%d,%d win=%dx%d", g.BaseX, g.BaseY, g.WinY0, g.CurW, g.CurH, g.BodyW, g.BodyH, win.W, win.H)
	win.showAt(x, y)
	logf("shortcut shown at %d,%d body=%d,%d", x, y, g.BaseX, g.BaseY)
}

func hideShortcut() {
	if shortcutWin != nil {
		shortcutWin.hide()
	}
}

func shortcutAction(a string) {
	if g == nil {
		return
	}
	hideShortcut()
	// 未解锁的快捷图标：给一句明确提示，不做静默失败
	if len(a) > 7 && a[:7] == "locked:" {
		g.say("这个功能还没解锁，先装上对应的芯片吧")
		return
	}
	switch a {
	case "main":
		g.showMainPanel()
	case "cure":
		g.say("恢复体力需要花费50赛尔豆，已为你的精灵恢复体力！")
	case "follow":
		// 跟随/取消跟随：原版只支持「召唤后跟随」，桌宠按子渊 2026-10-23 要求
		// 做成开关——再点一次就停下（原版是走开/回家休息才能解除）。
		if g.Save.Follow {
			g.Save.Follow = false
			g.Save.write()
			g.say("NONO 不跟着你啦")
			return
		}
		g.Save.Follow = true
		g.Save.write()
		// 原版点召唤后赛尔先停下、NONO 播 action/6 出场，再贴到身后。
		// 普通形态没有这套资源，summonAction 返回 false 时直接进入跟随。
		if g.summonAction() {
			g.sayFor("NONO 来了～", 2)
		} else {
			g.say("NONO 开始跟随主人")
		}
	case "exp":
		showShell("expadm")
	case "power":
		// 原版 9016 是「续费超能」，桌宠按子渊 2026-10-23 决定改为永久有效，
		// 充电退化为纯补能量（芯片制造机的货币来源）。
		if g.Save.Power < 100 {
			g.Save.Power += 20
			if g.Save.Power > 100 {
				g.Save.Power = 100
			}
		}
		g.Save.write()
		g.say("充电完成！能量 " + itoa(g.Save.Power) + "/100")
	case "storage":
		// 游戏 key5 走 PetStorageController.show()，是精灵仓库，不是芯片储藏
		showShell("petstorage")
	case "track":
		showShell("track")
	case "fly":
		// 原版 NonoController.onFlyClick：非 superNono 直接弹「成为超能NoNo才能使用此飞行模式」；
		// 反过来只要 superNono 成立就能飞——`superStage < i` 分支与 else 分支代码完全相同，
		// 等级判断写了但没生效（原版冗余，已核 NonoController.as:96-115）。
		// 桌宠保留 700017 芯片作为非超能的兜底通道（原版没有这条），文案与原版一致。
		if !g.Save.Super && !g.Save.hasChip(700017) {
			g.say("成为超能NoNo才能使用此飞行模式")
			return
		}
		// 原版 App_700017 只做一件事：NonoController.show() 弹出的选择面板，**没有 toggle**。
		// 飞行中再点快捷栏「飞行」图标不会落地（子渊 2026-10-27 指出，旧实现写成"再点=落地"）。
		// 取消飞行只有一条路：面板底部的「取消飞行模式」按钮（NonoController.onDownHandler
		// → nonoFlyAction(0)），见 flyChoiceAction 的 "cancel" 分支。
		showFlyChoice()
	}
}
