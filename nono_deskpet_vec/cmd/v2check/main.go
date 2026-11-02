package main

import (
	"fmt"
	"os"
	"path/filepath"

	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

func load(dir string) (*spec.Sprite, *spec.Library, error) {
	sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
	if err != nil {
		return nil, nil, err
	}
	lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
	if err != nil {
		return nil, nil, err
	}
	return sp, lib, nil
}

func main() {
	dir1, dir2 := os.Args[1], os.Args[2]

	sp1, lib1, err := load(dir1)
	if err != nil {
		panic(err)
	}
	sp2, lib2, err := load(dir2)
	if err != nil {
		panic(err)
	}

	fmt.Printf("01: format=%d canvas=%.3fx%.3f anchor=%v ballc=%v bodyc=%v parts=%d\n",
		sp1.Format, sp1.Canvas.W, sp1.Canvas.H, sp1.Canvas.Anchor, sp1.Canvas.BallC, sp1.Canvas.BodyC, len(lib1.Parts))
	fmt.Printf("02: format=%d canvas=%.3fx%.3f anchor=%v ballc=%v bodyc=%v parts=%d\n",
		sp2.Format, sp2.Canvas.W, sp2.Canvas.H, sp2.Canvas.Anchor, sp2.Canvas.BallC, sp2.Canvas.BodyC, len(lib2.Parts))

	r1 := sprite.New(sp1, lib1)
	r2 := sprite.New(sp2, lib2)

	f1 := sp1.Directions["default"].Frames
	f2 := sp2.Directions["default"].Frames
	if len(f1) != len(f2) {
		fmt.Printf("帧数不同: 01=%d 02=%d\n", len(f1), len(f2))
		return
	}

	total, worst, worstIdx := 0, 0, -1
	for i := range f1 {
		a, err := r1.RenderFullCanvas("default", i, 2, 2)
		if err != nil {
			fmt.Printf("f%d 01 渲染失败: %v\n", i, err)
			continue
		}
		b, err := r2.RenderFullCanvas("default", i, 2, 2)
		if err != nil {
			fmt.Printf("f%d 02 渲染失败: %v\n", i, err)
			continue
		}
		if a.Bounds() != b.Bounds() {
			fmt.Printf("f%d 尺寸不同 %v vs %v\n", i, a.Bounds(), b.Bounds())
			continue
		}
		n := 0
		bd := a.Bounds()
		for y := bd.Min.Y; y < bd.Max.Y; y++ {
			for x := bd.Min.X; x < bd.Max.X; x++ {
				ia, ib := a.PixOffset(x, y), b.PixOffset(x, y)
				if a.Pix[ia] != b.Pix[ib] || a.Pix[ia+1] != b.Pix[ib+1] ||
					a.Pix[ia+2] != b.Pix[ib+2] || a.Pix[ia+3] != b.Pix[ib+3] {
					n++
				}
			}
		}
		total += n
		if n > worst {
			worst, worstIdx = n, i
		}
	}
	fmt.Printf("共 %d 帧，差异像素合计 %d，最差帧 f%d (%d 像素)\n", len(f1), total, worstIdx, worst)
	if total == 0 {
		fmt.Println("RESULT: 01 与 02 渲染完全一致 ✓")
	} else {
		fmt.Println("RESULT: 存在差异 ✗")
	}
}
