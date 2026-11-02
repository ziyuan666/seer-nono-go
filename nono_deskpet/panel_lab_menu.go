//go:build windows

package main

import "unsafe"

// labPickMenu 弹出原生菜单选择材料，显示当前库存。选中后填入第一个空槽，
// 三槽都满时替换第 1 槽。对应原版合成机的翻页箭头 + 图标列表。
func labPickMenu() {
	if g == nil || labWin == nil {
		return
	}
	g.Save.ensureMaterials()
	slot := 0
	for i, id := range labSlots {
		if id == 0 {
			slot = i
			break
		}
	}
	h, _, _ := pCreatePopupMenu.Call()
	if h == 0 {
		return
	}
	defer pDestroyMenu.Call(h)

	idToMat := map[uintptr]int{}
	for i, d := range materialDefs {
		mid := uintptr(8000 + i)
		appendStr(HMENU(h), mid, d.Name+"  ∞", false)
		idToMat[mid] = d.ID
	}

	pt := POINT{}
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(uintptr(labWin.Hwnd))
	cmd, _, _ := pTrackPopupMenu.Call(h, TPM_RETURNCMD|TPM_RIGHTBUTTON,
		uintptr(pt.X), uintptr(pt.Y), 0, uintptr(labWin.Hwnd), 0)
	if id, ok := idToMat[cmd]; ok {
		labSlots[slot] = id
		labMsg = ""
		labRefresh()
	}
}
