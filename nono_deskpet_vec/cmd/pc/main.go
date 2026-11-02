package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"nonovec/pkg/pathd"
	"nonovec/pkg/spec"
)

func load(dir string) (*spec.Library, error) {
	return spec.LoadLibrary(filepath.Join(dir, "parts"))
}

func main() {
	l1, _ := load(os.Args[1])
	l2, _ := load(os.Args[2])
	bad := 0
	for name, pf1 := range l1.Parts {
		if len(name) > 40 { continue }
		pf2 := l2.Parts[name]
		if pf2 == nil {
			fmt.Printf("02 缺 shape %s\n", name)
			bad++
			continue
		}
		if len(pf1.Paths) != len(pf2.Paths) {
			fmt.Printf("%s path 数不同 %d vs %d\n", name, len(pf1.Paths), len(pf2.Paths))
			bad++
			continue
		}
		for i := range pf1.Paths {
			a, b := pf1.Paths[i], pf2.Paths[i]
			// 几何：段数 + 点坐标
			sa, _ := pathd.Parse(a.D)
			var sb []pathd.Seg
			if len(b.Segs) > 0 {
				sb = b.Segs
			} else {
				sb, _ = pathd.Parse(b.D)
			}
			if len(sa) != len(sb) {
				fmt.Printf("%s[%d] 段数不同 %d vs %d (01 d=%q)\n", name, i, len(sa), len(sb), a.D[:60])
				bad++
				continue
			}
			maxd := 0.0
			for k := range sa {
				if sa[k].Kind != sb[k].Kind {
					fmt.Printf("%s[%d] 段%d Kind 不同 %v vs %v\n", name, i, k, sa[k].Kind, sb[k].Kind)
					bad++
					break
				}
				for j := range sa[k].Pts {
					if j < len(sb[k].Pts) {
						if d := math.Abs(sa[k].Pts[j] - sb[k].Pts[j]); d > maxd {
							maxd = d
						}
					}
				}
			}
			if maxd > 1e-6 {
				fmt.Printf("%s[%d] 坐标最大差 %.6f\n", name, i, maxd)
				bad++
			}
			// 渐变
			if (a.Gradient == nil) != (b.Gradient == nil) {
				fmt.Printf("%s[%d] 渐变有无不同 %v vs %v\n", name, i, a.Gradient != nil, b.Gradient != nil)
				bad++
			} else if a.Gradient != nil {
				ga, gb := a.Gradient, b.Gradient
				if ga.Type != gb.Type || len(ga.Stops) != len(gb.Stops) {
					fmt.Printf("%s[%d] 渐变结构不同 %s/%d vs %s/%d\n", name, i, ga.Type, len(ga.Stops), gb.Type, len(gb.Stops))
					bad++
				} else {
					for s := range ga.Stops {
						if math.Abs(ga.Stops[s].Offset-gb.Stops[s].Offset) > 1e-4 ||
							ga.Stops[s].Color != gb.Stops[s].Color ||
							math.Abs(ga.Stops[s].Opacity-gb.Stops[s].Opacity) > 1e-4 {
							fmt.Printf("%s[%d] stop%d 不同 %v vs %v\n", name, i, s, ga.Stops[s], gb.Stops[s])
							bad++
						}
					}
					if ga.R != gb.R || math.Abs(ga.CX-gb.CX) > 1e-6 || math.Abs(ga.CY-gb.CY) > 1e-6 {
						fmt.Printf("%s[%d] 渐变几何不同 R %.4f vs %.4f CX %.4f vs %.4f\n", name, i, ga.R, gb.R, ga.CX, gb.CX)
						bad++
					}
				}
			}
			// base matrix
			for k := 0; k < 6; k++ {
				if math.Abs(a.Matrix[k]-b.Matrix[k]) > 1e-6 {
					fmt.Printf("%s[%d] Matrix[%d] 不同 %.8f vs %.8f\n", name, i, k, a.Matrix[k], b.Matrix[k])
					bad++
					break
				}
			}
			// 纯色 / 描边 / 规则
			if a.Fill != b.Fill {
				fmt.Printf("%s[%d] Fill 不同 %q vs %q\n", name, i, a.Fill, b.Fill)
				bad++
			}
			if a.FillRule != b.FillRule {
				fmt.Printf("%s[%d] FillRule 不同 %q vs %q\n", name, i, a.FillRule, b.FillRule)
				bad++
			}
			if (a.Stroke == nil) != (b.Stroke == nil) {
				fmt.Printf("%s[%d] Stroke 有无不同\n", name, i)
				bad++
			} else if a.Stroke != nil {
				if a.Stroke.Color != b.Stroke.Color || math.Abs(a.Stroke.Width-b.Stroke.Width) > 1e-4 {
					fmt.Printf("%s[%d] Stroke 不同 %q/%.4f vs %q/%.4f\n", name, i, a.Stroke.Color, a.Stroke.Width, b.Stroke.Color, b.Stroke.Width)
					bad++
				}
			}
			// fillAlpha
			if (a.FillAlpha == nil) != (b.FillAlpha == nil) {
				fmt.Printf("%s[%d] fillAlpha 有无不同 %v vs %v\n", name, i, a.FillAlpha, b.FillAlpha)
				bad++
			}
		}
	}
	fmt.Printf("问题项 %d\n", bad)
}
