package spec

// 02 量化格式加载器。
//
// 01（可读格式）与 02（量化格式）语义完全等价：
//   - 顶点存 twip 整数（÷20 = px），命令用 0/1/2/3 表示 moveTo/lineTo/quadTo/closePath；
//   - 矩阵存 16.16 定点整数（÷65536 = 浮点）；
//   - 渐变 stops 的 offset 与 alpha 存 0..255；
//   - 帧里每个部件压成 [shapeIdx, m0..m5, alpha255]。
//
// 加载后统一还原成 Sprite + PartFile，渲染层不需要区分 01/02。
// 这样磁盘上省掉大量 JSON 键名，渲染路径完全复用，风险最低。

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

type v2Path struct {
	V  []int   `json:"v"`            // 顶点：cmd,(coords)...
	M  []int   `json:"m,omitempty"`  // base 矩阵 16.16
	R  string  `json:"r,omitempty"`  // "e" = evenodd
	F  string  `json:"f,omitempty"`  // 纯色
	G  *v2Grad `json:"g,omitempty"`  // 渐变
	FA *int    `json:"fa,omitempty"` // fillAlpha 0..255
	ST []any   `json:"st,omitempty"` // [color, widthTwip, alpha255]
}

type v2Grad struct {
	T  string  `json:"t"`            // "l" / "r"
	M  []int   `json:"m"`            // 16.16
	S  string  `json:"s,omitempty"`  // spread
	P  [][]any `json:"p"`            // [[off255, color, alpha255], ...]
	L  []int   `json:"l,omitempty"`  // linear: x1,y1,x2,y2 (twip)
	RC []int   `json:"rc,omitempty"` // radial: cx,cy,r,fx,fy (twip)
}

// v2Root 是 02 文件的顶层结构。
type v2Root struct {
	F   int                  `json:"f"`
	N   string               `json:"n"`
	C   []*float64           `json:"c"` // w,h,ax,ay,bx,by,bcx,bcy（后四个可能 null）
	Col map[string]string    `json:"col"`
	Ly  [][]json.RawMessage  `json:"ly"`
	SN  []string             `json:"sn"`
	S   [][]v2Path           `json:"S"`
	D   map[string][][][]int `json:"D"`
}

func tw2px(v int) float64 { return float64(v) / 20.0 }
func fx2f(v int) float64  { return float64(v) / 65536.0 }

// vertsToD 把顶点数组还原成 SVG path 字符串。
// 只在加载时做一次，渲染层继续用现成的 pathd.Parse，避免改动渲染链路。
func vertsToD(v []int) string {
	var b strings.Builder
	b.Grow(len(v) * 5)
	i := 0
	num := func() (int, bool) {
		if i >= len(v) {
			return 0, false
		}
		x := v[i]
		i++
		return x, true
	}
	point := func() (float64, float64, bool) {
		x, ok1 := num()
		y, ok2 := num()
		return tw2px(x), tw2px(y), ok1 && ok2
	}
	// 一律写显式命令：原始 01 数据里每个坐标对前都有字母，
	// 隐式 lineTo 的省略写法会让 pathd.Parse 与实际段数不一致。
	for i < len(v) {
		cmd := v[i]
		i++
		switch cmd {
		case 0: // moveTo
			x, y, ok := point()
			if !ok {
				return b.String()
			}
			fmt.Fprintf(&b, "M%s %s", ftoa(x), ftoa(y))
		case 1: // lineTo
			x, y, ok := point()
			if !ok {
				return b.String()
			}
			fmt.Fprintf(&b, "L%s %s", ftoa(x), ftoa(y))
		case 2: // quadTo
			x1, y1, ok1 := point()
			x, y, ok2 := point()
			if !ok1 || !ok2 {
				return b.String()
			}
			fmt.Fprintf(&b, "Q%s %s %s %s", ftoa(x1), ftoa(y1), ftoa(x), ftoa(y))
		case 3: // closePath
			b.WriteByte('Z')
		default:
			return b.String()
		}
	}
	return b.String()
}

func ftoa(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (vp *v2Path) toPathDef() PathDef {
	pd := PathDef{D: vertsToD(vp.V)}
	if len(vp.M) == 6 {
		for i := 0; i < 6; i++ {
			pd.Matrix[i] = fx2f(vp.M[i])
		}
	}
	if vp.R == "e" {
		pd.FillRule = "evenodd"
	}
	pd.Fill = vp.F
	if vp.G != nil {
		g := &Gradient{
			Matrix: [6]float64{},
			Spread: vp.G.S,
		}
		if vp.G.T == "l" {
			g.Type = "linear"
		} else {
			g.Type = "radial"
		}
		for i := 0; i < 6 && i < len(vp.G.M); i++ {
			g.Matrix[i] = fx2f(vp.G.M[i])
		}
		for _, s := range vp.G.P {
			if len(s) < 3 {
				continue
			}
			off, _ := s[0].(float64)
			col, _ := s[1].(string)
			al, _ := s[2].(float64)
			g.Stops = append(g.Stops, Stop{Offset: off / 255.0, Color: col, Opacity: al / 255.0})
		}
		if len(vp.G.L) == 4 {
			g.X1, g.Y1 = tw2px(vp.G.L[0]), tw2px(vp.G.L[1])
			g.X2, g.Y2 = tw2px(vp.G.L[2]), tw2px(vp.G.L[3])
		}
		if len(vp.G.RC) == 5 {
			g.CX, g.CY = tw2px(vp.G.RC[0]), tw2px(vp.G.RC[1])
			g.R = tw2px(vp.G.RC[2])
			g.FX, g.FY = tw2px(vp.G.RC[3]), tw2px(vp.G.RC[4])
		}
		pd.Gradient = g
	}
	if vp.FA != nil {
		a := float64(*vp.FA) / 255.0
		pd.FillAlpha = &a
	}
	if len(vp.ST) == 3 {
		col, _ := vp.ST[0].(string)
		w, _ := vp.ST[1].(float64)
		al, _ := vp.ST[2].(float64)
		pd.Stroke = &StrokeDef{Color: col, Width: tw2px(int(w))}
		a := al / 255.0
		pd.StrokeAlpha = &a
	}
	return pd
}

// IsV2 判断文件是否为 02 格式（读首 64 字节找 "f":2）。
func IsV2(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	s := string(buf[:n])
	return strings.Contains(s, `"f":2`)
}

// LoadV2 读取 02 文件，返回与 Load+LoadLibrary 等价的产物。
func LoadV2(path string) (*Sprite, *Library, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var r v2Root
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, nil, fmt.Errorf("解析 02 格式失败: %w", err)
	}
	if r.F != 2 {
		return nil, nil, fmt.Errorf("不是 02 格式（f=%d）", r.F)
	}

	// canvas
	cv := Canvas{}
	get := func(i int) float64 {
		if i < len(r.C) && r.C[i] != nil {
			return *r.C[i]
		}
		return 0
	}
	cv.W, cv.H = get(0), get(1)
	cv.Anchor = [2]float64{get(2), get(3)}
	cv.BallC = [2]float64{get(4), get(5)}
	cv.BodyC = [2]float64{get(6), get(7)}

	sp := &Sprite{
		Format:     2,
		Name:       r.N,
		Canvas:     cv,
		Colors:     r.Col,
		Directions: map[string]Direction{},
	}
	for _, l := range r.Ly {
		var layer Layer
		if len(l) >= 1 {
			_ = json.Unmarshal(l[0], &layer.Name)
		}
		if len(l) >= 2 {
			_ = json.Unmarshal(l[1], &layer.Z)
		}
		if len(l) >= 3 {
			var t int
			_ = json.Unmarshal(l[2], &t)
			layer.Tintable = t != 0
		}
		sp.Layers = append(sp.Layers, layer)
	}

	// shape 表 → PartFile
	lib := &Library{Parts: map[string]*PartFile{}}
	for i, paths := range r.S {
		if i >= len(r.SN) {
			break
		}
		name := r.SN[i]
		pf := &PartFile{ID: name}
		for j := range paths {
			pf.Paths = append(pf.Paths, paths[j].toPathDef())
		}
		lib.Parts[name] = pf
		lib.Parts[name+"#"+name] = pf
	}

	// 帧
	for dir, frames := range r.D {
		d := Direction{}
		for _, ps := range frames {
			fr := Frame{}
			for _, row := range ps {
				// row = [shapeIdx, layerIdx, m0..m5, alpha255]
				if len(row) < 9 {
					continue
				}
				si, li := row[0], row[1]
				if si < 0 || si >= len(r.SN) {
					continue
				}
				layer := r.SN[si]
				if li >= 0 && li < len(sp.Layers) {
					layer = sp.Layers[li].Name
				}
				a := float64(row[8]) / 255.0
				fr.Parts = append(fr.Parts, Part{
					Layer:  layer,
					Part:   r.SN[si],
					Alpha:  &a,
					Matrix: [6]float64{fx2f(row[2]), fx2f(row[3]), fx2f(row[4]), fx2f(row[5]), fx2f(row[6]), fx2f(row[7])},
				})
			}
			d.Frames = append(d.Frames, fr)
		}
		sp.Directions[dir] = d
	}
	return sp, lib, nil
}
