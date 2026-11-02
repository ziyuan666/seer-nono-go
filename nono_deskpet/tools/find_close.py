#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""find_close.py —— 在面板底图右上角区域，从一堆候选按钮图里找出「关闭按钮」。

用法: python3 find_close.py <底图.png> <按钮目录> [区域宽 高]
输出按分数排序的候选。
"""
import os
import sys

import cv2
import numpy as np

from match_btn import load_rgba, match


def main():
    scene_path, btn_dir = sys.argv[1], sys.argv[2]
    bw = int(sys.argv[3]) if len(sys.argv) > 3 else 90
    bh = int(sys.argv[4]) if len(sys.argv) > 4 else 70
    scene = load_rgba(scene_path)
    H, W = scene.shape[:2]
    out = []
    for d in sorted(os.listdir(btn_dir)):
        p = os.path.join(btn_dir, d)
        if not os.path.isdir(p):
            continue
        ups = [f for f in os.listdir(p) if f.endswith('_up.png')]
        if not ups:
            continue
        tpl = load_rgba(os.path.join(p, ups[0]))
        ys, xs = np.nonzero(tpl[:, :, 3] > 8)
        if not len(xs):
            continue
        tpl = tpl[ys.min():ys.max() + 1, xs.min():xs.max() + 1]
        # 4x 导出的模板缩到 1x 再匹配（底图是 1x）
        if tpl.shape[1] > 200 or tpl.shape[0] > 200:
            tpl = cv2.resize(tpl, (tpl.shape[1] // 4, tpl.shape[0] // 4), interpolation=cv2.INTER_AREA)
        best = match(scene, tpl, max(0, W - bw), 0, W, min(H, bh), 0.8, 1.4, 0.05)
        if best:
            out.append((best[0], d, best[1:]))
    out.sort(reverse=True)
    for score, name, rect in out[:6]:
        print(f'{score:.4f}  {name}  rect={rect}')


if __name__ == '__main__':
    main()
