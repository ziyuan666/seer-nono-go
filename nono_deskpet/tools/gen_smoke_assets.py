#!/usr/bin/env python3
"""从原版 4x 尾焰层图提取烟团贴图与发射锚点（程序化烟雾用）。

输入：assets/fly/f_<style>_<dir>_1.png（FFDec 导出的 fireMc 层图，4x）
      assets/fly/metrics.json（每层在整帧中的偏移 off，4x 像素）
      vecFlyClip（本脚本内嵌，与 nono_deskpet/vec_fly.go 的 vecFlyClip 一致）
输出：assets/sprites/smoke/s<style>_main.png   主烟团贴图（4x）
      assets/sprites/smoke/anchors.json        每 style×方向 的尾焰 bbox（画布单位）
"""
import json
import os

import numpy as np
from PIL import Image

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FLY = os.path.join(ROOT, "assets", "fly")
OUT = os.path.join(ROOT, "assets", "sprites", "smoke")

# 与 vec_fly.go 的 vecFlyClip 保持一致：{style: [x, y, w, h]}（画布单位）
CLIP = {
    1: (-4.25, -4.00, 120, 146),
    2: (-37.00, -8.25, 177, 165),
    3: (-9.25, -4.00, 105, 149),
    4: (-30.75, -8.25, 167, 170),
}
DIRS = ["down", "leftdown", "left", "leftup", "up", "rightup", "right", "rightdown"]
ALPHA = 32  # 内容判定阈值，与冒烟验证一致


def content_bbox(img):
    a = np.array(img.convert("RGBA"))
    m = a[..., 3] > ALPHA
    if not m.any():
        return None
    ys, xs = np.nonzero(m)
    return xs.min(), ys.min(), xs.max() + 1, ys.max() + 1


def main():
    os.makedirs(OUT, exist_ok=True)
    metrics = json.load(open(os.path.join(FLY, "metrics.json")))
    anchors = {}
    for st in (1, 2, 3, 4):
        key = str(st)
        off = metrics[key]["layers"]["fireMc"]["off"]
        c = CLIP[st]
        anchors[key] = {}
        made_tex = False
        for d in DIRS:
            p = os.path.join(FLY, f"f_{st}_{d}_1.png")
            if not os.path.exists(p):
                continue
            im = Image.open(p)
            bb = content_bbox(im)
            if bb is None:
                continue
            x0, y0, x1, y1 = bb
            # 层图坐标 -> 整帧坐标 -> 画布坐标（画布 1x，层图 4x）
            ax = c[0] + (off[0] + x0) / 4.0
            ay = c[1] + (off[1] + y0) / 4.0
            aw = (x1 - x0) / 4.0
            ahh = (y1 - y0) / 4.0
            anchors[key][d] = {
                "x": round(ax, 2), "y": round(ay, 2),
                "w": round(aw, 2), "h": round(ahh, 2),
            }
            # 主烟团贴图：取 down 方向（四方向同图时只做一次）
            if not made_tex:
                pad = 6
                bx0, by0 = max(0, x0 - pad), max(0, y0 - pad)
                bx1, by1 = min(im.width, x1 + pad), min(im.height, y1 + pad)
                im.crop((bx0, by0, bx1, by1)).save(
                    os.path.join(OUT, f"s{st}_main.png"))
                made_tex = True
                print(f"style{st} 贴图 {bx1-bx0}x{by1-by0} @4x")
        print(f"style{st} 方向数 {len(anchors[key])}: "
              + ", ".join(f"{k}{v}" for k, v in anchors[key].items()))
    with open(os.path.join(OUT, "anchors.json"), "w") as f:
        json.dump(anchors, f, ensure_ascii=False, indent=1)
    print("写出", os.path.join(OUT, "anchors.json"))


if __name__ == "__main__":
    main()
