//go:build windows

package main

// 功能芯片环：对应客户端 CanChipCollectionPanel。
// 不是矩形面板，而是一圈图标绕主面板中间按钮展开：
// _radiusNum = 180，每页 8 个，位置 = 中心 + (cos,sin)*180，deg = i*360/8。

import (
	"image"
	"math"
)

const (
	// 椭圆半轴 A≈341 / B≈271（面板半宽/半高 + 间距 + 图标半宽），
	// 窗口再各加一个图标尺寸，保证最外圈图标不被裁
	// 主面板可见内容实际是 280×280 的圆形（main.png 535×395 里留白很大），
	// 环按圆形等距外扩：R = 140 + 间距 + 图标半宽
	chipPanelW  = 560
	chipPanelH  = 560
	chipCenterX = 280
	chipCenterY = 280
	chipPerPage = 8
	chipIcon    = 133  // MC700001 等按钮原生尺寸
	chipGap     = 6.0 // 图标外缘到面板轮廓的间距
)

var (
	chipPage     = 1
	chipPanelWin *uiWin
)

// 功能芯片全集（CanChipCollectionPanel._allChipId）
var funcChipIDs = []int{700001, 700002, 700003, 700004, 700005, 700011, 700015, 700016, 700017, 700018}

// inChipRing 判定某芯片是否已出现在「功能芯片环」上（funcChipIDs）。
//
// 20261027-13（子渊：「把那一圈有的功能先从菜单去掉」）：芯片环能直接点的功能芯片，
// 右键「使用芯片 → 功能芯片」不再重复列一遍，右键只保留环上没有的那 11 枚。
// 为什么不做成「环扩到 21 枚」：原版 CanChipCollectionPanel._allChipId 就是这 10 个，
// 扩环属于偏离官方，且环每页 8 个会多出一页。
func inChipRing(id int) bool {
	for _, v := range funcChipIDs {
		if v == id {
			return true
		}
	}
	return false
}

// menuHiddenChipIDs：芯片环上没有、但子渊指定不再放进右键「使用芯片 → 功能芯片」的芯片。
//
// 20261027-14：700006 机械臂 —— 芯片默认已加载（save.go 的 ensureAllChips），
// 效果就是「玩球」，本体动作/表情已经能表达，不需要菜单里多一个入口。
// 以后要再收口，只往这里加 ID 即可。
var menuHiddenChipIDs = []int{700006}

// hiddenFromChipMenu 判定某功能芯片是否应从右键菜单里隐藏：
// 芯片环上已有的（inChipRing） + 显式黑名单（menuHiddenChipIDs）。
func hiddenFromChipMenu(id int) bool {
	if inChipRing(id) {
		return true
	}
	for _, v := range menuHiddenChipIDs {
		if v == id {
			return true
		}
	}
	return false
}

// 提示语（_tipA，与 id 一一对应）
var chipTips = map[int]string{
	700001: "精灵恢复",
	700002: "分子转化仪",
	700003: "经验分配器",
	700004: "精灵模拟训练",
	700005: "跟随模式",
	700011: "精灵仓库",
	700015: "精灵追踪",
	700016: "技能排序",
	700017: "开启飞行模式",
	700018: "元神赋形",
}

// chipIDs 返回功能芯片全集。
// 原版 CanChipCollectionPanel：700001-700003 恒显示，其余按 func 位（已装芯片）追加。
// 20261023-05：原实现「不做功能位限制，统一铺满」，与 §8.2 的门禁语义不符，改为按
// Save.Chips 过滤；超能形态放行全部（原版 VipOnly 分支）。
func chipIDs() []int {
	out := make([]int, 0, len(funcChipIDs))
	for _, id := range funcChipIDs {
		if id <= 700003 || g == nil || g.Save == nil {
			out = append(out, id)
			continue
		}
		if g.Save.Super || g.Save.hasChip(id) {
			out = append(out, id)
		}
	}
	return out
}

// chipTotalPages 页数按**固定槽位数**算，不随已装芯片增减而变。
// 原实现按 len(chipIDs()) 算，少装一个芯片就会少一页、后面的芯片整体前移，
// 子渊 2026-10-27 明确要求「位置排版固定」，所以页数也固定。
func chipTotalPages() int {
	n := (len(funcChipIDs) + chipPerPage - 1) / chipPerPage
	if n < 1 {
		n = 1
	}
	return n
}

// chipPos 返回第 i 个图标的中心点。主面板不是正圆（上圆下方），
// 所以按"面板外轮廓的椭圆等距外扩"布点：沿 θ 方向取椭圆边界点，
// 保证每个图标到面板边缘的距离一致，不会上下远、左右压。
func chipPos(i, drawW, drawH int) (int, int) {
	th := float64(i) * 360.0 / float64(chipPerPage) * math.Pi / 180
	// drawW/drawH 是图标**可见图形**的直径（不是画布尺寸），才能保证视觉间隙一致
	a := float64(panelContentW)/2 + chipGap + float64(drawW)/2
	b := float64(panelContentH)/2 + chipGap + float64(drawH)/2
	cos, sin := math.Cos(th), math.Sin(th)
	r := 1 / math.Sqrt(cos*cos/(a*a)+sin*sin/(b*b))
	return chipCenterX + int(r*cos), chipCenterY + int(r*sin)
}

func ensureChipPanel() *uiWin {
	if chipPanelWin != nil {
		return chipPanelWin
	}
	win := newUIWin(chipPanelW, chipPanelH)
	win.Bg = solidRGBA(win.W, win.H, 0)
	win.OnAction = chipAction
	win.OnHover = chipHover
	chipPanelWin = win
	return win
}

func chipHover(idx int) {
	if g == nil || chipPanelWin == nil || idx < 0 || idx >= len(chipPanelWin.Btns) {
		return
	}
	a := chipPanelWin.Btns[idx].Action
	if len(a) > 4 && a[:4] == "use:" {
		if tip, ok := chipTips[atoiSafe(a[4:])]; ok {
			g.say(tip)
		}
	}
}

func rebuildChipButtons() {
	win := chipPanelWin
	if win == nil {
		return
	}
	win.Btns = nil
	owned := make(map[int]bool, len(funcChipIDs))
	for _, id := range chipIDs() {
		owned[id] = true
	}
	total := chipTotalPages()
	if chipPage > total {
		chipPage = total
	}
	if chipPage < 1 {
		chipPage = 1
	}
	// 固定槽位：第 slot 个方位永远对应 funcChipIDs[start+slot]，
	// 未拥有的芯片留空（不画按钮），后面的芯片不会前移补位。
	start := (chipPage - 1) * chipPerPage
	for slot := 0; slot < chipPerPage; slot++ {
		gi := start + slot
		if gi >= len(funcChipIDs) {
			break
		}
		id := funcChipIDs[gi]
		if !owned[id] {
			continue
		}
		name := "fn" + itoa(id)
		up := uiLoad(name + "_up.png")
		cw, ch := chipIcon, chipIcon
		if up != nil {
			cw, ch = up.Bounds().Dx()/2, up.Bounds().Dy()/2 // zoom2 导出，除以 2 回到原生画布
		}
		aw, ah := uiAlphaSize(up)
		if aw <= 0 {
			aw, ah = cw, ch
		}
		cx, cy := chipPos(slot, aw, ah)
		win.Btns = append(win.Btns, uiBtn{
			Action: "use:" + itoa(id),
			X:      cx - cw/2, Y: cy - ch/2, W: cw, H: ch,
			Up:   uiFit(up, cw, ch),
			Over: uiFit(uiLoad(name+"_over.png"), cw, ch),
			Down: uiFit(uiLoad(name+"_down.png"), cw, ch),
		})
	}
	// 翻页交给主面板左右箭头（游戏里 pageTxt/箭头就在主面板上），环内不再放箭头，避免压住底部图标。
	win.Labels = nil
	if win.Visible {
		win.Bg = solidRGBA(win.W, win.H, 0)
		win.paint()
	}
}

func showChipPanel() {
	win := ensureChipPanel()
	chipPage = 1
	rebuildChipButtons()
	updatePageTxt()
	// 与储藏空间互斥（同一侧弹窗）
	if storagePanelWin != nil && storagePanelWin.Visible {
		storagePanelWin.hide()
	}
	// 圆环中心 = 主面板中间按钮中心（游戏里 chipPanel 挂在 appLevel，中心对按钮）
	cx, cy := 0, 0
	if mainPanelWin != nil && mainPanelWin.Visible {
		// 游戏里 chipPanel 挂在 appLevel 中央 (480,280)，主面板居中时与面板中心重合
		// 面板窗口 535×395 里可见圆形不在正中（main.png 留白偏上），
		// 必须对齐可见内容中心，否则环整体偏上、上远下近
		cx = mainPanelWin.X + panelContentCX
		cy = mainPanelWin.Y + panelContentCY
	} else {
		vx, vy, vw, vh := virtualScreen()
		cx = vx + vw/2
		cy = vy + vh/2
	}
	win.showAt(cx-chipCenterX, cy-chipCenterY)
	logf("chip ring shown at %d,%d center=%d,%d", win.X, win.Y, cx, cy)
}

func chipAction(a string) {
	switch a {
	case "pageL":
		if chipPage > 1 {
			chipPage--
			rebuildChipButtons()
			updatePageTxt()
			if g != nil {
				g.say("功能芯片 " + itoa(chipPage) + "/" + itoa(chipTotalPages()))
			}
		}
	case "pageR":
		if chipPage < chipTotalPages() {
			chipPage++
			rebuildChipButtons()
			updatePageTxt()
			if g != nil {
				g.say("功能芯片 " + itoa(chipPage) + "/" + itoa(chipTotalPages()))
			}
		}
	default:
		if len(a) > 4 && a[:4] == "use:" {
			id := atoiSafe(a[4:])
			g.useChip(id)
			// CanChipCollectionPanel.onClickHandler 末尾 _closeFun()：点芯片后整界面关闭
			hideAllSubPanels()
			if mainPanelWin != nil {
				mainPanelWin.hide()
			}
			// 关闭后再打开对应功能面板（App_7000xx），顺序不能反，否则新面板会被一起隐藏
			if sid := chipShellID[id]; sid != "" {
				showShell(sid)
			} else if tip := chipTips[id]; tip != "" {
				g.say(tip)
			}
		}
	}
}

// solidRGBA 生成纯色画布（alpha=0 即全透明底）。
func solidRGBA(w, h int, c uint32) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	a := uint8(c & 0xff)
	r := uint8((c >> 24) & 0xff)
	gc := uint8((c >> 16) & 0xff)
	b := uint8((c >> 8) & 0xff)
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = r
		img.Pix[i+1] = gc
		img.Pix[i+2] = b
		img.Pix[i+3] = a
	}
	return img
}

// isFuncChip 判断是否常驻功能芯片（不做储藏物品）。
func isFuncChip(id int) bool {
	for _, f := range funcChipIDs {
		if f == id {
			return true
		}
	}
	return false
}

// uiAlphaSize 返回图片不透明区域的尺寸（已按 zoom2 除以 2）。
func uiAlphaSize(img *image.RGBA) (int, int) {
	if img == nil {
		return 0, 0
	}
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Dx(), b.Dy(), -1, -1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[y*img.Stride:]
		for x := b.Min.X; x < b.Max.X; x++ {
			if row[x*4+3] > 8 {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < 0 {
		return 0, 0
	}
	return (maxX - minX + 1) / 2, (maxY - minY + 1) / 2
}


// updatePageTxt 原在主面板底部中间显示「当前页/总页」（对应 MachineDogPanel.pageTxt）。
// 子渊 2026-10-27：这个页码不要了，不再显示。
// 函数与 4 个调用点保留（打开环 / 翻页 / 关闭环都要靠它同步主面板重绘），
// 将来若要恢复，只改这里即可。
func updatePageTxt() {
	if mainPanelWin == nil {
		return
	}
	mainPanelWin.Labels = mainPanelLabels()
	if mainPanelWin.Visible {
		mainPanelWin.paint()
	}
}
