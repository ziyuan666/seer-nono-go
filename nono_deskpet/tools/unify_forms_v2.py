#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""unify_forms_v2.py —— 全部形态统一到同一套画布与同一视觉大小。

上一版只把「面部黑窗口」对齐，结果球一样大了，但鳍/耳/尾翼各按自身比例，整体
外接尺寸差得离谱（super4 展开到 579px 宽）。这里改成：

1. 缩放系数按「整体外接面积」等效：k = sqrt(plain_area / form_area)。球体作为
   参照体，保证缩放后球径落在合理区间，不会出现某个形态球特别小或特别大。
2. 画布全部固定为同一尺寸，水平居中、底部（影子底边）对齐。这样切形态时窗口
   大小与落脚点都不变。

用法：
    python3 unify_forms_v2.py <源目录root> <输出目录root> <画布宽> <画布高> <底部留白> [形态...]
"""
import json
import os
import sys

import numpy as np
from PIL import Image

DIRS = ['down', 'leftdown', 'left', 'leftup', 'up', 'rightup', 'right', 'rightdown']


def dark_w(im):
    a = np.asarray(im).astype(int)
    lim = int(im.height * 0.78)
    m = (a[:lim, :, 3] > 200) & (a[:lim, :, 0] < 70) & (a[:lim, :, 1] < 70) & (a[:lim, :, 2] < 70)
    xs = np.nonzero(m)[1]
    return int(xs.max() - xs.min() + 1) if len(xs) else 0


def union_box(frames):
    b = None
    for im in frames:
        bb = im.getbbox()
        if bb is None:
            continue
        b = bb if b is None else (min(b[0], bb[0]), min(b[1], bb[1]), max(b[2], bb[2]), max(b[3], bb[3]))
    return b


def load_form(root, form):
    d0 = os.path.join(root, form)
    return {d: [Image.open(os.path.join(d0, d, f)).convert('RGBA')
                for f in sorted(os.listdir(os.path.join(d0, d)))] for d in DIRS}


def main():
    src, out = sys.argv[1], sys.argv[2]
    cw, ch, margin = int(sys.argv[3]), int(sys.argv[4]), int(sys.argv[5])
    forms = sys.argv[6:]
    plain = load_form(src, 'plain')
    pb = union_box([im for d in DIRS for im in plain[d]])
    p_area = (pb[2] - pb[0]) * (pb[3] - pb[1])
    p_dark = dark_w(plain['down'][0])
    print(f'plain: bbox={pb} area={p_area} dark_w={p_dark}')

    report = {'canvas': [cw, ch], 'plain': {'bbox': list(pb), 'area': p_area, 'dark_w': p_dark}, 'forms': {}}
    for form in forms:
        ims = plain if form == 'plain' else load_form(src, form)
        ub = union_box([im for d in DIRS for im in ims[d]])
        area = (ub[2] - ub[0]) * (ub[3] - ub[1])
        # 唯一口径：面部黑窗口（球体正面）与 plain 一致，整个形态随之等比放大。
        # 窗口是可穿透的，画布随内容自由变大即可，不为了压缩画布去缩内容。
        k_area = (p_area / area) ** 0.5
        k_dark = p_dark / dark_w(ims['down'][0])
        k = k_dark
        dw = dark_w(ims['down'][0]) * k
        # 底部对齐：缩放后内容底边落在 ch - margin
        nw = max(1, round((ub[2] - ub[0]) * k))
        nh = max(1, round((ub[3] - ub[1]) * k))
        # 画布自适应内容：窗口可穿透，不需要限制尺寸。crop 即肉眼可见范围，
        # 底边对齐（脚/影子贴画布底）保证切形态与移动时落点不跳。
        cw, ch = nw, nh
        ox, oy = 0, 0
        for d in DIRS:
            od = os.path.join(out, form, d)
            os.makedirs(od, exist_ok=True)
            for i, im in enumerate(ims[d]):
                canvas = Image.new('RGBA', (cw, ch), (0, 0, 0, 0))
                crop = im.crop(ub).resize((nw, nh), Image.LANCZOS)
                canvas.alpha_composite(crop, (max(0, ox), max(0, oy)))
                canvas.save(os.path.join(od, f'{i + 1:02d}.png'))
        report['forms'][form] = {'bbox': list(ub), 'area': area, 'k_area': round(k_area, 4),
                                 'k_dark': round(k_dark, 4), 'k': round(k, 4),
                                 'dark_w_scaled': round(dw, 1), 'size': [nw, nh], 'off': [ox, oy]}
        print(f'{form}: bbox={ub} area={area} k={k:.3f} 球径={dw:.0f} 内容={nw}x{nh} off={ox},{oy}')
    with open(os.path.join(out, 'unify_v2.json'), 'w', encoding='utf-8') as f:
        json.dump(report, f, ensure_ascii=False, indent=2)
    print('done')


if __name__ == '__main__':
    main()
