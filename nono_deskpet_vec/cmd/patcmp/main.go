package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"nonovec/pkg/pathd"
	"nonovec/pkg/spec"
)

func sameSegs(a, b []pathd.Seg) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || len(a[i].Pts) != len(b[i].Pts) {
			return false
		}
		for j := range a[i].Pts {
			if math.Abs(a[i].Pts[j]-b[i].Pts[j]) > 1e-9 {
				return false
			}
		}
	}
	return true
}

func main() {
	la, _ := spec.LoadLibrary(filepath.Join(os.Args[1], "parts"))
	lb, _ := spec.LoadLibrary(filepath.Join(os.Args[2], "parts"))
	n := 0
	for name, pa := range la.Parts {
		if strings.Contains(name, "#") {
			continue
		}
		pb := lb.Parts[name]
		if pb == nil {
			fmt.Println("缺:", name)
			continue
		}
		for i := range pa.Paths {
			x, y := pa.Paths[i], pb.Paths[i]
			var probs []string
			sa, sb := x.Segs, y.Segs
			if sa == nil {
				sa, _ = pathd.Parse(x.D)
			}
			if sb == nil {
				sb, _ = pathd.Parse(y.D)
			}
			if !sameSegs(sa, sb) {
				probs = append(probs, fmt.Sprintf("segs(%d vs %d)", len(sa), len(sb)))
			}
			if x.Matrix != y.Matrix {
				probs = append(probs, fmt.Sprintf("matrix %v vs %v", x.Matrix, y.Matrix))
			}
			nf := func(s string) string {
				if s == "none" {
					return ""
				}
				return s
			}
			if nf(x.Fill) != nf(y.Fill) {
				probs = append(probs, fmt.Sprintf("fill %q vs %q", x.Fill, y.Fill))
			}
			if x.FillRule != y.FillRule {
				probs = append(probs, fmt.Sprintf("rule %q vs %q", x.FillRule, y.FillRule))
			}
			af, bf := 1.0, 1.0
			if x.FillAlpha != nil {
				af = *x.FillAlpha
			}
			if y.FillAlpha != nil {
				bf = *y.FillAlpha
			}
			if math.Abs(af-bf) > 1e-9 {
				probs = append(probs, fmt.Sprintf("fillAlpha %v vs %v", af, bf))
			}
			if (x.Stroke == nil) != (y.Stroke == nil) {
				probs = append(probs, "stroke 有无不同")
			} else if x.Stroke != nil {
				if x.Stroke.Color != y.Stroke.Color || math.Abs(x.Stroke.Width-y.Stroke.Width) > 1e-9 {
					probs = append(probs, fmt.Sprintf("stroke %+v vs %+v", *x.Stroke, *y.Stroke))
				}
			}
			as, bs := 1.0, 1.0
			if x.StrokeAlpha != nil {
				as = *x.StrokeAlpha
			}
			if y.StrokeAlpha != nil {
				bs = *y.StrokeAlpha
			}
			if math.Abs(as-bs) > 1e-9 {
				probs = append(probs, fmt.Sprintf("strokeAlpha %v vs %v", as, bs))
			}
			if len(probs) > 0 {
				n++
				fmt.Printf("%s path%d: %s\n", name, i, strings.Join(probs, "; "))
			}
		}
	}
	fmt.Printf("语义差异条目: %d\n", n)
}
