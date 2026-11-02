package sprite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nonovec/pkg/spec"
)

// TestAllSpritesRender 遍历素材目录下的每个 sprite.json，加载并渲染首帧。
//
// 为什么要它：素材是批量脚本生成的，单帧 POC 通过不代表全部通过；
// 这里做一次全量冒烟，任何 spec 解析失败、路径失效、渲染 panic 都会暴露。
func TestAllSpritesRender(t *testing.T) {
	root := filepath.Join("..", "..", "assets", "sprites")
	var files []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(p, "sprite.json") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Skipf("素材目录不可用: %v", err)
	}
	if len(files) == 0 {
		t.Skip("没有找到 sprite.json")
	}
	ok, empty := 0, 0
	for _, f := range files {
		s, err := spec.Load(f)
		if err != nil {
			t.Errorf("%s 加载失败: %v", f, err)
			continue
		}
		lib, err := spec.LoadLibrary(filepath.Join(filepath.Dir(f), "parts"))
		if err != nil {
			t.Errorf("%s 部件库加载失败: %v", f, err)
			continue
		}
		r := New(s, lib)
		dir := ""
		for k := range s.Directions {
			dir = k
			break
		}
		if dir == "" {
			t.Errorf("%s 没有方向数据", f)
			continue
		}
		img, err := r.Render(dir, 0, 1, 2)
		if err != nil {
			t.Errorf("%s 渲染失败: %v", f, err)
			continue
		}
		// 统计不透明像素，确认不是空图
		n := 0
		for i := 3; i < len(img.Pix); i += 4 {
			if img.Pix[i] > 8 {
				n++
			}
		}
		if n == 0 {
			empty++
			t.Logf("%s 渲染为空（需人工确认是否本就全透明）", f)
			continue
		}
		ok++
	}
	t.Logf("渲染成功 %d/%d，空图 %d", ok, len(files), empty)
	if ok == 0 {
		t.Fatal("没有任何素材渲染成功")
	}
}
