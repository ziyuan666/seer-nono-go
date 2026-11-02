//go:build windows

package main

// UI 素材与绘制基础：加载 Game 内导出的 PNG，做缩放/合成/文字层。
// 设计要点（为什么这样做）：
//   1. 面板外观直接取自 SWF 导出图，不重绘——保证与游戏 1:1；
//   2. GDI TextOut 写入 32bpp DIB 时不会写 alpha，因此文字先画到独立 DIB，
//      再把「RGB 非黑」的像素按不透明合成回画布，避免 UpdateLayeredWindow 后文字不可见；
//   3. 所有素材按需加载并缓存，缺文件只记日志不 panic，缺图时面板仍可用。

import (
	"fmt"
	"image"
	"image/png"
	"nonodeskpet/internal/vec"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var pGetTextExtent = gdi32.NewProc("GetTextExtentPoint32W")

// uiCache 缓存已解码的 UI 素材（key = assets/ui 下的文件名）。
var uiCache = map[string]*image.RGBA{}

// uiVecEnabled 控制面板底图是否走矢量。2026-10-25 起默认开启。
//
// 2026-10-23 曾因「面板几乎全空」而默认关闭：当时 svg.go 的三个缺陷叠加——
// ① radialGradient 漏读 fx/fy（偏心焦点被当圆心），② 曲线细分按边长估算导致
// 大圆弧被折成 20 多段、轮廓发毛，③ UsesFillWindingRule 未传入 _assemble。
// 修完这三处后逐像素对齐验证（对 FFDec 用同一 SWF 渲染的真值）：
//     main    mean_abs 2.72 / diff>30 2.2%
//     storage mean_abs 4.03 / diff>30 5.3%
//     info    mean_abs 11.66 / diff>30 14%（差异集中在文字字形边缘的 1px）
// 差异全部落在 1px 边缘环，颜色与形状区域内为 0，视觉一致，故切为默认。
// 回退开关：NONO_VEC_UI=0（旧名 NONO_VEC=0 仍兼容）。
var uiVecEnabled = !(os.Getenv("NONO_VEC_UI") == "0" || os.Getenv("NONO_VEC") == "0")

// uiVecCache 缓存「SVG 路径 + 目标尺寸」的渲染结果。
var uiVecCache = map[string]*image.RGBA{}

// uiNoVec 指定必须走 PNG、不走向量的底图。
//
// info 面板底图现在有两套状态图（普通 / 超能），并且要在底图上「擦掉」三块
// 由代码按当前状态重画的内容：扳手（改由扳手/√ 状态图标画）、超能等级图标行
// （按 SuperLevel 换帧）、超能形态的左侧头像（按 superStage 换帧）。
// SVG 里对应的是一批合并过的 path，按坐标定位删除不可靠；PNG 是 4x 底图
// （1194×1398），缩到逻辑尺寸后与矢量同分辨率，观感一致（tools/mk_info_ui.py）。
var uiNoVec = map[string]bool{"info.png": true, "info_super.png": true}

// uiPanel 返回面板底图：优先用 assets/ui/vec/<name>.svg 按目标尺寸直接光栅化，
// 素材缺失或解析失败时回退到 PNG（uiLoad + uiFit）。
//
// 为什么按目标尺寸直接光栅化而不是先渲染 1x 再缩放：矢量路线的全部价值就在
// 「任意 DPI 都是原生清晰」，先出 1x 再放大等于把优势抵消掉。
func uiPanel(name string, w, h int) *image.RGBA {
	if uiVecEnabled && !uiNoVec[name] && w > 0 && h > 0 {
		base := strings.TrimSuffix(name, ".png")
		key := fmt.Sprintf("%s@%dx%d", base, w, h)
		if im, ok := uiVecCache[key]; ok {
			return im
		}
		if doc, err := vec.ParseFile(filepath.Join(assetsRoot(), "ui", "vec", base+".svg")); err == nil {
			im := doc.Render(w, h)
			uiVecCache[key] = im
			logf("vec panel %s -> %dx%d (%d shapes)", base, w, h, len(doc.Shapes))
			return im
		} else {
			logf("vec panel %s 失败(%v)，回退 PNG", base, err)
		}
	}
	return uiFit(uiLoad(name), w, h)
}

// uiLoad 从 assets/ui 读取 PNG，返回 RGBA。失败返回 nil 并记日志。
func uiLoad(name string) *image.RGBA {
	if im, ok := uiCache[name]; ok {
		return im
	}
	f, err := os.Open(filepath.Join(assetsRoot(), "ui", name))
	if err != nil {
		logf("ui open %s: %v", name, err)
		return nil
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		logf("ui decode %s: %v", name, err)
		return nil
	}
	b := src.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			rgba.Set(x, y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	uiCache[name] = rgba
	return rgba
}

// uiFit 把素材缩放到目标尺寸：缩小用面积平均（保锐度），放大用双线性。
func uiFit(img *image.RGBA, w, h int) *image.RGBA {
	if img == nil {
		return nil
	}
	if w <= 0 || h <= 0 {
		return nil
	}
	if img.Bounds().Dx() == w && img.Bounds().Dy() == h {
		return img
	}
	if w < img.Bounds().Dx() || h < img.Bounds().Dy() {
		return areaScale(img, w, h)
	}
	return bilinearScale(img, w, h)
}

// uiBlend 把 src 以普通 alpha-over 合成到 dst 的 (ox,oy)。
func uiBlend(dst, src *image.RGBA, ox, oy int) {
	if dst == nil || src == nil {
		return
	}
	db := dst.Bounds()
	sb := src.Bounds()
	for y := 0; y < sb.Dy(); y++ {
		dy := oy + y
		if dy < 0 || dy >= db.Dy() {
			continue
		}
		for x := 0; x < sb.Dx(); x++ {
			dx := ox + x
			if dx < 0 || dx >= db.Dx() {
				continue
			}
			s := src.PixOffset(sb.Min.X+x, sb.Min.Y+y)
			sa := uint32(src.Pix[s+3])
			if sa == 0 {
				continue
			}
			d := dst.PixOffset(dx, dy)
			if sa == 255 {
				dst.Pix[d] = src.Pix[s]
				dst.Pix[d+1] = src.Pix[s+1]
				dst.Pix[d+2] = src.Pix[s+2]
				dst.Pix[d+3] = 255
				continue
			}
			inv := 255 - sa
			dst.Pix[d] = uint8((uint32(src.Pix[s])*sa + uint32(dst.Pix[d])*inv) / 255)
			dst.Pix[d+1] = uint8((uint32(src.Pix[s+1])*sa + uint32(dst.Pix[d+1])*inv) / 255)
			dst.Pix[d+2] = uint8((uint32(src.Pix[s+2])*sa + uint32(dst.Pix[d+2])*inv) / 255)
			dst.Pix[d+3] = uint8(sa + uint32(dst.Pix[d+3])*inv/255)
		}
	}
}

// uiLabel 是一条要画的文字。Color 为 COLORREF（0x00BBGGRR）。
type uiLabel struct {
	Text   string
	X, Y   int
	Size   int
	Bold   bool
	Color  uint32
	Center bool // true 时 X 视为中心点
}

var textR *Renderer

func textRenderer(w, h int) *Renderer {
	if textR == nil || textR.W != w || textR.H != h {
		if textR != nil {
			textR.release()
		}
		textR = newRenderer(w, h)
	}
	return textR
}

func uiTextWidth(hdc HDC, s string, size int, bold bool) int {
	f := setFont(hdc, size, bold)
	old, _, _ := pSelectObject.Call(uintptr(hdc), uintptr(f))
	wt, _ := syscall.UTF16FromString(s)
	var sz SIZE
	pGetTextExtent.Call(uintptr(hdc), uintptr(unsafe.Pointer(&wt[0])),
		uintptr(len(wt)-1), uintptr(unsafe.Pointer(&sz)))
	pSelectObject.Call(uintptr(hdc), old)
	pDeleteObject.Call(uintptr(f))
	return int(sz.CX)
}

// uiDrawLabels 把标签画到画布。
// 做法：逐个标签用白色画到临时 DIB（白色分量即覆盖率），再按标签实际颜色做 alpha 混合。
// 为什么要绕这一下：GDI 在 32bpp DIB 上不写 alpha，若直接用文字颜色画，
// 黑色文字与"空白像素"完全无法区分；用白色画则可以用亮度当 alpha。
func uiDrawLabels(dst *image.RGBA, labels []uiLabel) {
	if dst == nil || len(labels) == 0 {
		return
	}
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	r := textRenderer(w, h)
	if r.Bits == nil {
		return
	}
	buf := unsafe.Slice((*byte)(r.Bits), w*h*4)
	for _, l := range labels {
		for i := range buf {
			buf[i] = 0
		}
		x := l.X
		if l.Center {
			x = l.X - uiTextWidth(r.Hdc, l.Text, l.Size, l.Bold)/2
		}
		// 白色 = 纯覆盖率，合成时才换成真实颜色
		text(r.Hdc, l.Text, x, l.Y, l.Size, l.Bold, 0x00FFFFFF)

		cr := int(l.Color & 0xFF)
		cg := int((l.Color >> 8) & 0xFF)
		cb := int((l.Color >> 16) & 0xFF)
		for y := 0; y < h; y++ {
			for x2 := 0; x2 < w; x2++ {
				i := (y*w + x2) * 4
				cov := int(buf[i+1]) // 白色文字的绿分量≈覆盖率
				if cov == 0 {
					continue
				}
				d := dst.PixOffset(x2, y)
				da := int(dst.Pix[d+3])
				outA := cov + da*(255-cov)/255
				for c := 0; c < 3; c++ {
					sc := cb
					if c == 0 {
						sc = cr
					} else if c == 1 {
						sc = cg
					}
					dst.Pix[d+c] = uint8(sc*cov/255 + int(dst.Pix[d+c])*(255-cov)/255)
				}
				dst.Pix[d+3] = uint8(outA)
			}
		}
	}
}

// darken 把图片整体压暗（用于禁用态）。
func uiDarken(src *image.RGBA) *image.RGBA {
	if src == nil {
		return nil
	}
	out := image.NewRGBA(src.Bounds())
	copy(out.Pix, src.Pix)
	for i := 0; i < len(out.Pix); i += 4 {
		out.Pix[i] = uint8(uint32(out.Pix[i]) * 45 / 100)
		out.Pix[i+1] = uint8(uint32(out.Pix[i+1]) * 45 / 100)
		out.Pix[i+2] = uint8(uint32(out.Pix[i+2]) * 45 / 100)
		out.Pix[i+3] = uint8(uint32(out.Pix[i+3]) * 65 / 100)
	}
	return out
}

// uiGray 灰度化（地图不允许跟随等置灰场景）。
func uiGray(src *image.RGBA) *image.RGBA {
	if src == nil {
		return nil
	}
	out := image.NewRGBA(src.Bounds())
	copy(out.Pix, src.Pix)
	for i := 0; i < len(out.Pix); i += 4 {
		b, g, r := uint32(out.Pix[i]), uint32(out.Pix[i+1]), uint32(out.Pix[i+2])
		v := (b*29 + g*150 + r*77) / 256
		out.Pix[i] = uint8(v)
		out.Pix[i+1] = uint8(v)
		out.Pix[i+2] = uint8(v)
	}
	return out
}

// uiAlpha 按比例调整整体不透明度（用于面板底板半透明化）。
func uiAlpha(img *image.RGBA, f float64) *image.RGBA {
	if img == nil {
		return nil
	}
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	out := image.NewRGBA(img.Bounds())
	copy(out.Pix, img.Pix)
	for i := 3; i < len(out.Pix); i += 4 {
		out.Pix[i] = uint8(float64(out.Pix[i]) * f)
	}
	return out
}

// cloneRGBA 深拷贝一张图，修改像素前必须复制，避免污染 uiCache。
func cloneRGBA(src *image.RGBA) *image.RGBA {
	if src == nil {
		return nil
	}
	dst := image.NewRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}
