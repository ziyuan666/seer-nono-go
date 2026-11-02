#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""逐层精确标定飞行素材偏移（替代 alpha-only 的 calibrate_fly_offsets.py）。

为什么重写（2026-10-24）：旧脚本只比 alpha 通道，4 层叠加的 style2/3/4 上
半透明的 bgMc 和会被染色的 colorMc 会互相吸走位置，实测 style3 偏差 11.6/255，
表现就是「阴影不见了、青色底板串位」。新版：

  1. 按 z 顺序**从顶到底**匹配（顶层无遮挡，匹配最可靠）
  2. 评分用 RGB 全通道 L1，只在「当前层或 root 有内容」的像素上算
  3. 1x 粗搜（步长 1）→ 4x 细化（±4），兼顾速度与精度

输出 assets/fly/offsets.json（4x 像素偏移，与旧文件同格式同量纲）。
"""
import glob
import json
import os
import sys

import numpy as np
from PIL import Image

OUT = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "assets", "fly")
METRICS = os.path.join(OUT, "metrics.json")
OFFSETS = os.path.join(OUT, "offsets.json")

# z 顺序：depth 升序（底→顶）。匹配时反向遍历。
Z_ORDER = {
    1: ["colorMc", "fireMc", "dirMc"],
    2: ["bgMc", "fireMc", "colorMc", "dirMc"],
    3: ["bgMc", "fireMc", "colorMc", "dirMc"],
    4: ["bgMc", "fireMc", "colorMc", "dirMc"],
}


def load_layer(style, name, metrics):
    lm = metrics[str(style)]["layers"][name]
    pre = lm["prefix"]
    p = os.path.join(OUT, "%s_%d_down_1.png" % (pre, style))
    if not os.path.exists(p):
        p = os.path.join(OUT, "%s_%d_down.png" % (pre, style))
    if not os.path.exists(p):
        return None
    return np.asarray(Image.open(p).convert("RGBA"))


def over(dst, src, x, y):
    """dst 上以 (x,y) 贴 src（alpha over），原地修改。"""
    H, W = dst.shape[:2]
    h, w = src.shape[:2]
    x0, y0 = max(0, x), max(0, y)
    x1, y1 = min(W, x + w), min(H, y + h)
    if x1 <= x0 or y1 <= y0:
        return
    s = src[y0 - y:y1 - y, x0 - x:x1 - x].astype(np.float32)
    d = dst[y0:y1, x0:x1].astype(np.float32)
    sa = s[:, :, 3:4] / 255.0
    d[:, :, :3] = s[:, :, :3] * sa + d[:, :, :3] * (1 - sa)
    d[:, :, 3:4] = s[:, :, 3:4] + d[:, :, 3:4] * (1 - sa)
    dst[y0:y1, x0:x1] = np.clip(d, 0, 255).astype(np.uint8)


def score(canvas, root, cover):
    """只在 cover（当前层或 root 有内容的像素）上比 RGB L1。"""
    a = canvas[:, :, :3].astype(np.int32)
    b = root[:, :, :3].astype(np.int32)
    d = np.abs(a - b).sum(axis=2)
    m = cover
    if m.sum() == 0:
        return 1e18
    return float(d[m].sum()) / float(m.sum())


def search(root, canvas, src, coarse_step, radius):
    """在 canvas 上逐位置贴 src，返回 (最优 x, y, score)。"""
    H, W = root.shape[:2]
    h, w = src.shape[:2]
    if h > H or w > W:
        return None
    best = (1e18, 0, 0)
    ys = range(0, H - h + 1, coarse_step)
    xs = range(0, W - w + 1, coarse_step)
    for y in ys:
        for x in xs:
            tmp = canvas.copy()
            over(tmp, src, x, y)
            cover = (tmp[:, :, 3] > 8) | (root[:, :, 3] > 8)
            s = score(tmp, root, cover)
            if s < best[0]:
                best = (s, x, y)
    if coarse_step == 1:
        return best
    # 细化：在粗解周边 radius 内逐像素
    bs, bx, by = best
    for y in range(max(0, by - radius), min(H - h, by + radius) + 1):
        for x in range(max(0, bx - radius), min(W - w, bx + radius) + 1):
            tmp = canvas.copy()
            over(tmp, src, x, y)
            cover = (tmp[:, :, 3] > 8) | (root[:, :, 3] > 8)
            s = score(tmp, root, cover)
            if s < best[0]:
                best = (s, x, y)
    return best


def main():
    metrics = json.load(open(METRICS, encoding="utf-8"))
    off = json.load(open(OFFSETS, encoding="utf-8")) if os.path.exists(OFFSETS) else {}
    only = None
    if "--style" in sys.argv:
        only = int(sys.argv[sys.argv.index("--style") + 1])

    for style in (1, 2, 3, 4):
        if only and style != only:
            continue
        roots = glob.glob("/tmp/flycomp_%d_*/spr/DefineSprite_*_item/1.png" % style)
        if not roots:
            print("style %d: 缺 root 帧，跳过" % style)
            continue
        root = np.asarray(Image.open(roots[0]).convert("RGBA"))
        H, W = root.shape[:2]
        print("== style %d root %dx%d" % (style, W, H))
        canvas = np.zeros((H, W, 4), np.uint8)
        order = Z_ORDER[style]
        got = {}
        # 从顶到底匹配：顶层无遮挡，先把它的位置钉死
        for name in reversed(order):
            src = load_layer(style, name, metrics)
            if src is None:
                continue
            tgt = src[::4, ::4]  # 1x 粗搜
            rc = canvas[::4, ::4]
            rr = root[::4, ::4]
            co = search(rr, rc, tgt, 1, 0)
            if co is None:
                print("   %-8s 尺寸超出" % name)
                continue
            _, cx, cy = co
            # 以 1x 粗解为中心在 4x 上逐像素细化 ±8
            off_x, off_y = cx * 4, cy * 4
            best = (1e18, off_x, off_y)
            for y in range(max(0, off_y - 8), min(H - src.shape[0], off_y + 8) + 1):
                for x in range(max(0, off_x - 8), min(W - src.shape[1], off_x + 8) + 1):
                    tmp = canvas.copy()
                    over(tmp, src, x, y)
                    cover = (tmp[:, :, 3] > 8) | (root[:, :, 3] > 8)
                    s = score(tmp, root, cover)
                    if s < best[0]:
                        best = (s, x, y)
            bs, bx, by = best
            got[name] = [int(bx), int(by)]
            over(canvas, src, bx, by)
            a = canvas[:, :, :3].astype(int)
            b = root[:, :, :3].astype(int)
            cur = np.abs(a - b).mean()
            print("   %-8s off=(%4d,%4d)  score=%.2f  累计 mean=%.2f" % (name, bx, by, bs, cur))
        off[str(style)] = got
        a = canvas[:, :, :3].astype(int)
        b = root[:, :, :3].astype(int)
        print("   → style %d 合成 mean|diff| = %.2f" % (style, np.abs(a - b).mean()))

    if not only:
        json.dump(off, open(OFFSETS, "w", encoding="utf-8"), ensure_ascii=False,
                  indent=1, sort_keys=True)
        print("offsets.json 已更新")


if __name__ == "__main__":
    main()
