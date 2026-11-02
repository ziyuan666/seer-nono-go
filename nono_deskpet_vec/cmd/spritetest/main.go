// spritetest：把 assets/sprites 下的矢量精灵按指定 dir/帧/缩放渲染成 PNG，
// 用于离线核对「动作帧里的本体球与 idle 球是否同尺寸」这类跨段一致性问题。
//
// 用法：
//
//	go run ./cmd/spritetest -dir ../nono_deskpet/assets/sprites/plain -name down -idx 0 -scale 1 -out /tmp/idle0.png
//	go run ./cmd/spritetest -dir ../nono_deskpet/assets/sprites/plain/action_1 -name default -idx 0 -scale 0.5 -out /tmp/a1.png
package main

import (
	"flag"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

func main() {
	dir := flag.String("dir", "", "精灵目录（含 sprite.json 与 parts/）")
	name := flag.String("name", "down", "方向名（动作/表情为 default）")
	idx := flag.Int("idx", 0, "帧号")
	scale := flag.Float64("scale", 1, "显示倍率")
	ss := flag.Int("ss", 2, "超采样倍数")
	full := flag.Bool("full", true, "整画布（false 则按帧 bbox 裁剪）")
	out := flag.String("out", "", "输出 PNG")
	// only 用于逐部件定位差异：逗号分隔的帧内 part 索引（0 起），
	// 例如 -only 12,13 只渲染该帧第 13、14 个部件。空=全部。
	only := flag.String("only", "", "只渲染指定 part 索引（逗号分隔，0 起）")
	flag.Parse()
	if *dir == "" || *out == "" {
		log.Fatal("需要 -dir 与 -out")
	}
	sp, err := spec.Load(filepath.Join(*dir, "sprite.json"))
	if err != nil {
		log.Fatalf("sprite.json: %v", err)
	}
	if *only != "" {
		var keep []int
		for _, s := range strings.Split(*only, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || n < 0 {
				log.Fatalf("-only 解析失败: %q", s)
			}
			keep = append(keep, n)
		}
		d, ok := sp.Directions[*name]
		if !ok {
			log.Fatalf("方向 %q 不存在", *name)
		}
		if *idx < 0 || *idx >= len(d.Frames) {
			log.Fatalf("帧号 %d 越界（共 %d 帧）", *idx, len(d.Frames))
		}
		fr := &d.Frames[*idx]
		var picked []spec.Part
		for _, n := range keep {
			if n < len(fr.Parts) {
				picked = append(picked, fr.Parts[n])
			}
		}
		if len(picked) == 0 {
			log.Fatalf("-only 未命中任何部件（该帧共 %d 个）", len(fr.Parts))
		}
		fr.Parts = picked
		// 裁剪后 bbox 不再成立，统一走整画布，避免按旧 bbox 开画布把内容切掉。
		fr.Bbox = nil
		*full = true
	}
	lib, err := spec.LoadLibrary(filepath.Join(*dir, "parts"))
	if err != nil {
		log.Fatalf("parts: %v", err)
	}
	r := sprite.New(sp, lib)
	var res = r.Render
	if *full {
		res = r.RenderFullCanvas
	}
	img, err := res(*name, *idx, *scale, *ss)
	if err != nil {
		log.Fatalf("渲染: %v", err)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	// 本项目内部像素是「非预乘 alpha」，但 image.RGBA 的类型语义是预乘：
	// png.Encode 会按预乘解释并对 R>A 的像素做反预乘，半透明边缘直接溢出变黑
	// （shape_46 的径向光晕最亮像素 255,255,255,254 会被写成 1,1,1,254）。
	// 显式转成 NRGBA（非预乘语义）再编码，预览图才和运行时看到的一致。
	nrgba := toNRGBA(img)
	if err := png.Encode(f, nrgba); err != nil {
		log.Fatal(err)
	}
	log.Printf("ok %s %s#%d scale=%.2f -> %s %dx%d", *dir, *name, *idx, *scale, *out, img.Bounds().Dx(), img.Bounds().Dy())
}

// toNRGBA 把内部「非预乘 RGBA 缓冲」逐字节搬到 image.NRGBA。
func toNRGBA(img *image.RGBA) *image.NRGBA {
	b := img.Bounds()
	out := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := img.PixOffset(x, y)
			j := out.PixOffset(x, y)
			copy(out.Pix[j:j+4], img.Pix[i:i+4])
		}
	}
	return out
}
