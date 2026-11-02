package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	swf "git.gammaspectra.live/WeebDataHoarder/swf-go"
	"git.gammaspectra.live/WeebDataHoarder/swf-go/tag"
)

func main() {
	in := os.Args[1]
	outSVG := "svg/main.svg"
	entryID := 102
	svgDir := "/tmp/ffout"
	width, height := 500.0, 375.0
	if len(os.Args) > 2 && os.Args[2] != "" {
		outSVG = os.Args[2]
	}
	if len(os.Args) > 3 {
		fmt.Sscan(os.Args[3], &entryID)
	}
	if len(os.Args) > 4 && os.Args[4] != "" {
		svgDir = os.Args[4]
	}

	f, err := os.Open(in)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	r, err := swf.NewReader(f)
	if err != nil {
		panic(err)
	}
	defer r.Close()

	swfVer := r.Header().Version
	var root []tag.Tag
	chars := map[uint16]tag.Tag{}
	shapeBounds := map[uint16][2]float64{}
	buttons := map[uint16]*Button{}
	symbols := map[string]uint16{}
	skipped := 0
	for {
		rec, err := r.Record()
		if err != nil {
			break
		}
		if rec == nil {
			break
		}
		code := int(rec.TagCodeAndLength >> 6)
		if code == 76 {
			for k, v := range parseSymbolClass(rec.Data) {
				symbols[k] = v
			}
		}
		if code == 34 {
			if btn, berr := parseButton2(rec.Data, swfVer); berr == nil && btn != nil && len(btn.Records) > 0 {
				buttons[btn.ID] = btn
			}
		}
		t, derr := rec.Decode()
		if derr != nil {
			skipped++
			continue
		}
		if t == nil {
			continue
		}
		switch v := t.(type) {
		case *tag.DefineShape:
			chars[v.ShapeId] = v
			shapeBounds[v.ShapeId] = [2]float64{float64(v.ShapeBounds.Xmin) / 20, float64(v.ShapeBounds.Ymin) / 20}
		case *tag.DefineShape2:
			chars[v.ShapeId] = v
			shapeBounds[v.ShapeId] = [2]float64{float64(v.ShapeBounds.Xmin) / 20, float64(v.ShapeBounds.Ymin) / 20}
		case *tag.DefineShape3:
			chars[v.ShapeId] = v
			shapeBounds[v.ShapeId] = [2]float64{float64(v.ShapeBounds.Xmin) / 20, float64(v.ShapeBounds.Ymin) / 20}
		case *tag.DefineShape4:
			chars[v.ShapeId] = v
			shapeBounds[v.ShapeId] = [2]float64{float64(v.ShapeBounds.Xmin) / 20, float64(v.ShapeBounds.Ymin) / 20}
		case *tag.DefineSprite:
			chars[v.SpriteId] = v
		}
		switch t.(type) {
		case *tag.PlaceObject2, *tag.PlaceObject3, *tag.RemoveObject2, *tag.RemoveObject, *tag.ShowFrame:
			root = append(root, t)
		}
	}
	hdr := r.Header()
	if hdr.FrameSize.Xmax > 0 {
		width = float64(hdr.FrameSize.Xmax-hdr.FrameSize.Xmin) / 20
		height = float64(hdr.FrameSize.Ymax-hdr.FrameSize.Ymin) / 20
	}
	fmt.Printf("chars=%d buttons=%d symbols=%d skipped=%d stage=%.0fx%.0f\n", len(chars), len(buttons), len(symbols), skipped, width, height)

	// 载入 FFDec 导出的 shape SVG
	shapes := map[uint16]*ShapeSVG{}
	entries, _ := os.ReadDir(svgDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".svg") {
			continue
		}
		var id uint16
		if _, err := fmt.Sscanf(strings.TrimSuffix(e.Name(), ".svg"), "%d", &id); err != nil {
			continue
		}
		if s, err := LoadShapeSVG(filepath.Join(svgDir, e.Name())); err == nil {
			shapes[id] = s
		}
	}
	fmt.Printf("ffdec shape svg loaded=%d\n", len(shapes))

	if os.Getenv("LISTSYM") != "" {
		names := make([]string, 0, len(symbols))
		for k := range symbols { names = append(names, k) }
		sort.Strings(names)
		for _, k := range names { fmt.Printf("SYM %s -> %d\n", k, symbols[k]) }
	}
	if os.Getenv("DUMP102") != "" {
		if sp, ok := chars[102].(*tag.DefineSprite); ok {
			for i, t := range sp.ControlTags {
				switch v := t.(type) {
				case *tag.PlaceObject2:
					fmt.Printf("[%d] PO2 depth=%d char=%d move=%v hasChar=%v hasMat=%v tx=%v ty=%v\n", i, v.Depth, v.CharacterId, v.Flag.Move, v.Flag.HasCharacter, v.Flag.HasMatrix, v.Matrix.TranslateX, v.Matrix.TranslateY)
				case *tag.ShowFrame:
					fmt.Printf("[%d] ShowFrame\n", i)
				}
			}
		}
	}
	if os.Getenv("DUMP101") != "" {
		if sp, ok := chars[101].(*tag.DefineSprite); ok {
			fmt.Printf("sprite101 frames=%d tags=%d\n", sp.FrameCount, len(sp.ControlTags))
			for i, t := range sp.ControlTags {
				if i > 20 { break }
				fmt.Printf("  [%d] %T %+v\n", i, t, t)
			}
		}
	}
	if os.Getenv("TRACE") != "" {
		traceDepth = new(int)
	}
	asm := &Assembler{chars: chars, buttons: buttons, shapes: shapes, bounds: shapeBounds, missing: map[uint16]bool{}}
	if sp, ok := chars[uint16(entryID)].(*tag.DefineSprite); ok {
	if v := os.Getenv("SKIP_CHARS"); v != "" {
		for _, f := range strings.Split(v, ",") {
			var id uint16
			if _, err := fmt.Sscanf(strings.TrimSpace(f), "%d", &id); err == nil {
				skipChars[id] = true
			}
		}
	}

		asm.Walk(sp.ControlTags, Ident(), []int{entryID}, 0, 1)
	} else {
		asm.Walk(root, Ident(), nil, 0, 1)
	}
	fmt.Printf("items=%d clips=%d missingShapeSVG=%d\n", len(asm.items), asm.clips, len(asm.missing))
	if len(asm.missing) > 0 {
		ids := make([]int, 0, len(asm.missing))
		for id := range asm.missing {
			ids = append(ids, int(id))
		}
		sort.Ints(ids)
		fmt.Printf("  missing ids: %v\n", ids)
	}
	if v := os.Getenv("LIMIT"); v != "" {
		fmt.Sscan(v, &limitItems)
	}
	x0, y0, x1, y1 := asm.Bounds()
	fmt.Printf("content bounds %.1f,%.1f .. %.1f,%.1f (%.1f x %.1f)\n", x0, y0, x1, y1, x1-x0, y1-y0)
	vw, vh := width, height
	vx, vy := 0.0, 0.0
	if v := os.Getenv("ALIGN"); v != "" {
		var ox, oy, aw, ah float64
		fmt.Sscanf(v, "%f,%f,%f,%f", &ox, &oy, &aw, &ah)
		vx, vy, vw, vh = -ox, -oy, aw, ah
	} else if os.Getenv("FIT") != "" && x1 > x0 && y1 > y0 {
		vx, vy, vw, vh = x0, y0, x1-x0, y1-y0
	}
	fmt.Printf("viewBox %.2f %.2f %.2f %.2f\n", vx, vy, vw, vh)
	if err := asm.WriteSVGFit(outSVG, vx, vy, vw, vh); err != nil {
		panic(err)
	}
	for i, it := range asm.items {
		if i > 40 {
			break
		}
		w := 0.0
		if sh := asm.shapes[it.shapeID]; sh != nil {
			w = sh.Width
		}
		fmt.Printf("item[%d] shape=%d w=%.1f depth=%v m=(%.3f,%.3f,%.3f,%.3f,%.2f,%.2f) alpha=%.2f\n",
			i, it.shapeID, w, it.depth, it.mat.A, it.mat.B, it.mat.C, it.mat.D, it.mat.Tx, it.mat.Ty, it.alpha)
	}
	fmt.Println("svg written:", outSVG)
}
