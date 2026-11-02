#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""unify_forms.py —— 以本体的「面部黑窗口」宽度为基准，把 5 个超能形态缩放到
和普通形态同一个球体尺寸，避免切换形态时整体忽大忽小。

度量说明：面部黑窗口（低亮度区域）在每个形态里都是球体正面的同一块结构，宽度
与球径成正比，比整体外框稳定（整体外框会被耳朵、鳍、尾翼拉长）。基准取普通
形态（plain）down 帧的窗口宽度。

做法：先按 8 方向内容的并集把画布收紧（去掉大块空白，super4 原来 401 宽里
只有 149 宽有内容），再整体乘缩放系数；位置与画布同比变化，落点不偏。

用法：
    python3 unify_forms.py <源目录root> <输出目录root> <形态...>
    例：unify_forms.py /tmp/final /tmp/unified super1 super2 super3 super4 super5
"""
import json
import os
import sys

import numpy as np
from PIL import Image

DIRS = ['down', 'leftdown', 'left', 'leftup', 'up', 'rightup', 'right', 'rightdown']


def dark_w(im):
    """面部黑窗口宽度：alpha 实心且 RGB 都很暗的像素，取 y < 0.78H 避开底部阴影。"""
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


def main():
    src_root, out_root = sys.argv[1], sys.argv[2]
    forms = sys.argv[3:]
    base = dark_w(Image.open(os.path.join(src_root, 'plain', 'down', '01.png')).convert('RGBA'))
    print(f'基准窗口宽度(plain) = {base}px')
    report = {}
    for form in forms:
        d0 = os.path.join(src_root, form)
        ims = {d: [Image.open(os.path.join(d0, d, f)).convert('RGBA')
                   for f in sorted(os.listdir(os.path.join(d0, d)))] for d in DIRS}
        w = dark_w(ims['down'][0])
        k = base / w
        ub = union_box([im for d in DIRS for im in ims[d]])
        cw, ch = ub[2] - ub[0], ub[3] - ub[1]
        nw, nh = max(1, round(cw * k)), max(1, round(ch * k))
        for d in DIRS:
            od = os.path.join(out_root, form, d)
            os.makedirs(od, exist_ok=True)
            for i, im in enumerate(ims[d]):
                crop = im.crop(ub).resize((nw, nh), Image.LANCZOS)
                crop.save(os.path.join(od, f'{i + 1:02d}.png'))
        report[form] = {'dark_w': w, 'k': round(k, 4), 'canvas': [nw, nh], 'crop': list(ub)}
        print(f'{form}: 窗口={w}px k={k:.3f} crop={ub} -> canvas {nw}x{nh}')
    with open(os.path.join(out_root, 'unify.json'), 'w', encoding='utf-8') as f:
        json.dump(report, f, ensure_ascii=False, indent=2)
    print('done')


if __name__ == '__main__':
    main()
