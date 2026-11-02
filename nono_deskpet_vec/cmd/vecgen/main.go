// vecgen —— 矢量精灵工具：校验、渲染单帧、渲染整张对照表、像素对比。
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "validate":
		cmdValidate(os.Args[2:])
	case "render":
		cmdRender(os.Args[2:])
	case "sheet":
		cmdSheet(os.Args[2:])
	case "diff":
		cmdDiff(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`用法:
  vecgen validate <sprite.json>
  vecgen render <sprite.json> -dir down -frame 0 -scale 4 -ss 4 -out x.png
  vecgen sheet  <sprite.json> -frames 17 -scale 3 -ss 3 -out sheet.png [-only down,left]
  vecgen diff   <a.png> <b.png> [-out diff.png]`)
}

func loadSprite(path string) (*spec.Sprite, *spec.Library) {
	s, err := spec.Load(path)
	if err != nil {
		fatal(err)
	}
	// 部件目录优先取 sprite.json 同级的 parts/（每形态独立）
	dir := filepath.Join(filepath.Dir(path), "parts")
	if _, err := os.Stat(dir); err != nil {
		dir = filepath.Join(filepath.Dir(path), "..", "parts")
	}
	lib, err := spec.LoadLibrary(dir)
	if err != nil {
		fatal(err)
	}
	return s, lib
}

func cmdValidate(args []string) {
	if len(args) < 1 {
		fatal(fmt.Errorf("缺少 sprite.json"))
	}
	s, lib := loadSprite(args[0])
	errs := s.Validate(lib)
	if len(errs) == 0 {
		n := 0
		for _, d := range s.Directions {
			n += len(d.Frames)
		}
		fmt.Printf("OK: %s canvas=%.1fx%.1f layers=%d dirs=%d frames=%d parts=%d\n",
			s.Name, s.Canvas.W, s.Canvas.H, len(s.Layers), len(s.Directions), n, len(lib.Parts))
		return
	}
	for _, e := range errs {
		fmt.Println("  ✗", e)
	}
	os.Exit(1)
}

func cmdRender(args []string) {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	dir := fs.String("dir", "down", "方向")
	frame := fs.Int("frame", 0, "帧号")
	scale := fs.Float64("scale", 4, "显示倍率")
	ss := fs.Int("ss", 4, "超采样倍数")
	out := fs.String("out", "out.png", "输出 PNG")
	layer := fs.String("layer", "", "只渲染该层")
	tint := fs.String("tint", "", "玩家色 #RRGGBB（只染 tintable 层），空=不染")
	tintFloor := fs.Int("tintfloor", 0, "染色亮度保底 0-255（0=纯乘）")
	grad := fs.String("grad", "", "渐变染色 #RRGGBB:#RRGGBB")
	gradMode := fs.Int("gradmode", 0, "渐变方向 0=竖直 1=水平 2=对角")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fatal(fmt.Errorf("缺少 sprite.json"))
	}
	s, lib := loadSprite(fs.Arg(0))
	r := sprite.New(s, lib)
	if *tint != "" {
		v, err := strconv.ParseUint(strings.TrimPrefix(*tint, "#"), 16, 32)
		if err != nil {
			fatal(fmt.Errorf("tint 解析失败: %w", err))
		}
		r.SetTint(uint32(v))
		r.SetTintFloor(uint8(*tintFloor))
	}
	if *grad != "" {
		parts := strings.SplitN(*grad, ":", 2)
		if len(parts) != 2 {
			fatal(fmt.Errorf("grad 格式应为 #RRGGBB:#RRGGBB"))
		}
		pa, err1 := strconv.ParseUint(strings.TrimPrefix(parts[0], "#"), 16, 32)
		pb, err2 := strconv.ParseUint(strings.TrimPrefix(parts[1], "#"), 16, 32)
		if err1 != nil || err2 != nil {
			fatal(fmt.Errorf("grad 解析失败"))
		}
		r.SetGradient(uint32(pa), uint32(pb), *gradMode)
	}
	img, err := r.RenderLayer(*dir, *frame, *scale, *ss, *layer)
	if err != nil {
		fatal(err)
	}
	savePNG(*out, img)
	fmt.Printf("写入 %s (%d×%d)\n", *out, img.Bounds().Dx(), img.Bounds().Dy())
}

func cmdSheet(args []string) {
	fs := flag.NewFlagSet("sheet", flag.ExitOnError)
	frames := fs.Int("frames", 17, "每方向帧数")
	scale := fs.Float64("scale", 3, "显示倍率")
	ss := fs.Int("ss", 3, "超采样")
	out := fs.String("out", "sheet.png", "输出")
	only := fs.String("only", "", "只渲染这些方向（逗号分隔）")
	bg := fs.String("bg", "#ffffff", "背景色，空=透明")
	layer := fs.String("layer", "", "只渲染该层")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fatal(fmt.Errorf("缺少 sprite.json"))
	}
	s, lib := loadSprite(fs.Arg(0))
	r := sprite.New(s, lib)

	dirs := spec.DirectionNames()
	if *only != "" {
		dirs = strings.Split(*only, ",")
	}
	cw := int(s.Canvas.W*(*scale) + 0.5)
	ch := int(s.Canvas.H*(*scale) + 0.5)
	sheet := image.NewRGBA(image.Rect(0, 0, cw*(*frames), ch*len(dirs)))
	if *bg != "" {
		c := parseColor(*bg)
		draw.Draw(sheet, sheet.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	}
	for dy, d := range dirs {
		for f := 0; f < *frames; f++ {
			img, err := r.RenderLayer(d, f, *scale, *ss, *layer)
			if err != nil {
				fmt.Printf("  %s[%d]: %v\n", d, f, err)
				continue
			}
			draw.Draw(sheet, image.Rect(f*cw, dy*ch, (f+1)*cw, (dy+1)*ch),
				img, image.Point{}, draw.Over)
		}
	}
	savePNG(*out, sheet)
	fmt.Printf("写入 %s (%d×%d)\n", *out, sheet.Bounds().Dx(), sheet.Bounds().Dy())
}

func cmdDiff(args []string) {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	out := fs.String("out", "", "差异图输出（可选）")
	tol := fs.Int("tol", 8, "单通道容差")
	fs.Parse(args)
	if fs.NArg() < 2 {
		fatal(fmt.Errorf("需要两张 PNG"))
	}
	a := loadPNG(fs.Arg(0))
	b := loadPNG(fs.Arg(1))
	if a.Bounds() != b.Bounds() {
		fmt.Printf("尺寸不同: %v vs %v\n", a.Bounds(), b.Bounds())
	}
	w := min(a.Bounds().Dx(), b.Bounds().Dx())
	h := min(a.Bounds().Dy(), b.Bounds().Dy())
	bad, total := 0, 0
	var dif *image.RGBA
	if *out != "" {
		dif = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	var sum float64
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ca := a.RGBAAt(x, y)
			cb := b.RGBAAt(x, y)
			// 透明区域不比较
			if ca.A == 0 && cb.A == 0 {
				continue
			}
			total++
			d := max3(abs(int(ca.R)-int(cb.R)), abs(int(ca.G)-int(cb.G)), abs(int(ca.B)-int(cb.B)))
			d = maxInt(d, abs(int(ca.A)-int(cb.A)))
			sum += float64(d)
			if d > *tol {
				bad++
			}
			if dif != nil {
				if d > *tol {
					dif.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
				} else {
					dif.SetRGBA(x, y, color.RGBA{200, 200, 200, 255})
				}
			}
		}
	}
	pct := 0.0
	if total > 0 {
		pct = float64(bad) * 100 / float64(total)
	}
	mean := 0.0
	if total > 0 {
		mean = sum / float64(total)
	}
	fmt.Printf("对比像素 %d, 超差 %d (%.3f%%), 平均差 %.2f\n", total, bad, pct, mean)
	if dif != nil {
		savePNG(*out, dif)
		fmt.Printf("写入差异图 %s\n", *out)
	}
	if pct > 1.0 {
		os.Exit(1)
	}
}

func parseColor(s string) color.NRGBA {
	if strings.HasPrefix(s, "#") && len(s) >= 7 {
		var r, g, b uint8
		fmt.Sscanf(s[1:3], "%02x", &r)
		fmt.Sscanf(s[3:5], "%02x", &g)
		fmt.Sscanf(s[5:7], "%02x", &b)
		return color.NRGBA{r, g, b, 255}
	}
	return color.NRGBA{255, 255, 255, 255}
}

func loadPNG(p string) *image.RGBA {
	f, err := os.Open(p)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		fatal(err)
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out
}

// toNRGBA 把「容器是 *image.RGBA、但 Pix 里存的是非预乘值」的图转成 image.NRGBA。
//
// 为什么不能走 color.NRGBAModel：那会对已经非预乘的数据再除一次 alpha，
// alpha<255 时 (255*255/204) 这类值超出 16 位会回绕，导出 PNG 出现红色噪点
// （fly 的 fireMc alpha=0.8008 一测就出）。这里只搬字节，不做颜色换算。
func toNRGBA(src *image.RGBA) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		si := src.PixOffset(b.Min.X, y)
		di := dst.PixOffset(b.Min.X, y)
		copy(dst.Pix[di:di+b.Dx()*4], src.Pix[si:si+b.Dx()*4])
	}
	return dst
}

func savePNG(p string, img image.Image) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	if rg, ok := img.(*image.RGBA); ok {
		img = toNRGBA(rg)
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(f, img); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}

func min(a, b int) int { if a < b { return a }; return b }
func maxInt(a, b int) int { if a > b { return a }; return b }
func abs(a int) int { if a < 0 { return -a }; return a }
func max3(a, b, c int) int { return maxInt(a, maxInt(b, c)) }

var _ = sort.Ints
