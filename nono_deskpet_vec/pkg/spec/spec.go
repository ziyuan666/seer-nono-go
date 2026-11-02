// Package spec 定义分层矢量精灵的 JSON 格式，负责加载与校验。
//
// 设计原则：
//  1. 时间轴扁平（形态→层→方向→帧→部件），不允许嵌套；
//  2. 几何与引用分离（parts/ 存几何，帧里只写引用 + 矩阵）；
//  3. 颜色走槽位（colors 表），染色只改槽位值；
//  4. format 版本号必须显式，加载器按版本兼容。
package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"nonovec/pkg/pathd"
)

// Canvas 画布与锚点。
//
// Anchor 是「画布原点相对 bbox 中心」的偏移，用于把 part 坐标搬到画布坐标系，
// 不是球心。BallC 才是球心：取彩色渲染帧里「面部黑窗口」的中心，绘制时靠它
// 把球体对齐到固定锚点，快捷栏环心也用它。
// 两者在矢量素材里能差 26px（plain：anchor y=60.46 vs 真球心 34.5），
// 混用会让本体整体画偏、快捷栏环心落到地面阴影上（子渊 2026-10-25 截图反馈）。
type Canvas struct {
	W      float64    `json:"w"`
	H      float64    `json:"h"`
	Anchor [2]float64 `json:"anchor"`
	// BallC 缺省（0,0）时回退 Anchor，兼容未重算球心的旧素材。
	BallC [2]float64 `json:"ballc"`
	// BodyC 动作/表情专用：本体（可染色球壳）在第 0 帧的中心。
	// 待机走 BallC 对齐球心；动作帧把 BodyC 对齐到同一个球心锚点，
	// 否则各段动作本体在各自 sprite 里的位置差异会让开播瞬间跳几十像素。
	BodyC [2]float64 `json:"bodyc"`
}

// Layer 图层声明。Z 越小越靠下。
type Layer struct {
	Name     string `json:"name"`
	Z        int    `json:"z"`
	Tintable bool   `json:"tintable"`
}

// Part 帧里的一个绘制单元。Part 形如 "body_down#face" 或 "body_down"。
type Part struct {
	Layer string `json:"layer"`
	Part  string `json:"part"`
	Fill  string `json:"fill,omitempty"` // 颜色槽位名或 #RRGGBB，空=用几何自带
	// Alpha 用指针区分「未设置」与「显式 0」：SWF 里 cxform 的 mul alpha=0
	// 是真正不可见（蒙版/定位辅助），按 1 渲染会露出绿环等不该出现的部件。
	Alpha  *float64   `json:"alpha,omitempty"`
	Matrix [6]float64 `json:"matrix"` // SVG 语义 a,b,c,d,e,f
	// CMul/CAdd 是 SWF cxform 的 RGB 部分，按嵌套顺序已累积好：
	//   c' = clamp(c*CMul[i] + CAdd[i])
	// 缺失(=nil)表示恒等。这两个字段曾经被整个丢弃，导致 41% 的 place
	// 颜色错误（super5/action_7 的黄色被画成青色光球就是这么来的）。
	CMul []float64 `json:"cmul,omitempty"`
	CAdd []float64 `json:"cadd,omitempty"`
	// Fid 指向 Frame.Filters 里的一个滤镜组。带 fid 的部件先合成到独立图层，
	// 再按滤镜（发光/阴影）处理，最后整组叠回主画布；不带 fid 的直接画。
	// 为什么按组而不是按部件：SWF 里滤镜是挂在 place 上、作用于整棵子树的，
	// 逐部件发光会让部件之间的重叠处出现双重亮度。
	Fid string `json:"fid,omitempty"`
}

// Filter 是 SWF FILTERLIST 里用得到的子集（发光 / 阴影 / 纯模糊）。
// BlurX/BlurY 单位是舞台像素（= 素材坐标 1x），渲染时再乘缩放。
type Filter struct {
	Type            string  `json:"type"` // glow | shadow | blur
	BlurX           float64 `json:"blurX"`
	BlurY           float64 `json:"blurY"`
	Strength        float64 `json:"strength"`
	Color           string  `json:"color,omitempty"` // #rrggbbaa
	Inner           bool    `json:"inner,omitempty"`
	Knockout        bool    `json:"knockout,omitempty"`
	CompositeSource bool    `json:"compositeSource,omitempty"`
	Angle           float64 `json:"angle,omitempty"`
	Distance        float64 `json:"distance,omitempty"`
}

// Frame 一帧。
type Frame struct {
	// Bbox 是这一帧内容的实际包围盒 [x, y, w, h]（画布坐标）。
	// 动作/表情单帧往往只占统一画布一小块，渲染时按它开画布可省十几倍像素。
	Bbox    []float64           `json:"bbox,omitempty"`
	Parts   []Part              `json:"parts"`
	Filters map[string][]Filter `json:"filters,omitempty"`
}

// AnimGroup 是一个「子时间轴」：原版 SWF 里嵌套的 MC 各自按自己的帧数
// 独立循环，不跟父时间轴同步（飞行器眼球 15/19 帧、青色光圈 18 帧）。
//
// 早先把它们展平进父帧序列（--anim-frames 8）会同时犯两个错：
// 1) 只导到前 8 帧，18 帧的光圈丢掉后半段（"缺帧"）；
// 2) 被 1 帧包装器包住的子 MC 帧号恒为 0（"眼睛不眨"）。
// 现在把这类子树单独存成 AnimGroup，渲染端按自己的时钟取帧。
type AnimGroup struct {
	// Z 是父 place 的 depth，用来和静态部分一起排序合成（官方 Z 序）。
	Z int `json:"z"`
	// Ms 是每帧毫秒数（25fps=40、24fps=42）。0 表示按 40 处理。
	Ms int `json:"ms"`
	// Frames 是逐帧的完整内容：同一个 depth 可能切换不同 shape
	// （眼睛 sh20→21→22），所以不能只存矩阵；帧对象复用 Frame，
	// 顺手也能带自己的 filters（光圈外发光就挂在子 MC 的 place 上）。
	Frames []Frame `json:"frames"`
}

// Direction 一个方向的所有帧。
type Direction struct {
	Frames []Frame `json:"frames"`
	// Groups 是独立循环的子时间轴（旧素材无此字段，行为不变）。
	Groups []AnimGroup `json:"groups,omitempty"`
}

// Sprite 一个形态。
type Sprite struct {
	Format     int                  `json:"format"`
	Name       string               `json:"name"`
	Canvas     Canvas               `json:"canvas"`
	Colors     map[string]string    `json:"colors"`
	Layers     []Layer              `json:"layers"`
	Directions map[string]Direction `json:"directions"`

	// ---- 02 量化格式字段（加载时由 Normalize 折叠进上面的可读字段）----
	//
	// PT 是 parts 名表，帧里只存索引；LY 是图层表；D 是帧数据：
	// 每帧是一个 part 数组，每个 part 为
	//   [partIdx, layerIdx, ma, mb, mc, md, me, mf, alpha255]
	// 矩阵 16.16 定点，alpha 0..255。
	PT []string `json:"pt,omitempty"`
	LY []Layer2 `json:"ly,omitempty"`
	// 行长度可变：9=无颜色变换，12=仅乘项，15=乘项+加项。
	// 用切片而不是 [9]int64，否则多余的分量会被直接截断丢掉。
	D map[string][][][]int64 `json:"d,omitempty"`

	// 02 短键名（体积优先），Normalize 时搬进上面的正式字段。
	QFormat *int              `json:"f,omitempty"`
	QName   string            `json:"n,omitempty"`
	QCanvas *Canvas           `json:"c,omitempty"`
	QColors map[string]string `json:"col,omitempty"`
}

// Layer2 是 02 格式里的图层声明。
type Layer2 struct {
	N string `json:"n"`
	Z int    `json:"z"`
	T int    `json:"t"` // 1 = tintable
}

// Normalize 把 02 帧数据还原成 01 的 Directions / Layers 表示。
// 幂等：已经是 01 的 sprite 直接返回。
func (s *Sprite) Normalize() {
	if len(s.D) == 0 && len(s.PT) == 0 {
		return
	}
	// 02 短键名先搬进来。
	if s.QFormat != nil && s.Format == 0 {
		s.Format = *s.QFormat
	}
	if s.Name == "" {
		s.Name = s.QName
	}
	if s.QCanvas != nil && s.Canvas.W == 0 {
		s.Canvas = *s.QCanvas
	}
	if len(s.Colors) == 0 {
		s.Colors = s.QColors
	}
	if len(s.Layers) == 0 && len(s.LY) > 0 {
		for _, l := range s.LY {
			s.Layers = append(s.Layers, Layer{Name: l.N, Z: l.Z, Tintable: l.T == 1})
		}
	}
	if len(s.Directions) == 0 && len(s.D) > 0 {
		dirs := make(map[string]Direction, len(s.D))
		for name, frames := range s.D {
			var dir Direction
			for _, row := range frames {
				var fr Frame
				for _, e := range row {
					p := Part{}
					if pi := int(e[0]); pi >= 0 && pi < len(s.PT) {
						p.Part = s.PT[pi]
					}
					if li := int(e[1]); li >= 0 && li < len(s.Layers) {
						p.Layer = s.Layers[li].Name
					}
					// 帧矩阵是 1e-6 微像素定点（与 01 的 round(v,6) 对齐）。
					for i := 0; i < 6; i++ {
						p.Matrix[i] = float64(e[2+i]) / 1000000
					}
					// alpha=255 等价于「未设置」，留 nil 可省一次指针分配。
					// alpha 是 x10000 定点（01 存的是 round(v,4)，
					// 多级 cxform 相乘后的值不是 1/256 的倍数）。
					if a := float64(e[8]) / 10000; a < 1 {
						p.Alpha = &a
					}
					// 行长度即有无颜色变换：9=无，12=仅乘项，15=乘项+加项。
					if len(e) >= 12 {
						p.CMul = []float64{
							float64(e[9]) / 1000000,
							float64(e[10]) / 1000000,
							float64(e[11]) / 1000000,
						}
					}
					if len(e) >= 15 {
						p.CAdd = []float64{
							float64(e[12]) / 10000,
							float64(e[13]) / 10000,
							float64(e[14]) / 10000,
						}
					}
					fr.Parts = append(fr.Parts, p)
				}
				dir.Frames = append(dir.Frames, fr)
			}
			dirs[name] = dir
		}
		s.Directions = dirs
	}
	// 02 折叠完成后归一成 01 的 format 号，Validate 与下游都只认 format=1。
	if s.Format == 2 {
		s.Format = 1
	}
	s.PT, s.LY, s.D = nil, nil, nil
}

// StrokeDef 描边定义。
type StrokeDef struct {
	Color string  `json:"color"`
	Width float64 `json:"width"`
	Cap   string  `json:"cap,omitempty"`
	Join  string  `json:"join,omitempty"`
}

// Stop 渐变上的一个色标。
type Stop struct {
	Offset  float64 `json:"offset"`
	Color   string  `json:"color"`
	Opacity float64 `json:"opacity"`
}

// Gradient 渐变（linear / radial），坐标为 SVG userSpaceOnUse + 矩阵。
type Gradient struct {
	Type   string     `json:"type"`
	Matrix [6]float64 `json:"matrix"`
	Spread string     `json:"spread,omitempty"`
	Stops  []Stop     `json:"stops"`

	// linear
	X1 float64 `json:"x1,omitempty"`
	Y1 float64 `json:"y1,omitempty"`
	X2 float64 `json:"x2,omitempty"`
	Y2 float64 `json:"y2,omitempty"`

	// radial
	CX float64 `json:"cx,omitempty"`
	CY float64 `json:"cy,omitempty"`
	R  float64 `json:"r,omitempty"`
	FX float64 `json:"fx,omitempty"`
	FY float64 `json:"fy,omitempty"`
}

// PathDef 一条路径。Fill 为空且无 Gradient 表示不填充（只描边）。
type PathDef struct {
	D string `json:"d"`
	// Matrix 是 SVG <g transform> 累积出来的 base（shape 本地 → 视口）。
	// 缺省时按单位矩阵处理（见 sprite.pathMatrix）。
	Matrix   [6]float64 `json:"matrix,omitempty"`
	Fill     string     `json:"fill,omitempty"`
	Gradient *Gradient  `json:"gradient,omitempty"`
	FillRule string     `json:"fillRule,omitempty"`
	Stroke   *StrokeDef `json:"stroke,omitempty"`
	// SVG 的 fill-opacity / stroke-opacity。FFDec 用它们表达「半透明填充」，
	// 忽略会把 0.2 的灰弧画成纯黑（action/1 第 1 帧下方那道黑弧）。
	FillAlpha   *float64 `json:"fillAlpha,omitempty"`
	StrokeAlpha *float64 `json:"strokeAlpha,omitempty"`

	// ---- 02 量化格式字段（加载时由 Normalize 折叠进上面的可读字段）----
	//
	// V 顶点数组：0=M(x,y) 1=L(x,y) 2=Q(cx,cy,x,y) 3=Z，坐标 twip 整数。
	// 用 twip 而不是 px 浮点，是因为导出时 `%.2f` 打印的 px 正好是 0.05 的倍数，
	// 乘 20 必然回到整数，往返无损（实测最大误差 0.000000px）。
	V []int32 `json:"v,omitempty"`
	// M 矩阵，16.16 定点整数（SVG 原值本就是定点数）。
	M []int64 `json:"m,omitempty"`
	// F 填充色 0xRRGGBB；用指针区分「黑色」与「未设置」。
	F *int64 `json:"f,omitempty"`
	// R 填充规则，1 = evenodd。
	R int `json:"r,omitempty"`
	// G 量化渐变定义。
	G *Grad2 `json:"g,omitempty"`
	// K 描边色 0xRRGGBB，W 描边宽度（twip）。
	K *int64 `json:"k,omitempty"`
	W int    `json:"w,omitempty"`
	// FA / SA 填充与描边不透明度，x1000 定点（0..1000）。
	FA *int64 `json:"fa,omitempty"`
	SA *int64 `json:"sa,omitempty"`

	// Segs 由 V 预解析出的段序列，渲染层优先用它，省掉每次渲染的字符串解析。
	Segs []pathd.Seg `json:"-"`
}

// Grad2 是 02 格式里的渐变，字段全部整数化以缩小体积。
type Grad2 struct {
	T string  `json:"t"`           // "linear" / "radial"
	M []int64 `json:"m"`           // 矩阵 16.16
	S string  `json:"s,omitempty"` // spread，缺省 pad
	// ST 每个色标 [offset, color, opacity]，后两者 x10000 定点（0..10000）。
	ST [][3]int64 `json:"st"`
	// G 几何：linear 为 x1,y1,x2,y2；radial 为 cx,cy,r,fx,fy（twip）。
	G []int32 `json:"g"`
}

// Normalize 把 02 量化字段折叠成可读的 01 表示，并预解析段序列。
// 幂等：已经是 01 的路径不做任何事。
func (p *PathDef) Normalize() {
	if len(p.V) > 0 && len(p.Segs) == 0 {
		p.Segs = VToSegs(p.V)
		if p.D == "" {
			p.D = SegsToD(p.Segs)
		}
	}
	// 与 01 的 round(v,6) 存储精度对齐：16.16 只有 1.5e-5，
	// 会让细长图形（宽 0.65px 的角）evenodd 判定翻转，整块颜色跳变。
	if len(p.M) == 6 && p.Matrix == [6]float64{} {
		for i := 0; i < 6; i++ {
			p.Matrix[i] = float64(p.M[i]) / 1000000
		}
	}
	if p.F != nil && p.Fill == "" {
		p.Fill = fmt.Sprintf("#%06x", *p.F)
	}
	if p.R == 1 && p.FillRule == "" {
		p.FillRule = "evenodd"
	}
	if p.G != nil && p.Gradient == nil {
		p.Gradient = p.G.toGradient()
	}
	if p.K != nil && p.Stroke == nil {
		p.Stroke = &StrokeDef{Color: fmt.Sprintf("#%06x", *p.K), Width: float64(p.W) / 20}
	}
	// FA / SA 是 x1000 定点：01 里这两个值都是 3 位小数（round(v,3)），
	// 用 65535 归一虽然看起来更细，但还原后与 01 差 6.5e-6，严格逐像素比对过不去；
	// x1000 正好无损往返。
	// SA 尤其不能漏：它是「这条描边不画」的唯一表达（alpha=0），
	// 漏了就会把 super4 每只角外圈的辅助描边按不透明画成黑线。
	if p.FA != nil && p.FillAlpha == nil {
		a := float64(*p.FA) / 1000
		p.FillAlpha = &a
	}
	if p.SA != nil && p.StrokeAlpha == nil {
		a := float64(*p.SA) / 1000
		p.StrokeAlpha = &a
	}
	// 折叠完释放量化字段，避免同一份几何在内存里存两份。
	p.V, p.M, p.F, p.G, p.K, p.W = nil, nil, nil, nil, nil, 0
	p.FA, p.SA = nil, nil
}

// toGradient 把量化渐变还原成浮点表示。
func (g *Grad2) toGradient() *Gradient {
	out := &Gradient{Type: g.T, Spread: g.S}
	// 渐变矩阵是 16.16 定点（与 path 的 round(v,6) 不同，别混用）。
	if len(g.M) == 6 {
		for i := 0; i < 6; i++ {
			out.Matrix[i] = float64(g.M[i]) / 65536
		}
	}
	for _, s := range g.ST {
		out.Stops = append(out.Stops, Stop{
			Offset:  float64(s[0]) / 10000,
			Color:   fmt.Sprintf("#%06x", s[1]),
			Opacity: float64(s[2]) / 10000,
		})
	}
	if g.T == "linear" && len(g.G) >= 4 {
		out.X1 = float64(g.G[0]) / 20
		out.Y1 = float64(g.G[1]) / 20
		out.X2 = float64(g.G[2]) / 20
		out.Y2 = float64(g.G[3]) / 20
	} else if g.T == "radial" && len(g.G) >= 5 {
		out.CX = float64(g.G[0]) / 20
		out.CY = float64(g.G[1]) / 20
		out.R = float64(g.G[2]) / 20
		out.FX = float64(g.G[3]) / 20
		out.FY = float64(g.G[4]) / 20
	}
	return out
}

// VToSegs 把 02 顶点数组还原成段序列（twip → px 除以 20）。
// 未知指令一律跳过，不 panic：坏数据宁可少画一段也不要整帧崩。
func VToSegs(v []int32) []pathd.Seg {
	out := make([]pathd.Seg, 0, len(v)/3+1)
	f := func(i int) float64 { return float64(v[i]) / 20 }
	for i := 0; i < len(v); {
		switch v[i] {
		case 0:
			if i+2 >= len(v) {
				return out
			}
			out = append(out, pathd.Seg{Kind: pathd.Move, Pts: []float64{f(i + 1), f(i + 2)}})
			i += 3
		case 1:
			if i+2 >= len(v) {
				return out
			}
			out = append(out, pathd.Seg{Kind: pathd.Line, Pts: []float64{f(i + 1), f(i + 2)}})
			i += 3
		case 2:
			if i+4 >= len(v) {
				return out
			}
			out = append(out, pathd.Seg{Kind: pathd.Quad,
				Pts: []float64{f(i + 1), f(i + 2), f(i + 3), f(i + 4)}})
			i += 5
		case 3:
			out = append(out, pathd.Seg{Kind: pathd.Close})
			i++
		default:
			i++
		}
	}
	return out
}

// SegsToD 把段序列写回 SVG d（仅为调试/兼容保留，渲染不依赖）。
func SegsToD(segs []pathd.Seg) string {
	var b strings.Builder
	for _, s := range segs {
		switch s.Kind {
		case pathd.Move:
			fmt.Fprintf(&b, "M%.2f %.2f ", s.Pts[0], s.Pts[1])
		case pathd.Line:
			fmt.Fprintf(&b, "L%.2f %.2f ", s.Pts[0], s.Pts[1])
		case pathd.Quad:
			fmt.Fprintf(&b, "Q%.2f %.2f %.2f %.2f ", s.Pts[0], s.Pts[1], s.Pts[2], s.Pts[3])
		case pathd.Close:
			b.WriteString("Z ")
		}
	}
	return strings.TrimSpace(b.String())
}

// PartFile 一个部件文件（可含多条路径）。
type PartFile struct {
	ID    string    `json:"id"`
	Paths []PathDef `json:"paths"`
}

// Normalize 规范化所有路径（02 → 01）。
func (pf *PartFile) Normalize() {
	for i := range pf.Paths {
		pf.Paths[i].Normalize()
	}
}

// Library 已加载的部件库：key = "文件名#id"（无 # 时用文件名）。
type Library struct {
	Parts map[string]*PartFile
}

// LoadLibrary 读取 dir 下所有 *.json 部件文件。
func LoadLibrary(dir string) (*Library, error) {
	return loadLibraryDir(dir)
}

func loadLibraryDir(dir string) (*Library, error) {
	lib := &Library{Parts: map[string]*PartFile{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return lib, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var pf PartFile
		if err := json.Unmarshal(raw, &pf); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		// 02 量化格式在这里一次性折叠成可读表示，渲染层完全不用关心版本差异。
		pf.Normalize()
		base := strings.TrimSuffix(e.Name(), ".json")
		if pf.ID != "" {
			lib.Parts[base+"#"+pf.ID] = &pf
		}
		lib.Parts[base] = &pf
	}
	return lib, nil
}

// Load 读取 sprite.json。
func Load(path string) (*Sprite, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Sprite
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// 02 帧数据同样在加载点折叠，下游（渲染、validate、工具）只认 01 结构。
	s.Normalize()
	return &s, nil
}

// Validate 做结构校验，返回所有问题（不是遇到第一个就停）。
func (s *Sprite) Validate(lib *Library) []string {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	if s.Format != 1 {
		add("format 必须为 1，实际 %d", s.Format)
	}
	if s.Name == "" {
		add("name 为空")
	}
	if s.Canvas.W <= 0 || s.Canvas.H <= 0 {
		add("canvas 尺寸非法: %vx%v", s.Canvas.W, s.Canvas.H)
	}
	layers := map[string]bool{}
	for _, l := range s.Layers {
		if l.Name == "" {
			add("存在空名图层")
			continue
		}
		if layers[l.Name] {
			add("图层重名: %s", l.Name)
		}
		layers[l.Name] = true
	}
	if len(s.Directions) == 0 {
		add("没有任何方向")
	}
	for dn, d := range s.Directions {
		if len(d.Frames) == 0 {
			add("方向 %s 没有帧", dn)
			continue
		}
		for fi, f := range d.Frames {
			for pi, p := range f.Parts {
				if !layers[p.Layer] {
					add("%s 第 %d 帧第 %d 个 part 引用了未声明图层 %q", dn, fi, pi, p.Layer)
				}
				if lib != nil && lib.Parts[p.Part] == nil {
					add("%s 第 %d 帧第 %d 个 part 找不到部件 %q", dn, fi, pi, p.Part)
				}
				if p.Fill != "" {
					if _, ok := s.ResolveColor(p.Fill); !ok {
						add("%s 第 %d 帧第 %d 个 part 颜色 %q 无法解析", dn, fi, pi, p.Fill)
					}
				}
			}
		}
	}
	// 各方向帧数一致性（不强制，但报出来便于发现导入缺失）
	counts := map[int][]string{}
	for dn, d := range s.Directions {
		counts[len(d.Frames)] = append(counts[len(d.Frames)], dn)
	}
	if len(counts) > 1 {
		var ks []int
		for k := range counts {
			ks = append(ks, k)
		}
		sort.Ints(ks)
		var b strings.Builder
		b.WriteString("各方向帧数不一致:")
		for _, k := range ks {
			fmt.Fprintf(&b, " %d帧=%v", k, counts[k])
		}
		add("%s", b.String())
	}
	return errs
}

// ResolveColor 把 "#RRGGBB" / "#AARRGGBB" / 槽位名 解析成 0xAARRGGBB。
func (s *Sprite) ResolveColor(v string) (uint32, bool) {
	if v == "" {
		return 0, false
	}
	if strings.HasPrefix(v, "#") {
		h := v[1:]
		switch len(h) {
		case 6:
			n, err := strconv.ParseUint(h, 16, 32)
			if err != nil {
				return 0, false
			}
			return 0xFF000000 | uint32(n), true
		case 8:
			n, err := strconv.ParseUint(h, 16, 32)
			if err != nil {
				return 0, false
			}
			return uint32(n), true
		}
		return 0, false
	}
	if raw, ok := s.Colors[v]; ok && raw != v {
		return s.ResolveColor(raw)
	}
	return 0, false
}

// DirectionNames 返回固定的方向顺序（原版 8 方向）。
func DirectionNames() []string {
	return []string{"down", "leftdown", "left", "leftup", "up", "rightup", "right", "rightdown"}
}
