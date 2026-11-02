// Package sprite 把 spec.Sprite 合成成位图。
//
// 渲染顺序：按 layer.z 升序，同层按帧内顺序。
// 每个 part 的坐标链：path 自身坐标 → part.matrix（SVG 语义）→ 整体 scale。
// 光栅化在 ss 倍超采样坐标系里做，最后 box 下采样。
package sprite

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"sync"
	"sync/atomic"

	"nonovec/pkg/pathd"
	"nonovec/pkg/raster"
	"nonovec/pkg/spec"
)

// baseOn 控制是否在合成时乘 path.matrix（FFDec 的 <g transform>）。
// 默认关：见 renderSuper 里的推导。NONO_VEC_BASE=1 打开，用于对照/回退。
var baseOn = os.Getenv("NONO_VEC_BASE") == "1"

// Renderer 一个形态的渲染器，带帧缓存。
type Renderer struct {
	S   *spec.Sprite
	Lib *spec.Library

	// Tint 按层名指定染色乘子（层声明 tintable=true 才生效）。
	// 赛尔号原版用 colorTransform 把玩家色「乘」到可染色层的材质上：
	// 白色部件直接变成玩家色，深色部件变成玩家色的暗版，明暗关系保留。
	// 之前 PNG 路径是整帧染色（tintedCopy），连眼睛一起染；矢量路径改成
	// 只乘 tintable 层，眼睛/面具这些不可染色部件保持原色。
	Tint map[string]color.NRGBA
	// ReplaceTint 是「整层刷色」染色层集合（SetReplaceTint 设置）。
	// 对应 AS3 ColorTransform.color 的语义：RGB 直接换成目标色、alpha 保留。
	// 飞行器的 colorMc 就是这种层——官方 NonoFlyModel 对它无条件 FillColor(玩家色)，
	// 玩家色为白时也刷成白。若用乘法，青绿底板会把白色乘回青绿，于是
	// 「飞行模式配色和非飞行模式不统一」（子渊 2026-10-24 / 10-27 两次反馈）。
	ReplaceTint map[string]bool
	// 由 S.Layers 预先算出的可染色层集合，避免每帧查结构体。
	// 素材没声明任何 tintable 层时（年度/至尊形态的动作与表情段导出后全是
	// shape_*），按填充色自动推断，见 tintableFromFill。
	tintable map[string]bool
	// tintFloor 染色后亮度保底（0-255，按基色亮度的百分比保留）。
	// 纯乘法会把纯白部件直接压成目标色：目标色为 #000000 时整块死黑，
	// 高光、球面层次全丢。保留一个很小的基色比例，是「维持观感」的下限。
	tintFloor atomic.Uint32
	// 渐变染色（本地新增「自定义渐变芯片」）：开启后 tintable 层的填充在
	// gradA→gradB 之间按像素位置插值，gradMode 0=竖直 1=水平 2=对角。
	// 只用两色 + 固定方向，是这类芯片最普遍的形态；色标更多时再扩成 stops。
	gradOn   bool
	gradA    uint32
	gradB    uint32
	gradMode uint8
	// OnlyLayers 非空时只渲染这些层（与 SkipLayers 二选一，OnlyLayers 优先）。
	// 用途：程序化尾焰要按原版 depth 叠在 dirMc 之下，需要单独取 dirMc。
	OnlyLayers map[string]bool
	// SkipLayers 指定渲染时整层跳过的层名（nil/空表示不跳过）。
	// 用途：程序化尾焰接管 fireMc 后，底图不再光栅化该层——实测 fireMc 占
	// 单帧耗时的 73%~95%（style1 248.5→66.8ms，style4 291.9→15.2ms）。
	SkipLayers map[string]bool

	mu    sync.Mutex
	cache map[string]*image.RGBA
	// parsed 缓存解析后的折线（部件名 → 每路径折线）
	parsed map[string][][]pathd.Point
}

// cacheLimit 是单帧缓存的最大条目数。
//
// 取值依据（对照 seer-20261102-05 D-10）：key 含 scale（%.4f）与 ss，
// 每次缩放/超采样变化都会产生整套新 key，旧倍率帧若不淘汰会永久驻留内存。
// 512 条足够覆盖「当前方向 + 相邻方向」的热点（一个方向约 20~60 帧，
// 8 方向 × 2 倍率 ≈ 2000 条封顶）；满则整表作废，代价只是清空后第一帧稍慢。
const cacheLimit = 512

// New 创建渲染器。
func New(s *spec.Sprite, lib *spec.Library) *Renderer {
	tintable := map[string]bool{}
	for _, l := range s.Layers {
		if l.Tintable {
			tintable[l.Name] = true
		}
	}
	if len(tintable) == 0 {
		tintable = tintableFromFill(s, lib)
	}
	return &Renderer{
		S:        s,
		Lib:      lib,
		tintable: tintable,
		cache:    map[string]*image.RGBA{},
		parsed:   map[string][][]pathd.Point{},
	}
}

// tintableFromFill 在素材没有声明任何 tintable 层时，按填充色推断可染层。
//
// 背景：原版 NonoModel 只对名字以 color_ 开头的子 MC 调 FillColor
// （NonoModel.as:383-396），而年度/至尊形态的动作与表情段从 SWF 导出后
// 没有 color_ 层，全是 shape_*，于是这些段永远不变色（待机变了、一动就白）。
//
// 判定「近中性白」：min(R,G,B) >= 200 且 max-min <= 25。
// 这样纯白/米白的本体壳算可染；青色护盾 (2,254,254)、黄色装饰
// (252,242,122)、黑描边、深灰阴影都不会被误染。
// 一层里 60% 以上的路径满足条件才整层可染，避免个别白点把整层拖下水。
func tintableFromFill(s *spec.Sprite, lib *spec.Library) map[string]bool {
	type acc struct{ white, total int }
	stats := map[string]*acc{}
	for _, d := range s.Directions {
		for _, fr := range d.Frames {
			for _, p := range fr.Parts {
				pf := lib.Parts[p.Part]
				if pf == nil {
					continue
				}
				a := stats[p.Layer]
				if a == nil {
					a = &acc{}
					stats[p.Layer] = a
				}
				for _, pd := range pf.Paths {
					if pd.Fill == "" {
						continue // 渐变/无填充：不参与白块判定
					}
					n, ok := s.ResolveColor(pd.Fill)
					if !ok {
						continue
					}
					a.total++
					r := int(n >> 16 & 0xFF)
					g := int(n >> 8 & 0xFF)
					b := int(n & 0xFF)
					mn, mx := r, r
					if g < mn {
						mn = g
					}
					if b < mn {
						mn = b
					}
					if g > mx {
						mx = g
					}
					if b > mx {
						mx = b
					}
					if mn >= 200 && mx-mn <= 25 {
						a.white++
					}
				}
			}
		}
	}
	out := map[string]bool{}
	for name, a := range stats {
		if a.total > 0 && a.white*10 >= a.total*6 {
			out[name] = true
		}
	}
	return out
}

// SetTintFloor 设置染色后的亮度保底（0-255，相对基色亮度的比例）。
// 0 = 纯乘法（原行为）；建议 8~16 之间，让钢琴黑这类极暗色保留球面层次。
func (r *Renderer) SetTintFloor(v uint8) {
	r.tintFloor.Store(uint32(v))
}

// SetGradient 开启渐变染色：tintable 层的填充按像素位置在 a→b 之间插值。
// mode 0=竖直（上 a 下 b）1=水平 2=对角。传一次就会清缓存，下一次渲染重新合成。
func (r *Renderer) SetGradient(a, b uint32, mode int) {
	r.mu.Lock()
	r.gradOn, r.gradA, r.gradB, r.gradMode = true, a&0xFFFFFF, b&0xFFFFFF, uint8(mode&3)
	r.cache = map[string]*image.RGBA{}
	r.mu.Unlock()
}

// ClearGradient 关掉渐变染色，回到 SetTint 的单色行为。
func (r *Renderer) ClearGradient() {
	r.mu.Lock()
	if r.gradOn {
		r.gradOn = false
		r.cache = map[string]*image.RGBA{}
	}
	r.mu.Unlock()
}

// gradShade 返回逐像素着色回调；未开渐变或该层不参与染色时返回 nil。
// 渐变按**本次输出位图**归一化：动作段用跨帧并集 clip 渲染，clip 就是本体
// 包围盒，渐变从头到脚铺满本体，不会因为帧内容多少而漂移。
func (r *Renderer) gradShade(base color.NRGBA, tc *color.NRGBA, b image.Rectangle) func(x, y float64) color.NRGBA {
	if !r.gradOn || tc == nil || b.Dx() <= 0 || b.Dy() <= 0 {
		return nil
	}
	w, h := float64(b.Dx()), float64(b.Dy())
	ar, ag, ab := uint8(r.gradA>>16), uint8(r.gradA>>8), uint8(r.gradA)
	br, bg, bb := uint8(r.gradB>>16), uint8(r.gradB>>8), uint8(r.gradB)
	fl := uint8(r.tintFloor.Load())
	mode := r.gradMode
	return func(x, y float64) color.NRGBA {
		var t float64
		switch mode {
		case 1:
			t = x / w
		case 2:
			t = (x/w + y/h) / 2
		default:
			t = y / h
		}
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
		mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }
		c := base
		c.R = tintMul(base.R, mix(ar, br), fl)
		c.G = tintMul(base.G, mix(ag, bg), fl)
		c.B = tintMul(base.B, mix(ab, bb), fl)
		return c
	}
}

// SetTint 设置玩家色（0xAARRGGBB 里的 RGB 部分）。
// 只影响 tintable 层；清空帧缓存让下一帧重新合成。
//
// 0xFFFFFF（默认白）走「不染色」快路径：tintable 层基色本身就是纯白，
// 乘白是恒等变换，跳过可以省掉每像素三次乘法。
// 0x000000 是合法的钢琴黑（变色芯片 700308），必须真染：旧写法把 0 也当成
// 「不染色」的哨兵，导致选钢琴黑时本体保持原白、芯片形同虚设（子渊 2026-10-27）。
func (r *Renderer) SetTint(rgb uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 有 ReplaceTint 层时白色不能走快路径：ColorTransform.color 对这类层是
	// 「无条件刷成目标色」，白色 = 刷成白，而不是「保持素材原色」。
	if rgb == 0xFFFFFF && len(r.ReplaceTint) == 0 {
		r.Tint = nil
	} else {
		c := color.NRGBA{
			R: uint8((rgb >> 16) & 0xFF),
			G: uint8((rgb >> 8) & 0xFF),
			B: uint8(rgb & 0xFF),
			A: 255,
		}
		r.Tint = map[string]color.NRGBA{}
		for name := range r.tintable {
			r.Tint[name] = c
		}
	}
	// 缓存里是染色后的像素，必须全部失效
	r.cache = map[string]*image.RGBA{}
}

// SetReplaceTint 把某些层切到「整层刷色」：RGB 直接换成玩家色、alpha 原样保留。
// 对应 AS3 ColorTransform.color（官方 NonoFlyModel 对 colorMc 用的就是它）：
// 飞行器底板被无条件刷成玩家色，玩家色为白时刷成白，而不是保留素材原色。
// 传空表示全部回到乘法染色。
func (r *Renderer) SetReplaceTint(layers ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(layers) == 0 {
		r.ReplaceTint = nil
	} else {
		m := make(map[string]bool, len(layers))
		for _, l := range layers {
			m[l] = true
		}
		r.ReplaceTint = m
	}
	r.cache = map[string]*image.RGBA{}
}

// SetOnlyLayers 设置只渲染哪些层（传空表示不限制）。清帧缓存。
func (r *Renderer) SetOnlyLayers(layers ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(layers) == 0 {
		r.OnlyLayers = nil
	} else {
		m := make(map[string]bool, len(layers))
		for _, l := range layers {
			m[l] = true
		}
		r.OnlyLayers = m
	}
	r.cache = map[string]*image.RGBA{}
}

// SetSkipLayers 设置渲染时要整层跳过的层名（传空/nil 表示不跳过）。
//
// 只影响 parts 的筛选，不改画布尺寸、不影响滤镜组对其它层的引用。
// 切到「跳过某层」时要清帧缓存：同一个 Renderer 可能先渲染过完整帧。
func (r *Renderer) SetSkipLayers(layers ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(layers) == 0 {
		r.SkipLayers = nil
	} else {
		m := make(map[string]bool, len(layers))
		for _, l := range layers {
			m[l] = true
		}
		r.SkipLayers = m
	}
	r.cache = map[string]*image.RGBA{}
}

// FrameCount 返回某方向帧数。
func (r *Renderer) FrameCount(dir string) int {
	d, ok := r.S.Directions[dir]
	if !ok {
		return 0
	}
	return len(d.Frames)
}

// Render 渲染某方向第 idx 帧，scale 为显示倍率，ss 为超采样倍数。
func (r *Renderer) Render(dir string, idx int, scale float64, ss int) (*image.RGBA, error) {
	return r.RenderLayer(dir, idx, scale, ss, "")
}

// RenderLayer 只渲染指定层（layer 为空 = 全部），用于逐层排查。
func (r *Renderer) RenderLayer(dir string, idx int, scale float64, ss int, layer string) (*image.RGBA, error) {
	key := fmt.Sprintf("%s|%d|%.4f|%d|%s", dir, idx, scale, ss, layer)
	r.mu.Lock()
	if img, ok := r.cache[key]; ok {
		r.mu.Unlock()
		return img, nil
	}
	r.mu.Unlock()

	img, err := r.render(dir, idx, scale, ss, layer, true, nil)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	// 缓存条目上限（2026-11-02 对照 seer D-10）：key 含 scale/ss，
	// 调倍率/超采样会产生新 key，旧帧永不淘汰会无限增长。
	if len(r.cache) >= cacheLimit {
		r.cache = map[string]*image.RGBA{}
	}
	r.cache[key] = img
	r.mu.Unlock()
	return img, nil
}

// RenderFullCanvas 渲染整块画布，忽略帧级 bbox 裁剪。
//
// 为什么需要：帧 bbox 只覆盖「该帧可见几何」，直接按 bbox 出图会让内容贴着
// 图边（飞行器 style1 的 bbox 是 [2.1,2.35,70,99.7]，画布 77.8×104.05），
// 描边与抗锯齿边缘被切掉 2px，实机上就是「边上少一圈」。调用方若需要把图
// 放回统一画布的固定锚点（飞行器落脚、气泡对齐），也必须用整画布坐标，
// 否则每帧 bbox 不同会让同一只飞行器在窗口里左右上下抖。
func (r *Renderer) RenderFullCanvas(dir string, idx int, scale float64, ss int) (*image.RGBA, error) {
	key := fmt.Sprintf("full|%s|%d|%.4f|%d", dir, idx, scale, ss)
	r.mu.Lock()
	if img, ok := r.cache[key]; ok {
		r.mu.Unlock()
		return img, nil
	}
	r.mu.Unlock()

	img, err := r.render(dir, idx, scale, ss, "", false, nil)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if len(r.cache) >= cacheLimit {
		r.cache = map[string]*image.RGBA{}
	}
	r.cache[key] = img
	r.mu.Unlock()
	return img, nil
}

// RenderLayerFullCanvas 只渲染指定层并按整画布出图。
//
// 用于标定「某层在画布里的位置」：RenderLayer 按帧 bbox 裁图，图原点不是
// 画布原点，量出来的坐标不能直接当画布坐标用（会偏 bbox 左上角几十像素）。
func (r *Renderer) RenderLayerFullCanvas(dir string, idx int, scale float64, ss int, layer string) (*image.RGBA, error) {
	key := fmt.Sprintf("full|%s|%d|%.4f|%d|%s", dir, idx, scale, ss, layer)
	r.mu.Lock()
	if img, ok := r.cache[key]; ok {
		r.mu.Unlock()
		return img, nil
	}
	r.mu.Unlock()

	img, err := r.render(dir, idx, scale, ss, layer, false, nil)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if len(r.cache) >= cacheLimit {
		r.cache = map[string]*image.RGBA{}
	}
	r.cache[key] = img
	r.mu.Unlock()
	return img, nil
}

// RenderBbox 只渲染该帧内容 bbox 区域，并返回该区域在画布里的左上角位置。
//
// 为什么要它（2026-10-25）：动作本体要放大到与待机等大（4x），但 clip 是
// **跨帧并集**——super5/action_7 并集 937x912，4x 后 3746x3648，整块渲染
// 单帧 408ms，远超 24fps 的 41ms 预算。而单帧真实内容常常只有 415x288（1x），
// 只渲染这一块能省 ~7 倍时间与内存。位置由返回的偏移交给调用方平移还原，
// 所以不会出现「逐帧 bbox 导致画面左右抖」的问题。
func (r *Renderer) RenderBbox(dir string, idx int, scale float64, ss int) (*image.RGBA, [2]float64, error) {
	d := r.S
	dd, ok := d.Directions[dir]
	if !ok || idx < 0 || idx >= len(dd.Frames) {
		return nil, [2]float64{}, fmt.Errorf("sprite: 方向 %q 第 %d 帧不存在", dir, idx)
	}
	bb := dd.Frames[idx].Bbox
	var clip [4]float64
	if len(bb) == 4 && bb[2] > 0 && bb[3] > 0 {
		// 外扩 1px：描边/抗锯齿会略微溢出几何 bbox。
		// 不把窗口夹回画布：动作里的大环/射线本来就会伸出画布边界，
		// 夹到 [0,Canvas] 会直接切掉可见像素（super5/action_7 第 205 帧
		// 的大光环左侧 x=-39 就被切没了）。负起点由返回的 offset 交给调用方平移。
		clip = [4]float64{bb[0] - 1, bb[1] - 1, bb[2] + 2, bb[3] + 2}
	} else {
		clip = [4]float64{0, 0, d.Canvas.W, d.Canvas.H}
	}
	img, err := r.render(dir, idx, scale, ss, "", false, clip[:])
	if err != nil {
		return nil, [2]float64{}, err
	}
	return img, [2]float64{clip[0], clip[1]}, nil
}

// RenderClip 把「画布坐标下的矩形区域」渲染成图，所有帧输出同尺寸。
//
// 为什么需要（2026-10-25）：矢量按 4x 渲染后，动作画布能到 1523x1112，
// 整画布出图既超出屏幕、又让每个动作吃 100MB+ 内存，窗口还被迫按画布暴涨。
// 但动作的真实内容只占其中一小块（super4 action_4 可见区 415x288）。用跨帧
// 并集矩形当渲染窗口，既能保证「任何一帧都不裁」，又能让窗口贴合内容。
//
// clip 为 [x, y, w, h]，画布坐标（1x 素材像素）；输出尺寸 = (w,h) × scale。
func (r *Renderer) RenderClip(dir string, idx int, scale float64, ss int, clip [4]float64) (*image.RGBA, error) {
	// 故意不写 r.cache：这个 cache 只增不删，动作动辄 300 帧（4x 单帧 6.7MB），
	// 一旦进 cache 就是 2GB 常驻（实测 RSS 顶到 3.4GB）。生命周期交给调用方的
	// 滑动窗口（vecAnim.Touch）管，那里会主动释放远端帧。
	return r.render(dir, idx, scale, ss, "", false, clip[:])
}

func (r *Renderer) render(dir string, idx int, scale float64, ss int, layerFilter string, useBbox bool, clip []float64) (*image.RGBA, error) {
	d, ok := r.S.Directions[dir]
	if !ok {
		return nil, fmt.Errorf("方向 %q 不存在", dir)
	}
	if idx < 0 || idx >= len(d.Frames) {
		return nil, fmt.Errorf("方向 %q 帧号 %d 越界（共 %d 帧）", dir, idx, len(d.Frames))
	}
	if scale <= 0 {
		scale = 1
	}
	if ss < 1 {
		ss = 1
	}
	w := int(r.S.Canvas.W*scale + 0.5)
	h := int(r.S.Canvas.H*scale + 0.5)
	// 有帧级 bbox 时只渲染这一小块：画布尺寸取 bbox，坐标整体平移到 bbox 原点。
	// 帧在统一画布里的位置由调用方按 (bbox.x, bbox.y) 还原。
	ox, oy := 0.0, 0.0
	if len(clip) == 4 && clip[2] > 0 && clip[3] > 0 {
		// 调用方给定的渲染窗口（画布坐标）：所有帧输出同一尺寸，帧与帧之间
		// 不会因为内容多少而改图大小，窗口/本体位置才稳定（动作逐帧 imgW 变化
		// 会让 NONO 在窗口里左右抖）。
		ox, oy = clip[0], clip[1]
		w = int(clip[2]*scale + 0.5)
		h = int(clip[3]*scale + 0.5)
	} else if bb := d.Frames[idx].Bbox; useBbox && len(bb) == 4 && bb[2] > 0 && bb[3] > 0 {
		ox, oy = bb[0], bb[1]
		w = int(bb[2]*scale + 0.5)
		h = int(bb[3]*scale + 0.5)
	}
	if w < 1 || h < 1 {
		return nil, fmt.Errorf("画布尺寸非法: %vx%v", r.S.Canvas.W, r.S.Canvas.H)
	}

	zOf := map[string]int{}
	for _, l := range r.S.Layers {
		zOf[l.Name] = l.Z
	}

	var parts []spec.Part
	for _, p := range d.Frames[idx].Parts {
		if layerFilter != "" && p.Layer != layerFilter {
			continue
		}
		// 整层跳过：程序化尾焰接管 fireMc 时底图不再画它。
		// 注意这里只过滤 parts，帧级 filters 组若引用被跳过的 part，
		// 该组渲染出的是空内容（不会 panic），也会自然消失。
		if len(r.OnlyLayers) > 0 && !r.OnlyLayers[p.Layer] {
			continue
		}
		if len(r.SkipLayers) > 0 && r.SkipLayers[p.Layer] {
			continue
		}
		parts = append(parts, p)
	}
	// 绘制顺序 = JSON 里的 parts 顺序（导入器按 SWF 显示列表 depth 升序写出）。
	// 不能按 layer.z 重排：同一个层名可能在多个 depth 上交错出现（color_1 在
	// 面罩前后各一次），按层名分组会把后半段画到前面，黑弧盖掉白壳。
	_ = zOf

	out := raster.RenderSuper(w, h, ss, func(dst *image.RGBA) {
		r.paintParts(dst, scale*float64(ss), ox, oy, parts, d.Frames[idx].Filters)
	})
	return out, nil
}

// paintParts 把一组 part 画进 dst。k 是「画布单位 → 目标像素」的总缩放
// （= scale×超采样），ox/oy 是裁剪窗口在画布里的原点。
//
// 从 render() 抽出来是为了让子时间轴（spec.AnimGroup）复用完全相同的
// 填充/描边/滤镜口径：眼睛和光圈必须和静态机体走同一条渲染路径，
// 否则发光边缘、偶奇填充会出现肉眼可见的差异。
func (r *Renderer) paintParts(dst *image.RGBA, k, ox, oy float64, parts []spec.Part,
	filters map[string][]spec.Filter) {
	{
		// drawPart 把单个 part 画进 dst —— dst 可能是主画布，也可能是滤镜组的临时层。
		drawPart := func(dst *image.RGBA, p spec.Part) {
			pf := r.Lib.Parts[p.Part]
			if pf == nil {
				return
			}
			alpha := 1.0
			if p.Alpha != nil {
				alpha = *p.Alpha
			}
			if alpha <= 0 {
				return // 完全不可见：cxform alpha=0 的蒙版/辅助部件
			}
			// 染色乘子：只有「声明 tintable 的层」+ 玩家设了非白颜色才生效。
			// 眼睛/面具在 body 层（tintable=false），不会被染到。
			var tc *color.NRGBA
			if r.Tint != nil {
				if c, ok := r.Tint[p.Layer]; ok {
					tc = &c
				}
			}
			for _, pd := range pf.Paths {
				// 02 量化资产在加载时已预解析好段序列，直接用；
				// 只有 01（含 d 字符串、无 Segs）才走解析。
				segs := pd.Segs
				if segs == nil {
					var perr error
					segs, perr = pathd.Parse(pd.D)
					if perr != nil {
						continue
					}
				}
				// 矩阵：part.matrix（SWF place 链，父坐标系）∘ path.matrix（SVG base）。
				//
				// base 是 FFDec 为了不让 SVG 出现负坐标加的 translate(-bounds.xmin)，
				// 而 SWF 的 shape 坐标本来就是相对 place 原点的局部坐标、place 已经
				// 负责定位，再乘一次 base 等于把内容整体再平移一个 -bounds.xmin。
				// 默认不乘；NONO_VEC_BASE=1 可回到旧行为做对照。
				m := p.Matrix
				if baseOn {
					m = mulMatrix(p.Matrix, pathMatrix(pd.Matrix))
				}
				m2 := [6]float64{
					m[0] * k, m[1] * k, m[2] * k, m[3] * k,
					(m[4] - ox) * k, (m[5] - oy) * k,
				}
				segs = pathd.Transform(segs, m2)
				polys := pathd.Polyline(segs, 0.005*k)
				// 描边宽度随矩阵缩放（SVG 导出可能带 zoom，矩阵里有 1/N 补偿）
				mscale := math.Sqrt(math.Abs(m[0]*m[3] - m[1]*m[2]))
				if mscale <= 0 {
					mscale = 1
				}
				// FFDec 导出的 shape 基本都是 even-odd（含自交与同心挖空），
				// 用 nonzero 填会把挖空补实，触角/光环会变粗。
				evenOdd := pd.FillRule == "evenodd"
				if pd.Gradient != nil {
					// 渐变矩阵的坐标链和 path 一样有两段：shape 本地 → (base∘place) → 画布，
					// 而 parts.matrix 与 gradient.matrix 在导出时都已减过画布原点 (cx,cy)。
					// 直接复合会减两次，完全不复合又缺 (base∘place)，两种都让渐变偏出形状。
					// 正确做法：先把 gM 的平移反推回 shape 本地，再左乘 place 矩阵 m。
					//   (A,T) ∘ gM_orig 的平移 = A·gM_t + T，而 T 已含 -cx，
					//   结果恰好等于目标「(base∘place)∘gM_orig − cx」，故无需再减 ox/oy。
					g := *pd.Gradient
					cx := -r.S.Canvas.Anchor[0]
					cy := -r.S.Canvas.Anchor[1]
					g.Matrix[4] += cx
					g.Matrix[5] += cy
					g.Matrix = mulMatrix(m, g.Matrix)
					if sh := r.shadeFor(&g, k, alpha, tc, p.CMul, p.CAdd, r.ReplaceTint[p.Layer]); sh != nil {
						raster.FillShaded(dst, polys, sh)
					}
				} else if pd.Fill != "" {
					fa := alpha
					if pd.FillAlpha != nil {
						fa *= *pd.FillAlpha
					}
					if c, ok := r.color(pd.Fill, fa, tc, p.CMul, p.CAdd, r.ReplaceTint[p.Layer]); ok {
						if sh := r.gradShade(c, tc, dst.Bounds()); sh != nil {
							// 渐变必须逐像素上色，纯色 FillPolys 只能用单色。
							if evenOdd {
								raster.FillShadedEvenOdd(dst, polys, sh)
							} else {
								raster.FillShaded(dst, polys, sh)
							}
						} else if evenOdd {
							raster.FillPolysEvenOdd(dst, polys, c)
						} else {
							raster.FillPolys(dst, polys, c)
						}
					}
				}
				if pd.Stroke != nil && pd.Stroke.Width > 0 {
					col := pd.Stroke.Color
					if p.Fill != "" && pd.Fill != "" {
						// part 级颜色覆盖只作用于填充，描边保持几何自带
						col = pd.Stroke.Color
					}
					sa := alpha
					if pd.StrokeAlpha != nil {
						sa *= *pd.StrokeAlpha
					}
					if c, ok := r.color(col, sa, tc, p.CMul, p.CAdd, r.ReplaceTint[p.Layer]); ok {
						raster.StrokePolys(dst, polys, c, pd.Stroke.Width*mscale*k)
					}
				}
			}
		}
		// 带 fid 的 part 必须成组（同一个 place 的整棵子树）先画到临时层，
		// 再做一次滤镜、整组叠回。逐 part 做会让组内重叠处亮度翻倍。
		var grp []spec.Part
		grpFid := ""
		flush := func() {
			if len(grp) == 0 {
				return
			}
			filt := r.filterFor(grpFid, filters)
			if len(filt) == 0 {
				for _, gp := range grp {
					drawPart(dst, gp)
				}
				grp, grpFid = nil, ""
				return
			}
			tmp := image.NewRGBA(dst.Bounds())
			for _, gp := range grp {
				drawPart(tmp, gp)
			}
			r.applyFilters(dst, tmp, filt, k)
			grp, grpFid = nil, ""
		}
		for _, p := range parts {
			if p.Fid == "" {
				flush()
				drawPart(dst, p)
				continue
			}
			if p.Fid != grpFid {
				flush()
				grpFid = p.Fid
			}
			grp = append(grp, p)
		}
		flush()
	}
}

// GroupCount 返回某方向的子时间轴数量（spec.AnimGroup）。
func (r *Renderer) GroupCount(dir string) int {
	d, ok := r.S.Directions[dir]
	if !ok {
		return 0
	}
	return len(d.Groups)
}

// RenderClipGroup 渲染第 gi 个子时间轴的第 phase 帧，窗口/缩放口径与
// RenderClip 完全一致（同一个 clip 出来的图可以直接叠回整帧）。
//
// 为什么单独一个入口：子时间轴的帧数（15/19/18）与方向段长（7~8）无关，
// 调用方必须能按自己的时钟取帧；塞进 RenderClip 的 idx 会被方向段长带偏。
func (r *Renderer) RenderClipGroup(dir string, gi, phase int, scale float64, ss int,
	clip [4]float64) (*image.RGBA, error) {
	d, ok := r.S.Directions[dir]
	if !ok {
		return nil, fmt.Errorf("方向 %q 不存在", dir)
	}
	if gi < 0 || gi >= len(d.Groups) {
		return nil, fmt.Errorf("方向 %q 子时间轴 %d 越界（共 %d 个）", dir, gi, len(d.Groups))
	}
	g := &d.Groups[gi]
	if len(g.Frames) == 0 {
		return nil, fmt.Errorf("方向 %q 子时间轴 %d 没有帧", dir, gi)
	}
	if phase < 0 {
		phase = 0
	}
	phase %= len(g.Frames)
	if scale <= 0 {
		scale = 1
	}
	if ss < 1 {
		ss = 1
	}
	w := int(clip[2]*scale + 0.5)
	h := int(clip[3]*scale + 0.5)
	if w < 1 || h < 1 {
		return nil, fmt.Errorf("裁剪窗口非法: %vx%v", clip[2], clip[3])
	}
	fr := &g.Frames[phase]
	out := raster.RenderSuper(w, h, ss, func(dst *image.RGBA) {
		r.paintParts(dst, scale*float64(ss), clip[0], clip[1], fr.Parts, fr.Filters)
	})
	return out, nil
}

// color 把颜色值（#RRGGBB / #AARRGGBB / 槽位名）解析成 NRGBA，并按 tint 相乘。
// tint 为 nil 时原样返回；相乘保留部件自身的明暗层次（白 → 玩家色，深色 → 玩家色暗版）。
// cxPart 把一个颜色分量按 SWF cxform 变换：c' = clamp(c*mul + add)。
// mul/add 是 01/02 里已按嵌套顺序累积好的值（见 build_sprite.expand）。
// 作用在 0..255 的非预乘分量上，调用点在乘 alpha 之前。
func cxPart(c float64, mul, add []float64, i int) float64 {
	if len(mul) >= 3 {
		c *= mul[i]
	}
	if len(add) >= 3 {
		c += add[i]
	}
	if c < 0 {
		return 0
	}
	if c > 255 {
		return 255
	}
	return c
}

func (r *Renderer) color(v string, alpha float64, tint *color.NRGBA, cmul, cadd []float64, replace bool) (color.NRGBA, bool) {
	n, ok := r.S.ResolveColor(v)
	if !ok {
		return color.NRGBA{}, false
	}
	a := float64((n>>24)&0xFF) * alpha
	if a > 255 {
		a = 255
	}
	// cxform 必须先于 tint：它属于 SWF 的 place 层，染色是我们运行时对
	// tintable 层的覆盖，顺序反了会让人体/装饰的基色被错误地再缩放一次。
	rr := cxPart(float64((n>>16)&0xFF), cmul, cadd, 0)
	gg := cxPart(float64((n>>8)&0xFF), cmul, cadd, 1)
	bb := cxPart(float64(n&0xFF), cmul, cadd, 2)
	c := color.NRGBA{
		R: uint8(rr + 0.5),
		G: uint8(gg + 0.5),
		B: uint8(bb + 0.5),
		A: uint8(a),
	}
	if tint != nil {
		if replace {
			// AS3 ColorTransform.color 语义：RGB 全替换成目标色，alpha 保留。
			// 只用于 colorMc 这种纯色底板层（官方 NonoFlyModel 对它无条件
			// FillColor(玩家色)）；乘法会把青绿底板乘回去，白色玩家色等于不染。
			c.R, c.G, c.B = tint.R, tint.G, tint.B
			return c, true
		}
		fl := uint8(r.tintFloor.Load())
		c.R = tintMul(c.R, tint.R, fl)
		c.G = tintMul(c.G, tint.G, fl)
		c.B = tintMul(c.B, tint.B, fl)
	}
	return c, true
}

// tintMul 把基色分量按玩家色相乘，并保留 floor 比例的基色亮度。
//
//	结果 = max(base*tint/255, base*floor/255)
//
// 纯乘法时 base=255、tint=0 会得到 0：一块死黑，球面的明暗起伏全被吃掉。
// 保底那一项让「本来就更亮的部件」仍然更亮，层次得以保住；floor=0
// 时行为与旧版逐像素一致。
func tintMul(base, tint, floor uint8) uint8 {
	v := uint32(base) * uint32(tint) / 255
	if floor > 0 {
		if f := uint32(base) * uint32(floor) / 255; v < f {
			v = f
		}
	}
	return uint8(v)
}

// pathMatrix 返回 shape 内单个 <path> 的 base 矩阵。
// 旧素材可能没有 matrix 字段（全零），此时按单位矩阵处理，避免整帧被压成一点。
func pathMatrix(m [6]float64) [6]float64 {
	if m[0] == 0 && m[1] == 0 && m[2] == 0 && m[3] == 0 {
		return [6]float64{1, 0, 0, 1, m[4], m[5]}
	}
	return m
}

// mulMatrix 计算 A∘B（先应用 B，再应用 A），SVG 语义 [a b c d e f]。
//
//	x' = a*x + c*y + e
//	y' = b*x + d*y + f
func mulMatrix(a, b [6]float64) [6]float64 {
	return [6]float64{
		a[0]*b[0] + a[2]*b[1],
		a[1]*b[0] + a[3]*b[1],
		a[0]*b[2] + a[2]*b[3],
		a[1]*b[2] + a[3]*b[3],
		a[0]*b[4] + a[2]*b[5] + a[4],
		a[1]*b[4] + a[3]*b[5] + a[5],
	}
}
