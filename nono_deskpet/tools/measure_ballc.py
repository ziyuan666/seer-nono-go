#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""measure_ballc.py —— 量出「本体球形中心」并生成统一锚点。

此前 `panel_shortcut.go` 用 `CurW/2` 和 `0.60*BodyH` 估球心，而球在画布里的
位置各形态差别很大（super5 球心在画布高度 28.8%，plain 在 47%），所以环心
永远对不准。

口径：
  1. 球心 x = down 方向首帧里「面部黑窗口」的水平中心（黑窗口是球体正面结构，
     不受耳朵/鳍长度影响，且左右对称）；
  2. 球心 y = 上述 x 所在列上「最长不透明段」的垂直中心（该段 = 外壳球体，
     底部地面阴影是单独一段，天然被排除）；
  3. 画布边距用「8 方向 × 全部帧」的并集 bbox 相对球心求，保证任何方向、
     任何一帧都不会被画布裁掉。

用法：
    python3 measure_ballc.py <assets根目录> [--write]

输出：
    - 控制台打印每形态 ballc / 边距；
    - --write 时写回 assets/<form>/meta.json 的 ballc 字段，
      并生成 assets/ball_anchor.json（统一锚点，单位=素材像素，zoom 见文件内字段）；
    - 始终生成核对图 /tmp/ballc_check.png（十字标球心）。
"""
import json
import os
import sys

import numpy as np
from PIL import Image, ImageDraw

FORMS = ['plain', 'super1', 'super2', 'super3', 'super4', 'super5']
DIRS = ['down', 'leftdown', 'left', 'leftup', 'up', 'rightup', 'right', 'rightdown']


def ball_center(path):
    """返回 (cx, cy)，素材像素，画布坐标。

    口径：面部黑窗口的 bbox 中心。理由：
      - 黑窗口就是球体正面那块结构，左右天然对称，不受长鳍/长耳/天线影响；
      - 跨形态可比：超能 1~5 级的黑窗口几乎铺满球面，中心即球面中心；
      - 比「中心列最长不透明段」稳：super5 头顶结构很厚，最长段会把球心
        拖到肚子上（实测差 35px），面部口径不会。
    上界只取画布高度的 78%，避免把底部的黑色身体算进黑窗口。
    """
    im = Image.open(path).convert('RGBA')
    a = np.asarray(im).astype(int)
    h, w = a.shape[:2]
    lim = int(h * 0.78)
    dark = (a[:lim, :, 3] > 200) & (a[:lim, :, 0] < 70) & (a[:lim, :, 1] < 70) & (a[:lim, :, 2] < 70)
    ys, xs = np.nonzero(dark)
    if len(xs) == 0:
        # 个别帧（眨眼/张嘴）可能出现整块黑窗口消失，调用方跳过该帧即可。
        return None
    # 去掉零星噪点：只保留面积最大的连通块（8 邻域）。
    from collections import deque
    seen = np.zeros_like(dark, dtype=bool)
    best = None
    H, W = dark.shape
    for y0, x0 in zip(ys, xs):
        if seen[y0, x0]:
            continue
        q = deque([(y0, x0)])
        seen[y0, x0] = True
        pts = []
        while q:
            y, x = q.popleft()
            pts.append((y, x))
            for dy in (-1, 0, 1):
                for dx in (-1, 0, 1):
                    ny, nx = y + dy, x + dx
                    if 0 <= ny < H and 0 <= nx < W and dark[ny, nx] and not seen[ny, nx]:
                        seen[ny, nx] = True
                        q.append((ny, nx))
        if best is None or len(pts) > len(best):
            best = pts
    bys = [p[0] for p in best]
    bxs = [p[1] for p in best]
    cx = int(round((min(bxs) + max(bxs)) / 2))
    cy = int(round((min(bys) + max(bys)) / 2))
    return cx, cy


def form_union_bbox(root, form):
    b = None
    for d in DIRS:
        dd = os.path.join(root, form, 'idle', d)
        if not os.path.isdir(dd):
            continue
        for f in sorted(os.listdir(dd)):
            if not f.endswith('.png'):
                continue
            bb = Image.open(os.path.join(dd, f)).convert('RGBA').getbbox()
            if bb is None:
                continue
            b = bb if b is None else (min(b[0], bb[0]), min(b[1], bb[1]),
                                      max(b[2], bb[2]), max(b[3], bb[3]))
    return b


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else 'assets'
    write = '--write' in sys.argv
    zooms, info = set(), {}
    max_l = max_r = max_u = max_d = 0
    tiles = []
    for form in FORMS:
        mp = os.path.join(root, form, 'meta.json')
        meta = json.load(open(mp, encoding='utf-8'))
        zoom = int(meta.get('zoom') or 4)
        zooms.add(zoom)
        p = os.path.join(root, form, 'idle', 'down', '01.png')
        # 17 帧待机动画里本体有原版呼吸浮动（实测 y 跨 7~20 素材px）。球心取
        # 全部帧的中位数，锚点落在浮动中心，动画上下对称，不会整体偏向一侧。
        dd = os.path.join(root, form, 'idle', 'down')
        cs = [ball_center(os.path.join(dd, f)) for f in sorted(os.listdir(dd)) if f.endswith('.png')]
        cs = [c for c in cs if c]
        if not cs:
            raise SystemExit(f'{form}: 17 帧都没有可测的球心')
        cxs = sorted(c[0] for c in cs)
        cys = sorted(c[1] for c in cs)
        cx, cy = int(round(cxs[len(cxs) // 2])), int(round(cys[len(cys) // 2]))
        print(f'  {form} 17帧球心 y 跨度 {min(cys)}..{max(cys)}，取中位 {cy}')
        bb = form_union_bbox(root, form)
        l, r = cx - bb[0], bb[2] - cx
        u, d = cy - bb[1], bb[3] - cy
        max_l, max_r = max(max_l, l), max(max_r, r)
        max_u, max_d = max(max_u, u), max(max_d, d)
        info[form] = {'ballc': [cx, cy], 'zoom': zoom, 'canvas': meta.get('canvas'), 'bbox': list(bb),
                      'margins': {'l': l, 'r': r, 'u': u, 'd': d}}
        print(f'{form}: ballc=({cx},{cy}) canvas={meta.get("canvas")} bbox={bb} '
              f'margins l/r/u/d={l}/{r}/{u}/{d}')
        im = Image.open(p).convert('RGBA')
        bg = Image.new('RGBA', im.size, (255, 255, 255, 255))
        bg.alpha_composite(im)
        dr = ImageDraw.Draw(bg)
        dr.line([(cx - 12, cy), (cx + 12, cy)], fill=(255, 0, 0, 255), width=2)
        dr.line([(cx, cy - 12), (cx, cy + 12)], fill=(255, 0, 0, 255), width=2)
        dr.text((2, 2), form, fill=(0, 120, 255, 255))
        tiles.append(bg)
    if len(zooms) != 1:
        print(f'⚠️ zoom 不一致: {zooms}（锚点按单一 zoom 换算，需要先统一）')
    zoom = zooms.pop() if len(zooms) == 1 else 4

    # 统一锚点（素材像素 / zoom -> 显示像素，scale=1）
    anchor_x = max_l
    anchor_y = max_u
    win_w = max_l + max_r
    win_h = max_u + max_d
    print(f'\nUNION: max margins l/r/u/d = {max_l}/{max_r}/{max_u}/{max_d}')
    print(f'ANCHOR(素材px)=(x{anchor_x},y{anchor_y}) WIN={win_w}x{win_h} zoom={zoom}')
    print(f'ANCHOR(显示px,scale=1)=({anchor_x/zoom:.2f},{anchor_y/zoom:.2f}) '
          f'WIN=({win_w/zoom:.2f}x{win_h/zoom:.2f})')

    if write:
        for form in FORMS:
            mp = os.path.join(root, form, 'meta.json')
            meta = json.load(open(mp, encoding='utf-8'))
            meta['ballc'] = info[form]['ballc']
            with open(mp, 'w', encoding='utf-8') as f:
                json.dump(meta, f, ensure_ascii=False, indent=2)
        anchor = {'zoom': zoom, 'anchor_x': anchor_x, 'anchor_y': anchor_y,
                  'win_w': win_w, 'win_h': win_h,
                  'margins': {'l': max_l, 'r': max_r, 'u': max_u, 'd': max_d}}
        ap = os.path.join(root, 'ball_anchor.json')
        with open(ap, 'w', encoding='utf-8') as f:
            json.dump(anchor, f, ensure_ascii=False, indent=2)
        print(f'written: {ap} + {len(FORMS)} meta.json')

    W = sum(t.width for t in tiles)
    H = max(t.height for t in tiles)
    out = Image.new('RGBA', (W, H), (255, 255, 255, 255))
    x = 0
    for t in tiles:
        out.alpha_composite(t, (x, 0))
        x += t.width
    out.save('/tmp/ballc_check.png')
    print('check image: /tmp/ballc_check.png')


if __name__ == '__main__':
    main()
