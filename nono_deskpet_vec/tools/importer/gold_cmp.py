#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""拿 FFDec 导出的帧当金标准，逐帧比对我们的渲染。

对齐口径：sprite 在舞台上的 place translate 减去 canvas.anchor 得到渲染图
左上角在舞台上的像素位置。背景色取 SWF 的 SetBackgroundColor（不同动画不同，
不能写死）。FFDec 不执行 AS3，所以比对覆盖几何/颜色/部件，不覆盖脚本驱动的 tween。
"""
import json
import sys
import glob
import os

import numpy as np
from PIL import Image


def load_frames(d):
    fs = glob.glob(os.path.join(d, '*.png'))
    fs.sort(key=lambda p: int(os.path.basename(p)[:-4]))
    return fs


def main():
    sprite_dir, ours_dir, gold_dir = sys.argv[1], sys.argv[2], sys.argv[3]
    tx, ty = float(sys.argv[4]), float(sys.argv[5])
    bg = tuple(int(sys.argv[6][i:i + 2], 16) for i in (0, 2, 4))

    sj = json.load(open(os.path.join(sprite_dir, 'sprite.json'), encoding='utf-8'))
    cv = sj['canvas']
    anchor = cv.get('ballc') or cv['anchor']
    ox = int(round(tx - anchor[0]))
    oy = int(round(ty - anchor[1]))

    ours = load_frames(ours_dir)
    gold = load_frames(gold_dir)
    n = min(len(ours), len(gold))
    print('比对 %d 帧  canvas=%.2fx%.2f anchor=%s 偏移=(%d,%d) 背景=#%02x%02x%02x'
          % (n, cv['w'], cv['h'], [round(v, 3) for v in anchor], ox, oy, *bg))

    worst = []
    total_bad = 0
    total_px = 0
    for i in range(n):
        o = np.asarray(Image.open(ours[i]).convert('RGBA')).astype(np.float64)
        g = np.asarray(Image.open(gold[i]).convert('RGB')).astype(np.float64)
        H, W = g.shape[:2]
        h, w = o.shape[:2]
        if oy < 0 or ox < 0 or oy + h > H or ox + w > W:
            print('  !! 帧%d 越出金标准画布' % (i + 1))
            continue
        a = o[:, :, 3:4] / 255.0
        base = np.zeros((H, W, 3))
        base[:, :] = bg
        base[oy:oy + h, ox:ox + w] = o[:, :, :3] * a + base[oy:oy + h, ox:ox + w] * (1 - a)
        d = np.abs(base - g)
        bad = int((d.sum(axis=2) > 6).sum())
        total_bad += bad
        total_px += H * W
        if bad:
            worst.append((bad, i + 1, float(d.mean()), float(d.max())))

    worst.sort(reverse=True)
    print('合计差异像素 %d / %d (%.4f%%)' % (total_bad, total_px, total_bad * 100.0 / total_px))
    print('有差异的帧: %d / %d' % (len(worst), n))
    for bad, idx, mean, mx in worst[:10]:
        print('  帧%-4d 差异 %-6d 平均差 %.2f 最大差 %.0f' % (idx, bad, mean, mx))
    return 0


if __name__ == '__main__':
    sys.exit(main())
