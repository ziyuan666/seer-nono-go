//go:build windows

package main


import (
	"encoding/json"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
)

// 飞行载具：原版 nonoFlyModel/1..4 的 _flyMachineMc 整帧。
// 原版该 MC 由三层组成——colorMc(底板,depth1) / fireMc(尾焰,depth3) / dirMc(本体,depth5)，
// 三层各带独立 matrix。桌宠早期只导了 dir 和 fire 两层，且尾焰位置靠猜（贴画布底部），
// 结果尾焰糊在 NONO 脸上；又因为缺 colorMc 底板，本体半透明处直接透出桌面文字
// （子渊 2026-10-24 反馈截图）。
// 现改为 tools/gen_fly_composite.py 预先把三层按 XML matrix + root 帧校准合成整帧，
// 运行时只贴一张图：<style>_<dir>.png，尺寸见同目录 metrics.json。

var flyCache [5][9]*image.RGBA

// flyFrameCache 缓存某风格某方向的整帧序列（原版该方向 7~8 帧，25fps 循环）。
var flyFrameCache [5][9][]*image.RGBA

// flyFrameScaled 缓存已缩放到目标宽度的帧序列，key 见 flyScaledKey。
var flyFrameScaled = map[int][]*image.RGBA{}

func flyScaledKey(style, dir, w int) int { return style*1000000 + dir*10000 + w }

// flyDirFrames 返回某风格某方向的帧数；metrics 缺失或老素材包（无 dirs）时返回 1，
// 调用方据此退回单帧静态显示，不至于整块不显示。
func flyDirFrames(style, dir int) int {
	m, ok := flyMetrics[style]
	if !ok || dir < 1 || dir > len(m.Dirs) {
		return 1
	}
	if m.Dirs[dir-1].Frames < 1 {
		return 1
	}
	return m.Dirs[dir-1].Frames
}

// flyScaledFrames 返回某风格某方向、本体宽度 wBody 时按序缩放的整帧序列。
// 帧数取自 metrics.json 的 dirs（与 SWF 帧标签一一对应，一帧不少）。
func flyScaledFrames(style, dir, wBody int) []*image.RGBA {
	if wBody < 1 {
		wBody = 1
	}
	m, ok := flyMetrics[style]
	if !ok || m.BodyW <= 0 || m.FullW <= 0 {
		m = flyMetric{FullW: 120, FullH: 146, BodyW: 84, BodyH: 110}
	}
	w := wBody * m.FullW / m.BodyW
	if w < 1 {
		w = 1
	}
	key := flyScaledKey(style, dir, w)
	if list, ok := flyFrameScaled[key]; ok {
		return list
	}
	// 2026-11-02 E-03 修复：缓存无上限会让缩放反复调整时旧尺寸帧永久驻留
	//（4 风格 × 8 方向 × 每套 7+ 帧，一套全尺寸帧几十 MB）。超上限整表作废
	//（下次按需重建），与 vec 待机缓存的作废策略一致。
	if len(flyFrameScaled) >= 64 {
		flyFrameScaled = map[int][]*image.RGBA{}
	}
	srcs := flyImages(style, dir)
	if len(srcs) == 0 {
		return nil
	}
	out := make([]*image.RGBA, 0, len(srcs))
	for _, src := range srcs {
		sb := src.Bounds()
		h := w * sb.Dy() / sb.Dx()
		if h < 1 {
			h = 1
		}
		out = append(out, resizeFrame(src, w, h))
	}
	flyFrameScaled[key] = out
	return out
}

// flyImages 懒加载某风格某方向的全部帧图；缺失时退回旧的单帧 <style>_<dir>.png。
func flyImages(style, dir int) []*image.RGBA {
	if style < 1 || style > 4 {
		style = 1
	}
	if dir < 1 || dir > 8 {
		dir = 1
	}
	if flyFrameCache[style][dir] != nil {
		return flyFrameCache[style][dir]
	}
	base := filepath.Join(assetsRoot(), "fly")
	n := flyDirFrames(style, dir)
	name := dirNames[dir-1]
	list := make([]*image.RGBA, 0, n)
	for k := 1; k <= n; k++ {
		p := filepath.Join(base, itoa(style)+"_"+name+"_"+itoa(k)+".png")
		img := loadPNGToRGBA(p)
		if img == nil {
			logf("fly frame missing %s", p)
			break
		}
		list = append(list, img)
	}
	if len(list) == 0 {
		// 老素材包只有单帧，退回静态，保证不空白
		if img := flyImage(style, dir); img != nil {
			list = append(list, img)
		}
	}
	flyFrameCache[style][dir] = list
	// 逐方向打印帧数：原版 63 帧按帧标签切 8 段（7/8/8/8/8/8/8/8），
	// 帧数对不上说明素材生成有漏，比"看着像在动"更硬的证据。
	logf("fly %d/%s frames=%d", style, name, len(list))
	return list
}

// dirNames 与 SWF 帧标签顺序一致（down 起，顺时针到 rightdown）。
var dirNames = [8]string{"down", "leftdown", "left", "leftup",
	"up", "rightup", "right", "rightdown"}

// 缩放结果缓存：key = style*1000000 + dir*10000 + 宽
var flyScaled = map[int]*image.RGBA{}

// flyMetric 记录某风格的「整帧」与「本体(dirMc)」逻辑尺寸，用于宽度换算。
// 各风格整帧差异很大（style1 120x146，style2 177x165），不能共用一个比例。
// Dirs 是该风格 8 个方向各自的帧数（原版 63 帧按帧标签切成 7/8/8/8/8/8/8/8），
// 桌宠按 25fps 循环播放——SetTimer 40ms 正好 25fps，与 SWF 时间轴一致。
type flyMetric struct {
	FullW    int `json:"fullW"`
	FullH    int `json:"fullH"`
	BodyW    int `json:"bodyW"`
	BodyH    int `json:"bodyH"`
	BodyTopY int `json:"bodyTopY"`
	Dirs     []struct {
		Name   string `json:"name"`
		Frames int    `json:"frames"`
	} `json:"dirs"`
}

var flyMetrics = map[int]flyMetric{}

// loadFlyMetrics 读 assets/fly/metrics.json（由 gen_fly_composite.py 生成）。
// 缺失时退回 style1 的比例，保证老素材包也能跑起来。
func loadFlyMetrics() {
	p := filepath.Join(assetsRoot(), "fly", "metrics.json")
	b, err := os.ReadFile(p)
	if err != nil {
		logf("fly metrics 读取失败 %s: %v（用默认比例）", p, err)
		return
	}
	var raw map[string]flyMetric
	if err := json.Unmarshal(b, &raw); err != nil {
		logf("fly metrics 解析失败: %v", err)
		return
	}
	for k, v := range raw {
		if n, err := strconv.Atoi(k); err == nil && v.BodyW > 0 && v.FullW > 0 {
			flyMetrics[n] = v
		}
	}
	logf("fly metrics loaded %v", flyMetrics)
}

// flyImageScaled 返回某风格某方向的整帧图（本体+尾焰+阴影已合成）。
// wBody 是「希望本体显示多宽」，整帧按 bodyW:fullW 等比换算后降采样；
// 4x 素材走 areaScale，边缘比直接放大干净。结果按 (style,dir,整帧宽) 缓存。
func flyImageScaled(style, dir, wBody int) *image.RGBA {
	if wBody < 1 {
		wBody = 1
	}
	m, ok := flyMetrics[style]
	if !ok || m.BodyW <= 0 || m.FullW <= 0 {
		m = flyMetric{FullW: 120, FullH: 146, BodyW: 84, BodyH: 110}
	}
	w := wBody * m.FullW / m.BodyW
	if w < 1 {
		w = 1
	}
	key := style*1000000 + dir*10000 + w
	if img, ok := flyScaled[key]; ok {
		return img
	}
	src := flyImage(style, dir)
	if src == nil {
		return nil
	}
	sb := src.Bounds()
	h := w * sb.Dy() / sb.Dx()
	if h < 1 {
		h = 1
	}
	out := resizeFrame(src, w, h)
	flyScaled[key] = out
	return out
}
// trimAlpha 按 alpha>16 的边界裁剪图片，没有不透明像素时原样返回。

func trimAlpha(img *image.RGBA) *image.RGBA {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Dx(), b.Dy(), -1, -1
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if img.RGBAAt(x, y).A > 16 {
				if x < minX {
					minX = x
				}
				if y < minY {
					minY = y
				}
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < minX || maxY < minY {
		return img
	}
	out := image.NewRGBA(image.Rect(0, 0, maxX-minX+1, maxY-minY+1))
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			out.Set(x-minX, y-minY, img.At(x, y))
		}
	}
	return out
}

// flyBodyBottomInset 返回「整帧底边 → 本体底边」的显示像素数。
// 整帧比本体多出尾焰和地面阴影，画布底边对齐 BaseY 时本体会被顶上去一个 inset；
// 调用方把这部分补回来，飞行器才停在改版前的位置（子渊此前已确认位置正确）。
func flyBodyBottomInset(style, flyH int) int {
	m, ok := flyMetrics[style]
	if !ok || m.FullH <= 0 || m.BodyH <= 0 {
		return 0
	}
	bodyBottom := m.BodyTopY + m.BodyH
	if bodyBottom >= m.FullH || bodyBottom <= 0 {
		return 0
	}
	return flyH * (m.FullH - bodyBottom) / m.FullH
}

// dirIndex 把方向名换成 1-8 的飞行帧序号（1=down，与帧标签顺序一致）。
func dirIndex(dir string) int {
	switch dir {
	case "leftdown":
		return 2
	case "left":
		return 3
	case "leftup":
		return 4
	case "up":
		return 5
	case "rightup":
		return 6
	case "right":
		return 7
	case "rightdown":
		return 8
	}
	return 1
}

// loadPNGToRGBA 读 PNG 并转成 *image.RGBA（统一坐标原点）。
func loadPNGToRGBA(path string) *image.RGBA {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		return nil
	}
	b := src.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			rgba.Set(x, y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return rgba
}

// flyImage 懒加载某风格某方向的载具图；方向图缺失时退回静态主帧，仍失败返回 nil。
func flyImage(style, dir int) *image.RGBA {
	if style < 1 || style > 4 {
		style = 1
	}
	if dir < 1 || dir > 8 {
		dir = 1
	}
	if flyCache[style][dir] != nil {
		return flyCache[style][dir]
	}
	base := filepath.Join(assetsRoot(), "fly")
	img := loadPNGToRGBA(filepath.Join(base, itoa(style)+"_"+itoa(dir)+".png"))
	if img == nil {
		img = loadPNGToRGBA(filepath.Join(base, itoa(style)+".png"))
		logf("fly dir %d/%d missing, fallback static", style, dir)
	}
	if img == nil {
		logf("fly image %d/%d missing entirely", style, dir)
		return nil
	}
	flyCache[style][dir] = img
	return img
}

// debugCanvas 仅在 NONO_DEBUG_CANVAS=1 时把合成结果落盘，用于排查绘制问题。
func debugCanvas(img *image.RGBA, name string) {
	if os.Getenv("NONO_DEBUG_CANVAS") == "" {
		return
	}
	// 原子写：先写临时文件再 rename，否则外部抓图会拿到写了一半的半截 PNG
	f, err := os.Create(name + ".tmp")
	if err != nil {
		return
	}
	// 必须转 NRGBA 再编码：画布是**非预乘** alpha，直接 png.Encode(*image.RGBA)
	// 会让编码器再做一次预乘→非预乘，半透明边缘上溢 wrap（249/a226 → 26/a226），
	// dump 里凭空长出一圈黑毛刺 —— 排查锯齿时会被这张假图带偏。
	nr := image.NewNRGBA(img.Bounds())
	copy(nr.Pix, img.Pix)
	if err := png.Encode(f, nr); err != nil {
		f.Close()
		return
	}
	f.Close()
	os.Rename(name+".tmp", name)
	logf("debug canvas -> %s %dx%d", name, img.Bounds().Dx(), img.Bounds().Dy())
}

// flyCanvasAnchor 返回该 style 矢量画布里 mc 原点的位置（sprite.json 的
// canvas.anchor，1x 画布单位）。缓存一次：4 个 style 都读一遍。
var (
	flyAnchorOnce  sync.Once
	flyAnchorCache = map[int][2]float64{}
)

func flyCanvasAnchor(style int) ([2]float64, bool) {
	flyAnchorOnce.Do(func() {
		for s := 1; s <= 4; s++ {
			p := filepath.Join(assetsRoot(), "sprites", "fly"+strconv.Itoa(s), "sprite.json")
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var d struct {
				Canvas struct {
					Anchor []float64 `json:"anchor"`
				} `json:"canvas"`
			}
			if json.Unmarshal(b, &d) != nil || len(d.Canvas.Anchor) != 2 {
				continue
			}
			flyAnchorCache[s] = [2]float64{d.Canvas.Anchor[0], d.Canvas.Anchor[1]}
		}
	})
	a, ok := flyAnchorCache[style]
	return a, ok
}

// flyRegPoint 返回飞行器 mc 原点在「整帧图」（即 flySrc）里的坐标。
//
// 依据：vecFlyClip 把原版整帧映射到矢量画布坐标（clip[0..1] 是整帧左上在
// 画布里的位置），canvas.anchor 是 mc 原点在画布里的位置 → 换到整帧坐标就是
// anchor - clip.min；整帧图按 flyH/fullH 缩放，所以再乘这个比例。
//
// 这个点是官方 addChildAt(flyMachineMc, 1) 的挂点，本体（people.sprite）与
// 飞行器共用同一坐标系，keepUp 的抬升量就是相对它的 y 偏移。
func flyRegPoint(style, flyH int) (float64, float64, bool) {
	clip, ok := vecFlyClip[style]
	if !ok {
		return 0, 0, false
	}
	full := flyOriginalFullH(style)
	if full <= 0 || flyH <= 0 {
		return 0, 0, false
	}
	a, ok := flyCanvasAnchor(style)
	if !ok {
		return 0, 0, false
	}
	k := float64(flyH) / float64(full)
	return (a[0] - clip[0]) * k, (a[1] - clip[1]) * k, true
}

// flyLiftFor 返回该风格本体的抬升像素（窗口上移量）。
// 原版 keepUp：1→-80 / 2→-50 / 3→-90 / 4→-70（§10），此处按原值。
func flyLiftFor(style int) int {
	switch style {
	case 2:
		return 50
	case 3:
		return 90
	case 4:
		return 70
	}
	return 80
}

// flyRegDyFromBaseY 返回飞行器 mc 原点相对 BaseY 的偏移（向下为正）。
//
// 依据 draw()：飞行态窗口顶 y0 = BaseY - bubbleH - flyH + inset，
// 整帧顶在屏幕上的 y = y0 + bubbleH = BaseY - flyH + inset，
// mc 原点 = 整帧顶 + ry。所以 BaseY 在飞行态恰好等于「机器主体底边」
// （inset 就是整帧底到本体底的空隙，见 flyBodyInsetV2）。
//
// 为什么需要它：官方 NonoFlyModel.config()（NonoFlyModel.as:145-152）是
//
//	keepUp(skeleton, -80/-50/-90/-70)             // 主人骨架抬起
//	this._people.sprite.addChildAt(flyMachineMc, 1) // 飞行器挂主人原点 (0,0)
//
// 即 mc 原点 = 主人脚下，主人被抬起来坐进飞行器。跟随时必须让**原点**
// 落在赛尔脚底；若像 2026-11-01 上一版那样让「机器底边」落在脚底，
// 机器会整个飘到赛尔头顶上方（实测差 53~115px），看着是「车飘在头上」。
func flyRegDyFromBaseY(style, flyH int) (int, bool) {
	_, ry, ok := flyRegPoint(style, flyH)
	if !ok {
		return 0, false
	}
	inset := flyBodyInsetV2(style, flyH)
	return -flyH + inset + int(math.Round(ry)), true
}

// flyRimAboveReg 返回飞行器主体（机器）顶边在 mc 原点上方多少像素。
// 飞行态赛尔要抬到「头/肩刚好露在机器口外」，抬升量由这个高度反推。
func flyRimAboveReg(style, dir, flyH int, fallback *image.RGBA) (int, bool) {
	box, _, ok := flyBodyBox(style, dir, flyH, fallback)
	if !ok || box.Empty() {
		return 0, false
	}
	_, ry, ok := flyRegPoint(style, flyH)
	if !ok {
		return 0, false
	}
	return int(math.Round(ry)) - box.Min.Y, true
}

// flySeatTopFrac 是「飞行器看得见的顶边」在赛尔脚底上方的距离 / 飞行器整帧高。
//
// 2026-11-01 四风格逐张实测（1280×800 实屏，取亮色宽行起点，脚底 383）：
//
//	style1 120/219=0.548、style2 84/247=0.340、style3 109/223=0.489、style4 114/255=0.447
//
// 为什么不现算：矢量渲染出的整帧带辉光内边距（style1 实出 198×240，flyH 却是 219），
// alpha bbox 与「帧坐标」不是同一空间，四个风格算出来的顶边误差 20~30px 且方向相反
// （style1 偏低 28px、style2 偏高 32px），bodyBox 只量 dirMc 也拿不到顶盖。
// 换成实测标定后，四风格一次对齐（偏差 ≤2px）。
// flyRideLift 是官方飞行挂载时「主人身体抬高量」，玩家本地单位，按 flyStyle 取。
//
// 唯一来源：PeculiarAction.keepUp()（PeculiarAction.as:143）
//
//	this._people.getBodyMC().y = param2;   // 调用处按 flyStyle 传 -80/-50/-90/-70
//
// 飞行器 NonoFlyModel 是 addChildAt(_people.sprite, 1)，坐标 (0,0) = 主人脚底，
// 不额外缩放。所以官方的「嵌入位置」只有这两个数，不需要按美术反推。
var flyRideLift = map[int]float64{1: 80, 2: 50, 3: 90, 4: 70}

// flyRideLiftAdj 是官方 keepUp 之外、按我方素材实测补的微调（玩家本地单位）。
//
// 只在「赛尔压在机器之上」这个我方特有图层顺序下才需要：官方机器盖住主人身体，
// 差几像素看不见；我方赛尔在上，接缝差多少一眼就能看出。
// 2026-11-01 实屏量接缝（赛尔腰环底 vs 机器轮廓顶，机器接地阴影已对齐脚底）：
//
//	style1 缝 15px、style2 缝 20px、style3 缝 10px、style4 重叠 26px（已贴合，不加）
//
// 目标缝 ≈ −5px（轻微压边，和 style4 一致），故补 −13/−17/−10 本地单位。
// 2026-11-01 21:40 赛尔挂载放大（seer-20261101-05 rideScaleMul=1.14）后重标：
// 人变大后头身整体上顶，style1 经子渊确认 dy=0 即对，其余三风格实测都还要下压 20px，
// 折成本地单位 −20/1.5 = −13.3，只动 2/3/4。
var flyRideLiftAdj = map[int]float64{1: -26, 2: -32.0, 3: -43.3, 4: -31.3}

// flyRideDX 是飞行挂载时飞行器相对赛尔的水平微调（屏幕像素，正 = 机器右移 = 人相对左移）。
// 2026-11-01 21:58 子渊「三四模式应该还要往左移 1」：实量赛尔金腰带中心 797.5、
// 整机 bbox 中心 style3 796.5 / style4 795.5，人偏右 1~2px，按指示 3、4 各移 1px。
var flyRideDX = map[int]float64{2: 1, 3: 1, 4: 2}

// flySeatTopFrac 是「飞行器看得见的顶边」在赛尔脚底上方的距离 / 飞行器整帧高。
//
// 2026-11-01 四风格逐张实测（1280×800 实屏，取亮色宽行起点，脚底 383）：
//
//	style1 120/219=0.548、style2 84/247=0.340、style3 109/223=0.489、style4 114/255=0.447
//
// 注意：这套数是**按亮色行**取的，量到的是光晕/高光而不是真轮廓，
// 抬升已经改回官方 keepUp 固定值（见 flyRideLift），这里只留作参考，不再参与定位。
var flySeatTopFrac = map[int]float64{1: 0.548, 2: 0.340, 3: 0.489, 4: 0.447}

// flyGroundFix 是「我方 mc 注册点相对接地阴影中心」的偏低量（屏幕像素，缩放 1.5 下实测）。
//
// 官方把飞行器挂在主人 sprite 原点 (0,0)，也就是赛尔脚底 —— 阴影中心应压在脚底上。
// 我方 style1 实测：阴影椭圆 y389..417（中心 403），赛尔脚底 383 → 偏低 20。
// 四个风格是同一套导出流程，先用同一个值；某个风格若实测偏差 >8px 再单独列。
var flyGroundFixByStyle = map[int]int{1: 20}

// flySeatArtY 是「飞行器素材里那块灰座位」的中心 y（素材像素，1x）。
//
// 子渊 2026-11-01：「NONO1级2级不是有灰色的吗，那个就是赛尔的位置」——
// 灰块就是原版美术留给驾驶员的座舱，人应该坐进那块灰里，不是浮在机器顶上。
// 实测（assets/fly/N.png，取该风格的平涂灰）：
//
//	style1 (200,200,200) 圆盘 y23..80 中心 36.0
//	style2 (102,102,102) 圆孔 y63..87 中心 75.1
//	style4 (149,162,159) 凹槽 y51..93 中心 75.2
//	style3 无灰块（整台青壳 + 黑脸），只能用官方 keepUp 兜底
var flySeatArtY = map[int]float64{1: 36.0, 2: 75.1, 4: 75.2}

// flySeatAuto 是「按灰座位自动算抬升」的开关。
//
// 2026-11-01 实测失败：矢量渲染出的整帧与 assets/fly/N.png 不是同一空间
// （矢量整帧带辉光内边距，style1 实出 198×240 而 flyH=219），
// 用素材坐标反推 style1 得 26px，而子渊选定的观感是 65px，差 39px。
// 所以这条路先关掉，抬升仍用实测常量；等哪天把矢量帧与素材帧对齐了再开。
var flySeatAuto = false

// flySizeAdj 把四个飞行器的显示大小归一到同一口径（子渊 2026-11-01：
// 「你要统一NONO的这个和赛尔的大小」）。
//
// 依据 assets/fly/N.png 的实心轮廓高（alpha>200）：style1 72 / style2 76 /
// style3 73 / style4 79，画布高 146/165/150/171。按同一显示比例 1.5 画出来，
// 四个机器的实心高度是 108/114/109.5/118.5px —— 换风格肉眼可见大小不一样。
// 这里统一到 style1 的 108px：108/(实心高×1.5)。
var flySizeAdj = map[int]float64{1: 1.000, 2: 0.947, 3: 0.986, 4: 0.911}

// flyDxFix 把飞行器整体右移，让机器中心对准赛尔中线（单位：屏幕 px）。
//
// 子渊 2026-11-01 画了两个圈：大圈=灰座舱（赛尔该在的位置），小圈=赛尔现在的头身，
// 要求两者重叠。实测四风格「机器最宽行中点」与「赛尔黑头中线」：
// style1 790 vs 796、style2 784 vs 796、style3 791 vs 796、style4 786 vs 796
// —— 机器普遍偏左 5~12px（素材注册点 x 不等于美术中线）。
// 这里按差值右移，让赛尔正好落在机器中线。
var flyDxFix = map[int]float64{1: 6, 2: 12, 3: 5, 4: 10}

// flySeerBodyCenterRows 是赛尔帧里「可见身体中心」到脚底的行数差。
// 赛尔帧 74x100：帽 8..25、黑头 22..55、金腰带环 55..68、脚 68..88。
// 可见身体 = 22..68（裁掉腿），中心 45 行，脚在 88 行 → 43 行。
const flySeerBodyCenterRows = 43.0

// flyRideLiftPx 返回当前飞行风格下赛尔应抬起的屏幕像素（0 = 不抬）。
//
// 只依赖「飞行风格 + 赛尔缩放」，不依赖跟随是否暂停 —— 悬停弹出快捷栏、
// 赛尔心跳短暂中断时，人不能从车里掉出来。
func (g *Game) flyRideLiftPx() int32 {
	if g == nil || g.Save == nil || !g.flyOn() {
		return 0
	}
	keep, ok := flyRideLift[g.Save.FlyStyle]
	if !ok {
		return 0
	}
	keep += flyRideLiftAdj[g.Save.FlyStyle]
	sc := g.flyScale()
	if v := atomic.LoadInt32(&seerFollowScale); v > 100 {
		sc = float64(v) / 1000
	}
	// 有灰座位的风格：直接让「赛尔可见身体中心」落在座位中心上。
	//
	// 推导（都在屏幕坐标里，脚底项会约掉）：
	//   飞行器 mc 原点 = 赛尔脚底（官方 addChildAt 口径）
	//   座位屏幕 y = 脚底 - ry + seatArt*sc        （ry = mc 原点在整帧里的 y）
	//   抬升后身体中心 y = 脚底 - 43*sc - lift
	//   两式相等 → lift = ry - (43 + seatArt)*sc
	// 好处：换素材、改显示大小、换缩放都不用重标定。
	if seatArt, hasSeat := flySeatArtY[g.Save.FlyStyle]; hasSeat && flySeatAuto {
		if fh := int(float64(flyOriginalFullH(g.Save.FlyStyle)) * sc * flySizeAdj[g.Save.FlyStyle]); fh > 24 {
			if _, ry, ok2 := flyRegPoint(g.Save.FlyStyle, fh); ok2 {
				lift := ry - (flySeerBodyCenterRows+seatArt)*sc
				if v := os.Getenv("NONO_FLY_RIDE"); v != "" {
					if f, e := strconv.ParseFloat(v, 64); e == nil && f >= -40 && f <= 60 {
						lift += f * sc
					}
				}
				if lift < 0 {
					lift = 0
				}
				if lift > 400 {
					lift = 400
				}
				return int32(lift + 0.5)
			}
		}
	}
	off := 0.0
	if v := os.Getenv("NONO_FLY_RIDE"); v != "" {
		if f, e := strconv.ParseFloat(v, 64); e == nil && f >= -40 && f <= 60 {
			off = f
		}
	}
	lift := int((keep+off)*sc + 0.5)
	if lift < 0 {
		lift = 0
	}
	if lift > 400 {
		lift = 400
	}
	return int32(lift)
}

// flyGroundFix 取某风格的注册点修正量，缺省 20。
func flyGroundFix(style int) int {
	if v, ok := flyGroundFixByStyle[style]; ok {
		return v
	}
	return 20
}

// flySeatTopAboveFeet 返回飞行器看得见的顶边在赛尔脚底上方多少像素（按当前显示高度缩放）。
func flySeatTopAboveFeet(style, flyH int) (int, bool) {
	if style < 1 || style > 4 {
		style = 1
	}
	f, ok := flySeatTopFrac[style]
	if !ok || flyH <= 0 {
		return 0, false
	}
	return int(f * float64(flyH)), true
}
