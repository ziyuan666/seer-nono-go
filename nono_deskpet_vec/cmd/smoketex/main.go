// Command smoketex 生成程序化尾焰用的烟团贴图与锚点表。
//
// 为什么不手工加滤镜：assets/fly/f_<style>_<dir>_1.png 是 FFDec 导出的
// fireMc 层图，只含形状与填充色，不含 place 上挂的 glow(16,#02d2d2,1.6992)
// + blur(7)。手工按参数复现时，box blur 在 64px 半径下的 alpha 量化台阶
// 会在烟团外圈留下一圈圈同心波纹（实测肉眼可见），而矢量链路是「放大到
// 2 倍超采样渲染再降采样」，台阶被平滑掉。
//
// 所以这里直接调 sprite.Renderer 渲染 fireMc 单层（与运行时同一条光栅化
// 路径，结果逐像素一致），裁 alpha bbox 输出贴图，并把 bbox 换算成画布
// 坐标写进 anchors.json。锚点因此不再需要 metrics.json 的 off 换算。
//
// 用法（在 nono_deskpet_vec 目录）：
//
//	go run ./cmd/smoketex ../nono_deskpet
package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"

	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

const (
	bakeScale = 4 // 与 assets/fly 的 4x 层图一致
	bakeSS    = 2 // 与 vec_fly.go 的 RenderClip(..., 2, clip) 一致
	alphaMin  = 8 // 贴图裁剪阈值：包含 glow 低强度尾部
)

var dirs = []string{"down", "leftdown", "left", "leftup",
	"up", "rightup", "right", "rightdown"}

// vecFlyClip 必须与 nono_deskpet/vec_fly.go 的同名表一致。
// 用于把 FFDec 层图（assets/fly/f_*.png）的坐标换回画布坐标。
var vecFlyClip = map[int][4]float64{
	1: {-4.25, -4.00, 120, 146},
	2: {-37.00, -8.25, 177, 165},
	3: {-9.25, -4.00, 105, 149},
	4: {-30.75, -8.25, 167, 170},
}

type flyLayerOff struct {
	Off [2]float64 `json:"off"`
}

type flyMetrics struct {
	Layers map[string]flyLayerOff `json:"layers"`
}

type anchor struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

type texMeta struct {
	W  int `json:"w"`
	H  int `json:"h"`
	CW int `json:"cw"`
	CH int `json:"ch"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: smoketex <nono_deskpet 目录>")
		os.Exit(2)
	}
	root := os.Args[1]
	sprDir := filepath.Join(root, "assets", "sprites")
	outDir := filepath.Join(sprDir, "smoke")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}
	metrics := map[string]flyMetrics{}
	if b, err := os.ReadFile(filepath.Join(root, "assets", "fly", "metrics.json")); err == nil {
		if err := json.Unmarshal(b, &metrics); err != nil {
			fmt.Println("metrics.json 解析失败:", err)
		}
	}
	anchors := map[string]map[string]anchor{}
	metas := map[string]texMeta{}
	for st := 1; st <= 4; st++ {
		dir := filepath.Join(sprDir, "fly"+strconv.Itoa(st))
		sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
		if err != nil {
			fmt.Printf("style%d 跳过: %v\n", st, err)
			continue
		}
		lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
		if err != nil {
			fmt.Printf("style%d 载入 parts 失败: %v\n", st, err)
			continue
		}
		r := sprite.New(sp, lib)
		anchors[strconv.Itoa(st)] = map[string]anchor{}
		for _, d := range dirs {
			if _, ok := sp.Directions[d]; !ok {
				continue
			}
			img, err := r.RenderLayer(d, 0, bakeScale, bakeSS, "fireMc")
			if err != nil || img == nil {
				continue
			}
			// RenderLayer 按「帧 bbox」裁图，图原点 = 帧 bbox 左上角。
			// 换算画布坐标必须加上这个偏移，否则锚点整体偏几十像素。
			bb := sp.Directions[d].Frames[0].Bbox
			if len(bb) != 4 {
				fmt.Printf("style%d %s 无帧 bbox，跳过\n", st, d)
				continue
			}
			bbx, bby := bb[0], bb[1]
			x0, y0, x1, y1 := alphaBBox(img)
			if x1 <= x0 {
				continue // 该方向没有尾焰（style4 的 leftup/up/rightup）
			}
			sub := image.NewRGBA(image.Rect(0, 0, x1-x0, y1-y0))
			for y := y0; y < y1; y++ {
				copy(sub.Pix[(y-y0)*sub.Stride:], img.Pix[y*img.Stride+x0*4:y*img.Stride+x1*4])
			}
			name := fmt.Sprintf("s%d_%s.png", st, d)
			fo, err := os.Create(filepath.Join(outDir, name))
			if err != nil {
				panic(err)
			}
			// raster 输出的是「容器 image.RGBA、Pix 存非预乘值」——
			// 直接 png.Encode 会按预乘解释，颜色会炸（青变红）。必须转 NRGBA。
			if err := png.Encode(fo, toNRGBA(sub)); err != nil {
				panic(err)
			}
			fo.Close()
			// 锚点 = 帧 bbox 原点 + 内容在 bbox 图中的偏移。
			// 已用「完整帧 - (跳过fire+dir + 只dir)」的差值图质心校验：
			// 算得中心 (39.68,65.48) vs 实测质心 (38.67,68.43)，偏差 ≈1/3 画布单位。
			cx := bbx + float64(x0+x1)/2/bakeScale
			cy := bby + float64(y0+y1)/2/bakeScale
			tw := float64(x1-x0) / bakeScale
			th := float64(y1-y0) / bakeScale
			anchors[strconv.Itoa(st)][d] = anchor{
				X: cx - tw/2,
				Y: cy - th/2,
				W: tw,
				H: th,
			}
			// 贴图内容 = 整张（已裁到 bbox），中心对齐锚点中心
			metas[strconv.Itoa(st)] = texMeta{
				W: x1 - x0, H: y1 - y0, CW: x1 - x0, CH: y1 - y0,
			}
			a := anchors[strconv.Itoa(st)][d]
			fmt.Printf("style%d %-9s 贴图 %dx%d 锚点 %.2f,%.2f %.2fx%.2f\n",
				st, d, x1-x0, y1-y0, a.X, a.Y, a.W, a.H)
		}
	}
	writeJSON(filepath.Join(outDir, "anchors.json"), anchors)
	writeJSON(filepath.Join(outDir, "tex.json"), metas)
	fmt.Println("写出 anchors.json / tex.json")
}

// flyContentCenter 返回 FFDec 层图内容的中心（画布坐标）。
//
// 换算：层图像素 → 整帧像素（+ metrics.fly<st>.layers.fireMc.off）→
// 整帧坐标 /4 → 画布坐标（+ vecFlyClip 原点）。
func flyContentCenter(flyDir string, metrics map[string]flyMetrics, st int, d string, _ int) (float64, float64, bool) {
	m, ok := metrics[strconv.Itoa(st)]
	if !ok {
		return 0, 0, false
	}
	off, ok := m.Layers["fireMc"]
	if !ok {
		return 0, 0, false
	}
	ox, oy := off.Off[0], off.Off[1]
	p := filepath.Join(flyDir, fmt.Sprintf("f_%d_%s_1.png", st, d))
	im, err := loadPNGFile(p)
	if err != nil {
		return 0, 0, false
	}
	x0, y0, x1, y1 := alphaBBox(im)
	if x1 <= x0 {
		return 0, 0, false
	}
	c := vecFlyClip[st]
	cx := c[0] + (ox+float64(x0+x1)/2)/4
	cy := c[1] + (oy+float64(y0+y1)/2)/4
	return cx, cy, true
}

// loadPNGFile 读绝对路径 PNG（不经过 assetsRoot 拼接）。
func loadPNGFile(p string) (*image.RGBA, error) {
	f, err := os.Open(p)
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
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			out.Set(x, y, im.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out, nil
}

func alphaBBox(img *image.RGBA) (int, int, int, int) {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[y*img.Stride:]
		for x := b.Min.X; x < b.Max.X; x++ {
			if row[x*4+3] >= alphaMin {
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
	if maxX < minX {
		return 0, 0, 0, 0
	}
	return minX, minY, maxX + 1, maxY + 1
}

// toNRGBA 把「容器是 *image.RGBA、Pix 存非预乘值」的图逐字节搬到 image.NRGBA。
func toNRGBA(src *image.RGBA) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		copy(dst.Pix[y*dst.Stride:y*dst.Stride+b.Dx()*4], src.Pix[y*src.Stride:y*src.Stride+b.Dx()*4])
	}
	return dst
}

func writeJSON(p string, v any) {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		panic(err)
	}
	os.WriteFile(p, b, 0o644)
}
