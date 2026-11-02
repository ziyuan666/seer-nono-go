#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""用 root 帧（原版三层的真实合成）反解各层在整帧里的偏移。

为什么需要：ffdec 按各自 bbox 裁切导出每个子 sprite，坐标原点各不相同；靠"质量中心"
或"尺寸相同就 0 偏移"猜位置，style1 实测与 root 差 6.79（17890 px 错位），style2 只是
碰巧对。原版 AS3（NonoFlyModel.as）只做 gotoAndStop(方向)，没有缩放/旋转，所以 root
帧 1 就是三层的权威合成——拿它当标尺反解偏移最可靠。

用法：python3 tools/calibrate_fly_offsets.py [style...]
输出：assets/fly/offsets.json  {"1": {"colorMc": [x,y], ...}, ...}
"""
import glob
import json
import os
import sys
import tempfile

import numpy as np
from PIL import Image

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from gen_fly_composite import (STYLE_LAYERS, OUT, load_frame, prepare,
                               sprite_dir)

CANVAS_W, CANVAS_H = None, None


def load(p):
    return np.asarray(Image.open(p).convert("RGBA")).astype(np.int16)


def paste_alpha(canvas_a, layer, x, y):
    """把 layer 以 (x,y) 为左上角贴到 canvas_a 上，返回新的 alpha 合成结果。

    alpha 合成：out = a1 + a2*(1-a1)，但这里只关心"哪里有内容"，用 max 近似足够
    定位；颜色层用 FillColor 改过色，本来就不该参与颜色比对。
    """
    H, W = canvas_a.shape
    lh, lw = layer.shape
    x0, y0 = max(0, x), max(0, y)
    x1, y1 = min(W, x + lw), min(H, y + lh)
    if x1 <= x0 or y1 <= y0:
        return canvas_a
    sub = layer[y0 - y:y1 - y, x0 - x:x1 - x]
    win = canvas_a[y0:y1, x0:x1]
    # a_out = a + b*(1-a)
    canvas_a[y0:y1, x0:x1] = win + sub * (1.0 - win)
    return canvas_a


def score(root_a, layers, offsets, order):
    """合成后 alpha 与 root 的 L1（归一化到 0..1）。"""
    canvas = np.zeros_like(root_a)
    for name in order:
        arr, _ = layers[name]
        x, y = offsets[name]
        paste_alpha(canvas, arr[:, :, 3].astype(np.float32) / 255.0, x, y)
    return float(np.abs(canvas - root_a).mean())


def optimize(style, tmp, coarse=2, radius=60, init=None):
    """init：可选的初值 {层名: [x,y]}（一般取上一版 offsets.json）。

    为什么要初值（2026-10-27）：从「居中」起步的坐标下降对 fireMc 会掉进局部最优
    ——style2/4 的火苗被压到整帧下半部（rgb 差 80/41），而正确位置在机体上方
    （rgb 差 1~2）。用现有 offsets.json 当起点即可锁定正确盆地。
    """
    root_id, layers = STYLE_LAYERS[style]
    spr, xml = prepare(style, tmp)
    root_dir = sprite_dir(spr, root_id)
    if root_dir is None:
        raise SystemExit("style %d: 找不到 root" % style)
    root = load(os.path.join(root_dir, "1.png"))
    root_a = root[:, :, 3].astype(np.float32) / 255.0
    H, W = root_a.shape

    layer_data = {}
    for name, chid in layers:
        d = sprite_dir(spr, chid)
        f1 = load_frame(d, 1)
        layer_data[name] = (f1, (0, 0))

    order = [n for n, _ in layers]          # 从底到顶
    # 初值：优先用传入的既有标定，其次按 alpha 面积居中
    offsets = {}
    for name in order:
        arr = layer_data[name][0]
        lh, lw = arr.shape[:2]
        if init and name in init and len(init[name]) >= 2:
            offsets[name] = (int(init[name][0]), int(init[name][1]))
        else:
            offsets[name] = ((W - lw) // 2, (H - lh) // 2)

    best = score(root_a, layer_data, offsets, order)
    print("  style %d 初值 score=%.5f" % (style, best))

    # 坐标下降：逐层在半径内搜索，两轮粗搜 + 一轮细搜
    for rnd, (r, step) in enumerate([(radius, 8), (radius, 2), (8, 1)]):
        for name in order:
            arr = layer_data[name][0]
            lh, lw = arr.shape[:2]
            bx, by = offsets[name]
            local_best = best
            for y in range(max(0, by - r), min(H - lh, by + r) + 1, step):
                for x in range(max(0, bx - r), min(W - lw, bx + r) + 1, step):
                    offsets[name] = (x, y)
                    s = score(root_a, layer_data, offsets, order)
                    if s < local_best - 1e-9:
                        local_best = s
                        nb = (x, y)
            offsets[name] = nb if local_best < best else (bx, by)
            best = local_best
        print("  style %d round%d score=%.5f offsets=%s"
              % (style, rnd, best, {k: v for k, v in offsets.items()}))
    return offsets, best


def main():
    styles = [int(x) for x in sys.argv[1:]] or [1, 2, 3, 4]
    out = {}
    if os.path.exists(os.path.join(OUT, "offsets.json")):
        out = json.load(open(os.path.join(OUT, "offsets.json"), encoding="utf-8"))
    for style in styles:
        tmp = tempfile.mkdtemp(prefix="flycal_%d_" % style)
        print("== style", style)
        offs, best = optimize(style, tmp, init=out.get(str(style)))
        out[str(style)] = {k: list(map(int, v)) for k, v in offs.items()}
        print("  -> ", out[str(style)])
    with open(os.path.join(OUT, "offsets.json"), "w", encoding="utf-8") as fh:
        json.dump(out, fh, ensure_ascii=False, indent=1, sort_keys=True)
    print("written", os.path.join(OUT, "offsets.json"))


if __name__ == "__main__":
    main()
