package main

import (
	"fmt"
	"os"
	"path/filepath"

	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

const samples = 12

func load(dir string) (*spec.Sprite, *spec.Library, error) {
	sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
	if err != nil { return nil, nil, err }
	lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
	if err != nil { return nil, nil, err }
	return sp, lib, nil
}

func main() {
	base1, base2 := os.Args[1], os.Args[2]
	var dirs []string
	filepath.Walk(base1, func(p string, info os.FileInfo, err error) error {
		if err != nil { return nil }
		if !info.IsDir() && info.Name() == "sprite.json" {
			dirs = append(dirs, filepath.Dir(p))
		}
		return nil
	})
	total, pass, warn, fail := 0, 0, 0, 0
	for _, d1 := range dirs {
		rel, _ := filepath.Rel(base1, d1)
		d2 := filepath.Join(base2, rel)
		if _, err := os.Stat(filepath.Join(d2, "sprite.json")); err != nil {
			fmt.Printf("SKIP %s (02 缺失)\n", rel); fail++; continue
		}
		sp1, lib1, e1 := load(d1)
		sp2, lib2, e2 := load(d2)
		if e1 != nil || e2 != nil { fmt.Printf("ERR  %s 加载失败 %v %v\n", rel, e1, e2); fail++; continue }
		r1, r2 := sprite.New(sp1, lib1), sprite.New(sp2, lib2)
		dir := "default"
		fr := sp1.Directions[dir].Frames
		if len(fr) == 0 { continue }
		step := 1
		if len(fr) > samples { step = len(fr) / samples }
		worstBig, sumBig, sumAll, nf := 0, 0, 0, 0
		for i := 0; i < len(fr); i += step {
			a, err1 := r1.RenderFullCanvas(dir, i, 2, 2)
			b, err2 := r2.RenderFullCanvas(dir, i, 2, 2)
			if err1 != nil || err2 != nil { continue }
			big, all := 0, 0
			bd := a.Bounds()
			for y := bd.Min.Y; y < bd.Max.Y; y++ {
				for x := bd.Min.X; x < bd.Max.X; x++ {
					ia, ib := a.PixOffset(x, y), b.PixOffset(x, y)
					md := 0
					for c := 0; c < 4; c++ {
						dd := int(a.Pix[ia+c]) - int(b.Pix[ib+c])
						if dd < 0 { dd = -dd }
						if dd > md { md = dd }
					}
					if md > 0 { all++ }
					if md >= 2 { big++ }
				}
			}
			if big > worstBig { worstBig = big }
			sumBig += big
			sumAll += all
			nf++
		}
		total++
		ratio := 0.0
		if nf > 0 { ratio = float64(sumBig) / float64(nf) / (float64(sp1.Canvas.W*sp1.Canvas.H) * 4) * 100 }
		status := "PASS"
		if worstBig > 0 && ratio > 0.1 { status, warn = "WARN", warn+1 }
		if worstBig == 0 { pass++ }
		if status != "PASS" {
			fmt.Printf("%s %-22s 帧%d 采样%d 差异>=2 合计%d 单帧最差%d 占比%.4f%%\n", status, rel, len(fr), nf, sumBig, worstBig, ratio)
		}
	}
	fmt.Printf("\n共 %d 个动画：完全一致 %d，告警 %d，失败 %d\n", total, pass, warn, fail)
}
