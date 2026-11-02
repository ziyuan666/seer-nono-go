//go:build windows

package main

// 官方尾焰帧绘制（2026-10-28，子渊定「模式1 单独做 / 2 3 4 共用」）。
//
// 素材来源：assets/fly/flame/<style>/<dir>_<stage>.png —— 由
// 变更工程/当前改动/20261028-04/bake_flame_official.py 从 FFDec 导出的
// SWF shape SVG 逐阶段光栅化后，叠加官方滤镜（GLOW 16px #02d2d2 strength
// 1.70 + BLUR 7px）与官方 colorTransform alpha 0.8008 得到。
//
// 为什么不直接让矢量层画 fireMc：nono_deskpet_vec 的 glow 滤镜把 #02d2d2
// 渲成 (211,3,3)，火焰会整片变红（style4 侧翼 #9bffff 还会变洋红）；
// 离线路由（shape SVG + 手写复刻滤镜）颜色与官方一致。
//
// 阶段结构（SWF 实证）：
//   style1        sprite12 的 shape 8/9/10/11   四阶段 × 2 帧
//   style2/3/4    共用 sprite15 的 shape 11/12/13/14（fly2 逐帧实证
//                 [11,11,12,12,13,13,14,11]）
// 位置全部来自 sprite.json 的 fireMc part 官方矩阵，逐方向不同（style1/3
// 八方向相同 = 火焰固定不随机体转；style2/4 逐方向旋转）。

import (
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// flameOffRec 是 assets/fly/flame/official.json 里单个「形态 × 方向」的记录。
// 单位一律 1x 画布单位（屏幕像素 = 1x × k）。
type flameOffRec struct {
	Cdx   float64 `json:"cdx"`   // 火焰 bbox 中心 − 机体 bbox 中心
	Cdy   float64 `json:"cdy"`   //
	Fw    float64 `json:"fw"`    // 火焰 bbox 宽
	Fh    float64 `json:"fh"`    //
	Above bool    `json:"above"` // true = 画在机体之上（官方只有模式4 的左上/上/右上）
	N     int     `json:"n"`     // 阶段数（4）
	Seq   []int   `json:"seq"`   // 8 帧循环的阶段下标（官方逐帧）
}

var (
	flameOffOnce sync.Once
	flameOffMap  map[string]map[string]flameOffRec
	flameTexMu   sync.Mutex
	flameTexes   = map[string]*image.RGBA{}
)

func flameOffLoad() {
	flameOffMap = map[string]map[string]flameOffRec{}
	b, err := os.ReadFile(filepath.Join(assetsRoot(), "fly", "flame", "official.json"))
	if err != nil {
		logf("flame official 读取失败（回退程序化）: %v", err)
		return
	}
	if err := json.Unmarshal(b, &flameOffMap); err != nil {
		logf("flame official 解析失败（回退程序化）: %v", err)
		flameOffMap = map[string]map[string]flameOffRec{}
	}
}

// flameOffFor 取某形态某方向的官方尾焰参数；nil 表示数据缺失。
func flameOffFor(style int, dirName string) *flameOffRec {
	flameOffOnce.Do(flameOffLoad)
	m := flameOffMap[itoa(style)]
	if m == nil {
		return nil
	}
	r, ok := m[dirName]
	if !ok {
		return nil
	}
	return &r
}

// flameOfficialAvail 报告某形态是否有官方帧素材（决定是否启用多相位动画）。
func flameOfficialAvail(style int) bool {
	flameOffOnce.Do(flameOffLoad)
	return flameOffMap[itoa(style)] != nil
}

// 提亮系数默认值（2026-10-28 子渊「需要提亮一点点」）。
//
// 只动两处，不碰几何与色相：
//   - alpha 增益：官方 colorTransform 是 0.8008，辉光部分被压得偏薄，
//     抬 30% 让外层青光变实（这是"看起来更亮"的主因）；
//   - 颜色增益：12%，把中间调提上来，白核本来就 255 不变。
//
// 调参不必重新烘焙 PNG：`NONO_FLAME_GAIN_A` / `NONO_FLAME_GAIN_C` 可临时覆盖，
// 例：NONO_FLAME_GAIN_A=1.3 NONO_FLAME_GAIN_C=1.12。
var (
	flameAlphaGain = flameGainFromEnv("NONO_FLAME_GAIN_A", 1.30)
	flameColorGain = flameGainFromEnv("NONO_FLAME_GAIN_C", 1.12)
)

func flameGainFromEnv(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return def
	}
	if f > 2 {
		f = 2
	}
	return f
}

// flameBrighten 对**非预乘** RGBA 做提亮（与 drawScaledEx 的取样口径一致）。
// alpha 与色值分别增益后各自 clamp，不做 gamma，避免白核溢出发灰。
func flameBrighten(src *image.RGBA) *image.RGBA {
	if flameAlphaGain == 1.0 && flameColorGain == 1.0 {
		return src
	}
	b := src.Bounds()
	out := image.NewRGBA(b)
	ga := flameAlphaGain
	gc := flameColorGain
	for i := 0; i < len(src.Pix); i += 4 {
		if src.Pix[i+3] == 0 {
			continue
		}
		for k := 0; k < 3; k++ {
			v := int(float64(src.Pix[i+k])*gc + 0.5)
			if v > 255 {
				v = 255
			}
			out.Pix[i+k] = uint8(v)
		}
		a := int(float64(src.Pix[i+3])*ga + 0.5)
		if a > 255 {
			a = 255
		}
		out.Pix[i+3] = uint8(a)
	}
	return out
}

// flameTexFor 懒加载单张阶段图（约 180x210 @4x，PNG 15~25KB）。
// 解码后保持**非预乘**语义（png.Decode 对带 alpha 的图返回 NRGBA），
// 与 drawScaledEx 的取样口径一致。
func flameTexFor(style int, dirName string, stage int) *image.RGBA {
	key := itoa(style) + "_" + dirName + "_" + itoa(stage)
	flameTexMu.Lock()
	if t, hit := flameTexes[key]; hit {
		flameTexMu.Unlock()
		return t
	}
	flameTexMu.Unlock()

	img, err := decodePNG(filepath.Join("fly", "flame", itoa(style),
		dirName+"_"+itoa(stage)+".png"))
	if err != nil {
		// 记空，避免每帧重复 IO
		flameTexMu.Lock()
		flameTexes[key] = nil
		flameTexMu.Unlock()
		logf("flame tex 缺失 %s: %v", key, err)
		return nil
	}
	flameTexMu.Lock()
	flameTexes[key] = flameBrighten(img)
	flameTexMu.Unlock()
	return flameTexes[key]
}

// drawFlameOfficial 用官方帧画尾焰，落位口径与旧 drawSmoke 完全一致：
// 中心 = 机体 bbox 中心 + (cdx,cdy)·k，尺寸 = 官方火焰 bbox × k。
//
// 返回 false 表示素材缺失，调用方回退到程序化/旧贴图链路。
func drawFlameOfficial(dst *image.RGBA, style int, dirName string, k float64,
	body image.Rectangle, phase int) bool {
	r := flameOffFor(style, dirName)
	if r == nil || r.N <= 0 || len(r.Seq) == 0 {
		return false
	}
	if k <= 0 || body.Dx() < 8 || body.Dy() < 8 {
		return true // 素材在，只是机体太小：吞掉，不要回退成程序化火焰
	}
	// 12 个喷流相位 → 8 帧官方循环（匀速映射，不跳帧）
	idx := phase * len(r.Seq) / smokePhase
	if idx < 0 {
		idx = 0
	}
	if idx >= len(r.Seq) {
		idx = len(r.Seq) - 1
	}
	stage := r.Seq[idx]
	if stage < 0 || stage >= r.N {
		stage = 0
	}
	tex := flameTexFor(style, dirName, stage)
	if tex == nil {
		return true
	}
	bcx := float64(body.Min.X+body.Max.X) / 2
	bcy := float64(body.Min.Y+body.Max.Y) / 2
	fw := r.Fw * k
	fh := r.Fh * k
	x := bcx + r.Cdx*k - fw/2
	y := bcy + r.Cdy*k - fh/2
	drawScaledEx(dst, tex, x, y, fw, fh, 1.0, 0, false)
	return true
}
