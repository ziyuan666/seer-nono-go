// 把 02 目录按 Go 侧加载后的 PathDef 原样写回 01 格式，用于定位渲染差异来源。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"nonovec/pkg/spec"
)

func main() {
	src, dst := os.Args[1], os.Args[2]
	os.MkdirAll(filepath.Join(dst, "parts"), 0o755)
	lib, err := spec.LoadLibrary(filepath.Join(src, "parts"))
	if err != nil {
		panic(err)
	}
	for name, pf := range lib.Parts {
		if strings.Contains(name, "#") {
			continue
		}
		b, _ := json.Marshal(pf)
		os.WriteFile(filepath.Join(dst, "parts", name+".json"), b, 0o644)
	}
	sp, err := spec.Load(filepath.Join(src, "sprite.json"))
	if err != nil {
		panic(err)
	}
	b, _ := json.Marshal(sp)
	os.WriteFile(filepath.Join(dst, "sprite.json"), b, 0o644)
}
