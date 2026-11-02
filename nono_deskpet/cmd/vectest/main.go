// vectest 是矢量管线的离线验证工具：把 shape SVG 光栅化成 PNG。
//
// 用法：
//
//	go run ./cmd/vectest -in main.svg -out out.png -w 535
//
// 与 cairosvg / FFDec 官方渲染做像素比对时使用，不参与桌面宠构建。
package main

import (
	"flag"
	"image"
	"image/png"
	"log"
	"os"
	"time"

	"nonodeskpet/internal/vec"
)

func main() {
	in := flag.String("in", "", "输入的合成 SVG")
	out := flag.String("out", "", "输出 PNG")
	w := flag.Int("w", 0, "目标宽度（0=用 SVG viewBox 宽）")
	h := flag.Int("h", 0, "目标高度（0=按比例）")
	dump := flag.Bool("dump", false, "打印每个 shape 的 bbox / 填充，不渲染")
	flag.Parse()
	if *in == "" {
		log.Fatal("需要 -in")
	}
	if !*dump && *out == "" {
		log.Fatal("需要 -out（或用 -dump）")
	}
	t0 := time.Now()
	doc, err := vec.ParseFile(*in)
	if err != nil {
		log.Fatalf("解析失败: %v", err)
	}
	if *dump {
		for i, s := range doc.Shapes {
			x0, y0, x1, y1 := 1e18, 1e18, -1e18, -1e18
			for _, pl := range s.Polys {
				for _, p := range pl {
					if p.X < x0 {
						x0 = p.X
					}
					if p.X > x1 {
						x1 = p.X
					}
					if p.Y < y0 {
						y0 = p.Y
					}
					if p.Y > y1 {
						y1 = p.Y
					}
				}
			}
			kind := "none"
			if s.Fill != nil {
				kind = "fill"
				if s.Fill.Grad != nil {
					kind = "grad"
				}
			}
			log.Printf("#%02d bbox=(%.1f,%.1f)-(%.1f,%.1f) %dx%d %s evenodd=%v stroke=%.1f polys=%d",
				i, x0, y0, x1, y1, int(x1-x0), int(y1-y0), kind, s.EvenOdd, s.StrokeW, len(s.Polys))
		}
		return
	}
	t0 = time.Now()
	parseMs := time.Since(t0).Milliseconds()

	tw, th := *w, *h
	if tw <= 0 && th <= 0 {
		tw, th = int(doc.VBw+0.5), int(doc.VBh+0.5)
	} else if tw <= 0 {
		tw = int(float64(th) * doc.VBw / doc.VBh)
	} else if th <= 0 {
		th = int(float64(tw) * doc.VBh / doc.VBw)
	}
	t1 := time.Now()
	img := doc.Render(tw, th)
	renderMs := time.Since(t1).Milliseconds()

	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, flipToTopDown(img)); err != nil {
		log.Fatal(err)
	}
	log.Printf("shapes=%d viewBox=%.1fx%.1f -> %dx%d 解析=%dms 渲染=%dms",
		len(doc.Shapes), doc.VBw, doc.VBh, tw, th, parseMs, renderMs)
}

// flipToTopDown 仅用于与 FFDec PNG 对齐比较时保持方向（当前渲染已是自上而下）。
func flipToTopDown(img *image.RGBA) *image.RGBA { return img }
