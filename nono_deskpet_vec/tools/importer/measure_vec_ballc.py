#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""量矢量素材的本体球心，写回 sprite.json 的 canvas.ballc。

为什么需要：`build_sprite.py` 写的 canvas.anchor 是 bbox 中心（画布原点偏移），
不是球心。矢量素材里两者能差 26px——plain 的 anchor y=60.46 落在影子附近，
真球心在 34.5。用错值会让本体整体画偏、快捷栏环心落到地面阴影上。

口径与 `nono_deskpet/tools/measure_ballc.py` 一致：
  球心 = down 方向「面部黑窗口」的连通域 bbox 中心，17 帧取中位数。

用法：
    python3 tools/importer/measure_vec_ballc.py [--sprites ../assets/sprites]
                                                [--vecgen ../build/vecgen]
"""
import argparse
import json
import os
import subprocess
import sys
from collections import deque

import numpy as np
from PIL import Image

FORMS = ['plain', 'super1', 'super2', 'super3', 'super4', 'super5', 'annual']
TMP_ROOT = '/tmp/vecballc'


def ball_center(path):
    """面部黑窗口的连通域 bbox 中心（画布像素坐标）。无黑窗口返回 None。"""
    a = np.asarray(Image.open(path).convert('RGBA')).astype(int)
    h, w = a.shape[:2]
    lim = int(h * 0.78)  # 上界 78%：不含底部黑色身体
    dark = (a[:lim, :, 3] > 200) & (a[:lim, :, 0] < 70) & (a[:lim, :, 1] < 70) & (a[:lim, :, 2] < 70)
    ys, xs = np.nonzero(dark)
    if len(xs) == 0:
        return None
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
    return (min(bxs) + max(bxs)) / 2.0, (min(bys) + max(bys)) / 2.0


def main():
    ap = argparse.ArgumentParser()
    # 默认路径按脚本位置解析，不依赖调用时的 cwd（从项目根或 tools/importer 跑都一样）
    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    ap.add_argument('--sprites', default=os.path.join(root, 'assets', 'sprites'))
    ap.add_argument('--vecgen', default=os.path.join(root, 'build', 'vecgen'))
    args = ap.parse_args()

    vecgen = os.path.abspath(args.vecgen)
    if not os.path.exists(vecgen):
        raise SystemExit(f'找不到 {vecgen}，先 go build -o build/vecgen ./cmd/vecgen')

    for form in FORMS:
        sj = os.path.join(args.sprites, form, 'sprite.json')
        if not os.path.exists(sj):
            print(f'{form}: 缺 sprite.json，跳过')
            continue
        d = json.load(open(sj, encoding='utf-8'))
        n = len(d['directions']['down']['frames'])
        outdir = os.path.join(TMP_ROOT, form)
        os.makedirs(outdir, exist_ok=True)
        cs = []
        frames = d['directions']['down']['frames']
        for i in range(n):
            png = os.path.join(outdir, '%02d.png' % i)
            r = subprocess.run([vecgen, 'render', '-dir', 'down', '-frame', str(i),
                                '-scale', '1', '-ss', '3', '-out', png, sj],
                               capture_output=True, text=True)
            if r.returncode != 0:
                continue
            c = ball_center(png)
            if c:
                # vecgen 只按「帧 bbox」裁剪输出，没有 -full 开关；
                # 而 canvas.ballc 要的是**画布坐标**。两套坐标差一个 bbox 左上角，
                # 不补偿的话球心会整体偏移（重导后帧 0 的 bbox 从 ~0 变成 8.3,2.0，
                # plain 的球心就被量成 18.5 而不是 26.8）。
                bb = frames[i].get('bbox') or [0.0, 0.0, 0.0, 0.0]
                cs.append((c[0] + bb[0], c[1] + bb[1]))
        if not cs:
            print(f'{form}: {n} 帧都没有可测的球心，跳过')
            continue
        cxs = sorted(c[0] for c in cs)
        cys = sorted(c[1] for c in cs)
        cx = round(cxs[len(cxs) // 2], 3)
        cy = round(cys[len(cys) // 2], 3)
        old = d['canvas'].get('anchor')
        d['canvas']['ballc'] = [cx, cy]
        json.dump(d, open(sj, 'w', encoding='utf-8'), ensure_ascii=False, separators=(',', ':'))
        print(f'{form}: {n} 帧测出 {len(cs)} 个，球心=({cx},{cy})  原 anchor={old}  '
              f'偏差=({cx - old[0]:+.1f},{cy - old[1]:+.1f})')


if __name__ == '__main__':
    main()
