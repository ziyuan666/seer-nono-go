//go:build windows

package main

// 精灵类功能面板（空壳）：1:1 复用游戏模块 SWF 导出的整面板贴图。
// 依据：
//   - 700002 分子转化仪 -> module/app/MoleculePanel.swf   DefineSprite_107_UI_MoleculePanel
//   - 700003 经验分配器 -> module/app/ExpAdmPanel.swf     DefineSprite_48_UI_ExpAdmPanel
//   - 700004 精灵模拟训练 -> module/app/ChoicePetImpersonationPanel.swf DefineSprite_106
//   - 700011 精灵仓库   -> module/app/PetStorage.swf      DefineSprite_470_UI_PetStorage
//   - 700015 精灵追踪   -> module/app/NonoSpriteTrack.swf DefineSprite_61_ui_SpriteTrack_main
//   - 700016 技能排序   -> module/app/PetListPanel.swf    DefineSprite_26_PetMainUI
//   - 700018 元神赋形   -> module/app/SoulTransformPanel.swf DefineSprite_58_UI_SoulTrasfingPanel
// 桌面端没有精灵系统，因此只还原界面外观 + 关闭；点击面板内其它按钮给统一提示。

import "image"

type shellDef struct {
	ID   string
	Img  string
	Hint string
	W, H int
}

var shellDefs = []shellDef{
	{"molecule", "molecule.png", "桌面版未接入精元数据，无法转化", 274, 371},
	{"expadm", "expadm.png", "桌面版未接入精灵数据，暂无经验可分配", 605, 314},
	{"train", "train.png", "桌面版未接入课程数据，无法开始模拟训练", 384, 302},
	{"petstorage", "petstorage.png", "桌面版未接入精灵仓库数据", 970, 570},
	{"track", "track.png", "桌面版未接入精灵追踪数据", 432, 447},
	{"petlist", "petlist.png", "桌面版未接入精灵列表，无法排序技能", 313, 332},
	{"soul", "soul.png", "桌面版未接入精灵数据，无法元神赋形", 881, 565},
}

func shellByID(id string) *shellDef {
	for i := range shellDefs {
		if shellDefs[i].ID == id {
			return &shellDefs[i]
		}
	}
	return nil
}

var shellWin *uiWin
var shellCur string

// showShell 打开指定功能面板；同一时间只保留一个，并收起 NONO 主界面。
func showShell(id string) {
	d := shellByID(id)
	logf("showShell id=%s def=%v", id, d != nil)
	if d == nil || g == nil {
		return
	}
	if shellWin != nil {
		// 2026-11-02 E-01：旧 shell 面板必须真正销毁，不能只 hide——
		// showShell 每次打开都 newUIWin，旧的留着就是窗口句柄/GDI 泄漏。
		shellWin.destroy()
		shellWin = nil
	}
	win := newUIWin(d.W, d.H)
	if win == nil {
		return
	}
	shellCur = id
	img := uiFit(uiLoad("shell/"+d.Img), d.W, d.H)
	win.Bg = img
	win.Labels = nil
	// 右上角关闭按钮热区（各面板贴图右上角圆叉位置略有差异，统一按 W-44..W-6）
	win.Btns = append(win.Btns, uiBtn{Action: "shell:close", X: d.W - 46, Y: 4, W: 42, H: 34})
	// 面板中部点击：给出"未接入数据"提示，避免误以为卡住
	win.Btns = append(win.Btns, uiBtn{Action: "shell:tap", X: 8, Y: 40, W: d.W - 16, H: d.H - 60})
	win.OnAction = shellAction
	vx, vy, vw, vh := virtualScreen()
	logf("showShell at vx=%d vy=%d vw=%d vh=%d win=%dx%d", vx, vy, vw, vh, win.W, win.H)
	win.showAt(vx+(vw-win.W)/2, vy+(vh-win.H)/2)
	shellWin = win
}

// shellDrawBody 已不再使用：提示改为点击面板时弹气泡，避免在贴图上叠色块。
// 保留函数签名仅供后续想加文字版提示时使用。
func shellDrawBodyUnused() {}

// shellDrawBody 只画统一提示底板，贴图由 win.Bg 负责。
func shellDrawBody(dst *image.RGBA, s float64, d *shellDef) {
	if dst == nil || d == nil {
		return
	}
	if s <= 0 {
		s = 1
	}
	// 提示文字底衬（半透明白条），保证深蓝面板上也读得清
	x0 := int(16 * s)
	x1 := int(float64(d.W-16) * s)
	y0 := int(float64(d.H-52) * s)
	y1 := int(float64(d.H-32) * s)
	for y := y0; y < y1 && y < dst.Bounds().Dy(); y++ {
		for x := x0; x < x1 && x < dst.Bounds().Dx(); x++ {
			i := dst.PixOffset(x, y)
			a := int(dst.Pix[i+3])
			// 与白色做 55% 混合
			dst.Pix[i] = uint8(int(dst.Pix[i]) + (255-int(dst.Pix[i]))*55/100)
			dst.Pix[i+1] = uint8(int(dst.Pix[i+1]) + (255-int(dst.Pix[i+1]))*55/100)
			dst.Pix[i+2] = uint8(int(dst.Pix[i+2]) + (255-int(dst.Pix[i+2]))*55/100)
			dst.Pix[i+3] = uint8(a)
		}
	}
}

func shellAction(a string) {
	d := shellByID(shellCur)
	logf("shellAction %q cur=%s", a, shellCur)
	switch a {
	case "shell:close":
		if shellWin != nil {
			shellWin.hide()
		}
	case "shell:tap":
		if g != nil && d != nil {
			g.say(d.Hint)
		}
	}
}
