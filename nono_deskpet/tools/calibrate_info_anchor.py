#!/usr/bin/env python3
"""校准：把各 SWF 子 sprite 的 4x 导出图在 137 整帧图里定位，得到真实锚点偏移。

背景：FFDec 导出 sprite 时，PNG 左上取的是该 sprite 的 bounds 左上，不一定等于
sprite 原点 (0,0)。所以「SWF 坐标 × 4 + 导出图」不一定对齐，必须实测锚点。
用法：python3 tools/calibrate_info_anchor.py <137整帧png> <137导出根目录>
"""
import glob
import os
import sys

import numpy as np
from PIL import Image

PANEL_S = 4.0
# 137 整帧图相对「面板原点」的像素偏移（模板匹配 bg 实测）
FRAME_OFF = (5, 2)

# (名字, 预测面板坐标, 素材路径 pattern)
ITEMS = [
    ('bgMc',        (0.00, 0.00),   'sp4/DefineSprite_14/1.png'),
    ('inner17',     (6.45, 37.00),  'sp4/DefineSprite_17/1.png'),
    ('chipline86',  (17.55, 235.70), 'sp4/DefineSprite_86/1.png'),
    ('lbl90',       (99.05, 43.25), 's90.png'),
    ('sp1',         (98.05, 84.40), 'sp4b/DefineSprite_94/1.png'),
    ('sp2',         (99.05, 102.40), 'sp4b/DefineSprite_97/1.png'),
    ('sp3',         (99.05, 120.90), 'sp4b/DefineSprite_100/1.png'),
    ('sp0f1',       (99.05, 65.75), 'sp4b/DefineSprite_118__fla.3r54656356fgfsdg_46__fla.3r54656356fgfsdg_46/1.png'),
    ('wrench120',   (249.60, 42.10), 'sp4b/DefineSprite_120/1.png'),
    ('ok122',       (246.40, 43.25), 'sp4b/DefineSprite_122/1.png'),
    ('nickbg135',   (159.00, 39.00), 'sp135/DefineSprite_135/1.png'),
    ('arrow89',     (260.10, 281.20), 'sp3b/DefineSprite_89/1.png'),
    ('bar22',       (69.15, 177.80), 'sp3b/DefineSprite_22/1.png'),
    ('fill20',      (70.60, 179.55), 'sp3b/DefineSprite_20/1.png'),
]


def load(p):
    return np.array(Image.open(p).convert('RGBA')).astype(np.int32)


def locate(big, small, pred, rad=28):
    h, w = small.shape[:2]
    mask = small[:, :, 3] > 240
    if mask.sum() < 50:
        return None
    best = (1e9, 0, 0)
    for dy in range(-rad, rad + 1):
        for dx in range(-rad, rad + 1):
            x, y = pred[0] + dx, pred[1] + dy
            if x < 0 or y < 0 or x + w > big.shape[1] or y + h > big.shape[0]:
                continue
            a = big[y:y + h, x:x + w]
            d = np.abs(a[:, :, :3] - small[:, :, :3])[mask].mean()
            if d < best[0]:
                best = (float(d), dx, dy)
    return best


def main():
    frame_png, root = sys.argv[1], sys.argv[2]
    big = load(frame_png)
    print('137 整帧', big.shape)
    print('%-12s %-16s %-8s %s' % ('name', 'pred', 'diff', 'anchor(dx,dy)'))
    for name, pos, pat in ITEMS:
        p = os.path.join(root, pat)
        if not os.path.exists(p):
            print('%-12s MISSING %s' % (name, p))
            continue
        sm = load(p)
        pred = (int(pos[0] * PANEL_S + FRAME_OFF[0]), int(pos[1] * PANEL_S + FRAME_OFF[1]))
        r = locate(big, sm, pred)
        if not r:
            print('%-12s 无有效像素' % name)
            continue
        print('%-12s %-16s %.2f     (%+d,%+d)  size=%dx%d' % (
            name, pred, r[0], r[1], r[2], sm.shape[1], sm.shape[0]))


if __name__ == '__main__':
    main()
