//go:build windows

package main

import (
	"image"
	"os"
)

// ghostDisabled：设 NONO_GHOST=0 关闭残影。
var ghostDisabled = os.Getenv("NONO_GHOST") == "0"

// 超能残影：原版把移动前的旧帧描在场景层（屏幕固定位置）再淡出。
//
// 2026-10-26 子渊选定方案 A：不再用独立的半透明 TOPMOST 分层窗口，改为把残影
// 直接画进主窗口画布，跟本体同一次 blit + updateLayered 上传。
//
// 换掉独立窗口的三个实测理由：
//  1. Wine 下每次 updateLayered 约 140ms，会把移动时的 25fps 打到 7fps；
//  2. 「大窗口 + 小内容 + 半透明」的分层合成会让 AA 边缘比不透明窗口更硬，
//     表现就是残影有锯齿而本体没有（子渊 2026-10-26 反馈）；
//  3. 多一个 TOPMOST 窗口就要多维护 Z 序、面板避让、鼠标穿透，全是纯副作用。
//
// 缓存策略（子渊要求「启动后先做好缓存」）：
//   - 位图用固定 3 槽环形缓冲复用，尺寸只增不减，运行期只做按行 copy，不申请内存；
//   - alpha 淡出表在 init 里一次算好，运行期只查表，不做除法。
//
// 唯一代价：窗口要能包住「本体 ∪ 残影」，否则拖影会被窗口边界切掉。扩窗在
// main.go draw() 里跟动作扩窗合并处理，BaseX 不动（不影响存档落点与切形态球心）。

type ghostFrame struct {
	ax, ay int         // 残影内容左上角的屏幕绝对坐标
	buf    *image.RGBA // 位图缓冲，可能大于 w×h（槽位复用只扩不缩）
	w, h   int         // 有效内容尺寸
	life   int
}

const (
	// ghostLife 是残影寿命（单位 tick）。
	//
	// 2026-10-29（子渊要求对齐官方残影）：12。同屏层数 = ceil((life-1)/pushEvery)
	// = ceil(11/3) = 4 层，与官方截图实测的 3~4 层一致。
	// 2026-10-26 曾从 12 降到 4（嫌「幻影要很久才消除」），那是同屏仅 2 层、
	// 尾巴只有 12px 的时代；现在每层更淡（末端 0.15）且间距更大，消失感由
	// alpha 衰减承担，不再靠缩短寿命。
	ghostLife = 12
	// ghostMaxLayers 残影层数（池子上限）。
	//
	// 稳态 4 层（life 11/8/5/2 → alpha 211/153/96/38），池子刚好装下；
	// pushGhost 里「保留最后 N-1 张 + 新 1 张」的口径依赖这个值，改成小于 4
	// 会丢层，大于 4 只是浪费内存。
	ghostMaxLayers = 4
	// ghostSlots 环形缓冲槽数，必须 > ghostMaxLayers，避免正在用的槽被下一帧覆盖。
	ghostSlots = 6
	// ghostPad 残影存在时窗口向四周留的固定边距（像素）。
	//
	// 不能按 ghostUnionBounds 逐帧扩边：残影是本体的「上一帧」，位置每帧都变，
	// 窗口就会每帧 resize、renderer 跟着重建（实测待机走路时窗口高度从 190
	// 一路涨到 328，每帧一次 newRenderer，直接把 25fps 打穿）。
	// 所以是固定值，只在「有残影 / 没残影」两种状态间切换（靠 ghostHoldTicks
	// 滞后，不会逐帧抖）。
	//
	// 2026-10-29 从 16 提到 48：4 层 × 3 tick 的尾巴在跟随态（4px/tick）
	// 能拖到 40px，16 只留得下 26px（画布留白 ~10 + 16），尾巴会被窗口边界
	// 切平成一条直边——子渊明确要求「需要显示完整」。48 留 8px 富余，
	// 覆盖气泡/动作帧导致的画布尺寸瞬时差异。
	// 代价：残影存在时窗口 137×181 → 201×245（super1@scale1.5，面积 1.99 倍），
	// 静止时仍是原尺寸。
	ghostPad = 48
	// ghostPushEvery 每几 tick 采一张残影。
	//
	// 间距 = pushEvery × 每 tick 位移（走路 3px、跟随 4px）。
	// 2026-10-29 从 2 改成 3：间距 6~8px → 9~12px，对上官方截图实测的
	// 10~12px（≈本体宽的 12~14%）。此前 2 tick 的间距让相邻残影贴成一团灰边，
	// 看起来像本体自带的阴影而不是拖尾。
	ghostPushEvery = 3
)

var (
	ghostPool     []ghostFrame
	ghostBufs     [ghostSlots]*image.RGBA
	ghostNext     int
	ghostAlphaTab [ghostLife + 1]uint8
	// ghostHoldTick 最近一次产生残影的 tick，用于「留边滞后」。
	//
	// 只在池非空时才留边会导致窗口在 289×190 ↔ 321×222 之间反复切换：
	// 残影寿命 4 tick、每 3 tick 才推一次，拖动的某一步没踩上 TickN%3 时池就会
	// 短暂清空 → 窗口缩回 → renderer 重建 → 下一帧又扩回来（实测一次拖动
	// 触发 17 次 resize）。这里改成「最近 ghostHoldTicks 内动过就一直留边」，
	// 移动期间窗口尺寸恒定，停下 2.4 秒后才缩回。
	ghostHoldTick = -100000
)

// ghostHoldTicks 留边滞后长度（tick）。25fps 下 60 tick = 2.4 秒。
const ghostHoldTicks = 60

func init() {
	// 启动即建表：i 是剩余寿命，越接近 0 越透明。
	// 下限必须是 0（线性淡到全透明）。原来最后几帧恒为 12% 不透明，
	// 视觉上是「一直挂着不散、然后突然消失」，不是淡出。
	for i := 0; i <= ghostLife; i++ {
		ghostAlphaTab[i] = uint8(i * 230 / ghostLife)
	}
}

// pushGhost 压入一帧残影（绝对屏幕坐标）。
// 位图从环形缓冲取，尺寸不变时零分配。
func (g *Game) pushGhost(img *image.RGBA, ax, ay int) {
	if img == nil {
		return
	}
	sb := img.Bounds()
	w, h := sb.Dx(), sb.Dy()
	if w <= 0 || h <= 0 {
		return
	}
	if len(ghostPool) >= ghostMaxLayers {
		ghostPool = ghostPool[len(ghostPool)-ghostMaxLayers+1:]
	}
	idx := ghostNext
	ghostNext = (ghostNext + 1) % ghostSlots
	b := ghostBufs[idx]
	// 只扩不缩：窗口尺寸只在形态/动作切换时变，其余帧全部命中已有缓冲。
	if b == nil || b.Bounds().Dx() < w || b.Bounds().Dy() < h {
		b = image.NewRGBA(image.Rect(0, 0, w, h))
		ghostBufs[idx] = b
	}
	copyRows(b, w, h, img)
	ghostPool = append(ghostPool, ghostFrame{ax: ax, ay: ay, buf: b, w: w, h: h, life: ghostLife})
	ghostHoldTick = g.TickN
}

// copyRows 按行把 src 拷进 dst 左上角 w×h 区域。两者 stride 可能不同
// （缓冲只扩不缩，实际比内容大），不能直接 copy(Pix)。
func copyRows(dst *image.RGBA, w, h int, src *image.RGBA) {
	sb := src.Bounds()
	for y := 0; y < h; y++ {
		so := (sb.Min.Y+y)*src.Stride + sb.Min.X*4
		do := y * dst.Stride
		copy(dst.Pix[do:do+w*4], src.Pix[so:so+w*4])
	}
}

// tickGhost 记录移动前的本体帧；非超能形态/面板打开时直接清空。
// ax/ay 是本帧画布左上角在屏幕上的绝对坐标。
//
// 调用时机被刻意夹在「本体画完」和「drawGhosts」之间：快照里不能含旧残影，
// 否则残影会被反复拍进下一帧，越叠越浓。
func (g *Game) tickGhost(img *image.RGBA, ax, ay int) {
	// 面板打开时不画残影（2026-10-23 实测）：面板会盖住本体，残影留在面板上很脏。
	// 飞行模式也不画（子渊 2026-10-28）：残影快照的是整张画布，飞行时里面是
	// 飞行器 + 尾焰，拖出来是一串发光的飞行器残像，既不像原版也糊住屏幕。
	// 残影只保留「超能形态 + 非飞行」这一种用法。
	if !g.ghostAllowed() {
		if len(ghostPool) > 0 {
			ghostPool = nil
		}
		return
	}
	mdx := g.BaseX - g.prevBaseX
	mdy := g.BaseY - g.prevBaseY
	// 位移异常大（首帧 prevBase 还是 0 或窗口 reshape）时不抓帧，否则残影会落到屏幕外。
	//
	// 2026-10-26：推帧间隔从每 3 tick 改成每 tick。原间隔下残影实际只有 8.3fps，
	// 拖动时能看出一格一格的台阶（子渊反馈「帧率太低」）。
	// 只在顶上界时算「该往回退多少」：每 ghostPushEvery tick 取一张时，
	// 这一张要落在「这两 tick 之前」的位置，否则残影会和本体重叠。
	if (mdx != 0 || mdy != 0) && absInt(mdx) <= 12 && absInt(mdy) <= 12 && g.TickN%ghostPushEvery == 0 {
		// 残影留在移动前的位置：当前帧位置减去本帧位移
		g.pushGhost(img, ax-mdx, ay-mdy)
	}
	kept := ghostPool[:0]
	for _, gh := range ghostPool {
		gh.life--
		if gh.life > 0 {
			kept = append(kept, gh)
		}
	}
	ghostPool = kept
}

// ghostAllowed 是残影的唯一开关：超能形态、非飞行、无面板遮挡、未被调试关闭。
//
// 三个调用点（tickGhost 抓帧、ghostActiveOrPending 预留边、清理旧池）必须用同一份
// 判定，否则会出现「不留边但仍抓帧」→ 残影被窗口边缘切掉。
func (g *Game) ghostAllowed() bool {
	if ghostDisabled || ghostBlockedByPanel() {
		return false
	}
	if g.Save.Fly {
		return false
	}
	return g.Save.SuperLevel > 0
}

// ghostActive 当前是否有残影需要显示（供 main.go 决定是否留边）。
func ghostActive() bool {
	return len(ghostPool) > 0
}

// ghostActiveOrPending 判断本帧窗口是否需要为残影留边：池里已有、本帧即将 push，
// 或者最近 ghostHoldTicks 内动过（滞后保持，避免窗口尺寸来回抖）。
//
// 扩边必须在 draw 前半段决定，而 push 发生在 draw 末尾（要先画出本体才能快照）。
// 只看池会让「刚开始移动的第一帧」漏掉留边，而那一帧的 alpha 恰好最高（172），
// 被窗口切掉会很明显。所以这里把 tickGhost 的 push 条件复述一遍做预判。
func ghostActiveOrPending(g *Game) bool {
	// 先排除「根本不该有残影」的情况：面板打开/飞行时要立刻收掉留边。
	if !g.ghostAllowed() {
		return false
	}
	if len(ghostPool) > 0 {
		return true
	}
	if g.TickN-ghostHoldTick < ghostHoldTicks {
		return true
	}
	mdx := g.BaseX - g.prevBaseX
	mdy := g.BaseY - g.prevBaseY
	return (mdx != 0 || mdy != 0) && absInt(mdx) <= 12 && absInt(mdy) <= 12 &&
		g.TickN%ghostPushEvery == 0
}

// ghostUnionBounds 返回所有残影的屏幕包围盒，供主窗口扩边使用。
func ghostUnionBounds() (minX, minY, maxX, maxY int, ok bool) {
	if len(ghostPool) == 0 {
		return 0, 0, 0, 0, false
	}
	minX, minY = ghostPool[0].ax, ghostPool[0].ay
	maxX, maxY = minX+ghostPool[0].w, minY+ghostPool[0].h
	for _, gh := range ghostPool[1:] {
		if gh.ax < minX {
			minX = gh.ax
		}
		if gh.ay < minY {
			minY = gh.ay
		}
		if gh.ax+gh.w > maxX {
			maxX = gh.ax + gh.w
		}
		if gh.ay+gh.h > maxY {
			maxY = gh.ay + gh.h
		}
	}
	return minX, minY, maxX, maxY, true
}

// drawGhosts 把残影按绝对坐标混进主画布。
// canvasX/canvasY 是主画布左上角的屏幕坐标，用来把绝对坐标换算成画布内偏移。
func (g *Game) drawGhosts(dst *image.RGBA, canvasX, canvasY int) {
	if len(ghostPool) == 0 {
		return
	}
	if logEnabled && g.TickN%15 == 0 {
		mnx, mny, mxx, mxy, _ := ghostUnionBounds()
		logf("ghost n=%d bbox=%d,%d..%d,%d canvas=%d,%d %dx%d",
			len(ghostPool), mnx, mny, mxx, mxy, canvasX, canvasY,
			dst.Bounds().Dx(), dst.Bounds().Dy())
	}
	for _, gh := range ghostPool {
		a := ghostAlphaTab[clampInt(gh.life, 0, ghostLife)]
		if a == 0 {
			continue
		}
		src := gh.buf.SubImage(image.Rect(0, 0, gh.w, gh.h)).(*image.RGBA)
		blendTo(dst, src, gh.ax-canvasX, gh.ay-canvasY, a)
	}
}

// clampInt 把 v 夹到 [lo, hi]，避免查表越界。
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// blendTo 把 src 以 alpha（0-255）非预乘 Over 混合到 dst 的 (ox,oy) 处，越界自动裁掉。
//
// 2026-10-26：改成直接按 Pix 索引 + 预先把越界范围算出来，不再逐像素调
// RGBAAt/SetRGBA（那两个是带 Bounds 检查的方法调用，55k 像素 × 2 层会吃掉几毫秒）。
func blendTo(dst, src *image.RGBA, ox, oy int, alpha uint8) {
	if alpha == 0 || src == nil {
		return
	}
	sb := src.Bounds()
	db := dst.Bounds()
	x0, x1 := 0, sb.Dx()
	if ox < 0 {
		x0 = -ox
	}
	if ox+x1 > db.Dx() {
		x1 = db.Dx() - ox
	}
	y0, y1 := 0, sb.Dy()
	if oy < 0 {
		y0 = -oy
	}
	if oy+y1 > db.Dy() {
		y1 = db.Dy() - oy
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}
	sa := uint32(alpha)
	for y := y0; y < y1; y++ {
		so := (sb.Min.Y+y)*src.Stride + (sb.Min.X+x0)*4
		do := (oy+y)*dst.Stride + (ox+x0)*4
		for x := x0; x < x1; x++ {
			sp, dp := so, do
			so += 4
			do += 4
			sc := uint32(src.Pix[sp+3])
			if sc == 0 {
				continue
			}
			sa2 := sc * sa / 255
			if sa2 == 0 {
				continue
			}
			dc := uint32(dst.Pix[dp+3])
			// 只往「完全透明」的空白处画。本体已经占据的像素一律不动——
			// 残影是在本体之后混进来的，不守这条会把旧帧盖到本体上，
			// 表现就是子渊说的「最上层画面割裂、不完整」。
			if dc != 0 {
				continue
			}
			inv := 255 - sa2
			outA := sa2 + dc*inv/255
			if outA == 0 {
				continue
			}
			r := (uint32(src.Pix[sp])*sa2 + uint32(dst.Pix[dp])*dc*inv/255) / outA
			gg := (uint32(src.Pix[sp+1])*sa2 + uint32(dst.Pix[dp+1])*dc*inv/255) / outA
			b := (uint32(src.Pix[sp+2])*sa2 + uint32(dst.Pix[dp+2])*dc*inv/255) / outA
			if r > 255 {
				r = 255
			}
			if gg > 255 {
				gg = 255
			}
			if b > 255 {
				b = 255
			}
			dst.Pix[dp] = uint8(r)
			dst.Pix[dp+1] = uint8(gg)
			dst.Pix[dp+2] = uint8(b)
			dst.Pix[dp+3] = uint8(outA)
		}
	}
}
