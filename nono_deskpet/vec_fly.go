//go:build windows

package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"nonovec/pkg/raster"
	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

// 飞行器矢量后端：assets/sprites/fly{1..4}/sprite.json。
//
// 四个 style 的 SWF 结构不同（1：color/fire/dir；2：bg/fire/color/dir；
// 3/4：同 2 但美术不同），但根 sprite 已经把三层都挂在同一时间轴上，
// 所以一次渲染就等于原版的三层合成，不必再走 fly_layers.go 的 PNG 叠加。
//
// 尺寸口径沿用旧逻辑：k = targetH / 原版整帧高，保证与 flyCompositeFrames
// 的输出高度一致；裁剪框按 vecFlyClip 对齐原版整帧，输出尺寸与旧 PNG 一致，
// 调用方布局公式不用改。

// vecFlyClip 把「原版整帧」映射到矢量画布坐标（画布单位，1x）。
//
// 为什么不用 RenderFullCanvas：dirMc 的 glow 会溢出画布 bbox（实测 f1 帧
// -17..121，画布只有 0..104），整画布出图会把辉光切出直角边；而按原版整帧
// 裁剪后，输出宽高比/内容落点和 assets/fly 的 PNG 完全一致，fly_layers.go
// 里那套落位公式可以原样复用。
//
// 数值来源：先用「去掉滤镜」的临时 sprite 渲染整画布，再与 assets/fly 的
// 4x 分层合成（bg/color/fire/dir 按 offsets.json 叠）做整像素平移搜索，
// 取 alpha 掩码 IoU 最大处，除以 4 换算回画布单位；随后用 FFDec 直接导出的
// DefineSprite 帧 PNG（金标准）复核，落点最大偏差 0.5 画布单位以内。
// 2026-10-27 与 FFDec 帧图对比（4x，alpha 掩码 IoU / 均值 RGB 差）：
//
//	style1 0.944 / 9.9、style2 0.976 / 9.2、style3 0.962 / 7.8、style4 0.968 / 5.5。
var vecFlyClip = map[int][4]float64{
	1: {-4.25, -4.00, 120, 146},
	2: {-37.00, -8.25, 177, 165},
	3: {-9.25, -4.00, 105, 149},
	4: {-30.75, -8.25, 167, 170},
}

// flyBoxRec 是飞行器主体的量测结果：R = alpha bbox，C = alpha 加权质心。
type flyBoxRec struct {
	R image.Rectangle
	C image.Point
}

type vecFlySet struct {
	mu    sync.Mutex
	cache map[int]*sprite.Renderer
	// frame 存「某 style×方向×尺寸」的整组帧。
	// 程序化尾焰开启时一组是 smokePhase 张（各相位一张），否则只有 1 张。
	// 每组 1.1~1.8MB × 8，切形态时按 curStyle 清掉旧组，避免 4 形态常驻 40MB。
	frame    map[string][]*image.RGBA
	// gframe 缓存子时间轴帧（眼睛 15/19 帧、光圈 18 帧）按 phase 渲染的小图，
	// key = style_方向_尺寸_染色_组号_相位。与 frame 分开是因为它们的生命周期
	// 只跟 style 有关，不该被 12 相位的尾焰组清掉。
	gframe   map[string]*image.RGBA
	// sbase/sovl 缓存「背景层（bgMc）」与「机体静态层（colorMc+dirMc 静态 part）」，
	// 两者与时间无关，只随 style/尺寸/染色变化。子时间轴每帧都要拿它们当底，
	// 不缓存的话每 40ms 就要重渲一次 60ms 的机体层。
	sbase map[string]*image.RGBA
	sovl  map[string]*image.RGBA
	curStyle int
	// body 是「只画 colorMc+dirMc」的第二套渲染器，专门用来量飞行器主体 bbox。
	// 为什么单独一套：主渲染器要出整帧（含尾焰/接地阴影），而快捷栏环心必须
	// 落在主体中心上；用主渲染器临时 SetOnlyLayers 会清帧缓存，切回来每帧重算。
	body map[int]*sprite.Renderer
	// box 缓存主体 bbox + 质心（屏幕像素，相对整帧左上）
	box map[string]flyBoxRec
}

var (
	vecFlyOnce sync.Once
	vecFlyInst *vecFlySet
)

func vecFly() *vecFlySet {
	vecFlyOnce.Do(func() {
		// 飞行器矢量默认开启：素材已按 --native 重导（4 style × 8 方向 × 8 帧），
		// 与 PNG 逐帧比对 IoU 0.96~1.00、画布尺寸一致。
		// NONO_VEC_FLY=0 可回退 PNG，用于对照/排错。
		if os.Getenv("NONO_VEC_FLY") == "0" {
			return
		}
		vecFlyInst = &vecFlySet{
			cache:  map[int]*sprite.Renderer{},
			frame:  map[string][]*image.RGBA{},
			gframe: map[string]*image.RGBA{},
			sbase:  map[string]*image.RGBA{},
			sovl:   map[string]*image.RGBA{},
			body:   map[int]*sprite.Renderer{},
			box:    map[string]flyBoxRec{},
		}
	})
	return vecFlyInst
}

var vecFlyDirName = map[int]string{
	1: "down", 2: "leftdown", 3: "left", 4: "leftup",
	5: "up", 6: "rightup", 7: "right", 8: "rightdown",
}

func (v *vecFlySet) renderer(style int) *sprite.Renderer {
	v.mu.Lock()
	defer v.mu.Unlock()
	if r, ok := v.cache[style]; ok {
		return r
	}
	dir := filepath.Join(assetsRoot(), "sprites", "fly"+itoa(style))
	sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
	if err != nil {
		v.cache[style] = nil
		return nil
	}
	lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
	if err != nil {
		v.cache[style] = nil
		return nil
	}
	r := sprite.New(sp, lib)
	// colorMc 是飞行器的实色底板层：素材原色 4 个 style 各不同（灰/青绿/青绿/青绿）。
	// 官方 NonoFlyModel 对它**无条件** FillColor(_info.color)，等于 AS3 的
	// ColorTransform.color：RGB 全刷成玩家色、alpha 保留，玩家色为白时也刷成白。
	// 之前走乘法染色，白玩家色 = 不染，于是飞行器保留青绿、本体是白，
	// 「飞行模式配色和非飞行模式不统一」（子渊 2026-10-24 / 10-27 两次反馈）。
	r.SetReplaceTint("colorMc")
	v.cache[style] = r
	// 主体 bbox 专用渲染器：层开关在创建时定死，之后不再改动
	if br, berr := newTileRenderer(dir); berr == nil {
		br.SetOnlyLayers("colorMc", "dirMc")
		v.body[style] = br
	}
	return r
}

// newTileRenderer 新建一套飞行器渲染器（不动全局缓存）。
func newTileRenderer(dir string) (*sprite.Renderer, error) {
	sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
	if err != nil {
		return nil, err
	}
	lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
	if err != nil {
		return nil, err
	}
	return sprite.New(sp, lib), nil
}

// bodyBox 返回飞行器「主体」（colorMc+dirMc，不含 bgMc 接地阴影和 fireMc 尾焰）
// 在整帧内的 bbox，单位=屏幕像素，相对整帧左上。
//
// 环心必须用它：整帧 alpha bbox 会被下方阴影和尾焰拉长，实测让环心下沉
// 40~55px，表现就是「图标圈不以飞行器为中心、下边很远」（子渊 2026-10-27 三次反馈）。
func (v *vecFlySet) bodyBox(style, dir, targetH int) (image.Rectangle, image.Point, bool) {
	name, ok := vecFlyDirName[dir]
	if !ok {
		return image.Rectangle{}, image.Point{}, false
	}
	if style < 1 || style > 4 {
		style = 1
	}
	if targetH < 1 {
		targetH = 1
	}
	key := "box" + itoa(style) + "_" + name + "_" + itoa(targetH)
	v.mu.Lock()
	if b, hit := v.box[key]; hit {
		v.mu.Unlock()
		return b.R, b.C, b.R.Dx() > 0
	}
	br := v.body[style]
	clip, okc := vecFlyClip[style]
	full := flyOriginalFullH(style)
	v.mu.Unlock()
	if br == nil || !okc || full <= 0 {
		return image.Rectangle{}, image.Point{}, false
	}
	k := float64(targetH) / float64(full)
	img, err := br.RenderClip(name, 0, k, 2, clip)
	if err != nil || img == nil {
		return image.Rectangle{}, image.Point{}, false
	}
	r := img.Bounds()
	minX, minY, maxX, maxY := r.Max.X, r.Max.Y, r.Min.X, r.Min.Y
	// alpha 加权质心：机体上部（白亮、opaque）权重高，底部收口/光晕权重低，
	// 比 bbox 几何中心更贴「看起来的中间」（实测 bbox 中心仍偏低 15~20px，
	// 因为 dirMc 底部带一圈向下的光效）。
	var sumX, sumY, sumA float64
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			a := img.Pix[img.PixOffset(x, y)+3]
			// 阈值 160：把半透明的推进器光晕/辉光排除在范围外。用 8 时
			// 底部的向下光效会把 bbox 拖长 40px，环心随之沉下去
			// （子渊 2026-10-27「不是以飞行器为中心」）。
			if a > 160 {
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
				w := float64(a)
				sumX += float64(x) * w
				sumY += float64(y) * w
				sumA += w
			}
		}
	}
	box := flyBoxRec{}
	if maxX >= minX && maxY >= minY {
		box.R = image.Rect(minX, minY, maxX+1, maxY+1)
		box.C = image.Pt((minX+maxX)/2, (minY+maxY)/2)
		if sumA > 0 {
			box.C = image.Pt(int(sumX/sumA+0.5), int(sumY/sumA+0.5))
		}
	}
	v.mu.Lock()
	v.box[key] = box
	v.mu.Unlock()
	return box.R, box.C, box.R.Dx() > 0
}

// frames 返回该方向的一帧（原版 fireMc 停在首帧，所以只渲染 frame 0）。
// tint 是玩家色：colorMc 层声明为 tintable，只染它，fireMc/dirMc 保持原样。
func (v *vecFlySet) frames(style, dir, targetH int, tint uint32) []*image.RGBA {
	// 调试：NONO_FLY_DIR=<方向名> 锁定方向，便于逐方向截图/量测。
	if e := os.Getenv("NONO_FLY_DIR"); e != "" {
		for d, nm := range vecFlyDirName {
			if nm == e {
				dir = d
				break
			}
		}
	}
	name, ok := vecFlyDirName[dir]
	if !ok {
		return nil
	}
	if style < 1 || style > 4 {
		style = 1
	}
	r := v.renderer(style)
	if r == nil {
		logf("FrameAt: renderer 为空 style=%d", style)
		return nil
	}
	r.SetTint(tint)
	full := flyOriginalFullH(style)
	if full <= 0 {
		logf("FrameAt: fullH=0 style=%d", style)
		return nil
	}
	// 尾焰是否交给程序化渲染。素材缺失时退回「整帧渲染」（原行为）。
	procedural := smokeAvailable(style)
	logf("fly render style=%d dir=%s targetH=%d full=%v procedural=%v", style, name, targetH, full, procedural)
	n := 1
	if procedural {
		n = smokePhase
	}
	key := "fly" + itoa(style) + "_" + name + "_" + itoa(targetH) + "_" +
		itoa(int(tint&0xFFFFFF)) + "_" + itoa(n)
	v.mu.Lock()
	if list, hit := v.frame[key]; hit {
		v.mu.Unlock()
		return list
	}
	t0 := time.Now()
	// 换形态时清掉旧形态的帧组：4 形态 × 8 相位全留着约 40MB，实际只用当前形态。
	if v.curStyle != style {
		v.frame = map[string][]*image.RGBA{}
		v.curStyle = style
	}
	v.mu.Unlock()
	// 按原版整帧裁剪，而不是整画布：飞行器各帧 bbox 不同，按 bbox 出图会让
	// 窗口尺寸与内容相对位置每帧变化（飞行器在窗口里抖），边缘描边也会被切掉；
	// 而整画布又会切掉溢出的辉光。原版整帧的裁剪框正好两头都满足。
	if !procedural {
		// 没有程序化尾焰素材时也走 FrameAt：子时间轴（眼睛/光圈）一样要动，
		// 只是少了火焰层。单一路径也免得两处渲染口径分叉。
		fr := v.FrameAt(style, dir, targetH, tint, 0)
		if fr == nil {
			return nil
		}
		v.mu.Lock()
		v.frame[key] = []*image.RGBA{fr}
		v.mu.Unlock()
		return []*image.RGBA{fr}
	}
	// 程序化尾焰 + 子时间轴：统一交给 FrameAt 按真实毫秒合成。
	// 这里仍然产 12 张是为了沿用上层「预渲染帧列表 + Next 索引」的接口；
	// 相位 p 换算成 p×40ms，FrameAt 内部再让各子时间轴按自己的帧长取帧。
	list := make([]*image.RGBA, n)
	for p := 0; p < n; p++ {
		list[p] = v.FrameAt(style, dir, targetH, tint, int64(p)*40)
		if list[p] == nil {
			return nil
		}
	}
	v.mu.Lock()
	v.frame[key] = list
	v.mu.Unlock()
	if dp := os.Getenv("NONO_DUMP_FLY"); dp != "" {
		// 帧数据是**非预乘**语义（raster 全程用 OverNonPremul 合成），
		// 必须用 NRGBA 落盘：直接用 image.RGBA 会被 png.Encode 当成预乘再反解，
		// 白色 (255,a<255) 会算成 255*255/a 溢出回绕成灰色，看起来像火焰"黑心"。
		if f, err := os.Create(dp); err == nil {
			b := list[0].Bounds()
			n := image.NewNRGBA(b)
			copy(n.Pix, list[0].Pix)
			_ = png.Encode(f, n)
			f.Close()
		}
	}
	// NONO_DUMP_FLY_ALL=<前缀>：把本方向全部相位落盘（前缀 + 序号 + .png），
	// 用于验证尾焰动画（官方帧方案是 12 相位映射到 8 帧循环）。
	if dp := os.Getenv("NONO_DUMP_FLY_ALL"); dp != "" {
		for i, im := range list {
			f, err := os.Create(dp + itoa(i) + ".png")
			if err != nil {
				break
			}
			b := im.Bounds()
			n := image.NewNRGBA(b)
			copy(n.Pix, im.Pix)
			_ = png.Encode(f, n)
			f.Close()
		}
	}
	logf("fly frames ready %s %v", key, time.Since(t0))
	return list
}

// FrameAt 返回 ms 时刻的飞行器整帧：
// 背景层(bgMc) → 尾焰(若在机体下层) → 机体(colorMc+dirMc 静态部分)
// → 子时间轴(眼睛/光圈，按各自帧长取帧) → 尾焰(若在最上层)。
//
// 为什么不再用「12 张预渲染 + TickN 取模」：那 12 张只是尾焰的相位表。
// 眼睛 15 帧（600ms）、光圈 18 帧（720ms）与它对不齐，套进去要么静止
// 要么错位；Flash 里嵌套 MC 本来就是各跑各的时钟，这里按真实毫秒算。
func (v *vecFlySet) FrameAt(style, dir, targetH int, tint uint32, ms int64) *image.RGBA {
	name, ok := vecFlyDirName[dir]
	if !ok {
		return nil
	}
	if style < 1 || style > 4 {
		style = 1
	}
	r := v.renderer(style)
	if r == nil {
		logf("FrameAt: renderer 为空 style=%d", style)
		return nil
	}
	r.SetTint(tint)
	full := flyOriginalFullH(style)
	if full <= 0 {
		logf("FrameAt: fullH=0 style=%d", style)
		return nil
	}
	k := float64(targetH) / float64(full)
	clip, okc := vecFlyClip[style]
	if !okc {
		clip = vecFlyClip[1]
	}
	procedural := smokeAvailable(style)
	if procedural {
		clip[1] -= 14
		clip[3] += 14
		clip[2] += 12
	}
	base, overlay := v.staticLayers(style, name, targetH, k, clip, procedural, tint)
	if base == nil {
		logf("FrameAt: base 为空 style=%d dir=%s proc=%v", style, name, procedural)
		return nil
	}
	img := image.NewRGBA(base.Bounds())
	copy(img.Pix, base.Pix)
	sb, above := image.Rectangle{}, false
	if procedural {
		sb, above = v.flameAnchor(style, dir, name, targetH, clip, k)
	}
	ph := v.flamePhase(ms)
	if procedural && !above {
		drawSmoke(img, style, name, clip, k, sb, ph)
	}
	if overlay != nil && overlay.Bounds() == img.Bounds() {
		raster.OverNonPremul(img, overlay, 0, 0)
	}
	// 子时间轴：按官方 depth 升序叠在机体之上（眼睛 z5、光圈 z10）。
	if d, okd := r.S.Directions[name]; okd && len(d.Groups) > 0 {
		order := make([]int, len(d.Groups))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			return d.Groups[order[a]].Z < d.Groups[order[b]].Z
		})
		for _, gi := range order {
			gim := v.groupFrame(style, name, gi, targetH, k, clip, tint, ms)
			if gim != nil && gim.Bounds() == img.Bounds() {
				raster.OverNonPremul(img, gim, 0, 0)
			}
		}
	}
	if procedural && above {
		drawSmoke(img, style, name, clip, k, sb, ph)
	}
	return img
}

// staticLayers 渲染并缓存「背景层」和「机体静态层」。
// 机体的静态部分不含子时间轴（导入器已把它们从帧里剔除），所以这两张
// 与时间无关，只在切形态/改尺寸/改染色时失效。
func (v *vecFlySet) staticLayers(style int, name string, targetH int, k float64,
	clip [4]float64, procedural bool, tint uint32) (*image.RGBA, *image.RGBA) {
	key := "s" + itoa(style) + "_" + name + "_" + itoa(targetH) + "_" +
		itoa(int(tint&0xFFFFFF)) + "_" + boolStr(procedural)
	v.mu.Lock()
	base, ovl := v.sbase[key], v.sovl[key]
	v.mu.Unlock()
	if base != nil {
		return base, ovl
	}
	r := v.renderer(style)
	if r == nil {
		return nil, nil
	}
	// 背景层只留 bgMc（接地阴影在火焰之下）；机体层只留 colorMc+dirMc。
	r.SetSkipLayers("fireMc", "dirMc", "colorMc")
	b, err := r.RenderClip(name, 0, k, 2, clip)
	r.SetSkipLayers()
	if err != nil {
		logf("staticLayers: base 渲染失败 %v", err)
		return nil, nil
	}
	r.SetOnlyLayers("colorMc", "dirMc")
	o, oerr := r.RenderClip(name, 0, k, 2, clip)
	r.SetOnlyLayers()
	if oerr != nil {
		o = nil
	}
	v.mu.Lock()
	v.sbase[key] = b
	if o != nil {
		v.sovl[key] = o
	}
	v.mu.Unlock()
	return b, o
}

// groupFrame 渲染某子时间轴在 ms 时刻的帧。相位 = ms / 该组帧长 % 帧数，
// 所以 15 帧的眼睛和 18 帧的光圈会各自循环，不会互相同步或漂移。
func (v *vecFlySet) groupFrame(style int, name string, gi, targetH int, k float64,
	clip [4]float64, tint uint32, ms int64) *image.RGBA {
	r := v.renderer(style)
	if r == nil {
		return nil
	}
	d, ok := r.S.Directions[name]
	if !ok || gi < 0 || gi >= len(d.Groups) {
		return nil
	}
	g := d.Groups[gi]
	n := len(g.Frames)
	if n == 0 {
		return nil
	}
	msf := g.Ms
	if msf <= 0 {
		msf = 40
	}
	ph := int(ms/int64(msf)) % n
	if ph < 0 {
		ph += n
	}
	key := "g" + itoa(style) + "_" + name + "_" + itoa(targetH) + "_" +
		itoa(int(tint&0xFFFFFF)) + "_" + itoa(gi) + "_" + itoa(ph)
	v.mu.Lock()
	if im, hit := v.gframe[key]; hit {
		v.mu.Unlock()
		return im
	}
	v.mu.Unlock()
	im, err := r.RenderClipGroup(name, gi, ph, k, 2, clip)
	if err != nil || im == nil {
		return nil
	}
	v.mu.Lock()
	v.gframe[key] = im
	v.mu.Unlock()
	return im
}

// flamePhase 把毫秒换算成尾焰相位（12 张循环，沿用 20261028 官方帧方案）。
func (v *vecFlySet) flamePhase(ms int64) int {
	if smokePhase <= 0 {
		return 0
	}
	p := int(ms/40) % smokePhase
	if p < 0 {
		p += smokePhase
	}
	return p
}

// flameAnchor 算尾焰喷口（相对整帧左上）和火焰层级（是否压在机体最上层）。
func (v *vecFlySet) flameAnchor(style, dir int, name string, targetH int,
	clip [4]float64, k float64) (image.Rectangle, bool) {
	// 喷口锚在机体（dirMc）bbox 下缘中点 —— 也就是游戏里两块白色板中间
	// 那个黑色开口。不依赖原版 fireMc 的偏移表：实测每个方向的火层内容在
	// 图内位置都不同（含旋转/翻转），单一 off 会把烟顶到机体上方。
	var body image.Rectangle
	if b, _, okb := v.bodyBox(style, dir, targetH); okb && b.Dx() > 2 {
		body = b
	} else if a, oka := smokeAnchorFor(style, name); oka {
		x0 := int((a.X - clip[0]) * k)
		x1 := int((a.X + a.W - clip[0]) * k)
		y0 := int((a.Y - clip[1]) * k)
		body = image.Rect(x0, y0, x1, y0)
	} else {
		body = image.Rect(0, 0, 8, 0)
	}
	sb := bodyShift(body, 14, k)
	// 官方 32 组核验（20261028-02/plume_measure/zorder_rows.json）：29 组火焰
	// 在机体全部层之下，只有模式4 的 leftup/up/rightup 在最上层。
	above := false
	if fo := flameOffFor(style, name); fo != nil {
		above = fo.Above
	} else if fp, ok := flameParamFor(style, name); ok {
		above = fp.Above
	}
	return sb, above
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// dumpFlySeq 一次性落盘前 n 个相位（每 40ms 一张）的整帧，文件名
// <prefix><i>.png。用于逐帧核对子时间轴：眼睛应出现睁/半闭/闭三态，
// 光圈应从满不透明缩放到全透明（18 帧正好一圈）。
func dumpFlySeq(style, dir, targetH int, tint uint32, prefix string, n int) {
	v := vecFly()
	if v == nil || n <= 0 {
		return
	}
	for i := 0; i < n; i++ {
		im := v.FrameAt(style, dir, targetH, tint, int64(i)*40)
		if im == nil {
			logf("dumpFlySeq: FrameAt 返回 nil (i=%d)", i)
			return
		}
		f, err := os.Create(prefix + itoa(i) + ".png")
		if err != nil {
			logf("dumpFlySeq: 创建失败 %v", err)
			return
		}
		b := im.Bounds()
		nr := image.NewNRGBA(b)
		copy(nr.Pix, im.Pix)
		_ = png.Encode(f, nr)
		f.Close()
	}
}

// bodyShift 把机体 bbox 平移到"顶部扩过 pad 个画布单位"的新画布坐标系。
// bodyBox 内部用的是未扩展的裁剪框，而火焰要画在扩展后的画布上。
func bodyShift(b image.Rectangle, pad float64, k float64) image.Rectangle {
	if b.Dy() <= 0 {
		return b
	}
	dy := int(pad * k)
	return image.Rect(b.Min.X, b.Min.Y+dy, b.Max.X, b.Max.Y+dy)
}
