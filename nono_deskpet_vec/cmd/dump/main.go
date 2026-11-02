package main

import (
	"fmt"
	"os"
	"path/filepath"

	"nonovec/pkg/spec"
)

func main() {
	dir := os.Args[1]
	sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
	if err != nil { panic(err) }
	lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
	if err != nil { panic(err) }
	fmt.Printf("format=%d name=%q canvas=%.2fx%.2f anchor=%v layers=%d dirs=%d\n",
		sp.Format, sp.Name, sp.Canvas.W, sp.Canvas.H, sp.Canvas.Anchor, len(sp.Layers), len(sp.Directions))
	for i, l := range sp.Layers { fmt.Printf("  layer[%d] %q z=%d tint=%v\n", i, l.Name, l.Z, l.Tintable) }
	d := sp.Directions["default"]
	fmt.Printf("default 帧数=%d\n", len(d.Frames))
	idx := 31
	if idx >= len(d.Frames) { idx = 0 }
	fmt.Printf("f%d parts=%d\n", idx, len(d.Frames[idx].Parts))
	for i, p := range d.Frames[idx].Parts {
		pf := lib.Parts[p.Part]
		np := -1
		if pf != nil { np = len(pf.Paths) }
		fmt.Printf("  [%d] part=%q layer=%q alpha=%v paths=%d m=%v\n", i, p.Part, p.Layer, p.Alpha, np, p.Matrix)
		if i >= 5 { fmt.Println("  ..."); break }
	}
	// 打印 shape_1 的 segs
	if pf := lib.Parts["shape_1"]; pf != nil {
		fmt.Printf("shape_1 paths=%d\n", len(pf.Paths))
		for i, pd := range pf.Paths {
			fmt.Printf("  p%d segs=%d Dlen=%d matrix=%v fill=%q rule=%q\n", i, len(pd.Segs), len(pd.D), pd.Matrix, pd.Fill, pd.FillRule)
		}
	} else {
		fmt.Println("!! lib.Parts[\"shape_1\"] 不存在")
	}
	keys := []string{}
	for k := range lib.Parts { keys = append(keys, k); if len(keys) >= 6 { break } }
	fmt.Println("lib keys 样例:", keys, "总数", len(lib.Parts))
}
