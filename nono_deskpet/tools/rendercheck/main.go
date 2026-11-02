// 临时诊断工具：分层渲染指定方向的帧，供目视核对；跑完即删。
package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

func main() {
	dir, dirName, out := os.Args[1], os.Args[2], os.Args[3]
	os.MkdirAll(out, 0o755)
	sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
	if err != nil {
		panic(err)
	}
	lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
	if err != nil {
		panic(err)
	}
	r := sprite.New(sp, lib)
	const scale, ss = 4.0, 1
	n := r.FrameCount(dirName)
	var union image.Rectangle
	first := true
	for i := 0; i < n; i++ {
		img, off, err := r.RenderBbox(dirName, i, scale, ss)
		if err != nil {
			panic(err)
		}
		rect := image.Rect(int(off[0]), int(off[1]), int(off[0])+img.Bounds().Dx(), int(off[1])+img.Bounds().Dy())
		if first {
			union, first = rect, false
		} else {
			union = union.Union(rect)
		}
	}
	for _, l := range []string{"", "color_1", "color_2", "body"} {
		if l == "" {
			r.SetOnlyLayers()
		} else {
			r.SetOnlyLayers(l)
		}
		for i := 0; i < n; i++ {
			img, off, err := r.RenderBbox(dirName, i, scale, ss)
			if err != nil {
				panic(err)
			}
			canvas := image.NewRGBA(image.Rect(0, 0, union.Dx(), union.Dy()))
			dx, dy := int(off[0])-union.Min.X, int(off[1])-union.Min.Y
			for y := 0; y < img.Bounds().Dy(); y++ {
				for x := 0; x < img.Bounds().Dx(); x++ {
					if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
						canvas.Set(dx+x, dy+y, img.At(x, y))
					}
				}
			}
			name := l
			if name == "" {
				name = "all"
			}
			f, _ := os.Create(filepath.Join(out, fmt.Sprintf("%s_%02d.png", name, i)))
			png.Encode(f, canvas)
			f.Close()
		}
	}
	fmt.Printf("%s union=%v frames=%d\n", dirName, union, n)
}
