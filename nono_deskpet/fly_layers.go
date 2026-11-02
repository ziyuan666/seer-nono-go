//go:build windows

package main

// 飞行器分层合成：colorMc（可染色底板）+ fireMc（尾焰）+ dirMc（本体），
// 外加 style2/3/4 的 bgMc（落地阴影）。
//
// 为什么分层（2026-10-24 子渊反馈）：
//   1. 「颜色不跟着变」——原版 NonoFlyModel 用 DisplayUtil.FillColor(colorMc, _info.color)
//      把底板染成玩家色（DisplayUtil.as:31 用 ColorTransform.color，语义是 RGB 替换、
//      alpha 保留）。整帧烤死就永远是素材原色，运行时没有通道可改。
//   2. 「尾焰还是不一样」——三层各有独立子 MC 周期（fireMc 内的火苗是 8 帧），
//      整帧合成把三层强行同步到方向段长（down 段只有 7 帧），火苗动到第 7 帧就被拉回，
//      节奏和轨迹都少一拍。分层后 fire 层单独走完整 8 帧循环。
//
// 坐标换算：层 PNG 是 SWF 的 4 倍分辨率（ZOOM=4），metrics 里的 fullW/fullH 是逻辑像素。
// 目标整帧宽 targetW（逻辑像素）对应缩放比 s = targetW / (fullW * flyZoom)。

import (
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const flyZoom = 4

type flyDirMeta struct {
	Name   string `json:"name"`
	Frames int    `json:"frames"`
}

type flyLayerMeta struct {
	Prefix string         `json:"prefix"`
	Off    []int          `json:"off"`
	Scale  float64        `json:"scale"` // 该层在 SWF 里被根 place 缩放的比例（缺省 1）
	Dirs   []flyDirMeta `json:"dirs"`
	W      int            `json:"w"`
	H      int            `json:"h"`
}

// flyMetricV2 是 gen_fly_layers.py 输出的分层版 metrics。
type flyMetricV2 struct {
	FullW      int                     `json:"fullW"`
	FullH      int                     `json:"fullH"`
	BodyLeft   int                     `json:"bodyLeft"`
	BodyTop    int                     `json:"bodyTop"`
	BodyRight  int                     `json:"bodyRight"`
	BodyBottom int                     `json:"bodyBottom"`
	BodyW      int                     `json:"bodyW"`
	BodyH      int                     `json:"bodyH"`
	Layers     map[string]flyLayerMeta `json:"layers"`
	Dirs       []flyDirMeta          `json:"dirs"`
}

var flyMetricsV2 = map[int]flyMetricV2{}

func loadFlyMetricsV2() {
	p := filepath.Join(assetsRoot(), "fly", "metrics.json")
	b, err := os.ReadFile(p)
	if err != nil {
		logf("fly metrics v2 读取失败: %v", err)
		return
	}
	var raw map[string]flyMetricV2
	if err := json.Unmarshal(b, &raw); err != nil {
		logf("fly metrics v2 解析失败: %v", err)
		return
	}
	for k, v := range raw {
		if n, err := strconv.Atoi(k); err == nil && v.FullW > 0 && len(v.Layers) > 0 {
			flyMetricsV2[n] = v
		}
	}
	logf("fly metrics v2 loaded %d styles", len(flyMetricsV2))
}

// flyLayerPix 保存某一层的已缩放像素与偏移（逻辑像素，相对整帧左上角）。
type flyLayerPix struct {
	img *image.RGBA
	ox  int
	oy  int
}

type flyCompositeKey struct {
	style, dir, targetW int
	tint                 uint32
}

var flyCompositeCache = map[flyCompositeKey][]*image.RGBA{}

// flyLayerOrder 按 SWF depth 从底到顶。style1 没有 bgMc。
func flyLayerOrder(style int) []string {
	if style == 1 {
		return []string{"colorMc", "fireMc", "dirMc"}
	}
	return []string{"bgMc", "fireMc", "colorMc", "dirMc"}
}

// flyCompositeFrames 返回某风格某方向、整帧显示高 targetH、底板染成 tint 时的帧序列。
// 各层按自己的段内帧数取帧循环（color 层恒定 1 帧），所以 fire 的 8 帧火苗
// 与 dir 的 7 帧本体各自推进，不会互相拽住。
func flyCompositeFrames(style, dir, targetH int, tint uint32) []*image.RGBA {
	if targetH < 1 {
		targetH = 1
	}
	if style < 1 || style > 4 {
		style = 1
	}
	if dir < 1 || dir > 8 {
		dir = 1
	}
	// 矢量后端优先：一次渲染就是原版三层合成，省掉 PNG 叠加与 15MB 素材。
	if v := vecFly(); v != nil {
		if fr := v.frames(style, dir, targetH, tint); len(fr) > 0 {
			return fr
		}
	}
	m, ok := flyMetricsV2[style]
	if !ok {
		return nil
	}
	// 严格原版（子渊 2026-10-24 定）：四形态一律按各自 SWF 的原始整帧尺寸
	// 等比放大，不做「可见内容体量」归一。
	// 依据：游戏里飞行器是 NonoFlyModel 直接 addChild 的整帧，四个 style 的
	// 原始尺寸本来就不同（1x：120×146 / 177×165 / 105×149 / 167×170），
	// 原版从没把它们对齐过；之前按 gem 归一属于我方发明，反而抹掉了原版差异。
	// targetH 入参语义 = 目标整帧高（屏幕像素），k = targetH / 原版整帧高(1x)。
	k := float64(targetH) / float64(m.FullH)
	targetHOut := int(float64(m.FullH) * k)
	if targetHOut < 1 {
		targetHOut = 1
	}
	targetW := int(float64(m.FullW) * k)
	if targetW < 1 {
		targetW = 1
	}
	// 4x 素材 → 输出的缩放比
	s := k / 4
	key := flyCompositeKey{style, dir, targetW, tint}
	if list, ok := flyCompositeCache[key]; ok {
		return list
	}
	name := dirNames[dir-1]
	base := filepath.Join(assetsRoot(), "fly")

	// 缩放比已在上面按高度算好（s），这里不再按宽度重算
	// 每层：读 4x 原图 → 缩放 → 记偏移
	type loaded struct {
		name  string
		imgs  []*image.RGBA
		ox    int
		oy    int
	}
	var sets []loaded
	for _, lname := range flyLayerOrder(style) {
		lm, has := m.Layers[lname]
		if !has {
			continue
		}
		n := 1
		for _, d := range lm.Dirs {
			if d.Name == name {
				n = d.Frames
				if n < 1 {
					n = 1
				}
				break
			}
		}
		var imgs []*image.RGBA
		for k := 1; k <= n; k++ {
			var p string
			if n == 1 && (lname == "bgMc" || lname == "colorMc") {
				p = filepath.Join(base, lm.Prefix+"_"+itoa(style)+"_"+name+".png")
			} else {
				p = filepath.Join(base, lm.Prefix+"_"+itoa(style)+"_"+name+"_"+itoa(k)+".png")
			}
			src := loadPNGToRGBA(p)
			if src == nil {
				if k == 1 {
					logf("fly layer missing %s", p)
				}
				break
			}
			imgs = append(imgs, src)
		}
		if len(imgs) == 0 {
			continue
		}
		// 缩放：4x 像素 → 逻辑像素。s 已经是 targetH/(fullH*4)，即 4x→目标的比，
		// 这里不能再用 s/flyZoom（那会多除 4，图层缩到 1/18，飞行器只剩 50px
		// 且边缘因极端缩小出现彩色噪点——子渊 2026-10-24 的「分辨率低 + 锯齿」）。
		sc := s
		// 层内再缩放：SWF 里有的层被根 place 缩放过（style3/4 的 bgMc 是 0.807），
		// 而 ffdec 导出的层 PNG 是 sprite bbox 的原尺寸，所以这里按 metrics 的
		// scale 补回来。缩放围绕层图中心——标定 offsets 时也是同一口径。
		ls := 1.0
		if lm.Scale > 0 && lm.Scale != 1 {
			ls = lm.Scale
		}
		offX, offY := 0, 0
		if len(lm.Off) >= 2 {
			offX = int(float64(lm.Off[0])*sc + 0.5)
			offY = int(float64(lm.Off[1])*sc + 0.5)
		}
		if ls != 1 && len(imgs) > 0 {
			b := imgs[0].Bounds()
			offX += int(float64(b.Dx())*(1-ls)*sc/2 + 0.5)
			offY += int(float64(b.Dy())*(1-ls)*sc/2 + 0.5)
		}
		scaledImgs := make([]*image.RGBA, 0, len(imgs))
		for _, src := range imgs {
			w := int(float64(src.Bounds().Dx())*sc*ls + 0.5)
			h := int(float64(src.Bounds().Dy())*sc*ls + 0.5)
			if w < 1 {
				w = 1
			}
			if h < 1 {
				h = 1
			}
			scaledImgs = append(scaledImgs, resizeFrame(src, w, h))
			if os.Getenv("NONO_DEBUG_LAYERS") != "" {
				debugCanvas(scaledImgs[len(scaledImgs)-1],
					"layer_"+itoa(style)+"_"+lname+"_"+itoa(len(scaledImgs))+".png")
			}
		}
		sets = append(sets, loaded{name: lname, imgs: scaledImgs, ox: offX, oy: offY})
	}
	if len(sets) == 0 {
		return nil
	}

	// 帧数：严格原版默认只合成段首帧。NonoFlyModel.direction 里
	// dirMc/colorMc/fireMc 三层全部 gotoAndStop(方向)，全类没有任何 play()，
	// 所以游戏里的飞行器（含尾焰）是静止的，方向切换才换帧。
	// SWF 里每层有 7~8 帧是给文件播放器看的，游戏不用——这也是「SWF 里
	// 动画快、游戏里慢」的第二处来源。
	// 调试开关 NONO_FLY_ANIM=1 恢复各层循环（旧观感），默认关闭。
	total := 1
	if os.Getenv("NONO_FLY_ANIM") != "" {
		for _, st := range sets {
			if len(st.imgs) > total {
				total = len(st.imgs)
			}
		}
	}
	out := make([]*image.RGBA, 0, total)
	// 画布高度就是目标整帧高（缩放严格按高度对齐，宽度由比例推出）
	targetH = targetHOut
	if targetH < 1 {
		targetH = 1
	}
	r, gg, b := rgb(tint)
	tintColor := color.RGBA{r, gg, b, 255}
	for k := 0; k < total; k++ {
		canvas := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
		for _, st := range sets {
			src := st.imgs[k%len(st.imgs)]
			// 原版 NonoFlyModel 对 colorMc **无条件**调 FillColor(_info.color)，
			// 玩家色是白色时也染成白。之前写成 tint != 0xFFFFFF 才染，等于保留
			// 各 style 的原始底色（style1 灰、style2/3/4 青绿），所以「颜色不统一」
			// （子渊 2026-10-24）。
			if st.name == "colorMc" {
				// ColorTransform.color 语义：RGB 全替换成目标色，alpha 保留
				src = tintKeepAlpha(src, tintColor)
			}
			overNonPremul(canvas, src, st.ox, st.oy)
		}
		out = append(out, canvas)
	}
	flyCompositeCache[key] = out
	return out
}

// overNonPremul 把 src 以非预乘 alpha 叠加到 canvas 的 (ox,oy)。
// 不能用 image/draw 的 draw.Over：Go 的 *image.RGBA 语义是**预乘 alpha**，
// 而本项目整条链路（loadPNGToRGBA、areaScale、blit）都按非预乘处理，
// 用 draw.Over 会把边缘像素当成预乘值再乘一次，透明边界被压成红/绿彩色描边，
// 内部半透明区变脏（子渊 2026-10-24「严重锯齿/颜色不统一」的直接原因）。
// 这里按标准非预乘 Over 公式逐像素合：
//   outA = sa + da*(1-sa)
//   outRGB = (srcRGB*sa + dstRGB*da*(1-sa)) / outA
func overNonPremul(dst, src *image.RGBA, ox, oy int) {
	db := dst.Bounds()
	sb := src.Bounds()
	for y := 0; y < sb.Dy(); y++ {
		dy := oy + y
		if dy < db.Min.Y || dy >= db.Max.Y {
			continue
		}
		for x := 0; x < sb.Dx(); x++ {
			dx := ox + x
			if dx < db.Min.X || dx >= db.Max.X {
				continue
			}
			c := src.RGBAAt(sb.Min.X+x, sb.Min.Y+y)
			if c.A == 0 {
				continue
			}
			if c.A == 255 {
				dst.SetRGBA(dx, dy, c)
				continue
			}
			d := dst.RGBAAt(dx, dy)
			sa := float64(c.A) / 255
			da := float64(d.A) / 255
			outA := sa + da*(1-sa)
			if outA <= 0 {
				continue
			}
			mix := func(s, dv uint8) uint8 {
				v := (float64(s)*sa + float64(dv)*da*(1-sa)) / outA
				return clamp8(v)
			}
			dst.SetRGBA(dx, dy, color.RGBA{
				mix(c.R, d.R),
				mix(c.G, d.G),
				mix(c.B, d.B),
				clamp8(outA * 255),
			})
		}
	}
}

// tintKeepAlpha 把整层 RGB 换成纯色、alpha 原样保留
// （对应 AS3 ColorTransform.color —— 官方 NonoFlyModel 对 colorMc 用的就是这个）。
// 注意是**替换**不是乘法：乘法会让青绿底板把白色玩家色乘回青绿，飞行器和
// 本体永远是两种颜色（子渊 2026-10-24 / 10-27 两次反馈「配色不统一」）。
func tintKeepAlpha(src *image.RGBA, c color.RGBA) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		si := src.PixOffset(b.Min.X, b.Min.Y+y)
		di := dst.PixOffset(0, y)
		for x := 0; x < b.Dx(); x++ {
			a := src.Pix[si+3]
			if a > 0 {
				// 整层刷成目标色，alpha 原样保留
				dst.Pix[di] = c.R
				dst.Pix[di+1] = c.G
				dst.Pix[di+2] = c.B
				dst.Pix[di+3] = a
			}
			si += 4
			di += 4
		}
	}
	return dst
}

// flyBodyInsetV2 返回「整帧底边 → 本体底边」的逻辑像素距离，用实测 bbox 算。
// 旧实现用 dirMc 导出画布高度（含透明边），bodyBottom 恒 >= fullH，函数直接返回 0，
// 抬升补偿从未生效，飞行时整只浮在 NONO 原位置上方很高（子渊 2026-10-24 反馈）。
func flyBodyInsetV2(style, flyH int) int {
	if style < 1 || style > 4 {
		style = 1
	}
	m, ok := flyMetricsV2[style]
	if !ok || m.FullH <= 0 || m.BodyBottom <= 0 {
		return 0
	}
	if m.BodyBottom >= m.FullH {
		return 0
	}
	return flyH * (m.FullH - m.BodyBottom) / m.FullH
}

// flyBodyRatio 是飞行器本体宽 / NONO 本体宽，取自游戏原版数据：
//   NonoModel.SCALE = 0.8（NonoModel.as:46），NONO 本体 SWF 宽 253/4 = 63.25px
//   飞行器 SWF 整帧宽 120px、本体宽 77px（style1 实测 bbox）
//   游戏内本体 63.25*0.8 = 50.6px、飞行器本体 77*0.8 = 61.6px → 比例 1.218?? 
// 见下方 flyBodyWidthFor 的实测推导，这里只保留「以本体宽度对齐」这一原则：
// 统一整帧宽会让 4 个 style 的本体差 45%（style1 本体 77/整帧 120 = 64%，
// style2 本体 79/整帧 177 = 45%），肉眼就是「每个模式大小不统一」（子渊 2026-10-24）。
// flyOriginalFullH 返回某 style 原版整帧高（1x 逻辑像素），供严格原版尺寸换算。
// 缺表时退回 style1 的 146，避免调用方拿到 0 导致整帧被压成 1px。
func flyOriginalFullH(style int) int {
	if m, ok := flyMetricsV2[style]; ok && m.FullH > 0 {
		return m.FullH
	}
	return 146
}

// flyScreenW 返回某 style 飞行器在屏幕上的整帧宽（像素），与 draw 里 flyW 同口径：// 整帧高 = flyOriginalFullH(style) × scale，宽按原版宽高比等比放大 → 恰好 FullW × scale。
// 快捷栏环绕半径必须用它：飞行器整帧 1x 宽 105~177，比本体球径（99）宽一倍多，
// 沿用「0.9×球径」当环绕半径会把 7 个图标压在飞行器身上
// （子渊 2026-10-27「飞行模式鼠标悬浮过去的功能图标圈有部分覆盖了」）。
func flyScreenW(style int, sc float64) int {
	if sc < 1 {
		sc = 1
	}
	if style < 1 || style > 4 {
		style = 1
	}
	m, ok := flyMetricsV2[style]
	if !ok || m.FullW <= 0 {
		return int(120 * sc)
	}
	return int(float64(m.FullW) * sc)
}

// flyBodyCenter 返回某 style 飞行器「本体」在整帧内的中心（屏幕像素，相对整帧左上）。
// 快捷栏环心要用它而不是整帧中心：整帧含大量透明 padding，且四边不对称
// （style3 整帧 105×149，本体 bbox (23,24)-(82,84) → 中心比整帧中心高 20px(1x)），
// 拿整帧中心当环心会让图标圈整体下沉，底部图标离飞行器很远、上面又贴脸
// （子渊 2026-10-27 实测偏差约 30px）。
func flyBodyCenter(style int, sc float64) (int, int, bool) {
	if sc < 1 {
		sc = 1
	}
	if style < 1 || style > 4 {
		style = 1
	}
	m, ok := flyMetricsV2[style]
	if !ok || m.BodyRight <= m.BodyLeft || m.BodyBottom <= m.BodyTop {
		return 0, 0, false
	}
	cx := float64(m.BodyLeft+m.BodyRight) / 2 * sc
	cy := float64(m.BodyTop+m.BodyBottom) / 2 * sc
	return int(cx + 0.5), int(cy + 0.5), true
}

// flyLogX/flyLogY 只在飞行器环心变化时打日志，避免每帧刷屏。
var flyLogX, flyLogY = -1, -1

// flyBodyBox 返回飞行器主体（不含接地阴影/尾焰）在整帧内的 bbox。
// 优先走矢量后端的分层渲染（colorMc+dirMc，结果最准）；
// 矢量关闭时退回 alpha 扫描（行分断，会把尾焰算进去，但至少不含独立阴影）。
func flyBodyBox(style, dir, flyH int, fallback *image.RGBA) (image.Rectangle, image.Point, bool) {
	if v := vecFly(); v != nil {
		if b, c, ok := v.bodyBox(style, dir, flyH); ok {
			return b, c, true
		}
	}
	if fallback != nil {
		if w, h, x, y, ok := flyVisibleBounds(fallback); ok {
			return image.Rect(x, y, x+w, y+h), image.Pt(x+w/2, y+h/2), true
		}
	}
	return image.Rectangle{}, image.Point{}, false
}

// flyVisibleBounds 返回飞行器帧「主体」的可见范围：宽、高、相对整帧左上的偏移。
//
// 不能直接用整帧 alpha bbox：飞行器素材里还叠了 bgMc 接地阴影（在下方，很宽）
// 和 fireMc 尾焰（很窄），直接取 bbox 会让环心被阴影拽下去 30~45px，
// 表现就是「图标圈不以飞行器为中心」、「上边贴脸下边很远」（子渊 2026-10-27）。
//
// 做法：按行统计不透明像素数 → 切成连续段 → 取像素总量最大的段（主体；
// 阴影一般被中间的透明空隙隔开）→ 段内剔掉行宽不足峰值 45% 的行（尾焰尾巴）。
// x 范围同样只取保留下来的行，避免尾焰/阴影把左右撑开。
//
// 结果按图像指针缓存：同一 (style, 方向, 缩放) 的合成帧复用同一批 *image.RGBA。
func flyVisibleBounds(img *image.RGBA) (w, h, dx, dy int, ok bool) {
	if img == nil {
		return 0, 0, 0, 0, false
	}
	flyBoxMu.Lock()
	if b, hit := flyBoxCache[img]; hit {
		flyBoxMu.Unlock()
		return b.w, b.h, b.x, b.y, b.w > 0
	}
	flyBoxMu.Unlock()

	r := img.Bounds()
	n := r.Dy()
	if n <= 0 || r.Dx() <= 0 {
		return 0, 0, 0, 0, false
	}
	cnt := make([]int, n)
	rowLo := make([]int, n)
	rowHi := make([]int, n)
	for y := 0; y < n; y++ {
		rowLo[y], rowHi[y] = -1, -1
		for x := 0; x < r.Dx(); x++ {
			if img.Pix[img.PixOffset(r.Min.X+x, r.Min.Y+y)+3] > 8 {
				cnt[y]++
				if rowLo[y] < 0 {
					rowLo[y] = x
				}
				rowHi[y] = x
			}
		}
	}
	// 连续非零段里取像素总量最大的那段
	bestLo, bestHi, bestSum := -1, -1, 0
	for y := 0; y < n; {
		if cnt[y] == 0 {
			y++
			continue
		}
		lo, sum := y, 0
		for y < n && cnt[y] > 0 {
			sum += cnt[y]
			y++
		}
		if sum > bestSum {
			bestSum, bestLo, bestHi = sum, lo, y-1
		}
	}
	if bestLo < 0 {
		flyBoxMu.Lock()
		flyBoxCache[img] = flyBox{}
		flyBoxMu.Unlock()
		return 0, 0, 0, 0, false
	}
	peak := 0
	for y := bestLo; y <= bestHi; y++ {
		if cnt[y] > peak {
			peak = cnt[y]
		}
	}
	th := peak * 45 / 100
	minY, maxY, minX, maxX := -1, -1, 1<<30, -1
	for y := bestLo; y <= bestHi; y++ {
		if cnt[y] < th {
			continue
		}
		if minY < 0 {
			minY = y
		}
		maxY = y
		if rowLo[y] < minX {
			minX = rowLo[y]
		}
		if rowHi[y] > maxX {
			maxX = rowHi[y]
		}
	}
	b := flyBox{}
	if minY >= 0 && maxX >= minX {
		b = flyBox{w: maxX - minX + 1, h: maxY - minY + 1, x: r.Min.X + minX, y: r.Min.Y + minY}
	}
	flyBoxMu.Lock()
	if len(flyBoxCache) > 512 { // 换形态/缩放会换一批帧，别无限涨
		flyBoxCache = map[*image.RGBA]flyBox{}
	}
	flyBoxCache[img] = b
	flyBoxMu.Unlock()
	return b.w, b.h, b.x, b.y, b.w > 0
}

type flyBox struct{ w, h, x, y int }

var (
	flyBoxMu    sync.Mutex
	flyBoxCache = map[*image.RGBA]flyBox{}
	// flyVisNow 记录「当前方向」的飞行器主体可见尺寸，key = {style, 屏幕整帧高, 方向}。
	//
	// 为什么按方向分开：四个方向的可见宽高差得很远（style3 down 73×91，
	// left/right 时整帧 105 宽横躺，主体宽翻倍），取「跨方向最大值」会让
	// 正对镜头时左右两侧的图标被撑到两倍远（子渊 2026-10-27 第二次反馈：
	// 「现在非常大且靠外」）。转身时窗口会重建一次，可接受——一圈雾面窗口
	// 重建只要几毫秒，且转身本身不频繁。
	flyVisNowMu sync.Mutex
	flyVisNow   = map[[3]int][2]int{}
)

// noteFlyVis 记录当前方向的主体可见尺寸（供快捷栏算椭圆半径）。
func noteFlyVis(style, flyH, dir, w, h int) {
	flyVisNowMu.Lock()
	defer flyVisNowMu.Unlock()
	flyVisNow[[3]int{style, flyH, dir}] = [2]int{w, h}
}

// flyVisFor 返回某 (style, 屏幕整帧高, 方向) 的主体可见宽高（可能为 0）。
func flyVisFor(style, flyH, dir int) (int, int) {
	flyVisNowMu.Lock()
	defer flyVisNowMu.Unlock()
	m := flyVisNow[[3]int{style, flyH, dir}]
	return m[0], m[1]
}

// flyVisAny 返回该 style 任意方向已记录的主体尺寸（首帧前兜底，避免半径算成 0）。
func flyVisAny(style, flyH int) (int, int) {
	flyVisNowMu.Lock()
	defer flyVisNowMu.Unlock()
	for k, m := range flyVisNow {
		if k[0] == style && k[1] == flyH && m[0] > 0 {
			return m[0], m[1]
		}
	}
	return 0, 0
}

// flyScreenH 返回某 style 飞行器在屏幕上的整帧高（像素），与 draw 里 fh 同口径。
func flyScreenH(style int, sc float64) int {
	if sc < 1 {
		sc = 1
	}
	h := int(float64(flyOriginalFullH(style)) * sc)
	if h < 24 {
		h = 24
	}
	return h
}

// flyContentGem 是各风格飞行器「可见内容」的几何平均尺寸（1x 逻辑像素，
// = sqrt(contentW*contentH)），由 assets/fly/offsets.json 的层偏移 + 各层
// alpha bbox 实测（4x 值 346.4/360.8/326.7/333.3 除以 4）。
// 2026-10-24 改严格原版后不再参与缩放，仅保留作诊断日志与后续核对参考。
var flyContentGem = map[int]float64{1: 80.6, 2: 70.4, 3: 71.0, 4: 77.8}
