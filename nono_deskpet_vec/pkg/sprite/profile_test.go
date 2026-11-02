package sprite

import (
	"os"
	"testing"

	"nonovec/pkg/spec"
)

// BenchmarkRenderClip 用真实素材测「按给定矩形渲染一帧」的成本。
//
// 为什么要这个 bench：24fps 的预算是 41ms/帧，而 super5/action_7 按帧 bbox
// 渲染实测 96ms。改之前必须先知道钱花在哪（解析 SVG 字符串占多少、折线细分
// 占多少、光栅填充占多少），否则容易改错地方。
//
// 环境变量 NONO_BENCH_DIR 指定素材目录（含 sprite.json 与 parts/）。
func BenchmarkRenderClip(b *testing.B) {
	dir := os.Getenv("NONO_BENCH_DIR")
	if dir == "" {
		b.Skip("未设置 NONO_BENCH_DIR")
	}
	sp, err := spec.Load(dir + "/sprite.json")
	if err != nil {
		b.Fatalf("加载 sprite 失败: %v", err)
	}
	lib, err := spec.LoadLibrary(dir + "/parts")
	if err != nil {
		b.Fatalf("加载 parts 失败: %v", err)
	}
	r := New(sp, lib)
	idx := 60
	if n := len(sp.Directions["default"].Frames); idx >= n {
		idx = n / 2
	}
	bb := sp.Directions["default"].Frames[idx].Bbox
	clip := []float64{bb[0] - 1, bb[1] - 1, bb[2] + 2, bb[3] + 2}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.render("default", idx, 4, 1, "", false, clip); err != nil {
			b.Fatal(err)
		}
	}
}
