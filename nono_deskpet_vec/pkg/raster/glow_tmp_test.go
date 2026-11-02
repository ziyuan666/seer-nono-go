package raster

import (
	"image"
	"image/color"
	"testing"
)

func TestGlowTmp(t *testing.T) {
	w, h := 120, 120
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 40; y < 80; y++ {
		for x := 40; x < 80; x++ {
			i := y*src.Stride + x*4
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 255, 255, 255, 255
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	DrawGlow(dst, src, 20, 20, 1.0, color.NRGBA{255, 255, 255, 255}, true, false)
	for _, r := range []int{0, 10, 20, 25, 30, 40} {
		i := 60*dst.Stride + (60+r)*4
		t.Logf("r=%d rgba=%v", r, dst.Pix[i:i+4])
	}
}
