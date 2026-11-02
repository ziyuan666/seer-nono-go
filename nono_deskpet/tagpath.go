package main

// 「两个发布包解压到同一文件夹」共存支持。
//
// 赛尔桌宠与 NONO 桌宠是两个独立工程（不同仓库、不同 save 结构），
// 但子渊要求它们能同时出现在同一目录下并各自记住自己的设置。
// 做法是按 exe 文件名派生前缀：
//
//	nono_deskpet.exe → "nono" → assets_nono/ + save_nono.json
//	seer_deskpet.exe → "seer" → assets_seer/ + save_seer.json
//
// 派生规则：取文件名第一个 '_' 之前的部分并小写。取不到（main.exe、
// go run 的随机临时名）返回空串，全部退回旧布局，开发期零影响。

import (
	"os"
	"path/filepath"
	"strings"
)

// appTag 返回 exe 名前缀；无法判定时返回空串。
//
// 无下划线时再按 "nono" 前缀认一次：用户把包里的 exe 改名成 nono.exe
// 这类短名字很常见，不认就会去找不存在的 assets/ 而启动失败。
func appTag() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	b := filepath.Base(exe)
	b = strings.TrimSuffix(b, filepath.Ext(b))
	lower := strings.ToLower(b)
	if i := strings.Index(lower, "_"); i > 0 {
		return lower[:i]
	}
	if strings.HasPrefix(lower, "nono") {
		return "nono"
	}
	return ""
}

// uniqueTaggedDir 找出同级唯一的 <name>_*/ 目录；没有或有多个都返回空。
//
// 多个时故意不猜：共存包里同时存在 assets_seer/ 与 assets_nono/，
// 猜错就是把另一个桌宠的素材读进来。
func uniqueTaggedDir(base, name string) string {
	ents, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	found := ""
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), name+"_") {
			continue
		}
		if found != "" {
			return ""
		}
		found = filepath.Join(base, e.Name())
	}
	return found
}

// looksLikeNonoSave 判断一份 JSON 是不是 NONO 自己的存档。
//
// 同目录共存后两个项目都要从旧的 save.json 迁移，只看文件名会把赛尔的读进来。
// NONO 的特征字段是 open / super / chips / owner，赛尔的是 clothSlots / doodleId。
func looksLikeNonoSave(raw []byte) bool {
	s := string(raw)
	if !strings.HasPrefix(strings.TrimSpace(s), "{") {
		return false
	}
	mine := strings.Contains(s, `"open"`) || strings.Contains(s, `"super"`) ||
		strings.Contains(s, `"chips"`) || strings.Contains(s, `"owner"`)
	theirs := strings.Contains(s, `"clothSlots"`) || strings.Contains(s, `"doodleId"`)
	return mine && !theirs
}
