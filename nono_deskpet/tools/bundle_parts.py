#!/usr/bin/env python3
"""把每个形态的 sprite parts 目录合并成单文件 bundle。

素材格式是「一个部件一个 JSON」，全量 3138 个文件、parts 目录 3075 个。
Linux/Wine 下 os.ReadFile 走内存缓存，单形态 400~500ms；Windows 上每次
CreateFile+Read+Close 还有杀软实时扫描回调，单形态 750 个文件实测约 2 秒。
这不是 CPU 性能问题（9800X3D 单核再强也救不了逐文件 IO），只能减少文件数。

产物：每个形态目录下 parts.all.json
    {"version":1,"count":N,"files":{"<basename>":{...原 PartFile...}}}

Go 侧 spec.LoadLibrary 优先读它，读不到才回退逐文件扫描。
用法：python3 tools/bundle_parts.py [--verify]
"""
import json
import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SPRITES = os.path.join(ROOT, "assets", "sprites")


def bundle_form(form_dir):
    parts = os.path.join(form_dir, "parts")
    if not os.path.isdir(parts):
        return None
    files = {}
    for name in sorted(os.listdir(parts)):
        if not name.endswith(".json"):
            continue
        with open(os.path.join(parts, name), encoding="utf-8") as f:
            files[name[:-5]] = json.load(f)
    out = os.path.join(form_dir, "parts.all.json")
    with open(out, "w", encoding="utf-8") as f:
        json.dump({"version": 1, "count": len(files), "files": files},
                  f, ensure_ascii=False, separators=(",", ":"))
    return out, len(files), os.path.getsize(out)


def main():
    forms = sorted(d for d in os.listdir(SPRITES) if os.path.isdir(os.path.join(SPRITES, d)))
    total = 0
    for form in forms:
        r = bundle_form(os.path.join(SPRITES, form))
        if not r:
            continue
        path, n, size = r
        total += 1
        print(f"{form}: {n} parts -> {os.path.basename(path)} {size/1048576:.2f}MB")
    print(f"done {total} forms")


if __name__ == "__main__":
    main()
