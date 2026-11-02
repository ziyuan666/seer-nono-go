//go:build windows

package main

import (
	"image"
	"image/color"
	"math"
	"os"
	"unsafe"
)

// tintedCopy 把接近纯白的像素替换为目标色（NONO 的 color_1/color_2 是纯白蒙版）。
// 只改色相不改 alpha，保留抗锯齿边缘。
func tintedCopy(src *image.RGBA, target color.RGBA) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	copy(dst.Pix, src.Pix)
	for i := 0; i < len(dst.Pix); i += 4 {
		a := dst.Pix[i+3]
		if a == 0 {
			continue
		}
		r, g, bl := dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2]
		// 白蒙版判定：三通道都很亮
		m := min3(r, g, bl)
		if m < 225 {
			continue
		}
		// 亮度越白，染色权重越高（保留边缘过渡）
		w := float32(m-225) / 30.0
		if w > 1 {
			w = 1
		}
		dst.Pix[i] = uint8(float32(target.R)*w + float32(r)*(1-w))
		dst.Pix[i+1] = uint8(float32(target.G)*w + float32(g)*(1-w))
		dst.Pix[i+2] = uint8(float32(target.B)*w + float32(bl)*(1-w))
	}
	return dst
}

func min3(a, b, c uint8) uint8 {
	if a < b {
		b = a
	}
	if b < c {
		return b
	}
	return c
}

// bilinearScale 手写双线性缩放，避免引入 x/image 依赖。
func bilinearScale(src *image.RGBA, w, h int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == w && sh == h {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		fy := (float64(y)+0.5)*float64(sh)/float64(h) - 0.5
		y0 := int(fy)
		if fy < 0 {
			y0 = 0
		}
		y1 := y0 + 1
		if y1 >= sh {
			y1 = sh - 1
		}
		wy := fy - float64(y0)
		if wy < 0 {
			wy = 0
		}
		for x := 0; x < w; x++ {
			fx := (float64(x)+0.5)*float64(sw)/float64(w) - 0.5
			x0 := int(fx)
			if fx < 0 {
				x0 = 0
			}
			x1 := x0 + 1
			if x1 >= sw {
				x1 = sw - 1
			}
			wx := fx - float64(x0)
			if wx < 0 {
				wx = 0
			}
			c00 := src.RGBAAt(sb.Min.X+x0, sb.Min.Y+y0)
			c10 := src.RGBAAt(sb.Min.X+x1, sb.Min.Y+y0)
			c01 := src.RGBAAt(sb.Min.X+x0, sb.Min.Y+y1)
			c11 := src.RGBAAt(sb.Min.X+x1, sb.Min.Y+y1)
			// 预乘 alpha 双线性：直接对非预乘 RGB 插值会把透明像素的 RGB(0)
			// 混进边缘，出现黑边和红/绿彩色描边（子渊 2026-10-24「严重锯齿」）。
			a := blerp(float64(c00.A), float64(c10.A), float64(c01.A), float64(c11.A), wx, wy)
			if a <= 0.5 {
				dst.SetRGBA(x, y, color.RGBA{})
				continue
			}
			pr := blerp(float64(c00.R)*float64(c00.A), float64(c10.R)*float64(c10.A),
				float64(c01.R)*float64(c01.A), float64(c11.R)*float64(c11.A), wx, wy)
			pg := blerp(float64(c00.G)*float64(c00.A), float64(c10.G)*float64(c10.A),
				float64(c01.G)*float64(c01.A), float64(c11.G)*float64(c11.A), wx, wy)
			pb := blerp(float64(c00.B)*float64(c00.A), float64(c10.B)*float64(c10.A),
				float64(c01.B)*float64(c01.A), float64(c11.B)*float64(c11.A), wx, wy)
			k := 1.0 / a
			dst.SetRGBA(x, y, color.RGBA{
				clamp8(pr * k),
				clamp8(pg * k),
				clamp8(pb * k),
				clamp8(a),
			})
		}
	}
	return dst
}

func blerp(v00, v10, v01, v11, wx, wy float64) float64 {
	top := v00*(1-wx) + v10*wx
	bot := v01*(1-wx) + v11*wx
	return top*(1-wy) + bot*wy
}

// Renderer 持有可复用的 DIB，避免每帧创建 GDI 对象。
type Renderer struct {
	Hdc  HDC
	Hbmp HBITMAP
	Old  HBITMAP
	Bits unsafe.Pointer
	W, H int
}

func newRenderer(w, h int) *Renderer {
	hdc, _, _ := pCreateCompatibleDC.Call(0)
	bi := BITMAPINFO{}
	bi.BmiHeader.BiSize = uint32(unsafe.Sizeof(bi.BmiHeader))
	bi.BmiHeader.BiWidth = int32(w)
	bi.BmiHeader.BiHeight = -int32(h) // 负高度 = 自上而下
	bi.BmiHeader.BiPlanes = 1
	bi.BmiHeader.BiBitCount = 32
	bi.BmiHeader.BiCompression = 0
	var bits unsafe.Pointer
	hbmp, _, _ := pCreateDIBSection.Call(hdc, uintptr(unsafe.Pointer(&bi)), DIB_RGB_COLORS,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	old, _, _ := pSelectObject.Call(hdc, hbmp)
	return &Renderer{Hdc: HDC(hdc), Hbmp: HBITMAP(hbmp), Old: HBITMAP(old),
		Bits: bits, W: w, H: h}
}

func (r *Renderer) release() {
	if r.Hdc != 0 {
		pSelectObject.Call(uintptr(r.Hdc), uintptr(r.Old))
		pDeleteObject.Call(uintptr(r.Hbmp))
		pDeleteDC.Call(uintptr(r.Hdc))
		r.Hdc = 0
	}
}

// blit 把 RGBA 写入 DIB（BGRA + 预乘 alpha）。
func (r *Renderer) blit(img *image.RGBA) {
	if r.Bits == nil {
		return
	}
	dst := unsafe.Slice((*byte)(r.Bits), r.W*r.H*4)
	sb := img.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 {
		return
	}
	// 2026-10-25 重写：旧版每像素 RGBAAt（含边界检查）+ 3 次除法，1036x1024
	// 实测吃 15~25ms，是 24fps 掉到 20fps 的主因。现在直接走 Pix、按 alpha
	// 分三种情况（不透明/全透明/半透明），内容区绝大多数像素走零除法路径。
	// 先整体清零（Go 的 memclr 很快），再只写「有内容」的行：
	// 动作画面 1036x1024 里真正有像素的常常只有中间 300 行左右，全量逐像素
	// 写 100 万点纯属浪费。
	for i := range dst {
		dst[i] = 0
	}
	nonEmpty := 0
	for y := 0; y < r.H; y++ {
		sy := y
		if sy >= sh {
			sy = sh - 1
		}
		row := img.Pix[img.PixOffset(sb.Min.X, sb.Min.Y+sy):]
		if rowAlphaEmpty(row, sw) {
			continue
		}
		nonEmpty++
		di := y * r.W * 4
		for x := 0; x < r.W; x++ {
			sx := x
			if sx >= sw {
				sx = sw - 1
			}
			p := row[sx*4 : sx*4+4]
			a := p[3]
			switch a {
			case 255:
				// UpdateLayeredWindow 要预乘，a=255 时预乘不变，直拷即可
				dst[di] = p[2]
				dst[di+1] = p[1]
				dst[di+2] = p[0]
				dst[di+3] = 255
			case 0:
				dst[di], dst[di+1], dst[di+2], dst[di+3] = 0, 0, 0, 0
			default:
				aa := uint32(a)
				dst[di] = uint8(uint32(p[2]) * aa / 255)
				dst[di+1] = uint8(uint32(p[1]) * aa / 255)
				dst[di+2] = uint8(uint32(p[0]) * aa / 255)
				dst[di+3] = a
			}
			di += 4
		}
	}
	if profEnabled && blitN%90 == 0 {
		logf("PROF blit %dx%d img=%dx%d 非空行=%d", r.W, r.H, sw, sh, nonEmpty)
	}
	// 调试：把刚写进 DIB 的最终位图原样落盘（预乘 BGRA）。用于区分
	// 「画布算错」与「Wine 合成不显示」——2026-10-26 排查主窗口整块黑时
	// 只靠 X11 截图无法判断，程序内部画布和屏幕上看到的不一致。
	if os.Getenv("NONO_DEBUG_DIB") != "" && r.W >= 500 && blitBigN < 3 {
		blitBigN++
		if f, err := os.Create("dib_dump.bgra"); err == nil {
			f.Write(dst)
			f.Close()
			logf("dib dump %dx%d -> dib_dump.bgra", r.W, r.H)
		}
	}
	blitN++
}

var blitN int
var blitBigN int

// rowAlphaEmpty 判断一整行是否全透明（alpha 全 0），用于跳过空行。
// 按 8 字节一次粗筛：RGBA 布局下 alpha 在每 4 字节的第 4 位，粗筛只在
// 全零时才快速返回，遇到任何非零字节就退回逐像素判断，不会误判。
func rowAlphaEmpty(row []byte, n int) bool {
	for x := 0; x < n; x++ {
		if row[x*4+3] != 0 {
			return false
		}
	}
	return true
}

// resizeFrame 缩小用面积平均（保锐度），放大用双线性（保平滑）。
func resizeFrame(src *image.RGBA, w, h int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == w && sh == h {
		return src
	}
	// 缩小 2 倍以上走多级面积平均：下采样场景面积平均就是正确的积分，
	// 没有 Lanczos 的 sin 核计算（pprof 实测 lanczosResize+lanczos2+sin
	// 合计约 24% CPU，动作期是掉帧的主因之一），速度约 5 倍。
	// 1.5 倍显示时 4x 素材正好缩小 2.67 倍，落在这条路径上。
	if float64(sw) >= float64(w)*2 || float64(sh) >= float64(h)*2 {
		return areaScaleMulti(src, w, h)
	}
	return lanczosResize(src, w, h)
}

// lanczosResize 预乘 alpha 的 Lanczos3 重采样（支持任意缩放）。
// 为什么不用 x/image/draw：它的 CatmullRom/BiLinear 对 RGBA 按预乘处理没错，
// 但只有固定核，缩小 4 倍以上会走样；这里自己算 Lanczos3 的归一化权重，
// 缩放比再大也能把整段源像素纳入积分。
func lanczosResize(src *image.RGBA, w, h int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == w && sh == h {
		return src
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	// 预乘展开
	pre := make([]float64, sw*sh*4)
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			c := src.RGBAAt(sb.Min.X+x, sb.Min.Y+y)
			a := float64(c.A) / 255
			i := (y*sw + x) * 4
			pre[i] = float64(c.R) * a
			pre[i+1] = float64(c.G) * a
			pre[i+2] = float64(c.B) * a
			pre[i+3] = float64(c.A)
		}
	}
	scaleX := float64(sw) / float64(w)
	scaleY := float64(sh) / float64(h)
	// 缩放时核半径跟着放大，保证缩小时覆盖足够多的源像素（抗走样）
	// Lanczos2（a=2）：振铃比 Lanczos3 小一半。Lanczos3 在青色机身与深色描边
	// 的交界处会把 G 抬过头、B 压过头，出成一颗颗亮绿点（子渊 2026-10-24 反馈的
	// 「颜色不统一」部分来源），a=2 兼顾锐度与过冲。
	supX := 2.0
	if scaleX > 1 {
		supX = 2.0 * scaleX
	}
	supY := 2.0
	if scaleY > 1 {
		supY = 2.0 * scaleY
	}
	tmp := make([]float64, w*sh*4)
	// 水平
	for y := 0; y < sh; y++ {
		for x := 0; x < w; x++ {
			cx := (float64(x)+0.5)*scaleX - 0.5
			x0 := int(math.Floor(cx - supX))
			x1 := int(math.Ceil(cx + supX))
			var acc [4]float64
			var wsum float64
			for xx := x0; xx <= x1; xx++ {
				sx := xx
				if sx < 0 {
					sx = 0
				}
				if sx >= sw {
					sx = sw - 1
				}
				d := math.Abs(float64(xx) - cx)
				if d > supX {
					continue
				}
				wt := lanczos2(d / (supX / 2.0))
				i := (y*sw + sx) * 4
				acc[0] += pre[i] * wt
				acc[1] += pre[i+1] * wt
				acc[2] += pre[i+2] * wt
				acc[3] += pre[i+3] * wt
				wsum += wt
			}
			o := (y*w + x) * 4
			if wsum != 0 {
				for k := 0; k < 4; k++ {
					tmp[o+k] = acc[k] / wsum
				}
			}
		}
	}
	// 垂直
	for y := 0; y < h; y++ {
		cy := (float64(y)+0.5)*scaleY - 0.5
		y0 := int(math.Floor(cy - supY))
		y1 := int(math.Ceil(cy + supY))
		for x := 0; x < w; x++ {
			var acc [4]float64
			var wsum float64
			// 邻域不透明像素的 RGB 范围（非预乘），用于压 Lanczos 过冲
			lo := [3]float64{255, 255, 255}
			hi := [3]float64{0, 0, 0}
			hasOpaque := false
			for yy := y0; yy <= y1; yy++ {
				sy := yy
				if sy < 0 {
					sy = 0
				}
				if sy >= sh {
					sy = sh - 1
				}
				d := math.Abs(float64(yy) - cy)
				if d > supY {
					continue
				}
				wt := lanczos2(d / (supY / 2.0))
				i := (sy*w + x) * 4
				acc[0] += tmp[i] * wt
				acc[1] += tmp[i+1] * wt
				acc[2] += tmp[i+2] * wt
				acc[3] += tmp[i+3] * wt
				wsum += wt
				if tmp[i+3] > 60 {
					hasOpaque = true
					for k := 0; k < 3; k++ {
						v := tmp[i+k] * 255.0 / tmp[i+3]
						if v < lo[k] {
							lo[k] = v
						}
						if v > hi[k] {
							hi[k] = v
						}
					}
				}
			}
			if wsum == 0 {
				continue
			}
			a := acc[3] / wsum
			if a <= 0.5 {
				continue
			}
			k := 255.0 / a
			r0 := acc[0] / wsum * k
			g0 := acc[1] / wsum * k
			b0 := acc[2] / wsum * k
			// 过冲抑制：Lanczos 核有负瓣，在「青色机身 / 深色描边」这类高对比
			// 交界上会把 G 抬过头、B 压过头，输出成一颗颗亮绿点（子渊 2026-10-24
			// 「颜色不统一」的残留来源）。夹回邻域真实颜色范围，不引入新色相。
			if hasOpaque {
				r0 = math.Min(math.Max(r0, lo[0]), hi[0])
				g0 = math.Min(math.Max(g0, lo[1]), hi[1])
				b0 = math.Min(math.Max(b0, lo[2]), hi[2])
			}
			dst.SetRGBA(x, y, color.RGBA{
				clamp8(r0),
				clamp8(g0),
				clamp8(b0),
				clamp8(a),
			})
		}
	}
	return dst
}

// lanczos2 标准 Lanczos 核（a=2）。
func lanczos2(x float64) float64 {
	if x == 0 {
		return 1
	}
	if x <= -2 || x >= 2 {
		return 0
	}
	px := math.Pi * x
	return 2 * math.Sin(px) * math.Sin(px/2) / (px * px)
}

// areaScaleMulti 多级面积平均下采样：每级最多缩一半，避免单级 box 在
// 大比例缩小时的走样。4x 素材缩到 0.2 倍时会走 4x→2x→1x→目标 三级。
func areaScaleMulti(src *image.RGBA, w, h int) *image.RGBA {
	cur := src
	for {
		sw := cur.Bounds().Dx()
		sh := cur.Bounds().Dy()
		if sw <= w && sh <= h {
			break
		}
		nw, nh := sw/2, sh/2
		if nw < w {
			nw = w
		}
		if nh < h {
			nh = h
		}
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
		cur = areaScale(cur, nw, nh)
		if nw == w && nh == h {
			return cur
		}
	}
	if cur.Bounds().Dx() != w || cur.Bounds().Dy() != h {
		cur = areaScale(cur, w, h)
	}
	return cur
}

// span 是面积平均的一次「源游程」：源下标 + 该方向上的覆盖权重。
// 预计算后内层循环只剩整数索引与乘法，不再逐像素调 math.Ceil/Min/Max。
type span struct {
	idx int
	w   float64
}

// areaAlpha 是 alpha 0..255 → 0..1 的查表，省掉每像素一次除法
// （255 项、数值与 float64(v)/255 完全一致）。
var areaAlpha = func() (t [256]float64) {
	for i := 0; i < 256; i++ {
		t[i] = float64(i) / 255
	}
	return
}()

// areaScale 面积平均下采样：4x 素材缩到 2x 显示时，边缘比双线性干净得多。
func areaScale(src *image.RGBA, w, h int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if w < 1 || h < 1 || sw < 1 || sh < 1 {
		return dst
	}
	// 预计算每个输出列/行的源游程与权重。
	//
	// 为什么值得预计算：超能形态的 4x 帧约 1093×936，缩到 293×174 要遍历
	// 一百多万源像素，原来每像素都调 math.Ceil/Min/Max 各两次，实测 15~18ms/tick
	// 把 40ms 的 tick 预算吃掉一半。游程只跟尺寸有关，算一次用一整帧，
	// 累加顺序与逐像素计算完全一致（同样的 wx、wy、同样的乘法与求和次序），
	// 输出逐像素相同。
	// 游程用「扁平数组 + 偏移表」而不是 [][]span：后者每列每行各是一次
	// 小切片分配，293×174 一帧就是 5 万个对象，GC 扫描成本比计算本身还高。
	perX := sw/w + 2
	colOff := make([]int32, w+1)
	colSpan := make([]span, 0, w*perX)
	for x := 0; x < w; x++ {
		colOff[x] = int32(len(colSpan))
		sx0 := float64(x) * float64(sw) / float64(w)
		sx1 := float64(x+1) * float64(sw) / float64(w)
		xEnd := int(math.Ceil(sx1))
		if xEnd > sw {
			xEnd = sw
		}
		for xx := int(sx0); xx < xEnd; xx++ {
			wx := math.Min(sx1, float64(xx+1)) - math.Max(sx0, float64(xx))
			if wx <= 0 {
				continue
			}
			colSpan = append(colSpan, span{idx: xx, w: wx})
		}
	}
	colOff[w] = int32(len(colSpan))
	perY := sh/h + 2
	rowOff := make([]int32, h+1)
	rowSpan := make([]span, 0, h*perY)
	for y := 0; y < h; y++ {
		rowOff[y] = int32(len(rowSpan))
		sy0 := float64(y) * float64(sh) / float64(h)
		sy1 := float64(y+1) * float64(sh) / float64(h)
		yEnd := int(math.Ceil(sy1))
		if yEnd > sh {
			yEnd = sh
		}
		for yy := int(sy0); yy < yEnd; yy++ {
			wy := math.Min(sy1, float64(yy+1)) - math.Max(sy0, float64(yy))
			if wy <= 0 {
				continue
			}
			rowSpan = append(rowSpan, span{idx: yy, w: wy})
		}
	}
	rowOff[h] = int32(len(rowSpan))
	minX, minY := sb.Min.X, sb.Min.Y
	for y := 0; y < h; y++ {
		rsp := rowSpan[rowOff[y]:rowOff[y+1]]
		for x := 0; x < w; x++ {
			csp := colSpan[colOff[x]:colOff[x+1]]
			var r, g, b, a, wsum, awsum float64
			// 边缘兜底：记下窗口内 alpha 最大的源像素颜色。低覆盖度区域用
			// alpha 加权平均会因分母过小把量化和半透明噪点放大成彩色杂点
			// （子渊 2026-10-24「严重锯齿」的主因），此时直接用最不透明的
			// 那个像素的颜色，视觉上就是正常抗锯齿边缘。
			bestA := uint8(0)
			var br, bg, bb uint8
			for _, ys := range rsp {
				row := (ys.idx + minY) * src.Stride
				for _, xs := range csp {
					p := row + (xs.idx+minX)*4
					ac := src.Pix[p+3]
					wgt := xs.w * ys.w
					af := areaAlpha[ac]
					r += float64(src.Pix[p+0]) * af * wgt
					g += float64(src.Pix[p+1]) * af * wgt
					b += float64(src.Pix[p+2]) * af * wgt
					a += float64(ac) * wgt
					awsum += af * wgt
					wsum += wgt
					if ac > bestA {
						bestA, br, bg, bb = ac, src.Pix[p+0], src.Pix[p+1], src.Pix[p+2]
					}
				}
			}
			if wsum <= 0 || a <= 0 {
				continue
			}
			cov := awsum / wsum // 平均 alpha 覆盖度
			var or, og, ob uint8
			if cov < 0.25 {
				or, og, ob = br, bg, bb
			} else {
				or = clamp8(r / awsum)
				og = clamp8(g / awsum)
				ob = clamp8(b / awsum)
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o+0] = or
			dst.Pix[o+1] = og
			dst.Pix[o+2] = ob
			dst.Pix[o+3] = clamp8(a / wsum)
		}
	}
	return dst
}

func clamp8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}
