// diamscan：全量核对「待机本体」与「各段动作/表情本体」的球径口径。
//
// 为什么需要：动作/表情统一用 scale=1.0 渲染，只有在「各段里代表本体的
// 那一层，其直径与待机同一层一致」时才真的做到尺寸统一。若动作里的
// color_1 直径 30、待机 color_2 直径 38，那本体在动作里就小了一圈，
// 光调 scale 是掩盖问题。
//
// 用法：
//
//	go run ./cmd/diamscan -root ../nono_deskpet/assets/sprites
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

// widestRow 返回 alpha 最宽那一行的宽度（圆的最宽行 = 直径）。
func widestRow(img *image.RGBA) int {
	b := img.Bounds()
	bestW := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		x0, x1 := -1, -1
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y).A > 40 {
				if x0 < 0 {
					x0 = x
				}
				x1 = x
			}
		}
		if x0 < 0 {
			continue
		}
		if w := x1 - x0 + 1; w > bestW {
			bestW = w
		}
	}
	return bestW
}

type layerDiam struct {
	name string
	diam int
	z    int
}

func scanDir(dir string) ([]layerDiam, error) {
	sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
	if err != nil {
		return nil, err
	}
	lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
	if err != nil {
		return nil, err
	}
	r := sprite.New(sp, lib)
	dirName := "down"
	if _, ok := sp.Directions[dirName]; !ok {
		dirName = ""
		for k := range sp.Directions {
			dirName = k
			break
		}
	}
	out := []layerDiam{}
	for _, l := range sp.Layers {
		img, err := r.RenderLayerFullCanvas(dirName, 0, 1, 1, l.Name)
		if err != nil {
			continue
		}
		if w := widestRow(img); w > 0 {
			out = append(out, layerDiam{l.Name, w, l.Z})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].z < out[j].z })
	return out, nil
}

// screenWidth 量「黑色屏幕」的宽度：NONO 的面罩在所有形态、所有动作里都是
// 深色块，是本体的身份特征，比 tint 层直径稳（tint 层在某些段含耳、在另一
// 些段不含，层名与语义都不统一）。
//
// 只取暗像素 bbox 的最大宽度，不做连通域：动作特效多为亮色，误判概率低；
// 一旦某段出现黑色特效，数值会明显偏大，正好人工复核。
func screenWidth(img *image.RGBA) (int, int) {
	b := img.Bounds()
	bestW, bestY := 0, -1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		x0, x1 := -1, -1
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if c.A < 40 {
				continue
			}
			if int(c.R)+int(c.G)+int(c.B) < 180 {
				if x0 < 0 {
					x0 = x
				}
				x1 = x
			}
		}
		if x0 < 0 {
			continue
		}
		if w := x1 - x0 + 1; w > bestW {
			bestW, bestY = w, y
		}
	}
	return bestW, bestY
}

// frameScreen 渲染整帧并量黑色屏幕（返回宽度与帧序号 0 的 y）。
func frameScreen(dir string) (int, error) {
	sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
	if err != nil {
		return 0, err
	}
	lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
	if err != nil {
		return 0, err
	}
	r := sprite.New(sp, lib)
	dirName := ""
	for k := range sp.Directions {
		dirName = k
		if k == "down" || k == "default" {
			break
		}
	}
	img, err := r.RenderFullCanvas(dirName, 0, 1, 1)
	if err != nil {
		return 0, err
	}
	w, _ := screenWidth(img)
	return w, nil
}

func main() {
	root := flag.String("root", "../nono_deskpet/assets/sprites", "素材根目录")
	flag.Parse()

	entries, err := os.ReadDir(*root)
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		key := e.Name()
		base := filepath.Join(*root, key)
		idle, err := scanDir(base)
		if err != nil {
			log.Printf("SKIP idle %s: %v", key, err)
			continue
		}
		parts := []string{}
		for _, l := range idle {
			parts = append(parts, fmt.Sprintf("%s=%d", l.name, l.diam))
		}
		iw, _ := frameScreen(base)
		fmt.Printf("== %-7s idle 屏幕=%d  %s\n", key, iw, strings.Join(parts, " "))

		subs, _ := os.ReadDir(base)
		for _, s := range subs {
			name := s.Name()
			if !s.IsDir() || (!strings.HasPrefix(name, "action_") && !strings.HasPrefix(name, "exp_")) {
				continue
			}
			ds, err := scanDir(filepath.Join(base, name))
			if err != nil || len(ds) == 0 {
				continue
			}
			ps := []string{}
			for _, l := range ds {
				ps = append(ps, fmt.Sprintf("%s=%d", l.name, l.diam))
			}
			aw, _ := frameScreen(filepath.Join(base, name))
			mark := ""
			if iw > 0 && aw > 0 {
				d := aw - iw
				if d < -3 || d > 3 {
					mark = fmt.Sprintf("  <<屏幕 %+d", d)
				}
			}
			fmt.Printf("   %-9s 屏幕=%-3d %s%s\n", name, aw, strings.Join(ps, " "), mark)
		}
	}
}
