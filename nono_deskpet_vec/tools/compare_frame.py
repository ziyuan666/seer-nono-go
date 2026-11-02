#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""compare_frame.py —— 把矢量渲染结果与 FFDec 导出的同帧 PNG 做内容对齐对比。

为什么要这个脚本：动作帧的画布口径与 FFDec 不同（我们按 shape 并集算，
FFDec 按 sprite frame bounds），直接按画布尺寸比会得到一堆假的偏移。
这里统一「各自裁到非透明内容 → 缩放到同一尺寸 → 算 IoU 与色差」，
只比形状与配色，不比留白。

用法：
    python3 tools/compare_frame.py <我们的png> <ffdec的png> [--out 对比图]
"""
import argparse
import sys

import numpy as np
from PIL import Image


def content(im, thr=8):
    a = np.array(im.convert('RGBA'))
    ys, xs = np.where(a[:, :, 3] > thr)
    if len(xs) == 0:
        return None
    return im.crop((int(xs.min()), int(ys.min()), int(xs.max()) + 1, int(ys.max()) + 1))


def score(mine_path, ref_path, size=256, out=None):
    mine = content(Image.open(mine_path))
    ref = content(Image.open(ref_path))
    if mine is None or ref is None:
        return {'error': '空图'}
    m = np.array(mine.resize((size, size), Image.LANCZOS)).astype(float)
    r = np.array(ref.resize((size, size), Image.LANCZOS)).astype(float)
    ma, ra = m[:, :, 3] > 40, r[:, :, 3] > 40
    union = (ma | ra).sum()
    iou = (ma & ra).sum() / union if union else 0.0
    both = ma | ra
    cd = np.abs(m[:, :, :3] - r[:, :, :3])[both].mean() if both.any() else 0.0
    res = {
        'mine_size': mine.size, 'ref_size': ref.size,
        'iou': round(float(iou), 4), 'color_diff': round(float(cd), 1),
    }
    if out:
        o = np.zeros((size, size, 3), np.uint8)
        o[:, :, 0] = r[:, :, 3].astype(np.uint8)
        o[:, :, 1] = m[:, :, 3].astype(np.uint8)
        Image.fromarray(o).save(out)
        res['overlay'] = out
    return res


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('mine')
    ap.add_argument('ref')
    ap.add_argument('--out')
    args = ap.parse_args()
    r = score(args.mine, args.ref, out=args.out)
    for k, v in r.items():
        print(f'{k}: {v}')


if __name__ == '__main__':
    main()
