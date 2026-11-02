package sprite

import (
	"os"
	"path/filepath"
	"testing"

	"nonovec/pkg/spec"
)

// spriteRoot 返回矢量素材根目录。
//
// 2026-10-26 的素材去重把 `nono_deskpet_vec/assets/sprites` 删了（与运行时侧
// `nono_deskpet/assets/sprites` 分叉成两份旧/新口径），只剩运行时那一份。
// 这里按「运行时侧优先、老布局兜底」解析，避免测试再因为目录搬迁而假失败。
func spriteRoot() string {
	for _, p := range []string{
		filepath.Join("..", "..", "..", "nono_deskpet", "assets", "sprites"),
		filepath.Join("..", "..", "assets", "sprites"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join("..", "..", "..", "nono_deskpet", "assets", "sprites")
}

// TestFormsRenderAllDirections 渲染 6 个形态的所有方向首帧。
//
// 桌面端启动时会一次性渲染整组方向帧，这里复现同样的路径，
// 用于在没有 Windows 环境时验证「默认走矢量」不会崩。
func TestFormsRenderAllDirections(t *testing.T) {
	base := spriteRoot()
	for _, form := range []string{"plain", "super1", "super2", "super3", "super4", "super5"} {
		s, err := spec.Load(filepath.Join(base, form, "sprite.json"))
		if err != nil {
			t.Fatalf("%s: %v", form, err)
		}
		lib, err := spec.LoadLibrary(filepath.Join(base, form, "parts"))
		if err != nil {
			t.Fatalf("%s parts: %v", form, err)
		}
		r := New(s, lib)
		n := 0
		for d := range s.Directions {
			img, err := r.Render(d, 0, 1, 2)
			if err != nil {
				t.Errorf("%s/%s 渲染失败: %v", form, d, err)
				continue
			}
			if img == nil {
				t.Errorf("%s/%s 返回 nil", form, d)
			}
			n++
		}
		t.Logf("%s: %d 个方向渲染完成", form, n)
	}
}
