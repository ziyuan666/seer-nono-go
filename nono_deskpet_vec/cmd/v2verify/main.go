// v2verify：全量比对 01（可读）与 02（量化）两套资产的渲染结果。
//
// 判据：同一个动画、同一帧、同一倍率，逐像素完全相等（差 0 才算过）。
// 任何差异都说明 02 有信息损失，必须修到 0 才能上运行时——
// 历史上栽过的坑：alpha 用 /255 归一（01 是 cxform 的 /256）造成 82 灰阶差、
// 帧矩阵用 16.16（1.5e-5）比 01 的 round(v,6) 粗 15 倍造成 evenodd 判定翻转。
package main

import (
	"flag"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

type cmpResult struct {
	dir    string
	frames int
	badPix int
	maxDif int
}

func load(dir string) (*sprite.Renderer, bool) {
	sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
	if err != nil {
		return nil, false
	}
	lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
	if err != nil {
		return nil, false
	}
	return sprite.New(sp, lib), true
}

func main() {
	dir01 := flag.String("a", "", "01 根目录")
	dir02 := flag.String("b", "", "02 根目录")
	scale := flag.Float64("scale", 1, "显示倍率")
	ss := flag.Int("ss", 2, "超采样倍数")
	limit := flag.Int("limit", 0, "每个动画最多验证多少帧（0=全部）")
	flag.Parse()

	if *dir01 == "" || *dir02 == "" {
		log.Fatal("需要 -a 与 -b")
	}

	entries, err := os.ReadDir(*dir01)
	if err != nil {
		log.Fatal(err)
	}
	var results []cmpResult
	totalFrames, totalBad, worst := 0, 0, 0

	for _, form := range entries {
		if !form.IsDir() {
			continue
		}
		anims, err := os.ReadDir(filepath.Join(*dir01, form.Name()))
		if err != nil {
			continue
		}
		for _, an := range anims {
			if !an.IsDir() || an.Name() == "parts" {
				continue
			}
			rel := filepath.Join(form.Name(), an.Name())
			r1, ok1 := load(filepath.Join(*dir01, rel))
			r2, ok2 := load(filepath.Join(*dir02, rel))
			if !ok1 || !ok2 {
				results = append(results, cmpResult{dir: rel, frames: -1})
				continue
			}
			res := cmpResult{dir: rel}
			// 逐个方向、逐帧比对。方向和帧数取 01 的（02 必须一致）。
			for dname, d := range r1.S.Directions {
				n := len(d.Frames)
				step := 1
				if *limit > 0 && n > *limit {
					step = n / *limit
					if step < 1 {
						step = 1
					}
				}
				for i := 0; i < n; i += step {
					img1, e1 := r1.RenderFullCanvas(dname, i, *scale, *ss)
					img2, e2 := r2.RenderFullCanvas(dname, i, *scale, *ss)
					if e1 != nil || e2 != nil || img1 == nil || img2 == nil {
						res.frames++
						res.badPix++
						continue
					}
					res.frames++
					bad, mx := diff(img1, img2)
					if bad > 0 {
						res.badPix += bad
						if mx > res.maxDif {
							res.maxDif = mx
						}
						if mx > 8 {
							fmt.Printf("      ! %s %s[%d] 差异 %d 最大差 %d\n", rel, dname, i, bad, mx)
						}
					}
				}
			}
			totalFrames += res.frames
			totalBad += res.badPix
			if res.maxDif > worst {
				worst = res.maxDif
			}
			results = append(results, res)
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].dir < results[j].dir })
	failN := 0
	for _, r := range results {
		switch {
		case r.frames < 0:
			fmt.Printf("  ✗ %-28s 加载失败\n", r.dir)
			failN++
		case r.badPix > 0:
			fmt.Printf("  ✗ %-28s %d 帧，差异像素 %d，最大差 %d\n", r.dir, r.frames, r.badPix, r.maxDif)
			failN++
		}
	}
	fmt.Printf("\n合计：%d 个动画，%d 帧，差异像素 %d，最大差 %d\n", len(results), totalFrames, totalBad, worst)
	if failN == 0 {
		fmt.Println("结论：01 / 02 渲染完全一致 ✅")
	} else {
		fmt.Printf("结论：%d 个动画存在差异 ❌\n", failN)
		os.Exit(1)
	}
}

func diff(a, b image.Image) (bad, mx int) {
	ba, bb := a.Bounds(), b.Bounds()
	if ba.Dx() != bb.Dx() || ba.Dy() != bb.Dy() {
		return ba.Dx() * ba.Dy(), 255
	}
	for y := ba.Min.Y; y < ba.Max.Y; y++ {
		for x := ba.Min.X; x < ba.Max.X; x++ {
			r1, g1, b1, a1 := a.At(x, y).RGBA()
			r2, g2, b2, a2 := b.At(x, y).RGBA()
			var d int
			for _, p := range [4][2]uint32{{r1, r2}, {g1, g2}, {b1, b2}, {a1, a2}} {
				v := int(p[0]>>8) - int(p[1]>>8)
				if v < 0 {
					v = -v
				}
				if v > d {
					d = v
				}
			}
			if d > 0 {
				bad++
				if d > mx {
					mx = d
				}
			}
		}
	}
	return
}

var _ = strings.TrimSpace
