package main

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"git.gammaspectra.live/WeebDataHoarder/swf-go/tag"
	"git.gammaspectra.live/WeebDataHoarder/swf-go/types"
)

var traceDepth *int

type Placement2 struct {
	CharID uint16
	Mat    Mat
	Alpha  float64
}

type emitItem struct {
	shapeID uint16
	mat     Mat
	alpha   float64
	depth   []int
}

// Assembler 用 FFDec 导出的 shape SVG 做几何，用 SWF display list 做组合变换。
type Assembler struct {
	chars   map[uint16]tag.Tag
	buttons map[uint16]*Button
	shapes  map[uint16]*ShapeSVG
	// bounds 是 shape 在 SWF 中的 ShapeBounds 左上角（px）。
	// FFDec 导出的 shape SVG 已把 bounds 平移到 (0,0)，渲染时要补回 -(minX,minY)。
	bounds map[uint16][2]float64
	items   []emitItem
	clips   int
	missing map[uint16]bool
}

// cxRGB 返回颜色乘/加项（0..1 / -255..255），用于统计与渲染。
func cxRGB(c types.CXFORMWITHALPHA) (mr, mg, mb, ar, ag, ab float64) {
	mr = float64(c.Multiply.Red) / 256
	mg = float64(c.Multiply.Green) / 256
	mb = float64(c.Multiply.Blue) / 256
	ar = float64(c.Add.Red)
	ag = float64(c.Add.Green)
	ab = float64(c.Add.Blue)
	return
}

func cxAlpha2(c types.CXFORMWITHALPHA) float64 {
	a := float64(c.Multiply.Alpha)/256 + float64(c.Add.Alpha)/255
	if a < 0 {
		a = 0
	}
	if a > 1 {
		a = 1
	}
	return a
}

func (r *Assembler) Walk(tags []tag.Tag, parent Mat, prefix []int, level int, inherit float64) {
	if level > 12 {
		return
	}
	dm := map[int]Placement2{}
	set := func(d int, charID *uint16, m *Mat, ct *types.CXFORMWITHALPHA) {
		// depth 256 是按钮 hitTest 层惯用槽位：不参与可见渲染
		if d >= 256 {
			return
		}
		p, ok := dm[d]
		if !ok {
			p.Alpha = 1
		}
		if charID != nil {
			p.CharID = *charID
		}
		if m != nil {
			p.Mat = *m
		}
		if ct != nil {
			p.Alpha *= cxAlpha2(*ct)
		}
		dm[d] = p
	}
	for _, t := range tags {
		switch v := t.(type) {
		case *tag.PlaceObject2:
			if v.Flag.HasClipDepth {
				r.clips++
			}
			var mm *Mat
			if v.Flag.HasMatrix {
				x := FromSWFMatrix(v.Matrix)
				mm = &x
			}
			var cid *uint16
			if v.Flag.HasCharacter {
				x := v.CharacterId
				cid = &x
			}
			var ct *types.CXFORMWITHALPHA
			if v.Flag.HasColorTransform {
				x := v.ColorTransform
				ct = &x
			}
			set(int(v.Depth), cid, mm, ct)
		case *tag.PlaceObject3:
			if v.Flag.HasClipDepth {
				r.clips++
			}
			var mm *Mat
			if v.Flag.HasMatrix {
				x := FromSWFMatrix(v.Matrix)
				mm = &x
			}
			var cid *uint16
			if v.Flag.HasCharacter {
				x := v.CharacterId
				cid = &x
			}
			var ct *types.CXFORMWITHALPHA
			if v.Flag.HasColorTransform {
				x := v.ColorTransform
				ct = &x
			}
			set(int(v.Depth), cid, mm, ct)
		case *tag.RemoveObject2:
			delete(dm, int(v.Depth))
		case *tag.ShowFrame:
			r.flatten(dm, parent, prefix, level, inherit)
			return
		}
	}
	r.flatten(dm, parent, prefix, level, inherit)
}

func (r *Assembler) flatten(dm map[int]Placement2, parent Mat, prefix []int, level int, inherit float64) {
	keys := make([]int, 0, len(dm))
	for k := range dm {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, d := range keys {
		p := dm[d]
		alpha := p.Alpha * inherit
		if alpha <= 0.004 {
			continue
		}
		m := parent.Mul(p.Mat)
		path := append(append([]int{}, prefix...), d)
		if traceDepth != nil {
			fmt.Printf("  %*sdepth=%d char=%d mat=(%.3f,%.3f,%.3f,%.3f,%.2f,%.2f) alpha=%.2f\n",
				level*2, "", d, p.CharID, p.Mat.A, p.Mat.B, p.Mat.C, p.Mat.D, p.Mat.Tx, p.Mat.Ty, alpha)
		}
		if btn, ok := r.buttons[p.CharID]; ok {
			r.placeButton(btn, m, path, level, alpha)
			continue
		}
		ch, ok := r.chars[p.CharID]
		if !ok {
			continue
		}
		switch v := ch.(type) {
		case *tag.DefineSprite:
			if skipChars[p.CharID] {
				continue
			}
			r.Walk(v.ControlTags, m, path, level+1, alpha)
		case *tag.DefineShape, *tag.DefineShape2, *tag.DefineShape3, *tag.DefineShape4:
			if skipChars[p.CharID] {
				continue
			}
			if _, ok := r.shapes[p.CharID]; !ok {
				r.missing[p.CharID] = true
				continue
			}
			mm := m
			if b, ok := r.bounds[p.CharID]; ok {
				mm = m.Mul(Translate(b[0], b[1]))
			}
			r.items = append(r.items, emitItem{shapeID: p.CharID, mat: mm, alpha: alpha, depth: path})
		}
	}
}

// placeButton 取 up 态（bit0），排除 hitTest（bit3）。
func (r *Assembler) placeButton(b *Button, parent Mat, prefix []int, level int, inherit float64) {
	dm := map[int]Placement2{}
	for _, rec := range b.Records {
		if rec.Flags&0x08 != 0 || rec.Flags&0x01 == 0 {
			continue
		}
		if int(rec.Depth) >= 256 {
			continue
		}
		dm[int(rec.Depth)] = Placement2{CharID: rec.CharID, Mat: FromSWFMatrix(rec.Matrix), Alpha: cxAlpha2(rec.CXForm)}
	}
	r.flatten(dm, parent, prefix, level+1, inherit)
}

// WriteSVGFit 用指定 viewBox 合成所有 shape 片段到一个 SVG。
func (r *Assembler) WriteSVGFit(path string, vx, vy, vw, vh float64) error {
	var defs, body strings.Builder
	n := len(r.items)
	if limitItems > 0 && limitItems < n {
		n = limitItems
	}
	for _, it := range r.items[:n] {
		s := r.shapes[it.shapeID]
		prefix := fmt.Sprintf("s%d_", it.shapeID)
		d, b := s.prefixIDs(prefix)
		if d != "" {
			defs.WriteString(d)
			defs.WriteString("\n")
		}
		fmt.Fprintf(&body, "<g transform=\"matrix(%.6f,%.6f,%.6f,%.6f,%.4f,%.4f)\"", it.mat.A, it.mat.B, it.mat.C, it.mat.D, it.mat.Tx, it.mat.Ty)
		if it.alpha < 0.999 {
			fmt.Fprintf(&body, " opacity=\"%.3f\"", it.alpha)
		}
		body.WriteString(">")
		body.WriteString(b)
		body.WriteString("</g>\n")
	}
	svg := fmt.Sprintf("<?xml version=\"1.0\"?>\n<svg xmlns=\"http://www.w3.org/2000/svg\" xmlns:xlink=\"http://www.w3.org/1999/xlink\" xmlns:ffdec=\"https://www.free-decompiler.com/flash\" viewBox=\"%.2f %.2f %.2f %.2f\" width=\"%.0f\" height=\"%.0f\">\n<defs>\n%s</defs>\n%s\n</svg>\n",
		vx, vy, vw, vh, vw, vh, defs.String(), body.String())
	return os.WriteFile(path, []byte(svg), 0644)
}

// WriteSVG 合成所有 shape 片段到一个 SVG。
var limitItems = 0

// skipChars 里的入口 sprite 不展开（用于让运行时动画层独占该元素）。
var skipChars = map[uint16]bool{}

func (r *Assembler) WriteSVG(path string, width, height float64) error {
	var defs, body strings.Builder
	n := len(r.items)
	if limitItems > 0 && limitItems < n {
		n = limitItems
	}
	for _, it := range r.items[:n] {
		s := r.shapes[it.shapeID]
		prefix := fmt.Sprintf("s%d_", it.shapeID)
		d, b := s.prefixIDs(prefix)
		if d != "" {
			defs.WriteString(d)
			defs.WriteString("\n")
		}
		fmt.Fprintf(&body, "<g transform=\"matrix(%.6f,%.6f,%.6f,%.6f,%.4f,%.4f)\"", it.mat.A, it.mat.B, it.mat.C, it.mat.D, it.mat.Tx, it.mat.Ty)
		if it.alpha < 0.999 {
			fmt.Fprintf(&body, " opacity=\"%.3f\"", it.alpha)
		}
		body.WriteString(">")
		body.WriteString(b)
		body.WriteString("</g>\n")
	}
	svg := fmt.Sprintf("<?xml version=\"1.0\"?>\n<svg xmlns=\"http://www.w3.org/2000/svg\" xmlns:xlink=\"http://www.w3.org/1999/xlink\" xmlns:ffdec=\"https://www.free-decompiler.com/flash\" viewBox=\"0 0 %.1f %.1f\" width=\"%.0f\" height=\"%.0f\">\n<defs>\n%s</defs>\n%s\n</svg>\n",
		width, height, width, height, defs.String(), body.String())
	return os.WriteFile(path, []byte(svg), 0644)
}

// Bounds 估算所有已放置 shape 的外框（shape svg 用正方形宽度近似）。
func (r *Assembler) Bounds() (float64, float64, float64, float64) {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, it := range r.items {
		s := r.shapes[it.shapeID]
		w := s.Width
		if w <= 0 {
			continue
		}
		h := s.Height
		if h <= 0 {
			h = w
		}
		for _, pt := range [][2]float64{{0, 0}, {w, 0}, {0, h}, {w, h}} {
			x, y := it.mat.Pt(pt[0], pt[1])
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	return minX, minY, maxX, maxY
}
