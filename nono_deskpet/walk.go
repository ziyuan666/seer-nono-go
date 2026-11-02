//go:build windows

package main

// WalkHomeX/WalkHomeY 是自由游走的活动区中心：首次 tick 锁定为当前位置，
// 每次拖动结束（main.go 的 WM_LBUTTONUP）会重置到落点——拖动即换家。
// 原版 NonoModel 的 _autoRect 是一个给定矩形，NONO 只在这个矩形里随机游走，
// 不会跑遍全场景。
//
// 2026-10-27 修「原地抽搐」（子渊）：旧实现把活动区固定成 640×440，再从里面
// 扣掉缩放后的 BodyW/BodyH。缩放调到 3 倍以上时 640-770 变负，靠「窗口能装下」
// 兜底后只剩 8px，tick 每帧越界→clamp→速度反转，表现就是「两个相反方向来回
// 旋转、拖到别处又回去」。这里改成：
//
//	① 活动区可被屏幕边界裁剪，裁剪时同步缩 w/h（旧实现只挪 x，区域会偷偷变大）；
//	② 可用区放不下「本体 + minSlack」时直接放开到整屏——宁可跑满屏，也不能让
//	   边界收缩到本体尺寸，那必然退化成每帧反弹。
func (g *Game) walkBounds() (int, int, int, int) {
	vx, vy, vw, vh := virtualScreen()
	if !g.WalkHomeSet {
		g.WalkHomeX, g.WalkHomeY = g.BaseX, g.BaseY
		g.WalkHomeSet = true
	}
	const halfW, halfH = 320, 220
	// minSlack：本体之外至少留这么多可走空间，否则活动区失去意义。
	const minSlack = 96
	x := g.WalkHomeX - halfW
	y := g.WalkHomeY - halfH
	w := halfW * 2
	h := halfH * 2
	if x < vx {
		w -= vx - x
		x = vx
	}
	if y < vy {
		h -= vy - y
		y = vy
	}
	if x+w > vx+vw {
		w = vx + vw - x
	}
	if y+h > vy+vh {
		h = vy + vh - y
	}
	// 塌缩兜底：整屏可用（此时也不能保证屏幕比本体大，那种情况由调用方
	// 的「无可走空间就不反弹」分支兜住，不会抽搐）。
	if w < g.BodyW+minSlack {
		x, w = vx, vw
	}
	if h < g.BodyH+minSlack {
		y, h = vy, vh
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return x, y, w, h
}
