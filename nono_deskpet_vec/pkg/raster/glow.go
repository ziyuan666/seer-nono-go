package raster

import (
	"image"
	"image/color"
	"math"
	"os"
	"strconv"
	"sync"
)

// f32Pool 复用模糊用的浮点平面。
//
// 为什么要池化：一次 glow 需要「alpha 平面 + 两块 ping-pong 缓冲」共 3 块
// w*h 的 float32；4x 帧一块就要 3~4MB，原来 6 次 boxBlur 各 new 一块，
// 单帧产生 20MB+ 垃圾。实测预热 15s 内 boxBlur 累计分配 1.4GB（占总量 43%），
// 把 GC 堆推到 800MB。复用不改变任何浮点运算顺序，输出逐像素一致。
var f32Pool = sync.Pool{New: func() any { return make([]float32, 0) }}

func getPlane(n int) []float32 {
	v := f32Pool.Get().([]float32)
	if cap(v) < n {
		return make([]float32, n)
	}
	return v[:n]
}

// 注：曾试过「只让 ≤1MB 的平面进池」以减少常驻，但实测大帧每次 new 三块
// 让 totalAlloc 翻 2.6 倍、GC 次数翻倍、tick 从 2.4ms 涨到 16ms —— 净负优化，
// 已回滚。sync.Pool 的块在 GC 时会被清空，长期常驻并不成立。
func putPlane(v []float32) {
	if v == nil {
		return
	}
	f32Pool.Put(v[:0])
}

// alphaToFloatInto 写进调用方给的平面，避免临时分配。
func alphaToFloatInto(src *image.RGBA, w, h int, a []float32) {
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride : y*src.Stride+w*4]
		base := y * w
		for x := 0; x < w; x++ {
			a[base+x] = float32(row[x*4+3]) / 255.0
		}
	}
}

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// boxBlurInto 写进 dst（长度须 ≥ len(src)），src 与 dst 不能是同一块。
func boxBlurInto(src, dst []float32, w, h int, r int, horiz bool) {
	if r < 1 {
		copy(dst, src)
		return
	}
	win := float32(2*r + 1)
	if horiz {
		for y := 0; y < h; y++ {
			base := y * w
			var sum float32
			for i := -r; i <= r; i++ {
				sum += src[base+clampi(i, 0, w-1)]
			}
			for x := 0; x < w; x++ {
				dst[base+x] = sum / win
				sum -= src[base+clampi(x-r, 0, w-1)]
				sum += src[base+clampi(x+r+1, 0, w-1)]
			}
		}
	} else {
		for x := 0; x < w; x++ {
			var sum float32
			for i := -r; i <= r; i++ {
				sum += src[clampi(i, 0, h-1)*w+x]
			}
			for y := 0; y < h; y++ {
				dst[y*w+x] = sum / win
				sum -= src[clampi(y-r, 0, h-1)*w+x]
				sum += src[clampi(y+r+1, 0, h-1)*w+x]
			}
		}
	}
}

// BlurAlpha 对 alpha 平面做三次盒滤波近似高斯，返回 0..1 的模糊 alpha。
// 返回值是独立新分配的平面（调用方可以长期持有）。
func BlurAlpha(a []float32, w, h int, radiusX, radiusY float64) []float32 {
	res := blurAlphaScratch(a, w, h, radiusX, radiusY, make([]float32, w*h), make([]float32, w*h))
	out := make([]float32, w*h)
	copy(out, res)
	return out
}

// blurAlphaScratch 用两块调用方提供的临时平面做 ping-pong，返回其中一块。
// 输入 a 不会被修改（先整块 copy 进 buf1），所以调用方之后仍可读 a
// （GlowLayer 的 inner/knockout 分支要用原始 alpha）。
func blurAlphaScratch(a []float32, w, h int, radiusX, radiusY float64,
	buf1, buf2 []float32) []float32 {
	rx, ry := gaussianRadius(radiusX), gaussianRadius(radiusY)
	n := w * h
	cur := buf1[:n]
	other := buf2[:n]
	copy(cur, a)
	// 3 轮 × (水平、垂直)，与改动前的运算顺序逐位一致。
	for i := 0; i < 3; i++ {
		boxBlurInto(cur, other, w, h, rx, true)
		cur, other = other, cur
		boxBlurInto(cur, other, w, h, ry, false)
		cur, other = other, cur
	}
	return cur
}

// gaussianRadius 把 Flash 的 blurX/blurY（像素半径）换算成单次盒滤波的核半径。
//
// 标定方式：super5/action_3 全 90 帧扫描 F=0.18/0.22/0.25/0.30/0.35/0.475，
// 平均差 3.46/3.35/3.35/3.35/3.54/—，F=0.25 均值最低（3.96），且径向 alpha
// 剖面与金标准「r=30 归零」最接近。可用 NONO_GLOW_F 覆盖做回归对比。
func gaussianRadius(blur float64) int {
	if blur <= 0 {
		return 0
	}
	f := 0.25
	if s := os.Getenv("NONO_GLOW_F"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			f = v
		}
	}
	r := int(math.Round(blur * f))
	if r < 1 {
		r = 1
	}
	return r
}

// GlowLayer 返回「本体（可选）+ 发光」合成好的新图层，尺寸与 src 相同。
//
// 语义与 Flash/Ruffle 一致：halo = blur(alpha)；halo 乘 strength 并染成 glow 色；
// compositeSource=true 时把本体叠在 halo 之上；knockout=true 时本体区域被挖空，
// 只留 halo；inner=true 时 halo 取内侧（1-blur），形成贴边内发光。
func GlowLayer(src *image.RGBA, blurX, blurY, strength float64, col color.NRGBA,
	compositeSource, inner, knockout bool) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if w <= 0 || h <= 0 {
		return out
	}
	if strength <= 0 || col.A == 0 || (blurX <= 0 && blurY <= 0) {
		if compositeSource {
			copyRGBA(out, src)
		}
		return out
	}
	n := w * h
	a := getPlane(n)
	alphaToFloatInto(src, w, h, a)
	buf1 := getPlane(n)
	buf2 := getPlane(n)
	bl := blurAlphaScratch(a, w, h, blurX, blurY, buf1, buf2)
	ca := float64(col.A) / 255.0
	for i := 0; i < n; i++ {
		var t float64
		if inner {
			t = (1.0 - float64(bl[i])) * float64(a[i])
		} else {
			t = float64(bl[i])
			if knockout {
				t *= 1.0 - float64(a[i])
			}
		}
		al := t * strength * ca
		if al <= 0 {
			continue
		}
		if al > 1 {
			al = 1
		}
		out.Pix[i*4+0] = col.R
		out.Pix[i*4+1] = col.G
		out.Pix[i*4+2] = col.B
		out.Pix[i*4+3] = uint8(al*255 + 0.5)
	}
	putPlane(a)
	putPlane(buf1)
	putPlane(buf2)
	if compositeSource && !knockout {
		OverNonPremul(out, src, 0, 0)
	}
	return out
}

// BlurLayer 返回整层模糊后的新图层（RGB 与 alpha 都模糊）。
//
// 为什么 RGB 也要模糊：Flash 的 BlurFilter 是对整张位图卷积，只糊 alpha
// 会留下「清晰色块 + 糊边」的假象。
func BlurLayer(src *image.RGBA, blurX, blurY float64) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if w <= 0 || h <= 0 {
		return out
	}
	if blurX <= 0 && blurY <= 0 {
		copyRGBA(out, src)
		return out
	}
	n := w * h
	p := getPlane(n)
	buf1 := getPlane(n)
	buf2 := getPlane(n)
	for ch := 0; ch < 4; ch++ {
		for i := 0; i < n; i++ {
			p[i] = float32(src.Pix[i*4+ch]) / 255.0
		}
		bl := blurAlphaScratch(p, w, h, blurX, blurY, buf1, buf2)
		for i := 0; i < n; i++ {
			v := bl[i]
			if v < 0 {
				v = 0
			}
			if v > 1 {
				v = 1
			}
			out.Pix[i*4+ch] = uint8(v*255 + 0.5)
		}
	}
	putPlane(p)
	putPlane(buf1)
	putPlane(buf2)
	return out
}

// DrawGlow 保留给单滤镜调用方：合成到 dst。
func DrawGlow(dst, src *image.RGBA, blurX, blurY, strength float64,
	col color.NRGBA, compositeSource, inner bool) {
	OverNonPremul(dst, GlowLayer(src, blurX, blurY, strength, col, compositeSource, inner, false), 0, 0)
}

// copyRGBA 把 src 的全部像素拷到 out（两者尺寸一致、原点都是 0）。
func copyRGBA(out, src *image.RGBA) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	for y := 0; y < h; y++ {
		copy(out.Pix[y*out.Stride:y*out.Stride+w*4], src.Pix[y*src.Stride:y*src.Stride+w*4])
	}
}
