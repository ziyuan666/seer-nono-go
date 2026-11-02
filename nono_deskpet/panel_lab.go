//go:build windows

package main

// 芯片制造机（发明室）：1:1 复刻 ChipMixturePanel.swf（414×413）。
// 依据反编译脚本 scripts/com/robot/module/app/ChipMixturePanel.as：
//   - _pageNum = 4（每页 4 个可选图标槽），翻页箭头浏览
//   - 三个模板：小型/中型/大型，价格 _moneyA = [100,200,500] 赛尔豆
//   - 三个材料槽，合成包 9004：mixId(模板) + count + [itemId+num]*count
//   - 服务端按 GM 权重 ChipMixtureRules 匹配（三种材料 ID 排序后比较）
//
// 桌面版按官方配方表实现（materials.go 的 officialRecipes，成功率 100）：
// 材料是 12 种采集道具，已预置在普通储藏里，合成时真实扣除。

import (
	"image"
)

const (
	labW = 414
	labH = 413
)

var (
	labWin   *uiWin
	labTpl   int    // 0=未选，1=小型，2=中型，3=大型
	labSlots [3]int // 三个材料槽的道具 ID，0=空
	labMsg   string
)

// labTplPrice 三个模板的价格。原版消耗赛尔豆 100/200/500，桌宠折算为能量。
func labTplPrice(tpl int) int {
	switch tpl {
	case 1:
		return 30
	case 2:
		return 50
	case 3:
		return 80
	}
	return 0
}

func labTplName(tpl int) string {
	switch tpl {
	case 1:
		return "小型芯片模板"
	case 2:
		return "中型芯片模板"
	case 3:
		return "大型芯片模板"
	}
	return "未选择模板"
}

func ensureLabPanel() *uiWin {
	if labWin != nil {
		return labWin
	}
	win := newUIWin(labW, labH)
	if win == nil {
		return nil
	}
	win.Bg = uiPanel("lab.png", win.W, win.H)
	win.OnAction = labAction
	win.Draw = labDraw
	win.Labels = labLabels()
	labWin = win
	rebuildLabButtons()
	return win
}

// rebuildLabButtons 登记模板 / 材料槽 / 开始 / 重置 / 关闭的命中区。
// 坐标不再按 SWF place 推算（底图与 SWF 差 1.3~1.4 倍，会把按钮框错位），
// 改为用 tools/match_btn.py 在 lab.png 上模板匹配得到的实测矩形。
// Up 留空由底图承担，只叠 over/down：悬停/按下才有反馈。
func rebuildLabButtons() {
	win := labWin
	if win == nil {
		return
	}
	win.Btns = win.Btns[:0]
	win.Btns = append(win.Btns,
		uiBtn{Action: "close", X: 357, Y: 14, W: 32, H: 32,
			Over: uiLoad("lab_close_over.png"), Down: uiLoad("lab_close_down.png")},
		uiBtn{Action: "tpl1", X: 70, Y: 96, W: 55, H: 55,
			Over: uiLoad("lab_tpl1_over.png"), Down: uiLoad("lab_tpl1_down.png")},
		uiBtn{Action: "tpl2", X: 188, Y: 96, W: 55, H: 55,
			Over: uiLoad("lab_tpl2_over.png"), Down: uiLoad("lab_tpl2_down.png")},
		uiBtn{Action: "tpl3", X: 299, Y: 96, W: 55, H: 55,
			Over: uiLoad("lab_tpl3_over.png"), Down: uiLoad("lab_tpl3_down.png")},
		uiBtn{Action: "slot0", X: 64, Y: 162, W: 60, H: 135},
		uiBtn{Action: "slot1", X: 166, Y: 162, W: 60, H: 135},
		uiBtn{Action: "slot2", X: 270, Y: 162, W: 60, H: 135},
		// 原版左右翻页箭头：桌宠改为「打开材料选择菜单」
		uiBtn{Action: "pick", X: 41, Y: 294, W: 27, H: 31,
			Over: uiLoad("lab_pageL_over.png"), Down: uiLoad("lab_pageL_down.png")},
		uiBtn{Action: "pick", X: 349, Y: 295, W: 26, H: 29,
			Over: uiLoad("lab_pageR_over.png"), Down: uiLoad("lab_pageR_down.png")},
		uiBtn{Action: "start", X: 84, Y: 353, W: 84, H: 37,
			Over: uiLoad("lab_start_over.png"), Down: uiLoad("lab_start_down.png")},
		uiBtn{Action: "reset", X: 246, Y: 353, W: 84, H: 37,
			Over: uiLoad("lab_reset_over.png"), Down: uiLoad("lab_reset_down.png")},
	)
}

func labLabels() []uiLabel {
	if g == nil {
		return nil
	}
	out := []uiLabel{}
	if labTpl > 0 {
		out = append(out, uiLabel{Text: itoa(labTplPrice(labTpl)), X: 264, Y: 223, Size: 14, Bold: true, Color: 0x1A5FA8FF})
	}
	for i, id := range labSlots {
		if id == 0 {
			continue
		}
		name := materialName(id)
		if r := []rune(name); len(r) > 5 {
			name = string(r[:5])
		}
		out = append(out, uiLabel{Text: name, X: 94 + i*102, Y: 214, Size: 11, Center: true, Color: 0xFF664A1A})
	}
	if labMsg != "" {
		out = append(out, uiLabel{Text: labMsg, X: 207, Y: 326, Size: 11, Center: true, Color: 0xAA3300FF})
	}
	return out
}

// labDraw 画模板选中框与材料槽里的材料图标。
func labDraw(img *image.RGBA, s float64) {
	if s <= 0 {
		s = 1
	}
	if labTpl > 0 {
		bx := []int{71, 174, 301}[labTpl-1]
		for k := 0; k < 3; k++ {
			labRect(img, s, bx-k, 100-k, 48+2*k, 46+2*k, 0xFFC83C)
		}
	}
	for i, id := range labSlots {
		if id == 0 {
			continue
		}
		path := "mats/" + itoa(id) + ".png"
		icon := uiFit(uiLoad(path), int(60*s), int(60*s))
		if icon == nil {
			continue
		}
		cx := int(float64(94+i*102)*s) - icon.Bounds().Dx()/2
		cy := int(float64(173)*s)
		uiBlend(img, icon, cx, cy)
	}
}

// labRect 在缩放坐标下画一个纯色矩形边框。
func labRect(img *image.RGBA, s float64, x, y, w, h int, rgb uint32) {
	x0, y0 := int(float64(x)*s), int(float64(y)*s)
	x1, y1 := int(float64(x+w)*s), int(float64(y+h)*s)
	b := img.Bounds()
	r := uint8(rgb >> 24)
	gg := uint8(rgb >> 16)
	bl := uint8(rgb >> 8)
	put := func(px, py int) {
		if px < 0 || py < 0 || px >= b.Dx() || py >= b.Dy() {
			return
		}
		i := img.PixOffset(px, py)
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = r, gg, bl, 255
	}
	for t := 0; t < 2; t++ {
		for px := x0; px < x1; px++ {
			put(px, y0+t)
			put(px, y1-1-t)
		}
		for py := y0; py < y1; py++ {
			put(x0+t, py)
			put(x1-1-t, py)
		}
	}
}

func labAction(a string) {
	win := labWin
	switch {
	case a == "close":
		if win != nil {
			win.hide()
		}
		labMsg = ""
	case a == "tpl1":
		labTpl = 1
		labRefresh()
	case a == "tpl2":
		labTpl = 2
		labRefresh()
	case a == "tpl3":
		labTpl = 3
		labRefresh()
	case a == "slot0":
		labPickSlot(0)
	case a == "slot1":
		labPickSlot(1)
	case a == "slot2":
		labPickSlot(2)
	case a == "pick":
		labPickMenu()
	case a == "start":
		g.labMix()
	case a == "reset":
		labSlots = [3]int{}
		labMsg = "材料已清空"
		labRefresh()
	}
}

func labRefresh() {
	if labWin == nil {
		return
	}
	labWin.Labels = labLabels()
	labWin.paint()
}

// labPickSlot 点材料槽：在 12 种材料里循环切换，跳过库存为 0 的。
func labPickSlot(idx int) {
	if g == nil {
		return
	}
	g.Save.ensureMaterials()
	// 候选顺序：空 → 12 种材料 → 回到空。空槽合法（官方配方有的只要 1~2 种材料）。
	cur := -1
	if labSlots[idx] == 0 {
		cur = 0
	} else {
		for i, d := range materialDefs {
			if d.ID == labSlots[idx] {
				cur = i + 1
				break
			}
		}
	}
	total := len(materialDefs) + 1
	next := 0
	for k := 1; k <= total; k++ {
		pos := (cur + k) % total
		if pos == 0 {
			next = 0
			break
		}
		d := materialDefs[pos-1]
		next = d.ID
		break
	}
	labSlots[idx] = next
	labMsg = ""
	labRefresh()
}

// labMix 执行合成：校验模板 + 材料 + 能量，按官方配方表匹配（成功率 100）。
func (g *Game) labMix() {
	if labTpl == 0 {
		labMsg = "你还没有选择芯片模板哦！"
		labRefresh()
		return
	}
	if labSlots[0] == 0 && labSlots[1] == 0 && labSlots[2] == 0 {
		labMsg = "先放点材料进去吧"
		labRefresh()
		return
	}
	g.Save.ensureMaterials()
	price := labTplPrice(labTpl)
	if g.Save.Power < price {
		labMsg = "能量不足，需要 " + itoa(price)
		labRefresh()
		return
	}
	chip := matchOfficialRecipe(labTpl, labSlots)
	if chip == 0 {
		labMsg = "这个组合合不出芯片（对照配方书再试试）"
		labRefresh()
		return
	}
	g.Save.Power -= price
	// 材料无限使用（子渊 2026-10-23 决定）：只校验配方，不扣库存。
	if g.Save.Counts == nil {
		g.Save.Counts = map[int]int{}
	}
	if !g.Save.hasChip(chip) {
		g.Save.Chips = append(g.Save.Chips, chip)
	}
	g.Save.Counts[chip]++
	g.Save.write()

	name := ""
	if c := chipByID[chip]; c != nil {
		name = c.Name
	}
	labSlots = [3]int{}
	labMsg = "合成成功：" + name + "！"
	logf("lab mix tpl=%d price=%d chip=%d", labTpl, price, chip)
	labRefresh()
	if g.Save.Sound {
		g.playAction(2)
	}
}

// matchOfficialRecipe 按模板 + 三个材料匹配官方配方。材料排序后比较，槽位顺序无关，
// 空槽（0）必须对上空槽。
func matchOfficialRecipe(tpl int, slots [3]int) int {
	a, b, c := sortThree(slots[0], slots[1], slots[2])
	for _, r := range officialRecipes {
		if r.Tpl != tpl {
			continue
		}
		r1, r2, r3 := sortThree(r.Mat1, r.Mat2, r.Mat3)
		if r1 == a && r2 == b && r3 == c {
			return r.Chip
		}
	}
	return 0
}

// sortThree 三数升序。
func sortThree(x, y, z int) (int, int, int) {
	if x > y {
		x, y = y, x
	}
	if y > z {
		y, z = z, y
	}
	if x > y {
		x, y = y, x
	}
	return x, y, z
}

// showLabPanel 打开芯片制造机（右键菜单入口）。
func (g *Game) showLabPanel() {
	win := ensureLabPanel()
	if win == nil {
		return
	}
	g.Save.ensureMaterials()
	labMsg = ""
	win.Labels = labLabels()
	if mainPanelWin != nil {
		mainPanelWin.hide()
	}
	vx, vy, vw, vh := virtualScreen()
	win.showAt(clamp(vx+(vw-win.W)/2, vx, vx+vw-win.W), clamp(vy+(vh-win.H)/2, vy, vy+vh-win.H))
}
