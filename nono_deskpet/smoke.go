//go:build windows

package main

import (
	"encoding/json"
	"image"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// 程序化尾焰（取代矢量/位图的 fireMc 层）
//
// 历史教训（2026-10-27）：
//   第一版把原版 fireMc **整层贴图**按 anchors bbox 整块贴上去，只做 ±0.4px
//   平移 + 3% 缩放 —— 视觉上就是「一整块烟雾在原地抖」，而且 style2/4 因为
//   metrics.json 的 fireMc.off 被误改（[0,0]），整块烟被顶到了机体上方。
//
//   现在改成「喷口 + 粒子喷流」：
//     · 喷口位置不查原版偏移表，直接取机体（dirMc）bbox 下缘中点 —— 也就是
//       游戏里两块白色板中间那个黑色开口；
//     · 粒子从喷口向下喷出，按年龄做位移/放大/淡出，形成真正的「喷出去」；
//     · 分三层：外烟（大、淡、慢）、内焰（小、亮、快）、喷口亮芯（加色）；
//     · 喷口亮芯画在 dirMc **之上**，形成「从开口里冒出来」的层次，
//       外烟/内焰画在 dirMc **之下**，被机体挡住根部。
//
//   贴图仍取原版 fireMc 单层（assets/sprites/smoke/s{style}_{dir}.png），
//   保留原版的絮状边缘与青色调，只是不再把整块当成一帧画面。
//
// 坐标系：body 是 RenderClip 输出的像素 bbox（与 dst 同坐标系、同 targetH），
// 所以喷口/粒子全部用像素算，不需要再乘 k。

type smokeAnchor struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

var (
	smokeOnce  sync.Once
	smokeAnch  map[string]map[string]smokeAnchor
	smokeTexes map[string]*image.RGBA
	smokeOK    bool
)

// smokePhase 是一圈喷流的相位数。40ms/tick × 12 = 480ms 一圈，
// 所有粒子的寿命都 ≤ 一圈（否则循环接不上，会出现「跳变」）。
const smokePhase = 12

// 三层粒子数。外烟同时存在 7 枚（年龄 0..6/7），近喷口的年轻、远的年老。
// 张数不能太多：贴图本身是一整团云，缩太小会连成一根「柱子」。
const (
	plumeSmokeN = 2 // 前后两团
	plumeFlameN = 1
	plumeCoreN  = 1
)

func smokeLoad() {
	smokeAnch = map[string]map[string]smokeAnchor{}
	smokeTexes = map[string]*image.RGBA{}
	dir := filepath.Join(assetsRoot(), "sprites", "smoke")
	f, err := os.Open(filepath.Join(dir, "anchors.json"))
	if err != nil {
		return
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&smokeAnch); err != nil {
		return
	}
	smokeOK = len(smokeAnch) > 0
}

// smokeTexture 懒加载某 style×方向的烟团贴图（约 173x203，140KB）。
func smokeTexture(style int, dirName string) *image.RGBA {
	key := itoa(style) + "_" + dirName
	if t, ok := smokeTexes[key]; ok {
		return t
	}
	p := "sprites/smoke/s" + key + ".png"
	img, err := decodePNG(p)
	if err != nil {
		smokeTexes[key] = nil
		return nil
	}
	img = boostAlpha(trimAlphaBox(img))
	smokeTexes[key] = img
	return img
}

// trimAlpha 裁掉四周透明边：原版火层图里大量边距是空的，
// 不裁的话缩放后实心部分只占一小块，火焰看起来又小又淡。
func trimAlphaBox(src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := src.Pix[src.PixOffset(b.Min.X, y):]
		for x := b.Min.X; x < b.Max.X; x++ {
			if row[(x-b.Min.X)*4+3] > 8 {
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
	if minX > maxX || minY > maxY {
		return src
	}
	r := image.Rect(minX, minY, maxX+1, maxY+1)
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		copy(out.Pix[y*out.Stride:(y+1)*out.Stride], src.Pix[src.PixOffset(r.Min.X, r.Min.Y+y):src.PixOffset(r.Max.X, r.Min.Y+y)])
	}
	return out
}

// smokeTextureFit 把 4x 原图预先缩放到最终屏幕尺寸（Lanczos），
// 避免运行时从大图双线性缩小 —— 那会把火焰揉成一团白斑。
type smokeFitImg struct {
	img    *image.RGBA
	cx, cy float64 // alpha 加权质心（0~1），用来把视觉中心对准喷口
}

var smokeFit = map[string]smokeFitImg{}

func smokeTextureFit(key string, src *image.RGBA, w, h int) (smokeFitImg, bool) {
	key = key + "_" + itoa(w) + "x" + itoa(h)
	if t, ok := smokeFit[key]; ok {
		return t, true
	}
	if src == nil {
		return smokeFitImg{}, false
	}
	if w < 2 {
		w = 2
	}
	if h < 2 {
		h = 2
	}
	t := resizeFrame(src, w, h)
	// alpha 加权质心：原版火层内容在贴图里不是居中（8 方向各偏一边），
	// 直接用 bbox 中心贴会「歪在机体一边」。
	var sx, sy, sa float64
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := float64(t.Pix[y*t.Stride+x*4+3]) / 255
			sx += float64(x) * a
			sy += float64(y) * a
			sa += a
		}
	}
	f := smokeFitImg{img: t, cx: 0.5, cy: 0.5}
	if sa > 0 {
		f.cx = sx / sa / float64(w)
		f.cy = sy / sa / float64(h)
	}
	smokeFit[key] = f
	return f, true
}

// boostAlpha 提高贴图的 alpha 对比度：原版火焰边上有一大圈极淡的雾，
// 缩到桌面尺寸（30~50px）后整团糊成白斑。压掉 <24 的雾、把主体拉满，
// 轮廓才立得住（颜色值不动，只改透明度）。
func boostAlpha(src *image.RGBA) *image.RGBA {
	for i := 3; i < len(src.Pix); i += 4 {
		a := float64(src.Pix[i])
		if a <= 12 {
			src.Pix[i] = 0
			continue
		}
		// gamma 提升中段：边缘那圈青色要留住（原版火焰外沿是青的），
		// 只把过淡的雾拉起来，不能把整团压成白核。
		v := math.Pow(a/255, 0.45) * 255
		if v > 255 {
			v = 255
		}
		src.Pix[i] = uint8(v)
	}
	return src
}

// smokeFrame 取原版第 n 帧（1-based）。assets/sprites/smoke/s{st}_{dir}_{n}.png
// 是从 assets/fly/f_{st}_{dir}_{n}.png 预缩到 2x 的逐帧图 —— 原版尾焰本来就是
// 8 帧动画，静态单帧再怎么抖都不像，直接播原版帧最还原。
func smokeFrame(style int, dirName string, n int) *image.RGBA {
	if n < 1 {
		n = 1
	}
	key := itoa(style) + "_" + dirName + "_" + itoa(n)
	if t, ok := smokeTexes[key]; ok {
		return t
	}
	img, err := decodePNG("sprites/smoke/s" + key + ".png")
	if err != nil {
		img = smokeTexture(style, dirName)
		if img == nil {
			return nil
		}
	} else {
		img = boostAlpha(trimAlphaBox(img))
	}
	smokeTexes[key] = img
	return img
}

func smokeFrames(style int, dirName string) int {
	n := 0
	for n < 8 {
		if _, err := os.Stat(filepath.Join(assetsRoot(), "sprites", "smoke",
			"s"+itoa(style)+"_"+dirName+"_"+itoa(n+1)+".png")); err != nil {
			break
		}
		n++
	}
	if n == 0 {
		return 1
	}
	return n
}

func smokeAvailable(style int) bool {
	// 官方帧素材（assets/fly/flame/official.json）优先：它才是现在的尾焰来源，
	// 旧 anchors.json 只是回退链路。两者都缺才返回 false（退化为单帧整帧渲染）。
	if flameOfficialAvail(style) {
		return true
	}
	smokeOnce.Do(smokeLoad)
	if !smokeOK {
		return false
	}
	_, ok := smokeAnch[itoa(style)]
	return ok
}

// smokeAnchorFor 返回该 style×方向的尾焰 bbox（画布单位）。
// 现在只用于「机体 bbox 算不出来」时的兜底落点，正常路径不再依赖它。
func smokeAnchorFor(style int, dirName string) (smokeAnchor, bool) {
	m := smokeAnch[itoa(style)]
	if m == nil {
		return smokeAnchor{}, false
	}
	if a, ok := m[dirName]; ok && a.W > 0 && a.H > 0 {
		return a, true
	}
	if a, ok := m["down"]; ok && a.W > 0 && a.H > 0 {
		return a, true
	}
	return smokeAnchor{}, false
}

// plumeNozzle 计算喷口像素坐标与基础尺寸。
// body 是机体 bbox（dirMc，含 alpha>160 的实体部分），坐标系与 dst 相同。
func plumeNozzle(body image.Rectangle) (bx, by, base float64) {
	bx = float64(body.Min.X+body.Max.X) / 2
	// 向上内缩 1px：喷口在机体下缘内侧，烟从缝里出来。
	by = float64(body.Max.Y) - 1
	base = float64(body.Dx()) * 0.80
	if base < 8 {
		base = 8
	}
	return
}

// drawSmoke 画尾焰，位置/形状全部取官方每方向的 fire 层（anchors.json +
// assets/sprites/smoke/s{style}_{dir}.png）。
//
// 为什么改成这样（2026-10-27 用户第三次对比官方）：
//
//	· 之前用「机体 bbox 下缘」当喷口，等于把所有方向都当成朝下飞，
//	  left/up 等方向火焰会跑到机体侧上方；
//	· 原版 8 个方向的火层贴图与位置都不同（down 在机体下缘、left 在右下，
//	  up 被画布裁掉），只能按官方数据逐方向放；
//	· 位置基准 = anchors（画布坐标），喷射方向 = 机体中心指向火层中心。
//
// 叠法：先画「后团」（沿喷射方向外移、稍大稍淡），再画「前团」（官方位置、
// 原尺寸、接近不透明）。两团都是原色，不做提亮/去色。
// flameGeoRec 是 assets/fly/flame_geo.json 里单个「形态 × 方向」的记录。
// 全部由 SWF 显示列表的矩阵与 shape 几何算出
// （见 变更工程/当前改动/20261027-24/尾焰确认表.md）。
type flameGeoRec struct {
	Has bool    `json:"has"`
	Ndx float64 `json:"ndx"` // (火焰中心 − 机体中心) / 机体宽
	Ndy float64 `json:"ndy"` // (火焰中心 − 机体中心) / 机体高
	Nw  float64 `json:"nw"`  // 火焰宽 / 机体宽
	Nh  float64 `json:"nh"`  // 火焰高 / 机体宽
	Rot float64 `json:"rot"` // 官方矩阵旋转角（度）；贴图本身已按方向画好，仅作诊断
}

var (
	flameGeoOnce sync.Once
	flameGeo     map[string]map[string]flameGeoRec
)

// flameGeoFor 取某形态某方向的官方尾焰几何；nil 表示数据缺失。
func flameGeoFor(style int, dirName string) *flameGeoRec {
	flameGeoOnce.Do(func() {
		flameGeo = map[string]map[string]flameGeoRec{}
		b, err := os.ReadFile(filepath.Join(assetsRoot(), "fly", "flame_geo.json"))
		if err != nil {
			logf("flame_geo 读取失败: %v", err)
			return
		}
		if err := json.Unmarshal(b, &flameGeo); err != nil {
			logf("flame_geo 解析失败: %v", err)
		}
	})
	m := flameGeo[itoa(style)]
	if m == nil {
		return nil
	}
	r, ok := m[dirName]
	if !ok {
		return nil
	}
	return &r
}

// flamePlace 是官方的尾焰摆放口径，全部量自 FFDec 导出的**完整帧**
// （sprite52_/85_/30_item，即 SWF 自己渲染出来的那张图）。
//
//	below=true  → 尾焰在机体下沿、向下喷（style1 / style3）
//	below=false → 尾焰在机体上沿、向上喷（style2 / style4）
//
// wRatio/hRatio 是尾焰 bbox 相对机体 bbox 的比例，gap 是实测的贴边间隙。
// 这些数字没有一个是估算的，来源见 变更工程/当前改动/20261027-24/尾焰确认表.md。
type flamePlace struct {
	below  bool
	wRatio float64
	hRatio float64
}

var flamePlaces = map[int]flamePlace{
	1: {true, 0.70, 0.43},
	2: {false, 0.61, 0.65},
	3: {true, 0.69, 0.32},
	4: {false, 0.42, 0.32},
}

// flameParamRec 是官方 32 组量测（assets/fly/flame_params.json，2026-10-28）。
// 与 flameGeoRec 的区别：这里给的是**像素口径**（1x 画布单位），直接可用来落位，
// 且 Z 序（Above）与逐方向的角度是官方真实值。
//
// 为什么废弃 flame_geo.json 的 ndx/ndy：那是用「机体 bbox 中心」归一化的旧量测，
// style1/style3 的火焰其实是固定不转的，归一化后 ndx 在 -0.23~+0.86 之间乱跳；
// style4 的 leftup/up/rightup 还被判成 has=false（火焰在 dirMc 层，漏查）。
type flameParamRec struct {
	Cdx   float64 `json:"cdx"`   // 火焰 bbox 中心 − 机体 bbox 中心（1x 画布单位）
	Cdy   float64 `json:"cdy"`   // 同上，y 向下为正
	Fw    float64 `json:"fw"`    // 火焰 bbox 宽（1x）
	Fh    float64 `json:"fh"`    // 火焰 bbox 高（1x）
	Angle float64 `json:"angle"` // 喷向（屏幕角度：0=右、90=下）
	Above bool    `json:"above"` // true=画在机体之上（官方只有模式4 的左上/上/右上）
}

var (
	flameParamOnce sync.Once
	flameParams    map[string]map[string]flameParamRec
)

// flameParamFor 取官方尾焰落位参数；nil 表示数据缺失（调用方回退旧口径）。
func flameParamFor(style int, dirName string) (*flameParamRec, bool) {
	flameParamOnce.Do(func() {
		flameParams = map[string]map[string]flameParamRec{}
		b, err := os.ReadFile(filepath.Join(assetsRoot(), "fly", "flame_params.json"))
		if err != nil {
			logf("flame_params 读取失败: %v", err)
			return
		}
		if err := json.Unmarshal(b, &flameParams); err != nil {
			logf("flame_params 解析失败: %v", err)
		}
	})
	m := flameParams[itoa(style)]
	if m == nil {
		return nil, false
	}
	r, ok := m[dirName]
	if !ok {
		return nil, false
	}
	return &r, true
}

// drawFlameParam 纯程序化尾焰（不读任何 PNG）。
//
// 形态照着官方 fireMc 的实测结构（放大 3x 看 mode2_down）：
//
//	根部   1 个大的实心白团，压在喷口上；
//	中部   1 个略偏斜的大团，和根部咬合；
//	尾部   3 个**互相分开的小球**，越远越小越淡，并且横向散开（扇形）；
//	整片外面还有一层青色柔光。
//
// 不能做成"等距递减的圆串"——那样就是一根柱子（用户原话「现在像一个柱子」）。
func drawFlameParam(dst *image.RGBA, cx, cy, fw, fh, angDeg float64, phase int) {
	L := math.Max(fw, fh)  // 沿喷向的总长
	Wd := math.Min(fw, fh) // 垂直喷向的宽
	if L < 2 || Wd < 2 {
		return
	}
	a := angDeg * math.Pi / 180
	dx, dy := math.Cos(a), math.Sin(a)
	t := float64(phase) / float64(smokePhase) * 2 * math.Pi
	// 局部坐标：dist 沿喷向（正=离开机体），lat 垂直喷向。
	blob := func(dist, lat, r, strength float64) {
		px := cx + dx*dist - dy*lat
		py := cy + dy*dist + dx*lat
		drawFlameBlob(dst, px, py, r, r, strength)
	}

	// ① 青色柔光底：比焰体大一圈，弱强度，负责"雾感"和尾部扩散范围。
	blob(L*0.06, 0, Wd*0.88, 0.15)
	blob(L*0.36, 0, Wd*0.72, 0.10)

	// ② 根部实心团：压住喷口，是整片焰里最亮的一块。
	blob(-L*0.30, 0, Wd*0.62, 0.95)
	// ③ 中部大团：略向一侧偏（官方不是严格轴对称），和根部咬合。
	blob(L*0.02, -Wd*0.08, Wd*0.56, 0.85)

	// ④ 尾部散球：三个独立小球，越远越小、越淡、横向张得越开。
	//    lat 正负交错是"散开"而不是"柱状收尖"的关键。
	for i, o := range [][3]float64{
		{0.26, -0.52, 0.36},
		{0.40, 0.56, 0.28},
		{0.50, -0.72, 0.22},
	} {
		sway := math.Sin(t+float64(i)*1.7) * 0.10 // 轻微摆动，相位循环
		blob(o[0]*L, (o[1]+sway)*Wd, Wd*o[2], 0.90-0.18*float64(i))
	}

	// ⑤ 喷口亮芯：落在根部（沿喷向反推 0.32L），小而亮。
	blob(-L*0.32, 0, Wd*0.22, 0.85)
}

// flameGain 是显示放大系数。官方尾焰在 167px 的画布上只有 28x22 像素，
// 桌宠窗口里小到看不出形状；按官方比例放大后仍以机体边缘为锚点，不会脱离机体。
const flameGain = 1.7

// drawSmoke 按官方摆放口径画尾焰：紧贴机体尾部、水平居中。
//
// 为什么不是查 sprite.json 的坐标：那份数据只展平了 sprite 内部矩阵，
// 没有父级补偿，各方向的机体位置会横向漂移 60~130 单位，用它算相对位置
// 会把火焰甩到机体外面去（2026-10-27 实测）。完整帧才是可信口径。
func drawSmoke(dst *image.RGBA, style int, dirName string, clip [4]float64, k float64, body image.Rectangle, phase int) {
	_ = clip
	// 官方帧优先（2026-10-28 子渊定稿）：四模式尾焰一律贴 SWF 逐阶段导出的
	// 官方帧（模式1 用 sprite12 的 shape 8/9/10/11，模式2/3/4 共用 sprite15 的
	// shape 11/12/13/14），落位仍是「机体 bbox 中心 + 官方矩阵算出的 (cdx,cdy)」。
	// 素材缺失（official.json 或 PNG 不在）才回退下面的程序化路线。
	if drawFlameOfficial(dst, style, dirName, k, body, phase) {
		return
	}
	// 官方参数（20261028-03 量测）：位置 = 机体 bbox 中心 + (cdx,cdy)·k，
	// 尺寸 = 官方火焰 bbox × k，方向 = 官方喷向。
	// 全程程序化绘制，不读 f_*.png；缺数据才回退下面的旧贴图逻辑。
	if p, ok := flameParamFor(style, dirName); ok && smokeUseVecFlame() {
		if body.Dx() < 8 || body.Dy() < 8 {
			return
		}
		cx := float64(body.Min.X+body.Max.X)/2 + p.Cdx*k
		cy := float64(body.Min.Y+body.Max.Y)/2 + p.Cdy*k
		// 尺寸用官方 bbox（1x）× k，不再乘 flameGain：flameGain=1.7 是给旧贴图
		// 路线（wRatio 相对机体、不含 gain）用的系数；这条路线 Fw/Fh 已是绝对
		// 像素口径，再乘一次会把火焰放大到比机体还宽（实测 90px vs 机体 71px）。
		fw := p.Fw * k
		fh := p.Fh * k
		// 喷向用「机体中心 → 火焰中心」的方向，不用 flame_params 里的 PCA 角。
		// 官方火焰是模糊团状，PCA 主轴在模式2 上系统性偏 30~45°（left 30.4°、
		// right 149.6°），而中心偏移方向与机体轴向的偏差 ≤5.6°；照 PCA 画会出现
		// 「火焰中心朝一个方向、团块朝另一个方向」，视觉上就是用户说的折线。
		// 中心偏移方向与位置天然共线，所以直接取它。
		ang := math.Atan2(p.Cdy, p.Cdx) * 180 / math.Pi
		drawFlameParam(dst, cx, cy, fw, fh, ang, phase)
		logf("flame param style=%d dir=%s c=(%.1f,%.1f) size=%.1fx%.1f ang=%.1f(pca %.1f) above=%v",
			style, dirName, cx, cy, fw, fh, ang, p.Angle, p.Above)
		return
	}
	pl, ok := flamePlaces[style]
	if !ok {
		pl = flamePlaces[1]
	}
	tex := smokeLayerTex(style, dirName)
	if tex == nil {
		return
	}
	if body.Dx() < 8 || body.Dy() < 8 {
		return
	}
	b := tex.Bounds()
	if b.Dx() < 2 || b.Dy() < 2 {
		return
	}
	bw, bh := float64(body.Dx()), float64(body.Dy())
	w := bw * pl.wRatio * flameGain
	h := bh * pl.hRatio * flameGain
	// 贴图本身的长宽比可能和实测 bbox 略有出入，取更接近官方比例的那个做约束，
	// 避免火焰被拉成一条。
	want := pl.wRatio / pl.hRatio
	have := float64(b.Dx()) / float64(b.Dy())
	if have > want*1.35 {
		h = w / want
	} else if have < want/1.35 {
		w = h * want
	}
	cx := float64(body.Min.X+body.Max.X) / 2
	// 根部压进机体 28%：官方实测尾焰与机体 bbox 只差 1px，但我们的 body 是
	// 渲染后的 alpha bbox（含机身高光），1px 间隙在屏幕上会看成断开，
	// 所以让火焰根部咬进机身，视觉上才是「从喷口冒出来」。
	const bite = 0.28
	x := cx - w/2
	var y float64
	if pl.below {
		y = float64(body.Max.Y) - h*bite
	} else {
		y = float64(body.Min.Y) - h*(1-bite)
	}
	// 越界就等比压进画布，别让整团火焰消失。
	W, H := float64(dst.Bounds().Dx()), float64(dst.Bounds().Dy())
	if y < 0 {
		if y+h < 6 {
			return
		}
		rs := (h + y) / h
		h *= rs
		w *= rs
		x = cx - w/2
		y = 0
	}
	if y+h > H {
		if H-y < 6 {
			return
		}
		rs := (H - y) / h
		h *= rs
		w *= rs
		x = cx - w/2
	}
	if x < 0 {
		if x+w < 6 {
			return
		}
		rs := (w + x) / w
		w *= rs
		h *= rs
		x = 0
	}
	if x+w > W {
		if W-x < 6 {
			return
		}
		rs := (W - x) / w
		w *= rs
		h *= rs
	}
	logf("fire style=%d dir=%s below=%v body=%v -> x=%.1f y=%.1f w=%.1f h=%.1f",
		style, dirName, pl.below, body, x, y, w, h)
	drawScaledEx(dst, tex, x, y, w, h, 1.0, 0, false)
}

// fireLayerOff 取 metrics.json 里 fireMc 的 4x 偏移（相对整帧左上角）。
func fireLayerOff(style int) []int {
	if m, ok := flyMetricsV2[style]; ok {
		if l, ok := m.Layers["fireMc"]; ok && len(l.Off) >= 2 {
			return l.Off
		}
	}
	return []int{0, 0}
}

// smokeLayerTex 直接读官方 fireMc 层原图（4x，不裁剪）：
// assets/fly/f{style}_{dir}_1.png。
var smokeLayerCache = map[string]*image.RGBA{}

func smokeLayerTex(style int, dirName string) *image.RGBA {
	key := itoa(style) + "_" + dirName
	if t, ok := smokeLayerCache[key]; ok {
		return t
	}
	p := filepath.Join("fly", "f_"+itoa(style)+"_"+dirName+"_1.png")
	img, err := decodePNG(p)
	if err != nil {
		smokeLayerCache[key] = nil
		return nil
	}
	// 官方 fireMc 是一层非常淡的青雾（alpha 峰值只有 ~204，均值 ~100），
	// 直接贴到深色桌面上会读成"白烟"。这里按 alpha 重新上色：
	// 外沿 #39DCDC、核心 #DFFFFF，并把 alpha 抬到 1.35 倍（夹 255），
	// 让它在 250px 的窗口里读成蓝色云团而不是白雾。
	tintFlame(img)
	if c := trimAlphaBox(img); c != nil {
		img = c
	}
	smokeLayerCache[key] = img
	return img
}

// tintFlame 按 alpha 给火焰层上色（就地修改）。
// 参数范围：img 任意尺寸 RGBA；返回值无。alpha==0 的像素保持全透明。
func tintFlame(img *image.RGBA) {
	if img == nil {
		return
	}
	const (
		edgeR, edgeG, edgeB = 18, 170, 205  // 外沿偏蓝
		coreR, coreG, coreB = 120, 240, 250 // 核心青白
		alphaGain           = 1.9
	)
	for i := 0; i < len(img.Pix); i += 4 {
		a := img.Pix[i+3]
		if a == 0 {
			continue
		}
		t := float64(a) / 255.0
		img.Pix[i] = uint8(float64(edgeR) + (coreR-edgeR)*t)
		img.Pix[i+1] = uint8(float64(edgeG) + (coreG-edgeG)*t)
		img.Pix[i+2] = uint8(float64(edgeB) + (coreB-edgeB)*t)
		na := float64(a) * alphaGain
		if na > 255 {
			na = 255
		}
		img.Pix[i+3] = uint8(na)
	}
}

// smokeUseVecFlame 默认用程序化团状火焰：官方 fireMc 层在任何缩放比下都是
// 一团模糊青雾，桌宠尺寸（机体 ~90px）放大后更像烟。程序化版用官方取色
// （外沿 #39DCDC、核心 #DFFFFF）画出有轮廓的团。NONO_FLAME_TEX=1 回退贴图版。
func smokeUseVecFlame() bool {
	return os.Getenv("NONO_FLAME_TEX") != "1"
}

// drawVecFlame 画团状尾焰：3 个高斯团沿喷射方向排开，越远越小越淡，
// 颜色从核心白青过渡到外沿青，add 混合出「发光」感。
func drawVecFlame(dst *image.RGBA, acx, acy, aw, ah, dx, dy float64, phase int) {
	t := float64(phase) / smokePhase * 2 * math.Pi
	// 团块沿方向排布：第一团贴喷口（最大最亮），后面两团是飘出去的火尾。
	for i := 0; i < 1; i++ {
		f := 0.0
		jit := math.Sin(t+float64(i)*2.1) * ah * 0.05
		cx := acx + dx*(ah*0.14+ah*0.56*f) + (-dy)*jit
		cy := acy + dy*(ah*0.14+ah*0.56*f) + dx*jit
		// 长轴沿喷射方向：上下喷时火焰是竖长的，左右喷时是横长的。
		long := math.Max(aw, ah) * 1.45
		short := math.Max(aw, ah) * 0.80
		rw, rh := long, short
		if math.Abs(dy) >= math.Abs(dx) {
			rw, rh = short, long
		}
		if rw < 2 || rh < 2 {
			continue
		}
		strength := 1.0 - 0.30*f
		drawFlameBlob(dst, cx, cy, rw, rh, strength)
	}
}

// drawFlameBlob 一个柔和椭圆团：alpha = (1-d)^1.5，颜色按距离从
// 白青 (223,250,250) 过渡到青 (57,220,220)，用加色混合产生发光感。
func drawFlameBlob(dst *image.RGBA, cx, cy, rw, rh, strength float64) {
	db := dst.Bounds()
	x0 := int(cx - rw)
	x1 := int(cx + rw + 1)
	y0 := int(cy - rh)
	y1 := int(cy + rh + 1)
	if x0 < db.Min.X {
		x0 = db.Min.X
	}
	if y0 < db.Min.Y {
		y0 = db.Min.Y
	}
	if x1 > db.Max.X {
		x1 = db.Max.X
	}
	if y1 > db.Max.Y {
		y1 = db.Max.Y
	}
	for py := y0; py < y1; py++ {
		ny := (float64(py) + 0.5 - cy) / rh
		for px := x0; px < x1; px++ {
			nx := (float64(px) + 0.5 - cx) / rw
			d := math.Sqrt(nx*nx + ny*ny)
			if d >= 1 {
				continue
			}
			// 官方雾团是「实心 + 柔边」，不是高斯衰减：d<0.45 满强度，
			// 之后用 smoothstep 收到 0，否则团会缩成一个亮核，外圈看不见。
			t := (1 - d) / 0.55
			if t > 1 {
				t = 1
			}
			if t < 0 {
				t = 0
			}
			f := strength * t * t * (3 - 2*t)
			if f <= 0.004 {
				continue
			}
			kf := math.Min(1, f*1.25)
			r := 150 + (255-150)*kf
			g := 235 + (255-235)*kf
			b := 235 + (255-235)*kf
			di := dst.PixOffset(px, py)
			dst.Pix[di+0] = clamp8(float64(dst.Pix[di+0]) + r*f*0.92)
			dst.Pix[di+1] = clamp8(float64(dst.Pix[di+1]) + g*f*0.92)
			dst.Pix[di+2] = clamp8(float64(dst.Pix[di+2]) + b*f*0.92)
			if na := float64(dst.Pix[di+3])/255 + f*(1-float64(dst.Pix[di+3])/255); na > 0 {
				dst.Pix[di+3] = clamp8(na * 255)
			}
		}
	}
}

// drawTexFlame 是贴图版（NONO_FLAME_TEX=1），保留作对照。
func drawTexFlame(dst *image.RGBA, style int, dirName string, acx, acy float64, phase int) {
	nf := smokeFrames(style, dirName)
	tex := smokeFrame(style, dirName, phase%nf+1)
	if tex == nil {
		return
	}
	tb := tex.Bounds()
	w := float64(tb.Dx())
	h := float64(tb.Dy())
	drawScaledEx(dst, tex, acx-w/2, acy-h/2, w, h, 1.0, 0, false)
}

// drawSmokeCore 已废弃：官方没有额外的喷口亮芯层（之前那层会白化火焰）。
// 保留空实现，调用点先留着，后续删。
func drawSmokeCore(dst *image.RGBA, style int, dirName string, body image.Rectangle, phase int) {
}

// drawScaledEx 把 tex 双线性缩放绘制到 dst 的 (x,y,w,h) 矩形（像素坐标）。
//
//	mixWhite: 0=原色，1=纯白（内焰/亮芯提亮）
//	add:      true 用加色混合（发光），false 用非预乘 src-over
//
// 性能：贴图 173x203，单次最大绘制约 base*2.9 ≈ 120px 见方；
// 每相位 20 次 ≈ 0.3M 像素运算，12 相位一次性约 20~50ms，进缓存后稳态 0。
func drawScaledEx(dst *image.RGBA, tex *image.RGBA, x, y, w, h, alpha, mixWhite float64, add bool) {
	if alpha <= 0 {
		return
	}
	if alpha > 1 {
		alpha = 1
	}
	if mixWhite < 0 {
		mixWhite = 0
	}
	if mixWhite > 1 {
		mixWhite = 1
	}
	db := dst.Bounds()
	tb := tex.Bounds()
	tw, th := tb.Dx(), tb.Dy()
	if tw < 1 || th < 1 || w < 1 || h < 1 {
		return
	}
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	x1, y1 := int(math.Ceil(x+w)), int(math.Ceil(y+h))
	if x0 < db.Min.X {
		x0 = db.Min.X
	}
	if y0 < db.Min.Y {
		y0 = db.Min.Y
	}
	if x1 > db.Max.X {
		x1 = db.Max.X
	}
	if y1 > db.Max.Y {
		y1 = db.Max.Y
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}
	sxStep := float64(tw) / w
	syStep := float64(th) / h
	for py := y0; py < y1; py++ {
		fy := (float64(py)-y+0.5)*syStep - 0.5
		iy := int(math.Floor(fy))
		wy := fy - float64(iy)
		if iy < 0 {
			iy, wy = 0, 0
		}
		if iy > th-1 {
			iy, wy = th-1, 0
		}
		iy2 := iy + 1
		if iy2 > th-1 {
			iy2, wy = iy, 0
		}
		row0 := tex.Pix[iy*tex.Stride:]
		row1 := tex.Pix[iy2*tex.Stride:]
		di := dst.PixOffset(x0, py)
		for px := x0; px < x1; px++ {
			fx := (float64(px)-x+0.5)*sxStep - 0.5
			ix := int(math.Floor(fx))
			wx := fx - float64(ix)
			if ix < 0 {
				ix, wx = 0, 0
			}
			if ix > tw-1 {
				ix, wx = tw-1, 0
			}
			ix2 := ix + 1
			if ix2 > tw-1 {
				ix2, wx = ix, 0
			}
			o0 := ix * 4
			o1 := ix2 * 4
			p00 := row0[o0:]
			p01 := row0[o1:]
			p10 := row1[o0:]
			p11 := row1[o1:]
			w00 := (1 - wx) * (1 - wy)
			w01 := wx * (1 - wy)
			w10 := (1 - wx) * wy
			w11 := wx * wy
			sr := float64(p00[0])*w00 + float64(p01[0])*w01 + float64(p10[0])*w10 + float64(p11[0])*w11
			sg := float64(p00[1])*w00 + float64(p01[1])*w01 + float64(p10[1])*w10 + float64(p11[1])*w11
			sb := float64(p00[2])*w00 + float64(p01[2])*w01 + float64(p10[2])*w10 + float64(p11[2])*w11
			sa := float64(p00[3])*w00 + float64(p01[3])*w01 + float64(p10[3])*w10 + float64(p11[3])*w11
			a := sa / 255 * alpha
			if a <= 0.002 {
				di += 4
				continue
			}
			if mixWhite > 0 {
				sr += (255 - sr) * mixWhite
				sg += (255 - sg) * mixWhite
				sb += (255 - sb) * mixWhite
			} else if mixWhite < 0 {
				// 负值 = 往青色偏：压红通道，蓝绿保持。
				k := -mixWhite
				if k > 1 {
					k = 1
				}
				sr *= 1 - 0.40*k
				sg *= 1 + 0.06*k
			}
			if add {
				// 加色：非预乘贴图直接加到目标色上，alpha 取较大者。
				dst.Pix[di+0] = clamp8(float64(dst.Pix[di+0]) + sr*a)
				dst.Pix[di+1] = clamp8(float64(dst.Pix[di+1]) + sg*a)
				dst.Pix[di+2] = clamp8(float64(dst.Pix[di+2]) + sb*a)
				if na := float64(dst.Pix[di+3])/255 + a*(1-float64(dst.Pix[di+3])/255); na > 0 {
					dst.Pix[di+3] = clamp8(na * 255)
				}
				di += 4
				continue
			}
			da := float64(dst.Pix[di+3]) / 255
			oa := a + da*(1-a)
			if oa <= 0 {
				di += 4
				continue
			}
			// 非预乘 src-over（raster 输出 Pix 存非预乘值）。
			k2 := da * (1 - a)
			dst.Pix[di+0] = clamp8((sr*a + float64(dst.Pix[di+0])*k2) / oa)
			dst.Pix[di+1] = clamp8((sg*a + float64(dst.Pix[di+1])*k2) / oa)
			dst.Pix[di+2] = clamp8((sb*a + float64(dst.Pix[di+2])*k2) / oa)
			dst.Pix[di+3] = clamp8(oa * 255)
			di += 4
		}
	}
}

// flameAnchor 返回尾焰中心在机体 bbox 内的归一化位置（x,y）。
// 数值来自官方完整帧（sprite30 系）与 8 方向截图实测：down/left*/right* 系列
// 喷口在机体上缘，up/leftup/rightup 机体翻转 180°，喷口落到下缘。
// 参数范围：dirName 为 SWF 的 8 个方向名；返回 0..1 归一化坐标。
func flameAnchor(dirName string) (float64, float64) {
	switch dirName {
	case "leftdown":
		return 0.40, -0.10
	case "left":
		return 0.32, 0.02
	case "leftup":
		return 0.50, 0.62
	case "up":
		return 0.50, 0.62
	case "rightup":
		return 0.50, 0.62
	case "right":
		return 0.68, 0.02
	case "rightdown":
		return 0.60, -0.10
	}
	return 0.52, -0.16 // down
}
