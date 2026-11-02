package sprite

import (
	"image"
	"image/color"
	"math"
	"strconv"

	"nonovec/pkg/raster"
	"nonovec/pkg/spec"
)

// filterFor 取某个滤镜组的滤镜列表（SWF 允许一个 place 挂多条，按顺序作用）。
func (r *Renderer) filterFor(fid string, all map[string][]spec.Filter) []spec.Filter {
	if fid == "" || all == nil {
		return nil
	}
	fs := all[fid]
	out := make([]spec.Filter, 0, len(fs))
	for _, f := range fs {
		switch f.Type {
		case "glow", "shadow", "blur":
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// applyFilters 把成组图层 src 依次过一遍滤镜链，再把结果合成进 dst。
//
// 为什么必须按顺序链式处理：真实素材里既有「先模糊 2px 再发光 5px」这种组合
// （super1/action_1），只取第一条会丢掉发光，整体观感差一档。
// k 是渲染缩放（scale*ss），模糊半径要跟着放大，否则 4x 渲染下光晕只有 1/4 大。
func (r *Renderer) applyFilters(dst, src *image.RGBA, fs []spec.Filter, k float64) {
	cur := src
	for _, f := range fs {
		col := parseRGBA(f.Color)
		switch f.Type {
		case "glow":
			cur = raster.GlowLayer(cur, f.BlurX*k, f.BlurY*k, f.Strength, col,
				f.CompositeSource, f.Inner, f.Knockout)
		case "shadow":
			sh := shiftImage(cur, f.Distance*k*math.Cos(f.Angle*math.Pi/180),
				f.Distance*k*math.Sin(f.Angle*math.Pi/180))
			cur = raster.GlowLayer(sh, f.BlurX*k, f.BlurY*k, f.Strength, col,
				f.CompositeSource, f.Inner, f.Knockout)
		case "blur":
			cur = raster.BlurLayer(cur, f.BlurX*k, f.BlurY*k)
		}
	}
	raster.OverNonPremul(dst, cur, 0, 0)
}

// shiftImage 按 (dx,dy) 平移一张图层，用于投影滤镜（只挪位置，不缩放）。
func shiftImage(src *image.RGBA, dx, dy float64) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(b)
	ox, oy := int(math.Round(dx)), int(math.Round(dy))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		sy := y - oy
		if sy < b.Min.Y || sy >= b.Max.Y {
			continue
		}
		for x := b.Min.X; x < b.Max.X; x++ {
			sx := x - ox
			if sx < b.Min.X || sx >= b.Max.X {
				continue
			}
			si := sy*src.Stride + sx*4
			di := y*out.Stride + x*4
			copy(out.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return out
}

// parseRGBA 解析素材里的 "#rrggbbaa"（6 位视为不透明）。
func parseRGBA(s string) color.NRGBA {
	if len(s) == 0 {
		return color.NRGBA{255, 255, 255, 255}
	}
	h := s
	if h[0] == '#' {
		h = h[1:]
	}
	v, err := strconv.ParseUint(h, 16, 64)
	if err != nil {
		return color.NRGBA{255, 255, 255, 255}
	}
	switch len(h) {
	case 6:
		return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
	case 8:
		return color.NRGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}
	}
	return color.NRGBA{255, 255, 255, 255}
}
