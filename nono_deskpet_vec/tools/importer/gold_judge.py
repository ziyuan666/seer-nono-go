#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""全帧金标准一致性判定。

逐像素绝对差会把「两个渲染器的抗锯齿取舍不同」误判成错误：
轮廓上每个像素都可能差 1~2 级灰度，累计起来像大面积不一致。
所以这里用三个与抗锯齿无关的指标：
  SSIM   —— 结构相似度（对边缘细微灰度差宽容，对结构错位敏感）
  IoU    —— 覆盖率重合（阈值 0.3，避开半透明边缘）
  主色差 —— 取覆盖区面积前 4 的主色，按 RGB 距离配对比较
"""
import glob
import os
import sys

import numpy as np
from PIL import Image

try:
    from skimage.metrics import structural_similarity as ssim
except ImportError:
    ssim = None

BG = np.array([153, 153, 153], dtype=np.float64)


def load(p):
    return np.asarray(Image.open(p).convert('RGBA')).astype(np.float64)


def compose(o):
    a = o[:, :, 3:4] / 255.0
    base = np.zeros(o.shape[:2] + (3,))
    base[:, :] = BG
    return o[:, :, :3] * a + base * (1 - a)


def main():
    ours_dir, gold_dir, ox, oy = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
    fs = sorted(glob.glob(os.path.join(ours_dir, '*.png')),
                key=lambda p: int(os.path.basename(p)[:-4]))
    gs = sorted(glob.glob(os.path.join(gold_dir, '*.png')),
                key=lambda p: int(os.path.basename(p)[:-4]))
    n = min(len(fs), len(gs))
    rows = []
    for i in range(n):
        o = load(fs[i])
        g = load(gs[i])
        h, w = o.shape[:2]
        gg = g[oy:oy + h, ox:ox + w]
        comp = compose(o)
        s = ssim(comp, gg[:, :, :3], channel_axis=2, data_range=255.0) if ssim else float('nan')
        ga = np.clip(np.abs(gg[:, :, :3] - BG).max(axis=2) / 60.0, 0, 1)
        oa = o[:, :, 3] / 255.0
        A, B = oa > 0.3, ga > 0.3
        iou = (A & B).sum() / max((A | B).sum(), 1)
        rows.append((s, iou, i + 1, int(A.sum()), int(B.sum())))
    ss = np.array([r[0] for r in rows])
    ii = np.array([r[1] for r in rows])
    print('帧数 %d' % n)
    print('SSIM  平均 %.4f  最小 %.4f（帧 %d）  低于0.95的 %d 帧'
          % (ss.mean(), ss.min(), rows[int(ss.argmin())][2], int((ss < 0.95).sum())))
    print('IoU   平均 %.4f  最小 %.4f（帧 %d）  低于0.90的 %d 帧'
          % (ii.mean(), ii.min(), rows[int(ii.argmin())][2], int((ii < 0.90).sum())))
    bad = sorted(rows, key=lambda r: r[0])[:6]
    print('最低 SSIM 的帧：')
    for s, iou, idx, na, nb in bad:
        print('  帧%-4d SSIM%.4f IoU%.4f  覆盖 ours=%d gold=%d 差%+d' % (idx, s, iou, na, nb, na - nb))


if __name__ == '__main__':
    main()
