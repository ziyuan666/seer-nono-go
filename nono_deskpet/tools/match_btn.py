#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""match_btn.py —— 在面板底图上用模板匹配定位 SWF 按钮的实际像素矩形。

背景：面板底图（PNG）与 SWF 里按钮的 place 坐标并不总是一致（导出时可能被
重排/缩放），命中区必须以底图实测为准。本工具用 alpha 掩码做归一化相关匹配，
在多个缩放尺度下搜索，返回最佳矩形。

用法:
  python3 match_btn.py <底图.png> <按钮图.png> [--range x0,y0,x1,y1] [--scale 0.6,1.6]
"""
import argparse
import sys

import cv2
import numpy as np


def load_rgba(path):
    im = cv2.imread(path, cv2.IMREAD_UNCHANGED)
    if im is None:
        raise SystemExit(f'读不到 {path}')
    if im.shape[2] == 3:
        im = cv2.cvtColor(im, cv2.COLOR_BGR2BGRA)
    return im


def match(scene, tpl, x0, y0, x1, y1, s_lo, s_hi, step=0.02):
    """在 scene 的 [x0,y0,x1,y1] 区域内多尺度匹配 tpl，返回 (score,x,y,w,h,scale)。"""
    roi = scene[y0:y1, x0:x1]
    best = None
    s = s_lo
    while s <= s_hi + 1e-6:
        w = max(8, int(round(tpl.shape[1] * s)))
        h = max(8, int(round(tpl.shape[0] * s)))
        if w > roi.shape[1] or h > roi.shape[0]:
            s += step
            continue
        t = cv2.resize(tpl, (w, h), interpolation=cv2.INTER_AREA if s < 1 else cv2.INTER_LANCZOS4)
        mask = (t[:, :, 3] > 16).astype(np.uint8) * 255
        if mask.sum() < 255 * 20:
            s += step
            continue
        try:
            res = cv2.matchTemplate(roi[:, :, :3], t[:, :, :3], cv2.TM_SQDIFF_NORMED, mask=mask)
        except cv2.error:
            s += step
            continue
        res = np.nan_to_num(res, nan=1.0, posinf=1.0, neginf=1.0)
        mn, _, mloc, _ = cv2.minMaxLoc(res)
        mx = 1.0 - mn
        if best is None or mx > best[0]:
            best = (mx, x0 + mloc[0], y0 + mloc[1], w, h, s)
        s += step
    return best


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('scene')
    ap.add_argument('tpl')
    ap.add_argument('--range', default='')
    ap.add_argument('--scale', default='0.6,1.6')
    ap.add_argument('--scene-scale', type=float, default=1.0)
    a = ap.parse_args()
    scene = load_rgba(a.scene)
    if a.scene_scale and a.scene_scale != 1.0:
        sc = a.scene_scale
        scene = cv2.resize(scene, (int(round(scene.shape[1] / sc)), int(round(scene.shape[0] / sc))),
                           interpolation=cv2.INTER_AREA)
    tpl = load_rgba(a.tpl)
    # 裁到 alpha bbox：模板的透明边会让归一化匹配漂移，也会让叠加时多盖底图
    ys, xs = np.nonzero(tpl[:, :, 3] > 8)
    if len(xs):
        tpl = tpl[ys.min():ys.max() + 1, xs.min():xs.max() + 1]
    H, W = scene.shape[:2]
    if a.range:
        x0, y0, x1, y1 = [int(v) for v in a.range.split(',')]
    else:
        x0, y0, x1, y1 = 0, 0, W, H
    x1, y1 = min(x1, W), min(y1, H)
    s_lo, s_hi = [float(v) for v in a.scale.split(',')]
    best = match(scene, tpl, x0, y0, x1, y1, s_lo, s_hi)
    if not best:
        print('NO_MATCH')
        return
    score, x, y, w, h, s = best
    print(f'score={score:.4f} rect=({x},{y},{w},{h}) scale={s:.2f}')


if __name__ == '__main__':
    main()
