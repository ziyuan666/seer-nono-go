//go:build windows

package main

// 主面板中间黑屏区域的 faceMc 动画（对应 MachineDogPanel.playFace）。
// 帧区间来自 MachineDogPanel.swf 的 DefineSprite_101 帧标签：
//
//	normal 1-25、close 26-45、open 46-60、loadChip 61-169、
//	charging 170-199、hide 200、levelup 201-279，SWF 帧率 24fps。
//
// 帧图已按面板黑屏矩形（1x 147,122 - 386,252）预缩放导出到 assets/ui/face/N.png。

import "image"

const (
	faceX = 147 // 黑屏区域在 main.png（1x）中的位置
	faceY = 122
	faceW = 239
	faceH = 130
)

type faceState struct {
	name  string
	start int
	end   int
	frame int
	loop  bool
}

var face = faceState{name: "normal", start: 1, end: 25, frame: 1, loop: true}

var faceCache = map[int]*image.RGBA{}

func faceFrame(i int) *image.RGBA {
	if i < 1 {
		i = 1
	}
	if im, ok := faceCache[i]; ok {
		return im
	}
	im := uiLoad("face/" + itoa(i) + ".png")
	faceCache[i] = im
	return im
}

// facePlay 切换表情动画；未知标签回 normal。
func facePlay(name string) {
	switch name {
	case "close":
		face = faceState{name: "close", start: 26, end: 45, frame: 26, loop: false}
	case "open":
		face = faceState{name: "open", start: 46, end: 60, frame: 46, loop: false}
	case "loadChip":
		face = faceState{name: "loadChip", start: 61, end: 169, frame: 61, loop: true}
	case "charging":
		face = faceState{name: "charging", start: 170, end: 199, frame: 170, loop: true}
	case "levelup":
		face = faceState{name: "levelup", start: 201, end: 279, frame: 201, loop: false}
	default:
		face = faceState{name: "normal", start: 1, end: 25, frame: 1, loop: true}
	}
}

// faceTick 推进一帧；返回 true 表示画面有变化需要重绘。
func faceTick() bool {
	if face.name == "" {
		return false
	}
	face.frame++
	if face.frame > face.end {
		if face.loop {
			face.frame = face.start
		} else {
			facePlay("normal")
			return true
		}
	}
	return true
}

// faceDraw 把当前帧贴到黑屏区域（paint 的 Draw 回调）。
func faceDraw(img *image.RGBA, s float64) {
	if img == nil {
		return
	}
	if s <= 0 {
		s = 1
	}
	im := faceFrame(face.frame)
	if im == nil {
		return
	}
	dst := uiFit(im, int(float64(faceW)*s+0.5), int(float64(faceH)*s+0.5))
	if dst == nil {
		return
	}
	uiBlend(img, dst, int(float64(faceX)*s+0.5), int(float64(faceY)*s+0.5))
}
