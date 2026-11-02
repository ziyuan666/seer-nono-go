#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""逐部件定位渲染差异：把全图差异归因到具体部件。

思路：每个部件单独渲染出 alpha 掩膜，再看全图（合成后）与金标准的差异
有多少落在该部件的覆盖区里。差异集中在某个部件 → 那个部件的颜色/alpha/变换错。
"""
import sys
import numpy as np
from PIL import Image

BG = (0x99, 0x99, 0x99)


def compose(rgba):
    a = rgba[:, :, 3:4] / 255.0
    base = np.zeros(rgba.shape[:2] + (3,))
    base[:, :] = BG
    return rgba[:, :, :3] * a + base * (1 - a)


def main():
    n = int(sys.argv[1])
    ox, oy = int(sys.argv[2]), int(sys.argv[3])
    full = np.asarray(Image.open(sys.argv[4]).convert('RGBA')).astype(np.float64) \
        if len(sys.argv) > 4 else None
    gold_png = None
    for k, v in enumerate(sys.argv):
        if v == '--gold':
            gold_png = sys.argv[k + 1]
    g = np.asarray(Image.open(gold_png).convert('RGB')).astype(np.float64)
    h, w = full.shape[:2]
    gg = g[oy:oy + h, ox:ox + w]
    d_full = np.abs(compose(full) - gg).sum(axis=2)
    total = int((d_full > 6).sum())
    print('全图差异 %d 像素' % total)
    print('  部件 覆盖px  区内差异px  区内差异占比  区内平均差')
    rows = []
    for i in range(n):
        p = np.asarray(Image.open('/tmp/p58_%d.png' % i).convert('RGBA')).astype(np.float64)
        m = p[:, :, 3] > 8
        cov = int(m.sum())
        if cov == 0:
            continue
        bad = int((d_full[m] > 6).sum())
        rows.append((bad / max(cov, 1), bad, cov, i, float(d_full[m].mean())))
    rows.sort(reverse=True)
    for ratio, bad, cov, i, mean in rows[:12]:
        print('  #%-3d %6d   %6d    %5.1f%%      %.1f' % (i, cov, bad, ratio * 100, mean))


if __name__ == '__main__':
    main()
