//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"sync"

	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

// assetsRoot 返回素材目录：优先 exe 同目录的 assets，其次工作目录。
//
// 有 appTag 时先试 assets_<tag>/（同目录与其它桌宠共存时的布局），
// 不存在再退回 assets/ —— 单项目布局与开发期行为完全不变。
// tag 为空时再兜一层：assets/ 不在、同级恰好只有一个 assets_*/ 就用它。
func assetsRoot() string {
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		if t := appTag(); t != "" {
			d := filepath.Join(base, "assets_"+t)
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				return d
			}
		}
		d := filepath.Join(base, "assets")
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
		if u := uniqueTaggedDir(base, "assets"); u != "" {
			return u
		}
	}
	return "assets"
}

type Meta struct {
	Key    string             `json:"key"`
	Zoom   int                `json:"zoom"` // 素材相对原始尺寸的导出倍率
	Canvas struct{ W, H int } `json:"canvas"`
	// Ball 是本体球径（原始像素）。各形态缩放后球径统一为同一个值，快捷栏、
	// 图标等按它换算才能和本体一致；用 Canvas.W 会被长鳍/长耳拉宽而失真。
	Ball int `json:"ball"`
	// BallC 是「本体球形中心」在 down 方向画布里的坐标（素材像素）。
	// 由 tools/measure_ballc.py 实测：x 取面部黑窗口水平中心，y 取该列上
	// 最长不透明段(球体)的垂直中心。快捷栏环绕与形态切换都以它为准，
	// 不再用 Canvas 中心或 Canvas.H*0.6 估算（球在画布里的位置各形态差别很大）。
	BallC  []float64      `json:"ballc"`
	Dirs   map[string]int `json:"dirs"`
	Layers []struct {
		Name string  `json:"name"`
		DX   int     `json:"dx"`
		DY   int     `json:"dy"`
		Err  float64 `json:"err"`
	} `json:"layers"`
}

// FrameSet 只保存文件清单，帧按需解码。
// 4x 素材单帧 253x270x4 ≈ 273KB，502 帧全驻留要 137MB，实测 RSS 283MB，
// 必须懒加载：待机只留当前+上次方向，动作/表情只留正在播的那一套。
type FrameSet struct {
	Key  string
	Meta Meta

	dirFiles    map[string][]string
	actionFiles map[int][]string
	expFiles    map[int][]string
	idleCache   map[string][]*image.RGBA
	animCache   []*image.RGBA
	animKey     int

	// idleAnim* 是「待机浮动」专用序列（用户提供的官方待机动画导出的 89 帧）。
	// 与方向帧分开存：方向帧负责移动/朝向，这套只管站定时本体的上下浮动。
	idleAnimFiles []string
	idleAnimCache []*image.RGBA
	idleAnimDur   []int

	// vec 非 nil 时，像素帧改由矢量精灵实时渲染（NONO_VEC=1）。
	// PNG 链路保持原样，只是不再被这些方法使用。
	vec *vecBackend
}

func listPNGs(fsys fs.FS, dir string) []string {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && path.Ext(e.Name()) == ".png" {
			names = append(names, dir+"/"+e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func decodePNG(name string) (*image.RGBA, error) {
	f, err := os.Open(filepath.Join(assetsRoot(), name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	im, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	if r, ok := im.(*image.RGBA); ok {
		return r, nil
	}
	b := im.Bounds()
	r := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r.Set(x, y, im.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return r, nil
}

func decodeAll(names []string) []*image.RGBA {
	out := make([]*image.RGBA, 0, len(names))
	for _, n := range names {
		im, err := decodePNG(n)
		if err != nil {
			continue
		}
		out = append(out, im)
	}
	return out
}

// ballAnchorCfg 是「跨形态统一的球心锚点」：所有形态把球心对到同一个
// 窗口内位置，切换形态时球心屏幕坐标就不会动。数值单位是素材像素
// （÷zoom 得显示像素），由 tools/measure_ballc.py 测量 8 方向所有帧的并集
// bbox 后写出，保证任何形态任何方向都不会被画布裁掉。
type ballAnchorCfg struct {
	// Schema 记录锚点口径版本：1x 时代（PNG 帧实测）没有这个字段，
	// 值为 0；4x 矢量口径为 2。缺失时 main.go 会告警（super4 球心会偏出画布）。
	Schema  int     `json:"schema"`
	Zoom    int     `json:"zoom"`
	AnchorX float64 `json:"anchor_x"`
	AnchorY float64 `json:"anchor_y"`
	WinW    float64 `json:"win_w"`
	WinH    float64 `json:"win_h"`
}

var (
	ballCfgOnce sync.Once
	ballCfgVal  *ballAnchorCfg
)

// ballAnchorCfg 读取 assets/ball_anchor.json；缺失时返回 nil，
// 调用方退回旧的「画布居中/底部对齐」逻辑（旧素材包仍能跑）。
func ballAnchor() *ballAnchorCfg {
	ballCfgOnce.Do(func() {
		b, err := os.ReadFile(filepath.Join(assetsRoot(), "ball_anchor.json"))
		if err != nil {
			return
		}
		var c ballAnchorCfg
		if err := json.Unmarshal(b, &c); err != nil {
			return
		}
		if c.Zoom < 1 {
			c.Zoom = 4
		}
		if c.WinW <= 0 || c.WinH <= 0 || c.AnchorX <= 0 || c.AnchorY <= 0 {
			return
		}
		ballCfgVal = &c
	})
	return ballCfgVal
}

func loadFrameSet(key string) (*FrameSet, error) {
	fsx := &FrameSet{
		Key:         key,
		dirFiles:    map[string][]string{},
		actionFiles: map[int][]string{},
		expFiles:    map[int][]string{},
		idleCache:   map[string][]*image.RGBA{},
	}
	b, err := os.ReadFile(filepath.Join(assetsRoot(), key, "meta.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &fsx.Meta); err != nil {
		return nil, err
	}
	if fsx.Meta.Zoom < 1 {
		fsx.Meta.Zoom = 1
	}
	root := os.DirFS(assetsRoot())
	for dir := range fsx.Meta.Dirs {
		files := listPNGs(root, key+"/idle/"+dir)
		if len(files) > 0 {
			fsx.dirFiles[dir] = files
		}
	}
	if entries, err := fs.ReadDir(root, key+"/action"); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			id, _ := strconv.Atoi(e.Name())
			if f := listPNGs(root, key+"/action/"+e.Name()); len(f) > 0 {
				fsx.actionFiles[id] = f
			}
		}
	}
	if entries, err := fs.ReadDir(root, key+"/exp"); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			id, _ := strconv.Atoi(e.Name())
			if f := listPNGs(root, key+"/exp/"+e.Name()); len(f) > 0 {
				fsx.expFiles[id] = f
			}
		}
	}
	// 待机浮动序列（可选）：idle_anim/ 下的连续帧 + idle_anim.json 里的每帧时长。
	// 这套帧带真实的本体上下位移，只在站定时播放；移动仍走 idle/<方向>/。
	if files := listPNGs(root, key+"/idle_anim"); len(files) > 0 {
		fsx.idleAnimFiles = files
		var cfg struct {
			DurationsMs []int `json:"durations_ms"`
		}
		if b, err := os.ReadFile(filepath.Join(assetsRoot(), key, "idle_anim", "idle_anim.json")); err == nil {
			_ = json.Unmarshal(b, &cfg)
		}
		fsx.idleAnimDur = cfg.DurationsMs
	}
	// 矢量后端：默认启用（NONO_VEC_SPRITE=0 回退到 PNG 烘焙帧）。
	// 开关与面板底图/飞行器拆开，三者互不连带：
	//   - 面板底图（uitheme.uiVecEnabled）：2026-10-25 修完 svg.go 三处缺陷
	//     （radialGradient 漏 fx/fy、曲线细分按边长估算、UsesFillWindingRule 未传入）
	//     后已切默认开。2026-10-26 复核 main 面板（535x395）：矢量 vs PNG
	//     平均差 1.68/255、diff>30 仅 1.94%，残差集中在抗锯齿 1px 边缘。
	//     旧注释「63 个 shape 只画出极少部分」已过期，勿再据此关掉。
	//   - 飞行器矢量（NONO_VEC_FLY）：素材尚未按 native 重导，默认关。
	// 精灵矢量本身在 sprite 级做过逐帧比对：6 形态抽检 IoU 1.0、RGB 差 0~0.3。
	if os.Getenv("NONO_VEC_SPRITE") != "0" {
		if vb, verr := loadVecBackend(key, &fsx.Meta); verr != nil {
			logf("矢量后端加载 %s 失败，回退 PNG: %v", key, verr)
		} else {
			fsx.vec = vb
			logf("矢量后端 %s: canvas=%dx%d ballc=%v", key, fsx.Meta.Canvas.W, fsx.Meta.Canvas.H, fsx.Meta.BallC)
		}
	}
	if fsx.vec == nil && len(fsx.dirFiles) == 0 {
		return nil, fmt.Errorf("%s 没有 idle 帧", key)
	}
	return fsx, nil
}

// idleFrameCount 只返回某方向的帧数，不解码像素。待机帧推进每个 tick 都要取
// 长度做取模，走 idleFrames 会在缓存未命中时解码整组帧，代价不能接受。
func (f *FrameSet) idleFrameCount(dir string) int {
	if f.vec != nil {
		return f.vec.count(dir)
	}
	if files, ok := f.dirFiles[dir]; ok {
		return len(files)
	}
	return 0
}

// idleFrames 返回某个方向的待机帧，缺失时退回任意已加载方向。
func (f *FrameSet) idleFrames(dir string) []*image.RGBA {
	if f.vec != nil {
		return f.vec.frames(dir)
	}
	if fr, ok := f.idleCache[dir]; ok && len(fr) > 0 {
		return fr
	}
	files, ok := f.dirFiles[dir]
	if !ok || len(files) == 0 {
		return nil
	}
	// 只保留当前方向和上一次用的方向，避免 8 个方向全驻留
	if len(f.idleCache) >= 2 {
		for k := range f.idleCache {
			if k != dir {
				delete(f.idleCache, k)
			}
		}
	}
	fr := decodeAll(files)
	f.idleCache[dir] = fr
	return fr
}

// hasIdleAnim 是否存在待机浮动序列（缺失时退回方向帧，行为与旧版一致）。
func (f *FrameSet) hasIdleAnim() bool {
	if f.vec != nil {
		return false // 89 帧待机浮动序列尚未迁移到矢量
	}
	return len(f.idleAnimFiles) > 0
}

// idleAnim 返回待机浮动帧。89 帧 × 253×270 常驻约 24MB，只在首次用到时解码，
// 之后一直保留（待机是最高频状态，反复释放反而更卡）。
func (f *FrameSet) idleAnim() []*image.RGBA {
	if f.idleAnimCache != nil {
		return f.idleAnimCache
	}
	f.idleAnimCache = decodeAll(f.idleAnimFiles)
	return f.idleAnimCache
}

// idleAnimDurAt 返回第 i 帧的停留时长（毫秒），缺表时按 33ms（≈30fps）兜底。
func (f *FrameSet) idleAnimDurAt(i int) int {
	if i < 0 || i >= len(f.idleAnimDur) || f.idleAnimDur[i] <= 0 {
		return 33
	}
	return f.idleAnimDur[i]
}

// animIDs 返回该形态实际拥有的动作(kind=1)/表情(kind=2) id 列表（升序）。
//
// ⚠ 菜单枚举必须走这里，不能直接读 f.actionFiles / f.expFiles：
// 那两个 map 是 2026-10-26 切成矢量素材之前的 PNG 索引（扫 key+"/action" 下的
// PNG 帧目录），矢量素材目录里没有这些子目录，永远是空 map。
// 子渊反馈「右键的动作演示没有内容了」就是这里：菜单用 len(actionFiles)==0
// 判成「本形态没有动作资源」，于是 7 个动作一个都不显示（资源其实都在，
// loadVecBackend 已经把 action_N / exp_N 目录扫进 vec.animSub 了）。
func (f *FrameSet) animIDs(kind int) []int {
	if f == nil {
		return nil
	}
	name := "action"
	if kind == 2 {
		name = "exp"
	}
	if f.vec != nil {
		f.vec.animMu.Lock()
		ids := make([]int, 0, len(f.vec.animSub))
		for k := range f.vec.animSub {
			if !strings.HasPrefix(k, name+":") {
				continue
			}
			n, err := strconv.Atoi(k[len(name)+1:])
			if err != nil || n <= 0 {
				continue
			}
			ids = append(ids, n)
		}
		f.vec.animMu.Unlock()
		sort.Ints(ids)
		return ids
	}
	m := f.actionFiles
	if kind == 2 {
		m = f.expFiles
	}
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (f *FrameSet) hasAction(id int) bool {
	if f.vec != nil {
		return f.vec.getAnim(1, id) != nil
	}
	_, ok := f.actionFiles[id]
	return ok
}

func (f *FrameSet) hasExp(id int) bool {
	if f.vec != nil {
		return f.vec.getAnim(2, id) != nil
	}
	_, ok := f.expFiles[id]
	return ok
}

func (f *FrameSet) actionFrames(id int) []*image.RGBA {
	if f.vec != nil {
		a := f.vec.getAnim(1, id)
		if a == nil {
			logf("vec action %d 未加载（已有 %v）", id, len(f.vec.actions))
			return nil
		}
		// 矢量后端不再缓存整段帧切片：帧是按需现渲染的（见 vecAnim.frame）。
		// 这里返回 nil 只是为了让 PNG 分支语义保持唯一入口，帧数走 animCount。
		logf("vec action %d: %d 帧（按需渲染）", id, a.nframes)
		return nil
	}
	return f.anim(f.actionFiles[id], 1_000_000+id)
}

// contentRect 返回非透明像素的包围盒（图内坐标）。空图返回零矩形。
func contentRect(img *image.RGBA) image.Rectangle {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+b.Dx()*4]
		for x := b.Min.X; x < b.Max.X; x++ {
			if row[(x-b.Min.X)*4+3] > 12 {
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
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX+1, maxY+1)
}

// animFrameBBox 返回动作/表情第 idx 帧的内容包围盒（画布坐标 ×倍率）。
// 只用于日志定位；数据来自 sprite.json 的帧 bbox，不再依赖渲染出的位图。
func (f *FrameSet) animFrameBBox(kind, id, idx int) image.Rectangle {
	if f.vec == nil {
		return image.Rectangle{}
	}
	var a *vecAnim
	if kind == 1 {
		a = f.vec.getAnim(1, id)
	} else {
		a = f.vec.getAnim(2, id)
	}
	if a == nil {
		return image.Rectangle{}
	}
	bb := a.frameBBox(idx)
	if bb == nil {
		return image.Rectangle{}
	}
	return image.Rect(
		int(bb[0]*a.scale), int(bb[1]*a.scale),
		int((bb[0]+bb[2])*a.scale), int((bb[1]+bb[3])*a.scale),
	)
}

// animBodyC 返回动作/表情第 0 帧的本体中心（sprite 画布坐标）。
// PNG 后端没有这个标注，返回零值让调用方退回贴底居中。
// animFrame 取动作/表情第 idx 帧的位图与「相对 clip 原点」的偏移（4x 像素）。
// 矢量后端每次现渲染（不缓存整段），PNG 后端走原有帧切片。
func (f *FrameSet) animFrame(kind, id, idx int) (*image.RGBA, [2]float64) {
	if f.vec == nil {
		var fr []*image.RGBA
		if kind == 1 {
			fr = f.actionFrames(id)
		} else {
			fr = f.expFrames(id)
		}
		if idx < 0 || idx >= len(fr) {
			return nil, [2]float64{}
		}
		return fr[idx], [2]float64{}
	}
	var a *vecAnim
	if kind == 1 {
		a = f.vec.getAnim(1, id)
	} else {
		a = f.vec.getAnim(2, id)
	}
	return a.frame(idx)
}

// animClipWH 返回动作/表情的跨帧并集（clip）像素尺寸，已含倍率。
//
// 窗口必须按 clip 算、不能按单帧图算：单帧图是 bbox 裁剪的，逐帧大小不同，
// 用它决定窗口会让窗口每帧 resize（实测拉到 3126x3961，单帧 367ms）。
// clip 对一段动画是常量，窗口因此只在这一段开始时定一次。
func (f *FrameSet) animClipWH(kind, id int) [2]float64 {
	if f.vec == nil {
		return [2]float64{}
	}
	var a *vecAnim
	if kind == 1 {
		a = f.vec.getAnim(1, id)
	} else {
		a = f.vec.getAnim(2, id)
	}
	if a == nil {
		return [2]float64{}
	}
	return [2]float64{a.clip[2] * a.scale, a.clip[3] * a.scale}
}

// animClipOrigin 返回 clip 原点在画布中的位置（1x 坐标）。
func (f *FrameSet) animClipOrigin(kind, id int) [2]float64 {
	if f.vec == nil {
		return [2]float64{}
	}
	var a *vecAnim
	if kind == 1 {
		a = f.vec.getAnim(1, id)
	} else {
		a = f.vec.getAnim(2, id)
	}
	if a == nil {
		return [2]float64{}
	}
	return [2]float64{a.clip[0], a.clip[1]}
}

// animSetVisible 把窗口可见区（clip 坐标 4x 像素）告诉矢量后端，
// 让渲染范围也只取可见部分。参数是 4x 像素，内部换算成 1x 素材坐标。
func (f *FrameSet) animSetVisible(kind, id int, x, y, w, h int, scale float64) {
	if f.vec == nil || scale <= 0 {
		return
	}
	var a *vecAnim
	if kind == 1 {
		a = f.vec.getAnim(1, id)
	} else {
		a = f.vec.getAnim(2, id)
	}
	if a == nil {
		return
	}
	// 必须用该段自己的渲染倍率 a.scale，不能用调用方传进来的 sc/zoom：
	// x/y/w/h 是屏幕像素（offX/offY/w/h），而 vis 要的是素材 1x 坐标。
	// 传 sc/zoom 时实测得到 1（scale=zoom=4），vis 被放大 4 倍
	// （y 225 → 900），与帧内容彻底不重叠 → 每帧都判「窗口外」→ 缓冲永远空
	// → 动作画面连续冻结 204 帧。
	// 2026-10-26 修正：s 必须用**显示倍率**（调用方传 sc），不能用素材倍率 a.scale。
	// x/y/w/h 是屏幕像素，clip 与 bbox 是 1x 素材坐标，换算只需要除以显示倍率。
	// 之前固定除 a.scale（4）在 Scale=4 时恰好也对（sc==zoom==4），
	// 但 Scale=1.5 时 vis 被算成正确值的 1/2.67：本体中心 387 → vis 只到 199，
	// 每帧 bbox 都判「窗口外」→ 返回 nil → 画面冻结 64 帧（子渊反馈「动作不显示」）。
	s := scale
	if s <= 0 {
		s = 1
	}
	r := [4]float64{
		float64(x) / s, float64(y) / s,
		float64(w) / s, float64(h) / s,
	}
	a.setVisible(r)
}

// animSetPinAll 开关某段动画的「整段驻留」（关机循环用，20261029-01）。
// PNG 后端不需要处理：整段切片本来就已经解在内存里。
func (f *FrameSet) animSetPinAll(kind, id int, v bool) {
	if f.vec == nil {
		return
	}
	var a *vecAnim
	if kind == 1 {
		a = f.vec.getAnim(1, id)
	} else {
		a = f.vec.getAnim(2, id)
	}
	if a != nil {
		a.setPinAll(v)
	}
}

// animStopWorker 停掉该动作/表情的后台渲染 goroutine（播放结束时调用）。
// 不停的话每次播动作都会留一个空转 goroutine，长跑必然泄漏。
func (f *FrameSet) animStopWorker(kind, id int) {
	if f.vec == nil {
		return
	}
	var a *vecAnim
	if kind == 1 {
		a = f.vec.getAnim(1, id)
	} else {
		a = f.vec.getAnim(2, id)
	}
	a.stopWorker()
}

// animMiss 返回该动作/表情「播放到帧时还没渲染好」的累计次数（冒烟诊断用）。
func (f *FrameSet) animMiss(kind, id int) int {
	if f.vec == nil {
		return 0
	}
	var a *vecAnim
	if kind == 1 {
		a = f.vec.getAnim(1, id)
	} else {
		a = f.vec.getAnim(2, id)
	}
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.miss
}

// animFreeze 返回（冻结次数、最长连续冻结帧数）。
func (f *FrameSet) animFreeze(kind, id int) (int, int) {
	if f.vec == nil {
		return 0, 0
	}
	var a *vecAnim
	if kind == 1 {
		a = f.vec.getAnim(1, id)
	} else {
		a = f.vec.getAnim(2, id)
	}
	if a == nil {
		return 0, 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.frozen, a.frozenMax
}

// animVisChanges 返回可见区被重置的次数（诊断用）。
func (f *FrameSet) animVisChanges(kind, id int) int {
	if f.vec == nil {
		return 0
	}
	var a *vecAnim
	if kind == 1 {
		a = f.vec.getAnim(1, id)
	} else {
		a = f.vec.getAnim(2, id)
	}
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.visChanges
}

// animCount 返回动作/表情在矢量后端的帧数（PNG 后端返回 0，调用方回退到切片）。
func (f *FrameSet) animCount(kind, id int) int {
	if f.vec == nil {
		return 0
	}
	var a *vecAnim
	if kind == 1 {
		a = f.vec.getAnim(1, id)
	} else {
		a = f.vec.getAnim(2, id)
	}
	if a == nil {
		return 0
	}
	return a.nframes
}

func (f *FrameSet) animBodyC(kind, id int) [2]float64 {
	if f.vec == nil {
		return [2]float64{}
	}
	switch kind {
	case 1:
		if a := f.vec.getAnim(1, id); a != nil {
			return a.bodyc
		}
	case 2:
		if a := f.vec.getAnim(2, id); a != nil {
			return a.bodyc
		}
	}
	return [2]float64{}
}

func (f *FrameSet) expFrames(id int) []*image.RGBA {
	if f.vec != nil {
		// 矢量后端不再提供整段帧切片（那是几百 MB 的位图缓存）。
		return nil
	}
	return f.anim(f.expFiles[id], 2_000_000+id)
}

// anim 缓存当前正在播放的动作/表情，换下一个时旧帧自然被 GC 回收。
func (f *FrameSet) anim(files []string, key int) []*image.RGBA {
	if len(files) == 0 {
		return nil
	}
	if f.animCache != nil && f.animKey == key {
		return f.animCache
	}
	f.animCache = decodeAll(files)
	f.animKey = key
	return f.animCache
}

// releaseAnim 动作播放结束后释放帧，降低常驻内存。
func (f *FrameSet) releaseAnim() {
	f.animCache = nil
	f.animKey = 0
}

// dropCaches 立刻释放该形态已缓存的位图（矢量 4x 帧 / PNG 解码帧 / 待机浮动帧）。
//
// 只在 trimFormCache 判定「这套形态短期内不会切回来」时调用；FrameSet 本身仍可用，
// 下次取帧会重新渲染。之所以要主动清而不是等 GC：一整套超能形态的 4x 帧是几十 MB，
// 而 g.FS 之外没人引用它们，清了 GC 才能立刻回收。
func (f *FrameSet) dropCaches() {
	if f == nil {
		return
	}
	f.idleCache = nil
	f.animCache = nil
	f.animKey = 0
	f.idleAnimCache = nil
	if f.vec != nil {
		f.vec.dropAll()
	}
}

// ---------- 矢量素材后端（NONO_VEC=1） ----------

// vecBackend 用矢量精灵实时渲染待机帧，对上层暴露与 FrameSet 相同的行为：
// 按方向返回 []*image.RGBA，按需渲染并缓存最近两个方向。
type vecBackend struct {
	renderer  *sprite.Renderer
	scale     float64
	dirFrames map[string]int
	// idleClip 是「跨方向跨帧 bbox 并集」，画布坐标（1x 素材像素）。
	// 待机每帧都按这个矩形渲染，输出恒为同尺寸：逐帧 bbox 会让帧图大小
	// 在 97~102 x 115~118 之间跳，球心随之左右漂 2~3px（R3 抖动根因）。
	idleClip [4]float64
	// idleSS 待机超采样倍数（按 idleClip 面积决定，见 pickSS）。
	idleSS int
	tintRGB uint32 // 当前已应用的玩家色，避免每帧重复清缓存
	// tintSet 区分「还没设过色」（零值 0）与「设成了钢琴黑 #000000」。
	// 缺了它，首次 setTint(0) 会命中 tintRGB==rgb 短路，渲染器永远收不到黑色。
	tintSet bool
	// 渐变染色状态（自定义渐变芯片）。gradSet 与 tintSet 同理，避免零值短路。
	gradSet  bool
	gradA    uint32
	gradB    uint32
	gradMode int

	mu    sync.Mutex
	cache      map[string][]*image.RGBA
	cacheOrder []string

	actions map[int]*vecAnim
	exps    map[int]*vecAnim
	// animSub 记录每个动作/表情的素材目录，key "action:7" / "exp:2"。
	// 只在第一次播放时按需解析（懒加载），切形态不碰这些目录。
	animSub map[string]string
	// animMu 保护 actions/exps/animSub：懒加载可能发生在预取 worker 的
	// goroutine 里，与主线程查询并发。
	animMu sync.Mutex
	// renderMu 串行化 raster 调用：补帧 goroutine 与主线程可能同时渲染。
	renderMu sync.Mutex
	// dispSc/dispZoom 是「4x 渲染结果 → 显示尺寸」的换算参数，取自
	// Save.Scale 与 Meta.Zoom。两者都 >0 时缓存里存的是显示图（≈204KB/帧），
	// 不再是 4x 中间图（1.45MB/帧，7 倍差）。零值 = 旧行为（存 4x）。
	// 锁保护：fillDir 在后台读，绘制端每 tick 比较。
	dispSc   float64
	dispZoom float64
	// key 形态名（plain/super1..5），懒加载时用于日志。
	key string
}

// prescale 报告待机帧是否已按显示尺寸预缩（缓存里是不是显示图）。
func (v *vecBackend) prescale() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.dispSc > 0 && v.dispZoom > 0
}

// syncDispScale 同步预缩放参数；参数变化时整表作废（旧帧尺寸已经不对）。
//
// 为什么必须清空而不是原地保留：帧的像素尺寸变了，绘制端是按图宽反推
// 显示尺寸的（frameW = img.Dx()），留着旧尺寸的帧会得到「窗口按旧图、
// 图片按新尺寸」的错配，本体一闪大一帧小一帧。
func (v *vecBackend) syncDispScale(sc, zoom float64) {
	if v == nil || sc <= 0 || zoom <= 0 {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.dispSc == sc && v.dispZoom == zoom {
		return
	}
	v.dispSc, v.dispZoom = sc, zoom
	v.cache = map[string][]*image.RGBA{}
	v.cacheOrder = nil
}

// dispParams 返回当前的预缩放参数（后台补帧 goroutine 也读，故加锁）。
func (v *vecBackend) dispParams() (float64, float64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.dispSc, v.dispZoom
}

// shrinkTo 把 4x 渲染结果缩到显示尺寸。取整口径必须与绘制端的
// frameW = int(w4 * sc / zoom) 逐位一致——差 1px 就会在贴图那一步
// 触发第二次重采样，等于白做。
func shrinkTo(img *image.RGBA, sc, zoom float64) *image.RGBA {
	if img == nil || sc <= 0 || zoom <= 0 {
		return img
	}
	b := img.Bounds()
	w := int(float64(b.Dx()) * sc / zoom)
	h := int(float64(b.Dy()) * sc / zoom)
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return resizeFrame(img, w, h)
}

// setTint 把玩家色同步给渲染器。颜色没变时直接返回（不清缓存）。
// 染色只作用于 tintable 层，见 pkg/sprite.Renderer.SetTint。
func (v *vecBackend) setTint(rgb uint32) {
	if v == nil || v.renderer == nil {
		return
	}
	v.mu.Lock()
	if v.tintSet && v.tintRGB == rgb {
		v.mu.Unlock()
		return
	}
	v.tintRGB = rgb
	v.tintSet = true
	v.mu.Unlock()

	v.renderer.SetTint(rgb)
	// 动作/表情段各有独立渲染器（各自一个 sprite.json），也要一起换色，
	// 否则待机是玩家色、一放动作就变回白色。
	for _, a := range v.actions {
		a.setTint(rgb)
	}
	for _, a := range v.exps {
		a.setTint(rgb)
	}

	v.mu.Lock()
	v.cache = map[string][]*image.RGBA{}
	v.mu.Unlock()
}

// setGradient 开启渐变染色并同步给所有动作/表情渲染器。
// 参数没变时直接返回，避免每帧清缓存。
func (v *vecBackend) setGradient(a, b uint32, mode int) {
	if v == nil || v.renderer == nil {
		return
	}
	v.mu.Lock()
	if v.gradSet && v.gradA == a && v.gradB == b && v.gradMode == mode {
		v.mu.Unlock()
		return
	}
	v.gradA, v.gradB, v.gradMode, v.gradSet = a, b, mode, true
	v.mu.Unlock()

	v.renderer.SetGradient(a, b, mode)
	for _, an := range v.actions {
		an.setGradient(a, b, mode)
	}
	for _, an := range v.exps {
		an.setGradient(a, b, mode)
	}

	v.mu.Lock()
	v.cache = map[string][]*image.RGBA{}
	v.mu.Unlock()
}

// clearGradient 关掉渐变，回到单色染色。
func (v *vecBackend) clearGradient() {
	if v == nil || v.renderer == nil {
		return
	}
	v.mu.Lock()
	if !v.gradSet {
		v.mu.Unlock()
		return
	}
	v.gradSet = false
	v.mu.Unlock()
	v.renderer.ClearGradient()
	for _, an := range v.actions {
		an.clearGradient()
	}
	for _, an := range v.exps {
		an.clearGradient()
	}
	v.mu.Lock()
	v.cache = map[string][]*image.RGBA{}
	v.mu.Unlock()
}

type vecFrame struct {
	img *image.RGBA
	off [2]float64
}

// 预取窗口参数。
//
// 24fps 每帧 41.67ms，单帧渲染实测平均 9.5ms、最慢 32.5ms（bench）。提前
// vecAhead 帧 = 250ms 缓冲，足够覆盖渲染抖动，播放路径永远不用等渲染。
// 内存：帧是「该帧 bbox ∩ 可见区」的小图（上限 1036x1024 = 4.2MB），
// 6 帧最多约 25MB —— 与旧版整画布缓存（单帧 54.9MB × 12 帧 = 658MB）
// 不是同一个量级，所以小窗口预取在这里是划算的。
const (
	// 12 帧 = 500ms 缓冲。实测 6 帧时动作里仍有 45 次「没渲好」，12 帧给足余量。
	// 内存上限 12 × 1036×1024×4B ≈ 50MB，仍远低于旧版 658MB。
	vecAhead  = 12
	vecBehind = 2
)

// vecScreenW/H 是当前虚拟屏幕的逻辑尺寸，由 main 在 draw 时更新。
// 矢量后端在「窗口位置还没确定」（动作刚开播、首帧渲染）时需要它来估算
// 可见区上界：实测不估算会渲染整个 clip（937×912×16 = 13.7Mpx ≈ 400ms），
// 首帧就晚 10 个 tick 才出现。
var vecScreenW, vecScreenH = 1280, 1024


// vecTintFloor 是染色后的亮度保底（相对基色的 0-255 比例）。
//
// 变色芯片是纯乘法染色：纯白球壳 × #000000 = 0，钢琴黑就是一块死黑，
// 球面明暗、边缘过渡全被吃掉。按基色保留约 6% 亮度，黑仍是黑，但不再「挖洞」，
// 观感更接近原版那种带反光的钢琴黑（子渊 2026-10-27 反馈）。
// 取 16：对白基色等于 #101010；再高（26+）就发灰，不像黑。
const vecTintFloor = 16

// vecFirstFrameGrace 是首帧等待的 tick 数上限（约 40 tick ≈ 1.6s）。
// 超过还没渲好就退回同步渲染兜底，避免 worker 异常时永久不出画面。
const vecFirstFrameGrace = 40

// vecWorkerCount 返回渲染 worker 数：最多 3，且永远给 UI 线程留至少 1 核。
//
// 单帧平均 9.5ms、最慢 32.5ms，4 核机器开 3 路能把追赶速度提到约 3 倍；
// 但硬编码 3 在 2 核机器上会和 UI 抢核，反而让 tick 变长（24fps 要的是
// 每 40ms 交出一次绘制，不是渲染越快越好）。
func vecWorkerCount() int {
	n := runtime.NumCPU() - 1
	// 上限从 3 提到 6（2026-10-26）：动作 ss 从 1 提到 3 后单帧渲染成本涨了
	// 约 5~9 倍，3 路 worker 追不上 24fps，实测 super5/action_7 冻 45 帧。
	// 9800X3D 16 核开 6 路毫无压力；低核机器仍受 NumCPU-1 限制，不会抢 UI 核。
	if n > 6 {
		n = 6
	}
	if n < 1 {
		n = 1
	}
	return n
}

// vecAnim 一段一次性播放的动画（动作/表情）。
//
// 帧策略（2026-10-25 最终版）：
//   - 不缓存整段位图（旧版 658MB）；
//   - 每帧只渲染「该帧 bbox ∩ 窗口可见区」，画完进小窗口（buf）；
//   - 渲染在后台 goroutine 里做，播放路径只取现成的图，取不到就用上一帧，
//     绝不阻塞 tick（同步渲染最慢 32.5ms，会吃掉整个 41.67ms 帧预算）；
//   - 窗口外的帧立即释放，播放结束 stopWorker 退出 goroutine。
type vecAnim struct {
	renderer *sprite.Renderer
	nframes  int
	scale    float64
	// clip 是跨帧并集（画布 1x 坐标），决定窗口尺寸与坐标原点。
	clip [4]float64
	// bodyc 是本体中心相对 clip 原点（4x 像素）。
	bodyc [2]float64
	// bboxes 是每帧几何包围盒（画布 1x 坐标），只存几何不存位图。
	bboxes [][4]float64

	mu      sync.Mutex
	cur     int
	buf     map[int]vecFrame
	busy    map[int]bool
	vis     [4]float64
	running bool
	stop    chan struct{}
	lastIdx int
	lastImg *image.RGBA
	lastOff [2]float64
	// miss 记录「播放到该帧时还没渲染好」的次数，用于冒烟判断预取是否够快。
	miss int
	// visChanges 记录可见区被重置的次数：正常一段动画只该变 1~2 次，
	// 频繁变说明 offX/w 不稳定，缓冲会被反复清空。
	visChanges int
	// frozen 记录「实际显示的帧号 != 应显示帧号」的次数，run 是当前连续次数，
	// frozenMax 是历史最长连续冻结。命中缓冲时两者都为 0，能直接看出画面卡不卡。
	frozen, run, frozenMax int
	// pinAll 为 true 时整段帧常驻（不清窗口外的帧），并把可见区固定为整个 clip。
	// 只给「关机循环」用（20261029-01）：默认策略是 cur-2..cur+12 的滑动窗口，
	// 循环播放每圈回绕到 0 都会把末尾帧删掉重渲，一圈就白渲染一次。
	// 代价是整段位图驻留内存（实测 super1/action_3 165 帧约 74MB，见说明.md）。
	pinAll bool
}

// frameRect 返回该帧的渲染矩形（clip 坐标系 1x），退化时用整块 clip。
func (a *vecAnim) frameRect(idx int) [4]float64 {
	if bb := a.frameBBox(idx); bb != nil {
		// 外扩 1px：描边/抗锯齿会略微溢出几何 bbox
		return [4]float64{bb[0] - 1, bb[1] - 1, bb[2] + 2, bb[3] + 2}
	}
	return a.clip
}

// frameBBox 返回第 idx 帧的几何包围盒（画布 1x 坐标）。
func (a *vecAnim) frameBBox(idx int) []float64 {
	if a == nil || idx < 0 || idx >= len(a.bboxes) {
		return nil
	}
	bb := a.bboxes[idx]
	if bb[2] <= 0 || bb[3] <= 0 {
		return nil
	}
	return bb[:]
}

// setVisible 设置窗口可见区（clip 坐标系 1x）。可见区变了，已渲染的图可能
// 覆盖不到新的可见范围，直接丢弃重渲。
// pinAll（关机循环）期间忽略：整段常驻时若跟着窗口/缩放改可见区，一圈就会被
// 清空重渲一次，缓存等于没有。
func (a *vecAnim) setVisible(rect [4]float64) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.pinAll {
		a.mu.Unlock()
		return
	}
	if a.vis != rect {
		a.vis = rect
		a.buf = map[int]vecFrame{}
		a.lastImg = nil
		a.visChanges++
		if a.visChanges <= 3 || a.visChanges%50 == 0 {
			logf("vecVis 变更为 %.1f,%.1f %.1fx%.1f (第 %d 次)", rect[0], rect[1], rect[2], rect[3], a.visChanges)
		}
	}
	a.mu.Unlock()
}

// setPinAll 打开/关闭「整段驻留」（见 vecAnim.pinAll）。
// 打开时把可见区固定成整块 clip 并清空缓冲，worker 会把 0..nframes 全渲一遍；
// 关闭时清空缓冲，回到 cur-2..cur+12 的滑动窗口策略。
func (a *vecAnim) setPinAll(v bool) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.pinAll == v {
		a.mu.Unlock()
		return
	}
	a.pinAll = v
	if v {
		a.vis = a.clip
	}
	a.buf = map[int]vecFrame{}
	a.lastImg = nil
	a.frozen, a.run = 0, 0
	nf := a.nframes
	a.mu.Unlock()
	logf("vecPinAll=%v nframes=%d", v, nf)
	if v {
		a.startWorker()
	}
}

// setTint 换色。帧不再长期缓存，清掉已渲染的即可。
func (a *vecAnim) setGradient(g1, g2 uint32, mode int) {
	if a == nil || a.renderer == nil {
		return
	}
	a.renderer.SetGradient(g1, g2, mode)
	a.mu.Lock()
	a.buf = map[int]vecFrame{}
	a.lastImg = nil
	a.mu.Unlock()
}

func (a *vecAnim) clearGradient() {
	if a == nil || a.renderer == nil {
		return
	}
	a.renderer.ClearGradient()
	a.mu.Lock()
	a.buf = map[int]vecFrame{}
	a.lastImg = nil
	a.mu.Unlock()
}

func (a *vecAnim) setTint(rgb uint32) {
	if a == nil || a.renderer == nil {
		return
	}
	a.renderer.SetTint(rgb)
	a.mu.Lock()
	a.buf = map[int]vecFrame{}
	a.lastImg = nil
	a.mu.Unlock()
}

// frame 返回第 idx 帧的位图与其相对 clip 原点的偏移（4x 像素）。
//
// 返回的图生命周期只到下一次 frame 调用为止，调用方必须在本次绘制内用完。
func (a *vecAnim) frame(idx int) (*image.RGBA, [2]float64) {
	if a == nil || a.nframes == 0 || a.renderer == nil {
		return nil, [2]float64{}
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= a.nframes {
		idx = a.nframes - 1
	}
	a.mu.Lock()
	if a.cur != idx {
		a.cur = idx
	}
	if f, ok := a.buf[idx]; ok {
		a.lastIdx, a.lastImg, a.lastOff = idx, f.img, f.off
		a.run = 0
		a.mu.Unlock()
		return f.img, f.off
	}
	last, lastOff := a.lastImg, a.lastOff
	a.mu.Unlock()

	a.startWorker()
	a.mu.Lock()
	a.miss++
	m := a.miss
	a.mu.Unlock()

	if last != nil {
		// 还没渲染好：先用上一帧顶着，本 tick 不阻塞。
		a.mu.Lock()
		a.frozen++
		a.run++
		if a.run > a.frozenMax {
			a.frozenMax = a.run
		}
		a.mu.Unlock()
		return last, lastOff
	}
	// 首帧也交给 worker：返回 nil 让绘制端跳过本次绘制，窗口保留上一帧画面
	// （待机图），worker 渲好后自然接上。同步渲染最慢 32.5ms，正好吃掉一个
	// 完整帧预算，没必要为了「第一帧立刻出现」牺牲整段动画的节奏。
	//
	// 兜底：worker 若因异常一直渲不出来，miss 会持续累加；超过首帧超时阈值
	// 就退回同步渲染，宁可卡一次也不能永久黑屏。
	if m > vecFirstFrameGrace {
		return a.renderInto(idx)
	}
	return nil, [2]float64{}
}

// startWorker 启动后台渲染 goroutine（幂等）。
func (a *vecAnim) startWorker() {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return
	}
	a.running = true
	a.stop = make(chan struct{})
	a.buf = map[int]vecFrame{}
	a.busy = map[int]bool{}
	a.mu.Unlock()
	for i := 0; i < vecWorkerCount(); i++ {
		go a.loop()
	}
}

// stopWorker 停止后台渲染并释放缓冲。播放结束（idle）时调用。
func (a *vecAnim) stopWorker() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return
	}
	a.running = false
	close(a.stop)
	a.stop = nil
	a.buf = map[int]vecFrame{}
	a.busy = map[int]bool{}
	a.lastImg = nil
	// pinAll 必须跟着复位：残留 true 会让下次播放同一段时无条件整段缓存，
	// 白白多吃几十 MB（关机循环 → 开机 → 再点关机才会重新设置）。
	a.pinAll = false
	a.mu.Unlock()
}

// loop 是后台渲染循环：始终把「当前播放帧 .. 当前 + vecAhead」补齐，
// 并释放窗口外的帧。渲染慢于播放时它自然落后，播放端用上一帧顶住。
func (a *vecAnim) loop() {
	for {
		a.mu.Lock()
		stop := a.stop
		cur := a.cur
		vis := a.vis
		pin := a.pinAll
		a.mu.Unlock()
		if stop == nil {
			logf("vecWorker 退出：stop 已被置空")
			return
		}
		select {
		case <-stop:
			logf("vecWorker 退出：收到 stop")
			return
		default:
		}

		lo := cur - vecBehind
		if lo < 0 {
			lo = 0
		}
		hi := cur + vecAhead
		if hi > a.nframes {
			hi = a.nframes
		}
		if pin {
			// 关机循环：整段常驻，回绕到 0 时不会再把末尾帧删掉。
			lo, hi = 0, a.nframes
		}

		a.mu.Lock()
		for k := range a.buf {
			if k < lo || k >= hi {
				delete(a.buf, k)
			}
		}
		next := -1
		// 从 lo 开始扫（原来从 cur 开始）：pinAll 的第一轮如果 cur 停在段中，
		// 只从 cur 往后渲会让 0..cur-1 永远没有帧，回绕时整圈都 miss。
		// 默认滑动窗口下 lo=cur-2，最多多检查 2 帧，等价无损。
		for i := lo; i < hi; i++ {
			if _, ok := a.buf[i]; ok {
				continue
			}
			if a.busy[i] {
				continue // 别的 worker 正在渲这一帧
			}
			next = i
			break
		}
		if next >= 0 {
			a.busy[next] = true
		}
		a.mu.Unlock()

		if next < 0 {
			select {
			case <-stop:
				logf("vecWorker 退出：空闲收到 stop")
				return
			case <-time.After(3 * time.Millisecond):
			}
			continue
		}
		img, off := a.renderOne(next, vis)
		a.mu.Lock()
		if img != nil {
			a.buf[next] = vecFrame{img: img, off: off}
		}
		delete(a.busy, next)
		a.mu.Unlock()
	}
}

// renderInto 同步渲染一帧并写入缓冲（首帧兜底用）。
func (a *vecAnim) renderInto(idx int) (*image.RGBA, [2]float64) {
	a.mu.Lock()
	vis := a.vis
	a.mu.Unlock()
	img, off := a.renderOne(idx, vis)
	if img != nil {
		a.mu.Lock()
		a.buf[idx] = vecFrame{img: img, off: off}
		a.lastIdx, a.lastImg, a.lastOff = idx, img, off
		a.mu.Unlock()
	}
	return img, off
}

// renderOne 渲染第 idx 帧的「bbox ∩ 可见区」，返回图与相对 clip 原点的偏移。
func (a *vecAnim) renderOne(idx int, vis [4]float64) (*image.RGBA, [2]float64) {
	rect := a.frameRect(idx)
	if vis[2] <= 0 || vis[3] <= 0 {
		// 动作刚开播、窗口位置尚未确定：用「以本体为中心、屏幕大小」作上界。
		// 绝不退化成整块 clip——那是 13.7Mpx，首帧要 400ms。
		sw := float64(vecScreenW) / a.scale
		sh := float64(vecScreenH) / a.scale
		cx := a.bodyc[0] / a.scale
		cy := a.bodyc[1] / a.scale
		vis = [4]float64{cx - sw/2, cy - sh/2, sw, sh}
	}
	if vis[2] > 0 && vis[3] > 0 {
		x0 := math.Max(rect[0], vis[0])
		y0 := math.Max(rect[1], vis[1])
		x1 := math.Min(rect[0]+rect[2], vis[0]+vis[2])
		y1 := math.Min(rect[1]+rect[3], vis[1]+vis[3])
		if x1 <= x0 || y1 <= y0 {
			return nil, [2]float64{} // 整帧都在窗口外：没有可见像素
		}
		rect = [4]float64{x0, y0, x1 - x0, y1 - y0}
	}
	// ss 固定 1：4x 素材在屏幕 1:1 显示，不需要超采样，边缘交给 raster 的
	// 1/64 像素覆盖采样（那本身就是抗锯齿）。ss>1 会让中间画布按平方膨胀。
	// 动态超采样：按「渲染区域在屏幕上的实际像素」决定 ss。
	//
	// 背景：动作帧原来是 ss=1，边缘硬台阶（子渊要求消除锯齿）；但一律上 3 会
	// 翻车——super5/action_7 的可见区到 1280x1024，×3 内部是 3840x3072 的
	// 光栅化，单帧几百毫秒，6 路 worker 也追不上 24fps（实测冻 34 帧）。
	// 折中：小区域保 ss=3（看得清），大区域自动降级（先保证播得动）。
	// 大动作本身细节多、画面大，降采样后锯齿在肉眼里远不如小图明显。
	ss := 3
	if px := math.Max(rect[2], rect[3]) * a.scale; px > 380 {
		ss = 2
		// 1600 阈值实测无效：1280px 的区域 ×2 内部仍是 2560x2048，单帧
		// 光栅化几 百毫秒，6 路 worker 追不上 24fps（action_7 冻 29 帧），
		// 所以维持在 900 提前降级，宁可大动作少一点采样也要保证播得动。
		if px > 900 {
			ss = 1
		}
	}
	img, err := a.renderer.RenderClip("default", idx, a.scale, ss, rect)
	if err != nil {
		return nil, [2]float64{}
	}
	return img, [2]float64{
		(rect[0] - a.clip[0]) * a.scale,
		(rect[1] - a.clip[1]) * a.scale,
	}
}

func (v *vecBackend) count(dir string) int { return v.dirFrames[dir] }

func (v *vecBackend) frames(dir string) []*image.RGBA {
	v.mu.Lock()
	if fr, ok := v.cache[dir]; ok {
		v.mu.Unlock()
		return fr
	}
	n := v.dirFrames[dir]
	out := make([]*image.RGBA, n)
	// 同步只渲染第 0 帧，其余交给后台。
	//
	// 2026-10-26 子渊 R2 打回：「鼠标跟随也会很卡，超能1级形态的跟随没怎么卡，
	// 但是2级开始，鼠标换一个方向它要卡好几秒才会跟过去」。根因就在这里：
	// 原来切方向要同步渲染整段 17 帧（ss=3），plain/super1 帧小所以只顿几十 ms，
	// super2 起帧变大、部件变多，17 帧就是几百 ms 到几秒 → 换方向的瞬间整个
	// 卡死。现在切方向只等 1 帧（体感接近实时），其余帧后台补齐。
	if n > 0 {
		t0 := time.Now()
		v.renderMu.Lock()
		img, err := v.renderer.RenderClip(dir, 0, v.scale, v.idleSS, v.idleClip)
		v.renderMu.Unlock()
		if err == nil {
			// 这里已在 v.mu 保护区内（frames 全程持锁），不能调 dispParams()
			// ——它会再 Lock 一次，Go 的 Mutex 不可重入，直接死锁。
			out[0] = shrinkTo(img, v.dispSc, v.dispZoom)
			dumpIdleFrame(dir, 0, out[0])
		} else {
			logf("vec frames %s/%s 首帧失败: %v", v.key, dir, err)
		}
		logf("vec frames %s/%s n=%d firstMs=%d", v.key, dir, n, time.Since(t0)/1e6)
	}
	// 方向帧全驻留（8 方向 × 19 帧 × ~100x120x4B ≈ 7MB，super5 最大约 17MB）。
	//
	// 原来只留「当前 + 上一次」两个方向：鼠标来回扫一下就要重渲整方向，
	// 而 super2+ 单帧 ss=3 要 250~280ms → 感觉像卡死。全驻留后方向来回切是
	// 纯查表。（内存上限见 vecMaxDirCache 注释，超了按最后使用淘汰。）
	v.cache[dir] = out
	v.cacheOrder = append(v.cacheOrder, dir)
	if len(v.cacheOrder) > 8 {
		old := v.cacheOrder[0]
		v.cacheOrder = v.cacheOrder[1:]
		if old != dir {
			delete(v.cache, old)
		}
	}
	v.mu.Unlock()
	go v.fillDir(dir, out)
	return out
}

// dropAll 清空矢量后端的待机帧缓存与动作/表情缓冲（形态被淘汰时调用）。
//
// 会把正在跑的动作预取 worker 停掉：不清的话 worker 还在往一张没人看的
// 缓冲里渲染（2026-10-26 那次「切形态后所有面板都卡」就是这个泄漏）。
func (v *vecBackend) dropAll() {
	if v == nil {
		return
	}
	v.mu.Lock()
	v.cache = map[string][]*image.RGBA{}
	v.cacheOrder = nil
	v.mu.Unlock()

	v.animMu.Lock()
	anims := make([]*vecAnim, 0, len(v.actions)+len(v.exps))
	for _, a := range v.actions {
		anims = append(anims, a)
	}
	for _, a := range v.exps {
		anims = append(anims, a)
	}
	v.actions = map[int]*vecAnim{}
	v.exps = map[int]*vecAnim{}
	v.animMu.Unlock()
	for _, a := range anims {
		a.stopWorker()
		a.mu.Lock()
		a.buf = nil
		a.lastImg = nil
		a.mu.Unlock()
	}
}

// fillDir 后台把某方向剩余帧补齐。out 与播放端共享：写入的是切片元素指针，
// 播放端读到 nil 时会回退到最近的有效帧（见 main.go 的 idleFrameAt）。
func (v *vecBackend) fillDir(dir string, out []*image.RGBA) {
	sc, zoom := v.dispParams()
	for i := 1; i < len(out); i++ {
		v.renderMu.Lock()
		img, err := v.renderer.RenderClip(dir, i, v.scale, v.idleSS, v.idleClip)
		v.renderMu.Unlock()
		if err != nil {
			continue
		}
		img = shrinkTo(img, sc, zoom)
		dumpIdleFrame(dir, i, img)
		v.mu.Lock()
		out[i] = img
		v.mu.Unlock()
		// 每帧让出 CPU 一下：补帧是后台工作，不能把主线程换方向的首帧挤到队尾
		// （实测串行抢锁时 right 首帧从 270ms 涨到 567ms）。
		time.Sleep(2 * time.Millisecond)
	}
}

// loadVecBackend 读取 assets/sprites/<key>/sprite.json 与 parts/，
// 并把 Meta 的画布/球心/方向帧数改写成矢量坐标（缩放 = scale，与旧 4x 素材同尺度）。
// Ball 不改：球径的换算口径在各处已按 4x 使用，保持 99 才能和快捷栏、图标对齐。
func loadVecBackend(key string, meta *Meta) (*vecBackend, error) {
	dir := filepath.Join(assetsRoot(), "sprites", key)
	sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
	if err != nil {
		return nil, err
	}
	lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
	if err != nil {
		return nil, err
	}
	for _, e := range sp.Validate(lib) {
		logf("vec %s 警告: %s", key, e)
	}
	// 渲染倍率 = 4：矢量要「怎么截屏都不糊」，就必须在**最终显示尺寸**上原生
	// 光栅化，而不是 1x 渲染再放大。1x 时球径只有 25px，1px 边缘在 25px 的球上
	// 就是肉眼可见的台阶（子渊 2026-10-25 反馈"矢量图怎么这么糊"）。
	// 4x 与旧 PNG 素材口径一致（球径 99px，接近游戏原版），实测单帧：
	//     plain 20ms / super1 22ms / super4 43ms（17 帧待机一次性 ≈0.7s，可后台）
	// 之前担心的 1055ms 是「4x 渲染 + 4x 超采样」叠加了 ss，与倍率本身无关。
	const scale = 4.0
	mainRenderer := sprite.New(sp, lib)
	mainRenderer.SetTintFloor(vecTintFloor)
	vb := &vecBackend{
		renderer:  mainRenderer,
		scale:     scale,
		dirFrames: map[string]int{},
		cache:     map[string][]*image.RGBA{},
		key:       key,
	}
	for d, dd := range sp.Directions {
		vb.dirFrames[d] = len(dd.Frames)
	}
	// 待机固定渲染窗口 = 所有方向所有帧 bbox 的并集（外扩 1px 收描边/抗锯齿）。
	// 一条口径同时解决两件事：① 每帧输出同尺寸，球心不漂（R3 画面抖动）；
	// ② 切方向时窗口尺寸不变，不会因为某方向内容更宽而整体跳一下。
	minx, miny := math.Inf(1), math.Inf(1)
	maxx, maxy := math.Inf(-1), math.Inf(-1)
	for _, dd := range sp.Directions {
		for _, fr := range dd.Frames {
			if len(fr.Bbox) != 4 || fr.Bbox[2] <= 0 || fr.Bbox[3] <= 0 {
				continue
			}
			minx = math.Min(minx, fr.Bbox[0])
			miny = math.Min(miny, fr.Bbox[1])
			maxx = math.Max(maxx, fr.Bbox[0]+fr.Bbox[2])
			maxy = math.Max(maxy, fr.Bbox[1]+fr.Bbox[3])
		}
	}
	if math.IsInf(minx, 1) {
		minx, miny, maxx, maxy = 0, 0, sp.Canvas.W, sp.Canvas.H
	}
	vb.idleClip = [4]float64{minx - 1, miny - 1, maxx - minx + 2, maxy - miny + 2}
	vb.idleSS = pickSS(vb.idleClip[2], vb.idleClip[3], scale)
	// 画布按 4x 渲染后，meta.Zoom 必须同步为 4，否则绘制端会把
	// 269x286 的位图再缩小成 67x72（这正是此前"窗口在、NONO 不见了"的成因）。
	// 球径保持 meta 原值 99（4x 素材像素口径），与画布倍率一致。
	meta.Zoom = int(scale)
	// 动作/表情：只扫描目录、记录路径，**不解析**。
	//
	// 2026-10-26 子渊质疑「9800X3D + 3080 怎么可能性能差」——他说得对，慢的不是
	// CPU：素材是「一个部件一个 JSON」，super1 单形态 750 个文件（action_1~7 共
	// 523 + exp_1~4 共 182 + 待机 parts 44）。Windows 上每次 CreateFile+Read+Close
	// 还带杀软实时扫描，逐个读就是 2 秒左右；Wine/Linux 走 page cache 才 400~500ms。
	// 16 核 3D 缓存救不了逐文件 IO，唯一出路是减少「必须现在读的文件数」：
	// 切形态只读待机（45 个），动作/表情等真正播放时再解析（见 getAnim）。
	vb.actions = map[int]*vecAnim{}
	vb.exps = map[int]*vecAnim{}
	vb.animSub = map[string]string{}
	nAct, nExp := 0, 0
	for _, kind := range []string{"action", "exp"} {
		paths, _ := filepath.Glob(filepath.Join(assetsRoot(), "sprites", key, kind+"_*", "sprite.json"))
		for _, p := range paths {
			sub := filepath.Dir(p)
			base := filepath.Base(sub)
			num := 0
			if _, err := fmt.Sscanf(strings.TrimPrefix(base, kind+"_"), "%d", &num); err != nil || num <= 0 {
				continue
			}
			vb.animSub[kind+":"+itoa(num)] = sub
			if kind == "action" {
				nAct++
			} else {
				nExp++
			}
		}
	}
	logf("vec %s: 动作 %d 段, 表情 %d 段（懒加载，切形态不解析）", key, nAct, nExp)

	meta.Zoom = int(scale)
	meta.Canvas.W = int(sp.Canvas.W*scale + 0.5)
	meta.Canvas.H = int(sp.Canvas.H*scale + 0.5)
	// 球心优先用 canvas.ballc（面部黑窗口中心，由 measure_vec_ballc.py 写入）；
	// 没有时回退 canvas.anchor（bbox 中心）。两者的差值会让本体画偏、
	// 快捷栏环心落到地面阴影上，所以不能混用。
	if sp.Canvas.BallC[0] != 0 || sp.Canvas.BallC[1] != 0 {
		// 球心要换算到「帧图内坐标」：待机帧现在从 idleClip 左上角开始渲染，
		// 直接拿画布坐标会让球心整体偏移 clip 原点那么多像素。
		meta.BallC = []float64{
			(sp.Canvas.BallC[0] - vb.idleClip[0]) * scale,
			(sp.Canvas.BallC[1] - vb.idleClip[1]) * scale,
		}
	} else {
		meta.BallC = []float64{
			(sp.Canvas.Anchor[0] - vb.idleClip[0]) * scale,
			(sp.Canvas.Anchor[1] - vb.idleClip[1]) * scale,
		}
	}
	meta.Dirs = vb.dirFrames
	return vb, nil
}

// pickSS 按「渲染区域在屏幕上的实际像素」选超采样倍数。
//
// 单一 ss=3 在两类场景下都会翻车：① 动作可见区 1280x1024，×3 是 3840x3072，
// 单帧几百毫秒；② 待机的 idleClip 是跨方向并集，某些形态并集很大（super2 的
// right 方向实测首帧 567ms）。用同一套阈值：小图保清晰，大图先保流畅。
func pickSS(w, h, scale float64) int {
	px := math.Max(w, h) * scale
	switch {
	case px <= 380:
		return 3
	case px <= 900:
		return 2
	}
	return 1
}

// loadVecAnimFrom 解析单个动作/表情精灵。
//
// 这段逻辑原来内联在 loadVecBackend 的循环里，切形态时会把该形态全部 11 个
// 动作/表情一次性解析（700+ 个小 JSON，Windows 实测约 2 秒）。现在改成首次
// 播放时才调用，切形态只读待机的 45 个文件。
func loadVecAnimFrom(key, kind string, num int, sub string, scale float64) *vecAnim {
	sp2, err := spec.Load(filepath.Join(sub, "sprite.json"))
	if err != nil {
		logf("vec %s/%s 加载失败: %v", key, filepath.Base(sub), err)
		return nil
	}
	alib, lerr := spec.LoadLibrary(filepath.Join(sub, "parts"))
	if lerr != nil {
		logf("vec %s/%s parts 失败: %v", key, filepath.Base(sub), lerr)
		return nil
	}
	nf := 0
	if dd, ok := sp2.Directions["default"]; ok {
		nf = len(dd.Frames)
	}
	if nf == 0 {
		return nil
	}
	clip := [4]float64{0, 0, sp2.Canvas.W, sp2.Canvas.H}
	if dd, ok := sp2.Directions["default"]; ok && len(dd.Frames) > 0 {
		minx, miny := math.Inf(1), math.Inf(1)
		maxx, maxy := math.Inf(-1), math.Inf(-1)
		found := false
		for _, fr := range dd.Frames {
			if len(fr.Bbox) != 4 || fr.Bbox[2] <= 0 || fr.Bbox[3] <= 0 {
				continue
			}
			found = true
			minx = math.Min(minx, fr.Bbox[0])
			miny = math.Min(miny, fr.Bbox[1])
			maxx = math.Max(maxx, fr.Bbox[0]+fr.Bbox[2])
			maxy = math.Max(maxy, fr.Bbox[1]+fr.Bbox[3])
		}
		if found {
			// 留 1px 余量：圆角描边/抗锯齿会略微溢出几何 bbox。
			minx, miny = minx-1, miny-1
			maxx, maxy = maxx+1, maxy+1
			// 滤镜扩散要留到画布外（glow blur 实测最大溢出 53px），放宽 64px。
			const glowRoom = 64.0
			minx = math.Max(-glowRoom, minx)
			miny = math.Max(-glowRoom, miny)
			maxx = math.Min(sp2.Canvas.W+glowRoom, maxx)
			maxy = math.Min(sp2.Canvas.H+glowRoom, maxy)
			clip = [4]float64{minx, miny, maxx - minx, maxy - miny}
		}
	}
	bbAll := make([][4]float64, nf)
	if dd, ok := sp2.Directions["default"]; ok {
		for i, fr := range dd.Frames {
			if i >= nf {
				break
			}
			if len(fr.Bbox) == 4 {
				bbAll[i] = [4]float64{fr.Bbox[0], fr.Bbox[1], fr.Bbox[2], fr.Bbox[3]}
			}
		}
	}
	animRenderer := sprite.New(sp2, alib)
	animRenderer.SetTintFloor(vecTintFloor)
	return &vecAnim{
		renderer: animRenderer,
		nframes:  nf,
		scale:    scale,
		clip:     clip,
		bboxes:   bbAll,
		// bodyc 必须是「相对渲染窗口」的坐标：main.go 用它把本体中心对齐到
		// 待机球心。spec 里 bodyc 是整画布 1x 坐标，减掉窗口原点再乘倍率。
		bodyc: [2]float64{
			(sp2.Canvas.BodyC[0] - clip[0]) * scale,
			(sp2.Canvas.BodyC[1] - clip[1]) * scale,
		},
	}
}

// getAnim 取动作(kind=1)/表情(kind=2)的矢量动画，未加载则当场解析。
//
// 懒加载的理由见 loadVecAnimFrom 上方注释：切形态不该为 11 个动作解析 700 个
// 小文件。首次播放某个动作会多花一次解析时间（Windows 约 200~400ms），
// 之后命中 map。加锁是因为预取 worker 可能在 goroutine 里查同一张表。
func (v *vecBackend) getAnim(kind, id int) *vecAnim {
	v.animMu.Lock()
	defer v.animMu.Unlock()
	m := v.actions
	name := "action"
	if kind == 2 {
		m, name = v.exps, "exp"
	}
	if a, ok := m[id]; ok {
		return a
	}
	sub := v.animSub[name+":"+itoa(id)]
	if sub == "" {
		m[id] = nil
		return nil
	}
	t0 := time.Now()
	a := loadVecAnimFrom(v.key, name, id, sub, v.scale)
	logf("vec lazy %s/%d %dms", name, id, time.Since(t0)/1e6)
	// 新渲染器默认是「不染色」的白，必须当场补上当前玩家色。
	// 否则 setTint 只覆盖了已存在的渲染器，首次播放的动作/表情仍是白色：
	// 待机黑、一动就白的割裂（钢琴黑最明显，2026-10-27 冒烟复现）。
	// 锁序 animMu(held) → v.mu，与 frames()/setTint() 不构成反转。
	if a != nil {
		v.mu.Lock()
		rgb, ok := v.tintRGB, v.tintSet
		g1, g2, gm, gok := v.gradA, v.gradB, v.gradMode, v.gradSet
		v.mu.Unlock()
		if ok {
			a.setTint(rgb)
			logf("vec tint 补 %s/%d #%06X", name, id, rgb)
		}
		if gok {
			a.setGradient(g1, g2, gm)
			logf("vec grad 补 %s/%d #%06X→#%06X", name, id, g1, g2)
		}
	}
	m[id] = a
	return a
}

// warmAnims 把该形态全部动作/表情解析进内存（供 prewarm 后台调用）。
//
// 动作 ss 提到 3 之后单帧渲染变贵，首次播放若还要现场解析 700 个小文件，
// 会出现几十帧冻结（实测 action_7 冻 45 帧）。预热阶段在后台解析完，
// 用户点动作时直接命中。
func (v *vecBackend) warmAnims() {
	if v == nil {
		return
	}
	for k := range v.animSub {
		i := strings.IndexByte(k, ':')
		if i <= 0 {
			continue
		}
		kind := 1
		if k[:i] == "exp" {
			kind = 2
		}
		id := 0
		if _, err := fmt.Sscanf(k[i+1:], "%d", &id); err != nil || id <= 0 {
			continue
		}
		v.getAnim(kind, id)
		// 节流：每个动作解析完让一下 CPU。预热是可有可无的后台工作，
		// 全速跑会和播放/UI 抢核（实测把正在播的 action_7 冻了 50 帧）。
		time.Sleep(25 * time.Millisecond)
	}
}

// idleDumpPrefix 非空时把待机帧按方向逐帧落 PNG（<前缀><dir>_<i>.png）。
//
// 只用于验证本身：预缩放改造（20261028-08）要求「缓存里存的显示图」与
// 「4x 渲染后再缩」逐像素相同，做法是同一场景在新旧两版各 dump 一遍再比
// MD5。dump 的是最终显示尺寸的图，所以两版输出可以直比。
var idleDumpPrefix = os.Getenv("NONO_IDLE_DUMP")

func dumpIdleFrame(dir string, idx int, img *image.RGBA) {
	if idleDumpPrefix == "" || img == nil {
		return
	}
	p := idleDumpPrefix + dir + "_" + itoa(idx) + ".png"
	f, err := os.Create(p)
	if err != nil {
		return
	}
	defer f.Close()
	_ = png.Encode(f, img)
}
