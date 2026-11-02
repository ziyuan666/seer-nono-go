//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 存档层单元测试（2026-11-02 nono-20261102-02 对照 seer D-18/D-02/D-03 补）。
// 用 NONO_SAVE_DIR 把存档目录指到临时目录，避免污染 exe 同级与 AppData。

func withTempSaveDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "save")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("建临时存档目录失败: %v", err)
	}
	old := os.Getenv("NONO_SAVE_DIR")
	os.Setenv("NONO_SAVE_DIR", dir)
	t.Cleanup(func() { os.Setenv("NONO_SAVE_DIR", old) })
	return dir
}

// TestSaveWriteAtomic 验证 write() 走「临时文件 + rename」（D-02）。
func TestSaveWriteAtomic(t *testing.T) {
	withTempSaveDir(t)
	s := defaultSave()
	s.Nick = "原子写测试"
	s.Scale = 2.0
	s.write()

	raw, err := os.ReadFile(savePath())
	if err != nil {
		t.Fatalf("读存档失败: %v", err)
	}
	var got Save
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("存档不是合法 JSON: %v", err)
	}
	if got.Nick != "原子写测试" {
		t.Fatalf("Nick = %q，期望「原子写测试」", got.Nick)
	}
	if got.Scale != 2.0 {
		t.Fatalf("Scale = %v，期望 2.0", got.Scale)
	}
	// 不应残留 .tmp
	if _, err := os.Stat(savePath() + ".tmp"); err == nil {
		t.Fatal("写完仍残留 .tmp 文件")
	}
}

// TestLoadSaveCorruptBackup 验证损坏存档会被备份成 .bad（D-03）。
func TestLoadSaveCorruptBackup(t *testing.T) {
	dir := withTempSaveDir(t)
	// 预写一个损坏存档
	p := savePath()
	if err := os.WriteFile(p, []byte("{ nope not json"), 0644); err != nil {
		t.Fatalf("写损坏存档失败: %v", err)
	}
	s := loadSave()
	if s.Nick == "" {
		t.Fatal("损坏存档应回退默认值，不能留空")
	}
	// 原损坏文件应被挪成 .bad
	if _, err := os.Stat(p + ".bad"); err != nil {
		t.Fatalf("损坏存档未被备份为 .bad: %v（dir=%s）", err, dir)
	}
}

// TestSavePathUsesEnv 验证 NONO_SAVE_DIR 生效（D-18 基础设施 + 便携部署）。
func TestSavePathUsesEnv(t *testing.T) {
	dir := withTempSaveDir(t)
	p := savePath()
	if filepath.Dir(p) != dir {
		t.Fatalf("savePath = %s，期望目录 %s", p, dir)
	}
}