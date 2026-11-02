//go:build windows

package main

// 储藏空间：1:1 复刻 MachineDogStoragePanel（242×300）。
// 依据 MachineDogStoragePanel.as：
//   - 9 格/页，格 60×60，间距 8，起点 (7.5,35.9)
//   - 双页签：普通储藏 / 超能储藏
//   - 翻页 leftBtn/rightBtn，pageTxt 显示 "当前/总数"
//   - 点击芯片走校验链（超能 → 开机 → AI → 飞行等级）后执行 Chip_xxxxxx

import (
	"image"
	"sort"
)

const (
	storagePanelW = 242
	storagePanelH = 300
	storageCols   = 3
	storageRows   = 3
	storagePerPage = storageCols * storageRows
	storageCellW  = 60
	storageGap    = 8
	storageCellX0 = 23
	storageCellY0 = 52
)

var (
	storagePanelWin *uiWin
	storagePage     int
	storageTab      int // 0 普通 1 超能
)

func ensureStoragePanel() *uiWin {
	if storagePanelWin != nil {
		return storagePanelWin
	}
	win := newUIWin(storagePanelW, storagePanelH)
	if win == nil {
		return nil
	}
	win.Bg = uiPanel("storage.png", win.W, win.H)
	win.OnAction = storageAction
	win.OnHover = storageHover
	win.Draw = storageDraw
	win.Labels = storageLabels()
	storagePanelWin = win
	rebuildStorageButtons()
	return win
}

// rebuildStorageButtons 按当前页与页签重建按钮（格子数量随内容变化）。
func rebuildStorageButtons() {
	win := storagePanelWin
	if win == nil {
		return
	}
	win.Btns = win.Btns[:0]
	// 背景图 storage.png 是面板的「静止态」（页签左亮右暗），所以 Up 一律留空，
	// 只叠 over/down：悬停/按下时才有反馈，静止时仍是底图，不会出现双重页签。
	// 命中区按 storage.png 实测像素标定（4x 底图 ÷4）：
	// 页签 17..107 / 109..195，右上 i 按钮 203..220，翻页箭头 y 258 左右各一。
	win.Btns = append(win.Btns,
		uiBtn{Action: "tab0", X: 17, Y: 25, W: 90, H: 21,
			Over: uiLoad("storage_tab1_over.png"), Down: uiLoad("storage_tab1_down.png")},
		uiBtn{Action: "tab1", X: 109, Y: 25, W: 86, H: 21,
			Over: uiLoad("storage_tab2_over.png"), Down: uiLoad("storage_tab2_down.png")},
		uiBtn{Action: "close", X: 202, Y: 27, W: 20, H: 20,
			Over: uiLoad("storage_close_over.png"), Down: uiLoad("storage_close_down.png")},
		uiBtn{Action: "pageL", X: 63, Y: 256, W: 20, H: 22,
			Over: uiLoad("storage_pageL_over.png"), Down: uiLoad("storage_pageL_down.png")},
		uiBtn{Action: "pageR", X: 157, Y: 256, W: 20, H: 22,
			Over: uiLoad("storage_pageR_over.png"), Down: uiLoad("storage_pageR_down.png")},
	)
	ids := storagePageIDs()
	for i, id := range ids {
		col, row := i%storageCols, i/storageCols
		x := storageCellX0 + col*(storageCellW+storageGap)
		y := storageCellY0 + row*(storageCellW+storageGap)
		win.Btns = append(win.Btns, uiBtn{
			Action: "chip:" + itoa(id),
			X:      x, Y: y, W: storageCellW, H: storageCellW,
		})
	}
	win.Labels = storageLabels()
}

// storagePageIDs 返回当前页的芯片 id（超能页暂空）。
func storagePageIDs() []int {
	if g == nil {
		return nil
	}
	chips := storageChipsFor(storageTab)
	start := storagePage * storagePerPage
	if start >= len(chips) {
		return nil
	}
	end := start + storagePerPage
	if end > len(chips) {
		end = len(chips)
	}
	ids := chips[start:end]
	logf("storage page %d/%d ids=%v", storagePage+1, storageTotalPages(), ids)
	return ids
}

// isSuperStorageChip 判定芯片是否归超能储藏页。
// 依据原版超能道具页的语义：名字带「超能」，或属于超能形态才有的能力
// （屏障 / 精炼 / 炫光 / 元神赋形 / 飞行）。超能魔方属超能专属玩具，一并归此页。
func isSuperStorageChip(id int) bool {
	switch id {
	case 700012, 700014, 700017, 700018, 700020, 700502:
		return true
	}
	return false
}

// storageChipsFor 返回指定页签的芯片列表。
// 普通页排除常驻功能芯片（它们只出现在芯片环里，原版也不进储藏）；
// 超能页只放超能专属芯片。
func storageChipsFor(tab int) []int {
	if g == nil {
		return nil
	}
	out := make([]int, 0, len(g.Save.Chips))
	for _, id := range g.Save.Chips {
		sup := isSuperStorageChip(id)
		// 2026-10-27 子渊：普通页不再按形态藏东西，全部摆出来，
		// 用不用得了交给悬浮提示说明（见 storageHover）。
		if tab == 1 && !sup {
			continue
		}
		// 常驻功能芯片（芯片环里那几个）仍然不进储藏；但超能专属的
		// 700017/700018 虽然也在环里，储藏里要留位置。
		if isFuncChip(id) && !sup {
			continue
		}
		out = append(out, id)
	}
	// 固定排序（子渊 2026-10-27）：玩具 → 变色（自定义两枚最前）→ 其他功能类。
	// 原实现沿用的是 ID 升序，看起来稳定，但变色芯片和玩具、能量芯片混在一起，
	// 找一枚芯片要在几页里翻。现在按类别分段，段内仍按 ID 升序，位置永远不变。
	// 材料是 4000xx 道具，排最后一段（见下方追加）。
	sort.SliceStable(out, func(i, j int) bool {
		gi, si := storageOrderRank(out[i])
		gj, sj := storageOrderRank(out[j])
		if gi != gj {
			return gi < gj
		}
		return si < sj
	})
	// 普通页末尾追加材料（子渊 2026-10-23 决定：材料直接放在普通储藏里，且无限使用）。
	// 材料是 4000xx 道具，与 700xxx 芯片靠 ID 段区分，渲染时走 mats/ 图标。
	// 因为不消耗，这里不按数量过滤，12 种恒定全部展示。
	if tab == 0 {
		for _, d := range materialDefs {
			out = append(out, d.ID)
		}
		sort.SliceStable(out, func(i, j int) bool {
			gi, si := storageOrderRank(out[i])
			gj, sj := storageOrderRank(out[j])
			if gi != gj {
				return gi < gj
			}
			return si < sj
		})
	}
	return out
}

// storageOrderRank 返回储藏格的分段键：(组, 组内序号)。
//
//	组 0 玩具（kind 101）
//	组 1 变色（kind 2），其中 700313 自定义颜色、700314 自定义渐变排最前
//	组 2 其他功能类（功能 / 能量 / 智慧）
//	组 3 合成材料（4000xx 道具）
//
// 组内仍按 ID 升序，保证同一个存档在任何时候打开都落在同一格。
func storageOrderRank(id int) (int, int) {
	if isMaterial(id) {
		return 3, id
	}
	kind := 0
	if c := chipByID[id]; c != nil {
		kind = c.Kind
	}
	switch kind {
	case 101:
		return 0, id
	case 2:
		switch id {
		case 700313:
			return 1, -2
		case 700314:
			return 1, -1
		}
		return 1, id
	}
	return 2, id
}

// storageChips 过滤掉功能芯片：它们是常驻功能（func 位控制），不作为储藏物品摆放。
func storageChips() []int {
	if g == nil {
		return nil
	}
	out := make([]int, 0, len(g.Save.Chips))
	for _, id := range g.Save.Chips {
		if isFuncChip(id) {
			continue
		}
		out = append(out, id)
	}
	return out
}

func storageTotalPages() int {
	if g == nil {
		return 1
	}
	n := len(storageChipsFor(storageTab))
	if n == 0 {
		return 1
	}
	p := (n + storagePerPage - 1) / storagePerPage
	if p < 1 {
		p = 1
	}
	return p
}

func storageLabels() []uiLabel {
	if g == nil {
		return nil
	}
	total := storageTotalPages()
	if storagePage >= total {
		storagePage = total - 1
	}
	labels := []uiLabel{}
	if total > 1 {
		// 页码放左右翻页按钮正中间（按钮 pageL 63..83、pageR 157..177，
		// 中点 120），字号取按钮高度 22 的 90% ≈ 20，颜色取按钮蓝色
		// #4FB3E8（uiLabel.Color 是 0x00BBGGRR）。
		labels = append(labels, uiLabel{
			Text:   itoa(storagePage+1) + "/" + itoa(total),
			X:      120,
			Y:      255,
			Size:   25,
			Center: true,
			Color:  0x00E8B34F,
		})
	}
	return labels
}

func storageDraw(img *image.RGBA, s float64) {
	if s <= 0 {
		s = 1
	}
	// 页签底图 storage.png 自带「左亮右暗」的固定状态，且压暗矩形会把
	// 「超能储藏」的青绿字糊成灰块（子渊 2026-10-23 反馈）。这里不再压暗，
	// 改用页签底部一条蓝色下划线标示当前选中页。
	tabX, tabW := 17, 88
	if storageTab == 1 {
		tabX, tabW = 107, 73
	}
	barY0 := int(48 * s)
	barY1 := int(50 * s)
	for yy := barY0; yy < barY1; yy++ {
		for xx := int(float64(tabX+8) * s); xx < int(float64(tabX+tabW-8)*s); xx++ {
			if xx < 0 || yy < 0 || xx >= img.Bounds().Dx() || yy >= img.Bounds().Dy() {
				continue
			}
			i := img.PixOffset(xx, yy)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 0x2A, 0x9F, 0xD4, 255
		}
	}

	ids := storagePageIDs()
	iconSize := int(44*s + 0.5)
	for i, id := range ids {
		col, row := i%storageCols, i/storageCols
		x := int(float64(storageCellX0+col*(storageCellW+storageGap)) * s)
		y := int(float64(storageCellY0+row*(storageCellW+storageGap)) * s)
		// 自定义两枚芯片没有独立素材，走运行时合成（外壳 + 当前色）。
		iconPath := "chips/" + itoa(id) + ".png"
		if isMaterial(id) {
			iconPath = "mats/" + itoa(id) + ".png"
		}
		var icon *image.RGBA
		if isMaterial(id) {
			icon = uiFit(uiLoad(iconPath), iconSize, iconSize)
		} else {
			icon = uiFit(chipIconImage(id), iconSize, iconSize)
		}
		if icon != nil {
			uiBlend(img, icon, x+(int(float64(storageCellW)*s)-iconSize)/2, y+int(8*s))
		} else {
			// 原版缺图（如 700020 超能炫光没有 chip/700020.swf），画个淡色方块占位，
			// 避免格子和空白混淆——芯片本身仍然可用
			storagePlaceholder(img, x+6, y+6, int(float64(storageCellW)*s)-12, int(float64(storageCellW)*s)-12)
		}
	}
}

func showStoragePanel() {
	win := ensureStoragePanel()
	if win == nil {
		return
	}
	rebuildStorageButtons()
	if chipPanelWin != nil && chipPanelWin.Visible {
		chipPanelWin.hide()
	}
	x, y := 0, 0
	if px, py, ok := placeBesideMainPanel(win.W, win.H, +1, 30); ok {
		// 游戏里储藏空间挂在主面板右侧；子渊 2026-10-27 定为
		// 「主面板可见圆右缘再往右 20px」，不再叠进主面板窗口里（旧值 -18）。
		x, y = px, py
	} else {
		vx, vy, vw, vh := virtualScreen()
		x = vx + vw - win.W - 24
		y = vy + (vh-win.H)/2
	}
	win.showAt(x, y)
	logf("storage panel shown at %d,%d size=%dx%d", x, y, win.W, win.H)
}

// storageHover 悬停在芯片格子上时，只在「当前形态用不了」时提示要求。
// 能用就不说话——子渊 2026-10-27：可用时不显示。
func storageHover(idx int) {
	win := storagePanelWin
	if g == nil || win == nil || idx < 0 || idx >= len(win.Btns) {
		return
	}
	a := win.Btns[idx].Action
	if len(a) <= 5 || a[:5] != "chip:" {
		return
	}
	if msg := chipFormRequirement(atoiSafe(a[5:])); msg != "" {
		// 用 say 而不是 setBubble：setBubble 会把 BubbleW 写进窗口宽度，
		// 窗口变宽后锚点会跳，气泡反而跑出屏幕。提示文案控制在十几个字，
		// 289 宽的窗口里 13 号字能完整显示。
		// 直接用 say 的通道，但把停留帧数拉到 4 倍：tick 是跟随渲染帧循环跑的，
		// 60 帧在高帧率下只有一两百毫秒，鼠标一扫就看不见了。
		g.Bubble = msg
		g.BubbleTicks = 240
		logf("say: %s", msg)
	}
}

// chipFormRequirement 返回「当前形态还差什么」的提示，可用时返回空串。
// 2026-10-27 子渊：颜色芯片（kind 2）所有形态都能用，也不再卡智能——
// 只有真正的超能专属道具才提示形态要求。
func chipFormRequirement(id int) string {
	if isSuperStorageChip(id) && !g.Save.Super {
		return "需超能形态的NONO"
	}
	return ""
}

func storageAction(a string) {
	switch {
	case a == "close":
		if storagePanelWin != nil {
			storagePanelWin.hide()
		}
	case a == "tab0":
		storageTab = 0
		storagePage = 0
		rebuildStorageButtons()
		logf("storage tab=0 chips=%d pages=%d", len(storageChipsFor(0)), storageTotalPages())
	case a == "tab1":
		// 超能页只放超能专属芯片，普通形态打开也没有意义（原版：只有超能NoNo可用）。
		// Super 现在由超能形态菜单写入（20261023-06），所以正常情况能进。
		if g == nil || !g.Save.Super {
			g.say("只有超能NoNo才能使用超能储藏哦！")
			return
		}
		storageTab = 1
		storagePage = 0
		rebuildStorageButtons()
		logf("storage tab=1 chips=%d pages=%d", len(storageChipsFor(1)), storageTotalPages())
	case a == "pageL":
		if storagePage > 0 {
			storagePage--
			rebuildStorageButtons()
		}
	case a == "pageR":
		if storagePage < storageTotalPages()-1 {
			storagePage++
			rebuildStorageButtons()
		}
	case len(a) > 5 && a[:5] == "chip:":
		id := atoiSafe(a[5:])
		if id <= 0 || g == nil {
			return
		}
		// 校验只剩两条（2026-10-27 子渊要求放开）：
		//   1. 超能专属道具仍要超能形态——和悬浮提示保持一致，不出现「提示要用不了、
		//      点了却能装上」的矛盾；颜色芯片（7003xx）不在名单里，任何形态都能用。
		//   2. 开机校验保留（原版行为，能量类芯片例外）。
		// AI 门槛整条删除：颜色/玩具/功能芯片不再卡智能。
		if isSuperStorageChip(id) && !g.Save.Super {
			g.say("需超能形态的NONO")
			return
		}
		if !g.Save.Open && !(id >= 700401 && id <= 700410) {
			g.say("该芯片只有在开机状态下才能使用。")
			return
		}
		g.Save.consumeChip(id)
		g.useChip(id) // 使用成功的反馈就是外观/数值变化本身，不再弹气泡
		g.Save.write()
		rebuildStorageButtons()
	}
	if storagePanelWin != nil {
		storagePanelWin.paint()
	}
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// chipShortName 从 chips.json 的名字里取简短显示名；材料走 materialName。
func chipShortName(id int) string {
	if isMaterial(id) {
		if n := materialName(id); n != "" {
			return n
		}
		return itoa(id)
	}
	if c, ok := chipByID[id]; ok && c != nil {
		return c.Name
	}
	return itoa(id)
}

// storagePlaceholder 画一个淡蓝方块，用于原版就没有图标的芯片（如 700020）。
func storagePlaceholder(img *image.RGBA, x, y, w, h int) {
	b := img.Bounds()
	for yy := y; yy < y+h; yy++ {
		if yy < 0 || yy >= b.Dy() {
			continue
		}
		for xx := x; xx < x+w; xx++ {
			if xx < 0 || xx >= b.Dx() {
				continue
			}
			i := img.PixOffset(xx, yy)
			img.Pix[i] = 198
			img.Pix[i+1] = 216
			img.Pix[i+2] = 232
			img.Pix[i+3] = 170
		}
	}
}
