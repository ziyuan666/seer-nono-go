//go:build windows

package main

import "testing"

// TestFlyStrictOriginalSize 验证严格原版口径：四形态输出整帧尺寸
// = SWF 原始整帧尺寸(1x) × sc，比例彼此保留（不做 gem 归一）。
func TestFlyStrictOriginalSize(t *testing.T) {
	loadFlyMetricsV2()
	if len(flyMetricsV2) == 0 {
		t.Fatal("fly metrics 未加载（assets/fly/metrics.json 路径不对）")
	}
	want := map[int][2]int{1: {120, 146}, 2: {177, 165}, 3: {105, 149}, 4: {167, 170}}
	for style := 1; style <= 4; style++ {
		fh := flyOriginalFullH(style)
		fr := flyCompositeFrames(style, 1, fh, 0xFFFFFF)
		if len(fr) != 1 {
			t.Fatalf("style %d 默认应只合成 1 帧（原版 gotoAndStop），实际 %d", style, len(fr))
		}
		got := fr[0].Bounds()
		if got.Dx() != want[style][0] || got.Dy() != want[style][1] {
			t.Errorf("style %d 尺寸=%dx%d，原版=%dx%d", style, got.Dx(), got.Dy(), want[style][0], want[style][1])
		}
	}
}
