//go:build windows

package main

// 芯片工坊：桌宠没有服务端经济，用「能量值」充当货币。
//
// 20261027-11：右键「芯片工坊」子菜单已删除（与「使用芯片」42 枚芯片完全重复，
// 且 save.go 的 ensureAllChips 已把全部芯片默认装上、物品无限）。
// 本文件的 chipPrice / buyChip 目前**没有任何调用点**，按子渊 2026-10-27 要求
// （选项 B：留着不调用）保留，将来若要恢复能量经济可直接接回菜单。
// 对应原版发明室的芯片合成（9023/9024/9025），价格按芯片类别定档：
// 变色最便宜、功能最贵，与「功能芯片要用米币买」的体感一致。
// 能量来源：快捷栏 key4 充电（每次 +20，上限 100）。

// chipPrice 返回某芯片的合成价（能量）。
func chipPrice(id, kind int) int {
	switch kind {
	case 2: // 变色芯片
		return 20
	case 3: // 能量芯片
		return 30
	case 4: // 智慧芯片
		return 40
	case 101: // 玩具芯片
		return 30
	case 1: // 功能芯片
		return 60
	}
	return 30
}

// buyChip 校验能量、扣费、写入已装列表。
func (g *Game) buyChip(id int) {
	c := chipByID[id]
	if c == nil {
		return
	}
	if g.Save.hasChip(id) {
		g.say("这个芯片已经拥有啦！")
		return
	}
	price := chipPrice(id, c.Kind)
	if g.Save.Power < price {
		g.say("能量不足（需要 " + itoa(price) + "），先用快捷栏充电吧")
		return
	}
	g.Save.Power -= price
	g.Save.Chips = append(g.Save.Chips, id)
	if g.Save.Counts == nil {
		g.Save.Counts = map[int]int{}
	}
	if g.Save.Counts[id] < 1 {
		g.Save.Counts[id] = 1
	}
	g.Save.write()
	g.say(c.Name + " 合成成功！")
}
