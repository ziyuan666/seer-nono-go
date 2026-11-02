// markbodyc：给动作/表情 sprite 标定「本体球心」（sprite 画布坐标）。
//
// 为什么不能只用几何 bbox 中心：tintable 层（球壳 color_*）除了球本身还带
// 耳根、触手、底部支架等结构，bbox 中心会比真实球心偏上 4~6px，动作开播
// 瞬间本体就会跳这么多。这里改成渲染该层 alpha mask，取「最宽那一行」的
// 中点——圆的最宽行必经圆心，比 bbox 中心稳。
//
// 用法：
//
//	go run ./cmd/markbodyc -root ../nono_deskpet/assets/sprites [-dry-run]
package main

import (
	"encoding/json"
	"flag"
	"image"
	"log"
	"os"
	"path/filepath"
	"strings"

	"nonovec/pkg/spec"
	"nonovec/pkg/sprite"
)

// sphereCenter 用渲染后的 alpha mask 求圆心：取最宽行，返回该行中点与其 y。
func sphereCenter(img *image.RGBA) (float64, float64, int) {
	b := img.Bounds()
	bestW, bestY := 0, -1
	bestX0, bestX1 := 0, 0
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
			bestW, bestY, bestX0, bestX1 = w, y, x0, x1
		}
	}
	if bestY < 0 {
		return 0, 0, 0
	}
	return float64(bestX0+bestX1) / 2, float64(bestY), bestW
}

func round2(v float64) float64 {
	if v < 0 {
		return float64(int(v*100-0.5)) / 100
	}
	return float64(int(v*100+0.5)) / 100
}

func main() {
	root := flag.String("root", "../nono_deskpet/assets/sprites", "素材根目录")
	dry := flag.Bool("dry-run", false, "只打印不写盘")
	flag.Parse()

	entries, err := os.ReadDir(*root)
	if err != nil {
		log.Fatal(err)
	}
	updated := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		key := e.Name()
		subs, err := os.ReadDir(filepath.Join(*root, key))
		if err != nil {
			continue
		}
		for _, s := range subs {
			name := s.Name()
			if !s.IsDir() || (!strings.HasPrefix(name, "action_") && !strings.HasPrefix(name, "exp_")) {
				continue
			}
			dir := filepath.Join(*root, key, name)
			sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
			if err != nil {
				log.Printf("SKIP %s/%s: %v", key, name, err)
				continue
			}
			lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
			if err != nil {
				log.Printf("SKIP %s/%s parts: %v", key, name, err)
				continue
			}
			r := sprite.New(sp, lib)
			// 优先用 Z 最小的 tintable 层（球壳本体），拿不到再退回整帧。
			lowest := ""
			lowZ := 1 << 30
			for _, l := range sp.Layers {
				if l.Tintable && l.Z < lowZ {
					lowest, lowZ = l.Name, l.Z
				}
			}
			var cx, cy float64
			var w int
			if lowest != "" {
				if img, err := r.RenderLayerFullCanvas("default", 0, 1, 1, lowest); err == nil {
					cx, cy, w = sphereCenter(img)
				}
			}
			if w == 0 {
				img, err := r.RenderFullCanvas("default", 0, 1, 1)
				if err != nil {
					log.Printf("SKIP %s/%s 渲染: %v", key, name, err)
					continue
				}
				cx, cy, w = sphereCenter(img)
			}
			if w == 0 {
				log.Printf("SKIP %s/%s 空图层", key, name)
				continue
			}
			path := filepath.Join(dir, "sprite.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				log.Printf("SKIP %s/%s 读: %v", key, name, err)
				continue
			}
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(raw, &doc); err != nil {
				log.Printf("SKIP %s/%s json: %v", key, name, err)
				continue
			}
			var cv map[string]json.RawMessage
			if err := json.Unmarshal(doc["canvas"], &cv); err != nil {
				log.Printf("SKIP %s/%s canvas: %v", key, name, err)
				continue
			}
			var oldC [2]float64
			if b, ok := cv["bodyc"]; ok {
				_ = json.Unmarshal(b, &oldC)
			}
			newC := [2]float64{round2(cx), round2(cy)}
			log.Printf("%-8s %-9s bodyc=[%7.2f,%7.2f] 直径=%d 层=%q%s", key, name,
				newC[0], newC[1], w, lowest,
				map[bool]string{true: "  <-更新", false: ""}[oldC != newC])
			if oldC == newC || *dry {
				continue
			}
			nb, _ := json.Marshal(newC)
			cv["bodyc"] = nb
			cb, _ := json.Marshal(cv)
			doc["canvas"] = cb
			out, err := json.Marshal(doc)
			if err != nil {
				log.Printf("FAIL %s/%s: %v", key, name, err)
				continue
			}
			if err := os.WriteFile(path, out, 0o644); err != nil {
				log.Printf("FAIL %s/%s: %v", key, name, err)
				continue
			}
			updated++
		}
	}
	log.Printf("更新 %d 个 sprite.json（dry-run=%v）", updated, *dry)
}
