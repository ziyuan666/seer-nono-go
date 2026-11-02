//go:build windows

package main

// 基本资料面板：按 NewNonoInfoPanel.as + NewNonoInfoPanel.swf 的 display list 1:1 还原。
//
// 口径（子渊 2026-10-26）：底图只放 SWF 里的**静态件**，动态件全部按原版逻辑由代码
// 绘制/切换，不再用「放大位图 + 白底遮盖」冒充。
//
// NewNonoBaseInfoMc = DefineSprite_137，1 帧，子实例（depth / cid / twips÷20 坐标）：
//
//	1   bgMc        14    (0,0)          2 帧：1=普通灰、2=超能青   ← 底图（整张）
//	3   (白框)      17    (6.45,37)      静态                       ← 底图
//	7   mc1         26    (15.55,177)    智慧值进度条               ← 代码
//	10  mc2         28    (15.55,196.45) 能量值进度条               ← 代码
//	13  mc3         30    (15.55,214.7)  同步率进度条               ← 代码
//	16  mc0         32    (15.6,158)     超能值进度条               ← 代码
//	19  faceMc      84    (1.55,27.65)   头像                       ← 代码
//	44  (加载芯片)  86    (17.55,235.7)  静态分隔线                 ← 底图
//	47  rightMc     89    (260.1,281.2)  右翻页箭头                 ← 代码
//	49  leftMc      89    (25.6,281.2)   左翻页箭头（scaleX=-0.9）  ← 代码
//	51  (昵 称)     90    (99.05,43.25)  静态标签（文字转 shape）   ← 底图
//	52  sp1         94    (98.05,84.4)   「AI 等级:」标签           ← 代码（值另画）
//	55  sp2         97    (99.05,102.4)  「主　人:」标签             ← 代码（值另画）
//	58  sp3         100   (99.05,120.9)  「生　日:」标签             ← 代码（值另画）
//	61  sp0         118   (99.05,65.75)  10 帧「超能等级:+球N翼」   ← 代码
//	77  changeMc    120   (249.6,42.1)   扳手                       ← 代码
//	79  sucMc       122   (246.4,43.25)  √                          ← 代码
//	81  superIconMc 126   (66.6,258.2)   超能芯片行                 ← 代码
//	84  iconMc      127   (36.6,296.6)   普通芯片行                 ← 代码
//	88  closeBtn    133   (262.35,7.25)  关闭                       ← 代码
//	93  nickBgMc    135   (159,39)       昵称高亮底（编辑 alpha.3）  ← 代码
//	95  nickTxt     136   (160.8,43.6)   昵称值                     ← 代码
//
// 素材由 tools/mk_info_assets.py 从 SWF 以 -zoom 4 导出（矢量原生栅格化，不糊）。

import "image"

// 面板尺寸 = bgMc 原生 298×350（旧值 349 少 1px，底图会被压缩）。
const (
	infoPanelW = 298
	infoPanelH = 350
)

var infoPanelWin *uiWin

// 贴图锚点（面板像素）＝ SWF 的 DL 坐标 + bounds.min。
//
// 为什么不能直接用 DL 坐标画贴图：FFDec 导出 sprite 时 PNG 左上取的是该 sprite 的
// bounds 左上，不是 sprite 原点，多数 sprite 的 bounds.min 是 (-5,-5) 面板 px，直接
// 套 DL 坐标会整体偏 5px（这就是上一版"文字压线、图标错位"的直接原因）。
// 下面每个值都由 tools/calibrate_info_anchor.py 在 137 整帧 4x 渲染图上模板匹配实测，
// 括号里是实测偏移 ÷4。
const (
	// sp1/sp2/sp3：DL(98.05,84.4)/(99.05,102.4)/(99.05,120.9) + (5.0,4.25)
	infoLblAIX, infoLblAIY       = 103.05, 88.65
	infoLblOwnerX, infoLblOwnerY = 104.05, 106.65
	infoLblBirthX, infoLblBirthY = 104.05, 125.15
	infoLblAIW, infoLblAIH       = 175.0, 16.25
	infoLblOwnerW, infoLblOwnerH = 174.0, 16.25
	infoLblBirthW, infoLblBirthH = 176.25, 17.0
	// 「昵称:」：原先是烘焙进底图的（cid 90 shape，POS_NICKLBL=(99.05,43.25)），
	// 比上面三行标签左出 4~5px，四行看着不成一条竖线。改为运行时贴图，
	// X 取其它标签同一值（子渊 2026-10-27：只对齐这一行，别的不要动）。
	// Y 43.25→50：子渊 2026-10-27 要求「把昵称往下」，让昵称行与「超能等级」行
	// 的行距和其它行一致（值墨迹底 65 → 超能 71 = 6，与 AI→主人相同）。
	infoLblNickX, infoLblNickY = 104.05, 50.0
	infoLblNickW, infoLblNickH = 54.5, 12.0

	// sp0：DL(99.05,65.75) + (5.0,-1.75)（该 sprite 图形比原点高 1.75）
	infoSp0X, infoSp0Y = 104.05, 64.0
	infoSp0W, infoSp0H = 160.25, 20.75

	// faceMc(84)：DL(1.55,27.65) + (2.25,7.5)。84 的 bounds 由 superMc 决定，
	// 而 superMc 的 bgMc(66) 与 84 同原点同 bounds，所以超能身体直接落在 faceMc 锚点。
	infoFaceX, infoFaceY = 3.8, 35.15

	// 普通头像 norMc(83)：77 的 PNG 内容填满边界（bbox=0,0,291,403）⇒ bounds.min=0，
	// 所以身体贴图落在 norMc 原点 = (1.55+10.05, 27.65+18.2) = (11.6,45.85)。
	infoNorBodyX, infoNorBodyY = 11.6, 45.85
	// 普通眼睛 eyeMc(82)：在 83 整帧里模板匹配得 (+17.8,+38.4)（diff 1.87）。
	infoNorEyeDX, infoNorEyeDY = 17.8, 38.4

	// 超能眼睛 eyeMc(70)：在 71 整帧里模板匹配得 (30,52.25)（diff 2.70）。
	infoSuperEyeDX, infoSuperEyeDY = 30.0, 52.25

	// 进度条：底框 cid22 在 mc(25) 内 (0,0.8)，mc(25) 在 mcN 内 (53.6,0)，
	// 实测贴图锚点 = DL(69.15, y+0.8) + (5,5) ⇒ (74.15, y+5.8)；填充 cid20 再 +（1.45,1.75)。
	infoBarBgX     = 74.15
	infoBarBgDY    = 5.8
	infoBarBgW     = 202.75
	infoBarBgH     = 14.0
	infoBarFillDX  = 1.45
	infoBarFillDY  = 1.75
	infoBarFillW   = 200.0
	infoBarFillH   = 10.5
	infoBarCenterX = 175 // 条中心 = 74.15 + 202.75/2，原版 mc.txt 居中
	infoBarTextDY  = 2.0 // 数值文本绘制原点相对条顶（12px 字，垂直居中）

	// 关闭按钮；实测贴图锚点 (266.6,11.25)，尺寸 19.5×19.5。
	infoCloseX, infoCloseY = 266, 11

	infoNickBgX, infoNickBgY = 159.0, 46.0 // 高亮条跟着昵称行一起下移，编辑框仍在条上
	infoNickBgW, infoNickBgH = 84.0, 19.75
)

// 头像部件尺寸（面板像素，= FFDec 4x 图 ÷4）。
const (
	infoNorBodyW, infoNorBodyH     = 72.75, 100.75
	infoSuperBodyW, infoSuperBodyH = 97.75, 114.75
	infoEyeW, infoEyeH             = 37.75, 26.0
)

// 原版 _powerA（NewNonoInfoPanel.as:104）：超能值上限按等级取，索引 = min(level,10)。
var infoPowerA = [11]int{0, 100, 250, 450, 700, 1300, 1900, 2600, 3400, 5000, 5000}

// infoBars 四条进度条，顺序与原版 sprite 一一对应（mc0..mc3）。
// Y 是 mcN sprite 的原点（twips÷20），条体在 sprite 内的偏移由 infoBarOff* 补。
var infoBars = []struct {
	Y     float64
	Pct   func(g *Game) float64
	Value func(g *Game) string
	Tip   func(g *Game) string
}{
	{ // mc0 超能值：superEnergy / _powerA[superL]；非超能 txt 隐藏、整条灰度
		Y: 158.0,
		Pct: func(g *Game) float64 {
			if g == nil {
				return 0
			}
			if !infoIsSuper() {
				return 1 // 原版非超能分支不给 mc.scaleX 赋值 ⇒ 保持初始 1
			}
			return float64(g.Save.SuperEnergy) / float64(infoPowerA[infoSuperL()])
		},
		Value: func(g *Game) string {
			if g == nil || !infoIsSuper() {
				return ""
			}
			return itoa(g.Save.SuperEnergy) + "/" + itoa(infoPowerA[infoSuperL()])
		},
		Tip: func(g *Game) string {
			if g == nil {
				return ""
			}
			if !infoIsSuper() {
				return "曾经的超级能量" + itoa(g.Save.SuperEnergy)
			}
			cap := infoPowerA[infoSuperL()]
			if (cap == g.Save.SuperEnergy && cap == 5000) || g.Save.SuperEnergy > 5000 {
				return "十级超能NoNo"
			}
			days := (cap - g.Save.SuperEnergy + 4) / 5 // ceil(/5)
			return "距离升级还需要" + itoa(days) + "天超级能量"
		},
	},
	{ // mc1 智慧值：iq / (7*ainew²+93*ainew)，ainew = ai+1（ai<100 时）
		Y: 177.0,
		Pct: func(g *Game) float64 {
			if g == nil {
				return 0
			}
			return float64(g.Save.Exp) / float64(infoAITotal(g.Save.AI))
		},
		Value: func(g *Game) string {
			if g == nil {
				return "0/0"
			}
			return itoa(g.Save.Exp) + "/" + itoa(infoAITotal(g.Save.AI))
		},
		Tip: func(g *Game) string {
			if g == nil {
				return ""
			}
			if g.Save.AI == 100 {
				return "你的AI已经满级了哦"
			}
			return "距离AI升级还需要" + itoa(infoAITotal(g.Save.AI)-g.Save.Exp) + "点智慧值"
		},
	},
	{ // mc2 能量值：power / 100
		Y: 196.45,
		Pct: func(g *Game) float64 {
			if g == nil {
				return 0
			}
			return float64(g.Save.Power) / 100
		},
		Value: func(g *Game) string {
			if g == nil {
				return "0/100"
			}
			return itoa(g.Save.Power) + "/100"
		},
		Tip: func(g *Game) string {
			if g == nil {
				return ""
			}
			if infoIsSuper() {
				return "能量满满哦！"
			}
			switch {
			case g.Save.Power <= 10:
				return "你的NoNo需要补充能量咯！"
			case g.Save.Power <= 50:
				return "记得充电哦！"
			case g.Save.Power <= 80:
				return "NoNo很开心，电力充足，能量满满！"
			default:
				return "只要能量充足，什么问题都难不倒NoNo！"
			}
		},
	},
	{ // mc3 同步率：mate / 100
		Y: 214.7,
		Pct: func(g *Game) float64 {
			if g == nil {
				return 0
			}
			return float64(g.Save.Mate) / 100
		},
		Value: func(g *Game) string {
			if g == nil {
				return "0/100"
			}
			return itoa(g.Save.Mate) + "/100"
		},
		Tip: func(g *Game) string {
			if g == nil {
				return ""
			}
			switch {
			case g.Save.Mate <= 30:
				return "要花更多时间跟你的NoNo交流哦！"
			case g.Save.Mate <= 60:
				return "保持最高的同步率才能百战百胜！"
			default:
				return "非常好哦，继续保持这样的默契！"
			}
		},
	},
}

// infoSuperL 原版 superL = superLevel<10 ? superLevel : 10；0 级时按第 1 帧（原版 gotoAndStop(0) 停在第 1 帧）。
func infoSuperL() int {
	if g == nil {
		return 1
	}
	lv := g.Save.SuperLevel
	if lv < 1 {
		return 1
	}
	if lv > 10 {
		return 10
	}
	return lv
}

// infoAITotal 原版 setData() 的 total = 7*ainew² + 93*ainew。
func infoAITotal(ai int) int {
	n := ai
	if ai < 100 {
		n = ai + 1
	}
	if n <= 0 {
		n = 1
	}
	return 7*n*n + 93*n
}

// infoNorFace 原版 makeNorNoFace()：按 mate / power 选普通眼睛帧（1..4）。
func infoNorFace(power, mate int) int {
	if mate <= 30 {
		switch {
		case power <= 50:
			return 4
		case power <= 80:
			return 3
		default:
			return 1
		}
	}
	if mate <= 60 {
		switch {
		case power <= 10:
			return 4
		case power <= 50:
			return 3
		case power <= 80:
			return 1
		default:
			return 2
		}
	}
	switch {
	case power <= 10:
		return 3
	case power <= 50:
		return 1
	default:
		return 2
	}
}

func ensureInfoPanel() *uiWin {
	if infoPanelWin != nil {
		return infoPanelWin
	}
	win := newUIWin(infoPanelW, infoPanelH)
	if win == nil {
		return nil
	}
	// 底图只有静态件（bgMc 帧1 + 白框 + 「加载芯片」分隔线 + 「昵 称」标签），
	// 两张对应 bgMc 的 2 帧：普通灰边 / 超能青边。
	win.Bg = uiPanel("info_base_plain.png", win.W, win.H)
	win.BgPick = func() *image.RGBA {
		if infoIsSuper() {
			return uiPanel("info_base_super.png", win.W, win.H)
		}
		return win.Bg
	}
	win.OnHover = infoPanelHover
	win.Draw = infoPanelDraw
	win.OnAction = infoPanelAction
	win.Labels = infoPanelLabels()
	infoPanelWin = win
	rebuildInfoBtns()
	return win
}

// infoChipBtns 当前页芯片格的热区：原版每个 ChipInfoBgPanel 自带 ToolTipManager，
// 悬停显示芯片名（ChipInfoBgPanel 里 ToolTipManager.add(this, chipInfo.name)）。
func infoChipBtns() []uiBtn {
	var out []uiBtn
	for _, row := range chipRows() {
		start := (row.page - 1) * row.per
		if start < 0 {
			start = 0
		}
		for i := 0; i < row.per; i++ {
			k := start + i
			if k >= len(row.ids) {
				break
			}
			x := row.x + float64(i)*chipStepPx
			out = append(out, uiBtn{
				Action: "chip:" + itoa(row.ids[k]),
				X:      int(x), Y: int(row.y),
				W: int(chipIconPx), H: int(chipIconPx),
			})
		}
	}
	return out
}

// rebuildInfoBtns 重建按钮表：固定控件 + 当前页芯片格（翻页后必须重建，否则热区错页）。
func rebuildInfoBtns() {
	win := infoPanelWin
	if win == nil {
		return
	}
	btns := make([]uiBtn, 0, 24)
	btns = append(btns, infoRenameBtn(nickHwnd != 0))
	if nickHwnd != 0 {
		// 编辑态：点「主人」「生日」两行可切过去接着改（昵称行是默认行，
		// 点它本身不需要热区）。子渊 2026-10-27：三行都要能改。
		for i := 1; i < 3; i++ {
			r := editRows[i]
			btns = append(btns, uiBtn{Action: "edit:" + itoa(i), X: r.BX, Y: r.BY, W: r.BW, H: 20})
		}
	}
	// closeBtn = DefineButton2_133，贴图锚点实测 (266.6,11.25)，尺寸 19.5×19.5
	btns = append(btns, uiBtn{Action: "close", X: infoCloseX, Y: infoCloseY, W: 20, H: 20,
		Up:   uiLoad("info_close.png"),
		Over: uiLoad("info_close_over.png"),
		Down: uiLoad("info_close_down.png")})
	// leftMc / rightMc：箭头已烘进底图（静态、带发光），这里只留热区。
	btns = append(btns,
		uiBtn{Action: "chipleft", X: 13, Y: 285, W: 22, H: 25},
		uiBtn{Action: "chipright", X: 264, Y: 285, W: 22, H: 25},
		// sp0 整行（"超能等级:" + 等级图标）：只出 tooltip，不可点
		uiBtn{Action: "leveltip", X: 104, Y: 64, W: 160, H: 21},
		// superIconMc["nono"]：超能芯片徽章，原版只挂 tooltip「超能芯片」
		uiBtn{Action: "chipnono", X: 30, Y: 256, W: 39, H: 36},
	)
	// 四条进度条的 tooltip 热区（原版 ToolTipManager 挂在 mc0..mc3 上）
	for i := range infoBars {
		btns = append(btns, uiBtn{
			Action: "bar" + itoa(i),
			X:      74, Y: int(infoBars[i].Y + infoBarBgDY),
			W: 203, H: 14})
	}
	btns = append(btns, infoChipBtns()...)
	win.Btns = btns
}

// 底部"加载芯片"区：原版 NewNonoInfoPanel 有**两行** ChipInfoBgPanel，
// 几何取自 SWF sprite 137（NewNonoBaseInfoMc）的 PlaceObject，twips/20：
//
//	superIconMc tx=1332 ty=5164 → (66.6, 258.2)  6 个/页  ← 超能芯片行
//	iconMc      tx= 732 ty=5932 → (36.6, 296.6)  7 个/页  ← 普通芯片行
//
// 单个 ChipInfoBgPanel 宽 27.3，`icon.x = (width + 2.6) * i` ⇒ 步距 29.9；
// 图标被缩到 25×25（原版 onComHandler 里 _engMc.width = height = 25）。
//
// 芯片集合 = 原版 _idA 全表（NewNonoInfoPanel.as:91，19 个功能芯片），
// **不是** chips.json 的全部 40 枚：变色/能量/智慧/玩具芯片在原版不进这个栏，
// 只在工坊与快捷栏出现。按 items.xml 的 VipOnly 属性分两行。
var chipBarAll = []int{
	700019, 700018, 700017, 700016, 700015, 700014, 700013,
	700012, 700011, 700010, 700009, 700008, 700007, 700006,
	700005, 700004, 700003, 700002, 700001,
}

// chipVIP 对应 /app/working/workspaces/a2ZDuB/kaiyuanhao/data/items.xml 的 VipOnly="1"（超能专属 → 第一行）。
var chipVIP = map[int]bool{
	700011: true, 700012: true, 700014: true, 700015: true,
	700016: true, 700017: true, 700019: true,
	700013: true, 700018: true,
}

const (
	chipNormalPerPage = 7    // 原版 _page
	chipSuperPerPage  = 6    // 原版 _superPage
	chipIconPx        = 25.0 // 原版 _engMc.width/height
	chipStepPx        = 29.9 // 27.3 + 2.6
	chipSuperX        = 66.6 // superIconMc
	chipSuperY        = 258.2
	chipNormalX       = 36.6 // iconMc
	chipNormalY       = 296.6
)

// infoChipPage[0] = 超能行当前页(_curSuperPage)，infoChipPage[1] = 普通行(_curPage)，1-based。
var infoChipPage = [2]int{1, 1}

// chipRow 一行的绘制参数。
type chipRow struct {
	ids  []int
	per  int
	x, y float64
	page int
}

// chipRows 返回两行（超能行在前）的芯片与几何。
func chipRows() []chipRow {
	super := make([]int, 0, len(chipBarAll))
	normal := make([]int, 0, len(chipBarAll))
	for _, id := range chipBarAll {
		if chipVIP[id] {
			super = append(super, id)
		} else {
			normal = append(normal, id)
		}
	}
	return []chipRow{
		{ids: super, per: chipSuperPerPage, x: chipSuperX, y: chipSuperY, page: infoChipPage[0]},
		{ids: normal, per: chipNormalPerPage, x: chipNormalX, y: chipNormalY, page: infoChipPage[1]},
	}
}

// chipPageCount 返回该行总页数（至少 1 页，原版 makePage 同口径）。
func chipPageCount(n, per int) int {
	if n <= per {
		return 1
	}
	if n%per == 0 {
		return n / per
	}
	return n/per + 1
}

// chipFlip 左右翻页。原版 onLeftHandler/onRightHandler 是两行一起翻。
func chipFlip(dir int) {
	rows := chipRows()
	for i, r := range rows {
		total := chipPageCount(len(r.ids), r.per)
		p := infoChipPage[i] + dir
		if p < 1 {
			p = 1
		}
		if p > total {
			p = total
		}
		infoChipPage[i] = p
	}
	if infoPanelWin != nil {
		rebuildInfoBtns() // 翻页后芯片热区要跟着换，否则 hovering 到的是上一页的芯片名
		infoPanelWin.paint()
	}
}

func infoPanelDraw(img *image.RGBA, s float64) {
	if g == nil || img == nil {
		return
	}
	if s <= 0 {
		s = 1
	}
	// 原版层级：bg < mc0..3 < faceMc < sp1..3 < sp0 < 按钮 < 芯片行。
	infoDrawBars(img, s)
	infoDrawFace(img, s)
	if nickHwnd != 0 {
		// 编辑高亮条画在文字之前：原版 _nickBg 在 _nickTxt 下面，
		// 叠在上面会让文字发灰。
		infoDrawEditBg(img, s)
	}
	infoDrawLabels(img, s) // 「AI 等级:/主　人:/生　日:」标签贴图
	infoDrawLevel(img, s)  // sp0 整帧（含「超能等级:」）
	infoDrawChips(img, s)
}

// infoDrawChips 画两行芯片（原版 iconMc / superIconMc 里的 ChipInfoBgPanel）。
func infoDrawChips(img *image.RGBA, s float64) {
	owned := map[int]bool{}
	for _, id := range g.Save.Chips {
		owned[id] = true
	}
	for _, row := range chipRows() {
		start := (row.page - 1) * row.per
		if start < 0 {
			start = 0
		}
		for i := 0; i < row.per; i++ {
			k := start + i
			if k >= len(row.ids) {
				break // 末页不足一页：原版 removeIcon 留空
			}
			id := row.ids[k]
			icon := uiFit(uiLoad("chips/"+itoa(id)+".png"),
				int(chipIconPx*s+0.5), int(chipIconPx*s+0.5))
			if icon == nil {
				continue
			}
			if !owned[id] {
				// 未装配：去色 + 压暗，模拟 ChipInfoBgPanel 的 ColorFilter.setGrayscale()
				icon = grayIcon(icon)
			}
			x := row.x + float64(i)*chipStepPx
			uiBlend(img, icon, int(x*s+0.5), int(row.y*s+0.5))
		}
	}
}

// infoDrawBars 四条进度条：底框（cid22）+ 填充（cid20，原版靠 scaleX 拉伸）+ 灰度。
func infoDrawBars(img *image.RGBA, s float64) {
	bg := uiLoad("info_bar_bg.png")
	fill := uiLoad("info_bar_fill.png")
	if infoIsSuper() {
		infoBarFillCache = nil
	} else if infoBarFillCache == nil && fill != nil {
		// 非超能：mc0 要整条灰度（原版 mc0.filters=[setGrayscale]）。满宽固定，可缓存。
		infoBarFillCache = &image.RGBA{}
		*infoBarFillCache = *grayIcon(fill)
	}
	for i, b := range infoBars {
		p := b.Pct(g)
		if p < 0 {
			p = 0
		}
		if p > 1 {
			p = 1
		}
		gray := i == 0 && !infoIsSuper()
		bx := infoBarBgX
		by := b.Y + infoBarBgDY
		bb := bg
		if gray {
			bb = grayCached("info_bar_bg.png", bg)
		}
		if bb != nil {
			uiBlend(img, uiFit(bb, int(infoBarBgW*s+0.5), int(infoBarBgH*s+0.5)),
				int(bx*s+0.5), int(by*s+0.5))
		}
		w := int(infoBarFillW*p*s + 0.5)
		if w <= 0 || fill == nil {
			continue
		}
		f := fill
		if gray {
			f = infoBarFillCache
		}
		uiBlend(img, uiFit(f, w, int(infoBarFillH*s+0.5)),
			int((bx+infoBarFillDX)*s+0.5), int((by+infoBarFillDY)*s+0.5))
	}
}

// grayCached 灰度图缓存（非超能形态每帧都要用，避免重复分配）。
var infoGrayCache = map[string]*image.RGBA{}

func grayCached(name string, src *image.RGBA) *image.RGBA {
	if src == nil {
		return nil
	}
	if im, ok := infoGrayCache[name]; ok {
		return im
	}
	im := grayIcon(src)
	infoGrayCache[name] = im
	return im
}

// infoBarFillCache 非超能时 mc0 满宽灰填充。
var infoBarFillCache *image.RGBA

// grayIcon 复制一份并去色（不污染 uiCache 里的原图）。
func grayIcon(src *image.RGBA) *image.RGBA {
	icon := cloneRGBA(src)
	b := icon.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := icon.PixOffset(x, y)
			r0, g0, b0 := int(icon.Pix[i]), int(icon.Pix[i+1]), int(icon.Pix[i+2])
			gray := (r0*299 + g0*587 + b0*114) / 1000
			gray = gray/2 + 90
			icon.Pix[i] = uint8(gray)
			icon.Pix[i+1] = uint8(gray)
			icon.Pix[i+2] = uint8(gray)
			icon.Pix[i+3] = uint8(int(icon.Pix[i+3]) * 3 / 4)
		}
	}
	return icon
}

// infoDrawLabels 画 sp1/sp2/sp3 的静态标签（这 3 个 sprite 导出图里只有标签，
// 数值是 TextField，运行时才有值，所以值由 infoPanelLabels 用 GDI 画）。
func infoDrawLabels(img *image.RGBA, s float64) {
	for _, t := range []struct {
		name string
		x, y float64
		w, h float64
	}{
		{"info_lbl_nick.png", infoLblNickX, infoLblNickY, infoLblNickW, infoLblNickH},
		{"info_lbl_ai.png", infoLblAIX, infoLblAIY, infoLblAIW, infoLblAIH},
		{"info_lbl_owner.png", infoLblOwnerX, infoLblOwnerY, infoLblOwnerW, infoLblOwnerH},
		{"info_lbl_birth.png", infoLblBirthX, infoLblBirthY, infoLblBirthW, infoLblBirthH},
	} {
		if im := uiLoad(t.name); im != nil {
			uiBlend(img, uiFit(im, int(t.w*s+0.5), int(t.h*s+0.5)),
				int(t.x*s+0.5), int(t.y*s+0.5))
		}
	}
}

// infoDrawLevel 画 sp0 = DefineSprite_118 的第 superLevel 帧。
// 整帧含「超能等级:」标签 + 球N翼图标，直接按 4x 贴图绘制（不再擦字重画）。
// 非超能形态：原版 `_ps0Mc.filters=[ColorFilter.setGrayscale()]`。
func infoDrawLevel(img *image.RGBA, s float64) {
	lv := infoSuperL()
	name := "info_lv_" + itoa(lv) + ".png"
	ic := uiLoad(name)
	if ic == nil {
		return
	}
	if !infoIsSuper() {
		ic = grayCached(name, ic)
	}
	uiBlend(img, uiFit(ic, int(infoSp0W*s+0.5), int(infoSp0H*s+0.5)),
		int(infoSp0X*s+0.5), int(infoSp0Y*s+0.5))
}

// infoDrawFace 画 faceMc（DefineSprite_84）。
//
//	普通：norMc = DefineSprite_83 = 身体(77) + eyeMc(82 四帧，帧号 makeNorNoFace)
//	超能：superMc = DefineSprite_71 = bgMc(66 四帧，帧号 superStage) + eyeMc(70 三帧)
//	      eyeMc：mate≤30→3、≤60→1、其它→2
func infoDrawFace(img *image.RGBA, s float64) {
	if infoIsSuper() {
		stage := superLevelToForm(g.Save.SuperLevel)
		if stage < 1 {
			stage = 1
		}
		if stage > 4 { // bgMc 只有 4 帧
			stage = 4
		}
		bx, by := infoFaceX, infoFaceY
		if body := uiLoad("info_super_body_" + itoa(stage) + ".png"); body != nil {
			uiBlend(img, uiFit(body, int(infoSuperBodyW*s+0.5), int(infoSuperBodyH*s+0.5)),
				int(bx*s+0.5), int(by*s+0.5))
		}
		eye := 2
		switch {
		case g.Save.Mate <= 30:
			eye = 3
		case g.Save.Mate <= 60:
			eye = 1
		}
		if ev := uiLoad("info_super_eye_" + itoa(eye) + ".png"); ev != nil {
			uiBlend(img, uiFit(ev, int(infoEyeW*s+0.5), int(infoEyeH*s+0.5)),
				int((bx+infoSuperEyeDX)*s+0.5), int((by+infoSuperEyeDY)*s+0.5))
		}
		return
	}
	bx, by := infoNorBodyX, infoNorBodyY
	if body := uiLoad("info_nor_body.png"); body != nil {
		uiBlend(img, uiFit(body, int(infoNorBodyW*s+0.5), int(infoNorBodyH*s+0.5)),
			int(bx*s+0.5), int(by*s+0.5))
	}
	idx := infoNorFace(g.Save.Power, g.Save.Mate)
	if ev := uiLoad("info_nor_eye_" + itoa(idx) + ".png"); ev != nil {
		uiBlend(img, uiFit(ev, int(infoEyeW*s+0.5), int(infoEyeH*s+0.5)),
			int((bx+infoNorEyeDX)*s+0.5), int((by+infoNorEyeDY)*s+0.5))
	}
}

// infoNickBgAlpha 取 nickBgMc 贴图并按百分比压低不透明度（原版 alpha=0.3/0.6）。
// 编辑态三行共用这张图，当前行 45%、其余 22%，一眼能看出在改哪一行。
func infoNickBgAlpha(pct int) *image.RGBA {
	if im, ok := infoBgCache[pct]; ok {
		return im
	}
	src := uiLoad("info_nickbg.png")
	if src == nil {
		return nil
	}
	im := cloneRGBA(src)
	for i := 3; i < len(im.Pix); i += 4 {
		im.Pix[i] = uint8(int(im.Pix[i]) * pct / 100)
	}
	if infoBgCache == nil {
		infoBgCache = map[int]*image.RGBA{}
	}
	infoBgCache[pct] = im
	return im
}

var infoBgCache map[int]*image.RGBA

// infoDrawEditBg 编辑态三行高亮条（昵称/主人/生日）。
// 昵称条几何来自 nickBgMc（84×20 @ 159,39）；主人/生日沿用同一贴图，
// 宽度按内容放宽（见 panel_input.go editRows）。
func infoDrawEditBg(img *image.RGBA, s float64) {
	for i := 0; i < 3; i++ {
		r := editRows[i]
		pct := 22
		if i == editIdx {
			pct = 45
		}
		tex := infoNickBgAlpha(pct)
		if tex == nil {
			return
		}
		uiBlend(img, uiFit(tex, int(float64(r.BW)*s+0.5), int(20*s+0.5)),
			int(float64(r.BX)*s+0.5), int(float64(r.BY)*s+0.5))
	}
}

func infoPanelLabels() []uiLabel {
	if g == nil {
		return nil
	}
	s := g.Save
	nick := s.Nick
	if nick == "" {
		nick = "NONO"
	}
	const cText = 0x00000000 // COLORREF 纯黑
	// 值的位置 = 原版 TextField 的 x/ty（twips÷20）：
	//   nickTxt (3216,872) → (160.8, 43.6)
	//   sp1.txt (1177,16)+sp1(1961,1688) → (156.9, 85.2)
	//   sp2.txt (1179,16)+sp2(1981,2048) → (158.0, 103.2)
	//   sp3.txt (1184,16)+sp3(1981,2418) → (158.25, 121.7)
	// TextField 框顶到墨迹还有 ~2.5px，uiLabel 的 Y 是绘制原点，所以 Y 取 SWF y+2.5。
	// X 额外 +6：Flash 的 TextField 左边有内边距，uiLabel 是墨迹左缘，直接对齐会压掉
	// 标签末尾的冒号（实测 "AI 等级:" 的冒号正好落在 157，值从那开始就把冒号盖没了）。
	// X 用官方 TextField 的墨迹左缘：nickTxt x=160.8，uiLabel 画的是墨迹、
	// 比 TextField 的 x 大约多 3.4px，实测 161 时冒号到值的间隙 10.7px，
	// 与官方截图的 10.2px 一致。之前写 167（+6 防压冒号）偏右 6px，
	// 就是子渊说的「昵称行位置不对」。
	const (
		nickX, nickY   = 161, 50 // 与「昵称:」标签同一墨迹带 50..62（子渊 2026-10-27：值要跟标签水平对齐）
		aiX, aiY       = 163, 88
		ownerX, ownerY = 164, 106
		birthX, birthY = 164, 124
	)
	// 三行可编辑值（昵称/主人/生日）：统一走 editValue，编辑态自动读草稿，
	// 非编辑态读存档。EDIT 控件只是键盘缓冲，显示一律由面板自绘。
	labs := []uiLabel{}
	labs = append(labs, uiLabel{Text: editValue(0), X: nickX, Y: nickY, Size: 15, Color: cText})
	labs = append(labs,
		uiLabel{Text: itoa(s.AI), X: aiX, Y: aiY, Size: 15, Color: cText},
		uiLabel{Text: editValue(1), X: ownerX, Y: ownerY, Size: 15, Color: cText},
		uiLabel{Text: editValue(2), X: birthX, Y: birthY, Size: 15, Color: cText},
	)
	// 四条进度条上的 "值/上限"：原版 mc.txt，居中压在条上（条 x 74.15..276.9，中心 175）
	for i, b := range infoBars {
		if i == 0 && !infoIsSuper() {
			continue // 原版非超能：mc0.txt.visible = false
		}
		txt := b.Value(g)
		if txt == "" {
			continue
		}
		labs = append(labs, uiLabel{Text: txt, X: infoBarCenterX,
			Y: int(b.Y + infoBarBgDY + infoBarTextDY), Size: 12, Color: cText, Center: true})
	}
	return labs
}

// infoPanelHover 原版 ToolTipManager 的挂点（NewNonoInfoPanel.as addEvent/setData/makeTips）。
func infoPanelHover(idx int) {
	if infoPanelWin == nil || idx < 0 || idx >= len(infoPanelWin.Btns) || g == nil {
		hideInfoTip()
		return
	}
	a := infoPanelWin.Btns[idx].Action
	switch {
	case a == "rename":
		showInfoTip("修改昵称")
	case a == "close":
		showInfoTip("关闭")
	case a == "leveltip":
		if infoIsSuper() {
			showInfoTip(itoa(g.Save.SuperLevel) + "级超能NoNo")
		} else {
			showInfoTip("曾经超能" + itoa(g.Save.SuperLevel) + "级")
		}
	case a == "chipnono":
		showInfoTip("超能芯片")
	case len(a) == 4 && a[:3] == "bar":
		i := int(a[3] - '0')
		if i >= 0 && i < len(infoBars) {
			showInfoTip(infoBars[i].Tip(g))
		} else {
			hideInfoTip()
		}
	case len(a) > 5 && a[:5] == "chip:":
		// 原版 ChipInfoBgPanel 的 ToolTipManager：悬停显示芯片名
		id := atoiSafe(a[5:])
		if c, ok := chipByID[id]; ok && c != nil {
			showInfoTip(c.Name)
		} else {
			hideInfoTip()
		}
	default:
		hideInfoTip()
	}
}

// infoIsSuper 当前是否超能形态（原版 _info.superNono，NewNonoInfoPanel.as:181）。
func infoIsSuper() bool {
	return g != nil && g.Save.SuperLevel > 0
}

// infoRenameBtn 改名按钮：非编辑态 changeMc（扳手，DefineSprite_120），
// 编辑态 sucMc（√，DefineSprite_122）。原版是两个实例同位叠放、visible 互斥，
// 这里整块替换（只出现一个），不会叠加。
func infoRenameBtn(editing bool) uiBtn {
	if editing {
		// sucMc：DL(246.4,43.25) + (5,5)，贴图 19.25×16
		return uiBtn{Action: "renameok", X: 251, Y: 48, W: 19, H: 16,
			Up:   uiLoad("info_ok.png"),
			Over: uiLoad("info_ok_over.png"),
			Down: uiLoad("info_ok_down.png")}
	}
	// changeMc：DL(249.6,42.1) + (5,5)，贴图 15×18.75
	return uiBtn{Action: "rename", X: 254, Y: 47, W: 15, H: 19,
		Up:   uiLoad("info_wrench.png"),
		Over: uiLoad("info_wrench_over.png"),
		Down: uiLoad("info_wrench_down.png")}
}

// setInfoRenameEditing 切换改名按钮外观（扳手 ↔ √）。
func setInfoRenameEditing(editing bool) {
	if infoPanelWin == nil {
		return
	}
	_ = editing
	rebuildInfoBtns() // infoRenameBtn(nickHwnd != 0) 内部按编辑态返回不同按钮
	infoPanelWin.paint()
}

func showInfoPanel() {
	win := ensureInfoPanel()
	if win == nil {
		return
	}
	win.Labels = infoPanelLabels()
	x, y := 0, 0
	if px, py, ok := placeBesideMainPanel(win.W, win.H, -1, 20); ok {
		// 游戏里基本资料出现在主面板左侧；子渊 2026-10-27 定为
		// 「主面板可见圆左缘再往左 20px」，不再叠进主面板窗口里（旧值 +18）。
		x, y = px, py
	} else {
		vx, vy, vw, vh := virtualScreen()
		x = vx + (vw-win.W)/2
		y = vy + (vh-win.H)/2
	}
	win.showAt(x, y)
	logf("info panel shown at %d,%d size=%dx%d", x, y, win.W, win.H)
}

func infoPanelAction(a string) {
	switch a {
	case "close":
		if infoPanelWin != nil {
			infoPanelWin.hide()
		}
		hideInfoTip() // 面板没了鼠标不再移动，提示窗会一直挂在桌面上
		closeNickInput()
	case "rename":
		// 进入就地改名模式：图标换成 √、昵称框高亮（nickBgMc alpha .3）、出现输入框。
		// 顺序必须是「先建输入框、再重建按钮」—— infoRenameBtn 判据是 nickHwnd != 0，
		// 反过来写第一次重建时 nickHwnd 还是 0，按钮仍是扳手，就成了
		// 「点一下出输入框但图标不变，点第二下才变 √」（子渊 2026-10-26 报的现象）。
		showNickInput()
		setInfoRenameEditing(true)
	case "renameok":
		commitNickInput()
	case "edit:1", "edit:2":
		// 编辑态里点「主人」「生日」两行：切到那一行继续输入（内容已预填）
		switchEditIdx(int(a[5] - '0'))
	case "chipleft":
		chipFlip(-1)
	case "chipright":
		chipFlip(1)
	}
}
