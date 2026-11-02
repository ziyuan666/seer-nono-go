// 临时对比工具：验证 01 的 d 解析结果与 02 的顶点数组还原结果是否逐点一致。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nonovec/pkg/pathd"
	"nonovec/pkg/spec"
)

func main() {
	dir1, dir2 := os.Args[1], os.Args[2]
	lib1, err := spec.LoadLibrary(filepath.Join(dir1, "parts"))
	if err != nil {
		panic(err)
	}
	lib2, err := spec.LoadLibrary(filepath.Join(dir2, "parts"))
	if err != nil {
		panic(err)
	}

	worst := 0.0
	segDiff := 0
	files := 0
	for name, pf1 := range lib1.Parts {
		if strings.Contains(name, "#") {
			continue // 跳过 base 别名
		}
		pf2 := lib2.Parts[name]
		if pf2 == nil {
			continue
		}
		files++
		for i := range pf1.Paths {
			if i >= len(pf2.Paths) {
				fmt.Printf("%s: 02 路径数不足 (%d < %d)\n", name, len(pf2.Paths), len(pf1.Paths))
				break
			}
			segs1, e1 := pathd.Parse(pf1.Paths[i].D)
			segs2 := pf2.Paths[i].Segs
			if e1 != nil {
				continue
			}
			if len(segs1) != len(segs2) {
				fmt.Printf("%s path%d: 段数不同 %d vs %d\n", name, i, len(segs1), len(segs2))
				segDiff++
				continue
			}
			for k := range segs1 {
				if segs1[k].Kind != segs2[k].Kind {
					fmt.Printf("%s path%d seg%d: 类型不同 %v vs %v\n", name, i, k, segs1[k].Kind, segs2[k].Kind)
					segDiff++
					continue
				}
				for j := range segs1[k].Pts {
					if j >= len(segs2[k].Pts) {
						break
					}
					d := segs1[k].Pts[j] - segs2[k].Pts[j]
					if d < 0 {
						d = -d
					}
					if d > worst {
						worst = d
					}
				}
			}
		}
	}
	fmt.Printf("对比 %d 个部件：段结构差异 %d 处，最大坐标差 %.10f px\n", files, segDiff, worst)
}
