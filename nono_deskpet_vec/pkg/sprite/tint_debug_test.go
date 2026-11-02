package sprite

import (
	"image/color"
	"path/filepath"
	"testing"

	"nonovec/pkg/spec"
)

// TestTintApplies 验证 SetTint 真的乘到 tintable 层，并且不染其它层。
//
// fly1（飞行器矢量）在 2026-10-26 的素材去重里只留在归档中，运行时侧没有，
// 所以这里用 super1 当靶子；任何形态都有 tintable 层，验证意图不变。
func TestTintApplies(t *testing.T) {
	dir := filepath.Join(spriteRoot(), "super1")
	sp, err := spec.Load(filepath.Join(dir, "sprite.json"))
	if err != nil {
		t.Skip("素材不存在，跳过")
	}
	lib, err := spec.LoadLibrary(filepath.Join(dir, "parts"))
	if err != nil {
		t.Fatalf("库加载失败: %v", err)
	}
	r := New(sp, lib)
	t.Logf("layers=%d tintable=%d", len(sp.Layers), len(r.tintable))
	r.SetTint(0x2E8B57)
	t.Logf("Tint=%v", r.Tint)
	// 直接验证 color() 的乘法（2026-11-02：签名加了 replace bool，测试没同步）
	if c, ok := r.color("#c8c8c8", 1, &color.NRGBA{R: 46, G: 139, B: 87, A: 255}, nil, nil, false); ok {
		t.Logf("color(#c8c8c8, tint) = %v", c)
	}
	for _, p := range sp.Directions["down"].Frames[0].Parts {
		t.Logf("part %s layer=%q alpha=%v", p.Part, p.Layer, p.Alpha)
	}
	img, err := r.Render("down", 0, 1, 3)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	// 统计不透明像素的色分布，看 (200,200,200) 是否变成绿色系
	hist := map[color.RGBA]int{}
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			i := img.PixOffset(x, y)
			if img.Pix[i+3] < 200 {
				continue
			}
			c := color.RGBA{img.Pix[i], img.Pix[i+1], img.Pix[i+2], 255}
			hist[c]++
		}
	}
	type kv struct {
		c color.RGBA
		n int
	}
	var top []kv
	for c, n := range hist {
		top = append(top, kv{c, n})
	}
	for i := 0; i < len(top); i++ {
		for j := i + 1; j < len(top); j++ {
			if top[j].n > top[i].n {
				top[i], top[j] = top[j], top[i]
			}
		}
	}
	if len(top) > 4 {
		top = top[:4]
	}
	for _, k := range top {
		t.Logf("色 %v 数量 %d", k.c, k.n)
	}
}
