#!/usr/bin/env python3
"""素材完整性校验：帧数、尺寸一致性、meta 与 sfx。"""
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ASSETS = ROOT / "assets"

DIRS = ["up", "rightup", "right", "rightdown", "down", "leftdown", "left", "leftup"]


def main() -> int:
    bad = []
    meta_path = ASSETS / "plain" / "meta.json"
    if not meta_path.exists():
        print("缺少 meta.json")
        return 1
    meta = json.loads(meta_path.read_text(encoding="utf-8"))
    zoom = meta.get("zoom", 1)
    canvas = meta.get("canvas", {})
    print(f"形态 plain  zoom={zoom}  canvas={canvas.get('w')}x{canvas.get('h')}")

    sizes = {}
    total = 0
    for d in DIRS:
        p = ASSETS / "plain" / "idle" / d
        files = sorted(p.glob("*.png")) if p.exists() else []
        if not files:
            bad.append(f"idle/{d} 缺帧")
            continue
        from PIL import Image

        sz = Image.open(files[0]).size
        sizes[d] = len(files)
        total += len(files)
        off = [f.name for f in files if Image.open(f).size != sz]
        if off:
            bad.append(f"idle/{d} 尺寸不一致: {off[:3]}")
        print(f"  idle/{d:<10} {len(files):>3} 帧  {sz[0]}x{sz[1]}")

    for kind in ("action", "exp"):
        base = ASSETS / "plain" / kind
        if not base.exists():
            continue
        for sub in sorted(base.iterdir()):
            if not sub.is_dir():
                continue
            files = sorted(sub.glob("*.png"))
            total += len(files)
            if not files:
                bad.append(f"{kind}/{sub.name} 空目录")
                continue
            print(f"  {kind}/{sub.name:<10} {len(files):>3} 帧")

    print(f"帧总数 {total}")

    missing = [str(p) for p in [
        ASSETS / "sfx" / f"exp{i}.wav" for i in (1, 2, 3, 4)
    ] + [ASSETS / "sfx" / "boot.wav"] if not p.exists()]
    if missing:
        bad.append("缺音效: " + ", ".join(missing))

    chips_path = ASSETS / "chips.json"
    if chips_path.exists():
        chips = json.loads(chips_path.read_text(encoding="utf-8"))
        kinds = {}
        for c in chips:
            kinds[c["kind"]] = kinds.get(c["kind"], 0) + 1
        print(f"芯片 {len(chips)} 项  分类 {kinds}")
    else:
        bad.append("缺 chips.json")

    if bad:
        print("\n问题:")
        for b in bad:
            print(" -", b)
        return 1
    print("\n素材校验通过")
    return 0


if __name__ == "__main__":
    sys.exit(main())
