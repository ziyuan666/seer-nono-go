package main

import (
	"fmt"
	"os"
	"path/filepath"

	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

func load(dir string) (*spec.Sprite, *spec.Library) {
	sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
	if err != nil { panic(err) }
	lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
	if err != nil { panic(err) }
	return sp, lib
}

func diff(a, b interface{ Bounds() interface{} }) {}

func main() {
	dir1, dir2 := os.Args[1], os.Args[2]
	fi := 46
	sp1, lib1 := load(dir1)
	sp2, lib2 := load(dir2)
	n := len(sp1.Directions["default"].Frames[fi].Parts)
	fmt.Printf("f%d 共 %d 个 part\n", fi, n)

	prev := 0
	for k := 1; k <= n; k++ {
		// 只保留前 k 个 part
		a := sp1.Directions["default"].Frames[fi]
		b := sp2.Directions["default"].Frames[fi]
		if len(b.Parts) < k { break }
		orig1 := sp1.Directions["default"].Frames[fi].Parts
		orig2 := sp2.Directions["default"].Frames[fi].Parts
		sp1.Directions["default"].Frames[fi].Parts = orig1[:k]
		sp2.Directions["default"].Frames[fi].Parts = orig2[:k]
		r1 := sprite.New(sp1, lib1)
		r2 := sprite.New(sp2, lib2)
		img1, e1 := r1.RenderFullCanvas("default", fi, 2, 2)
		img2, e2 := r2.RenderFullCanvas("default", fi, 2, 2)
		if e1 != nil || e2 != nil { fmt.Printf("k=%d 渲染失败 %v %v\n", k, e1, e2); sp1.Directions["default"].Frames[fi].Parts = orig1; sp2.Directions["default"].Frames[fi].Parts = orig2; continue }
		d := 0
		bd := img1.Bounds()
		for y := bd.Min.Y; y < bd.Max.Y; y++ {
			for x := bd.Min.X; x < bd.Max.X; x++ {
				ia, ib := img1.PixOffset(x, y), img2.PixOffset(x, y)
				if img1.Pix[ia] != img2.Pix[ib] || img1.Pix[ia+1] != img2.Pix[ib+1] || img1.Pix[ia+2] != img2.Pix[ib+2] || img1.Pix[ia+3] != img2.Pix[ib+3] { d++ }
			}
		}
		if d != prev {
			fmt.Printf("k=%2d 后差异 %d (新增 %+d)  part=%s/%s  layer=%s\n", k, d, d-prev, a.Parts[k-1].Part, b.Parts[k-1].Part, a.Parts[k-1].Layer)
		}
		prev = d
		sp1.Directions["default"].Frames[fi].Parts = orig1
		sp2.Directions["default"].Frames[fi].Parts = orig2
	}
}
