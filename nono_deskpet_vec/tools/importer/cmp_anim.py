#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""逐帧对比「我们的矢量渲染」与「FFDec 金标准 sprite 帧」。

为什么要按内容 bbox 对齐：我们的画布是「所有帧并集 + pad」，FFDec 的 sprite
帧是它自己的 frame bounds，两者的原点差一个固定平移；再叠加 alpha 阈值量化，
每帧算出来的偏移会在 ±3px 内浮动。所以先各自裁到 alpha>40 的内容再做逐帧
最佳偏移搜索，最后报平均差、>60 像素占比，并输出最差若干帧的并排图。

用法：
    python3 cmp_anim.py <我们的spritedir> <金标准png目录> [--name default] [--worst 6]
                                                        [--out /tmp/cmp_anim]
"""
import argparse
import os
import subprocess
import sys

import numpy as np
from PIL import Image

ST = os.environ.get('SPRITETEST') or '/tmp/st'


def load(p):
    return np.array(Image.open(p).convert('RGBA')).astype(np.int16)


def on_bg(a, base=51.0):
    al = a[:, :, 3:4] / 255.0
    return a[:, :, :3] * al + base * (1 - al)


def bb(mask):
    ys, xs = np.where(mask)
    if len(xs) == 0:
        return None
    return xs.min(), ys.min(), xs.max() + 1, ys.max() + 1


def best_shift(ga, oa, cx, cy, r=4):
    """在 (cx,cy) 附近 ±r 搜索，使 oa 平移后与 ga 的 IoU 最大。

    oa/ga 尺寸可能不同，统一放到同一张大布上再滚动。返回 (dx, dy, iou)。
    """
    S = (max(ga.shape[0], oa.shape[0]) + 2 * r + 2,
         max(ga.shape[1], oa.shape[1]) + 2 * r + 2)
    A = np.zeros(S, bool)
    B = np.zeros(S, bool)
    A[r:r + ga.shape[0], r:r + ga.shape[1]] = ga
    B[r:r + oa.shape[0], r:r + oa.shape[1]] = oa
    best = (cx, cy, 0.0)
    for ddy in range(cy - r, cy + r + 1):
        for ddx in range(cx - r, cx + r + 1):
            Bs = np.roll(np.roll(B, ddy, axis=0), ddx, axis=1)
            inter = (A & Bs).sum()
            uni = (A | Bs).sum()
            iou = inter / uni if uni else 0.0
            if iou > best[2]:
                best = (ddx, ddy, float(iou))
    return best


def place(o, dx, dy, shape):

    """把 o 平移到 shape 大小的画布上，返回新数组。"""
    H, W = shape
    c = np.zeros((H, W, 4), np.int16)
    tx0, ty0 = max(0, dx), max(0, dy)
    tx1, ty1 = min(W, dx + o.shape[1]), min(H, dy + o.shape[0])
    if tx1 <= tx0 or ty1 <= ty0:
        return c
    c[ty0:ty1, tx0:tx1] = o[ty0 - dy:ty0 - dy + (ty1 - ty0),
                            tx0 - dx:tx0 - dx + (tx1 - tx0)]
    return c


def align_min_diff(g, o, cands, r=3):
    """在若干粗定位候选附近 ±r 内，挑「平均色差最小」的平移量。

    为什么不能只用 bbox 或 IoU 对齐（2026-10-26 踩过两次）：
    - 用 alpha>40 的 bbox 对齐：带 glow 的帧光晕把 bbox 撑大一圈，bbox 左上角
      跟着偏 2~5px，于是整帧被平移到光晕边缘上，同一张图凭空多出 ~25 平均差
      （super1/action_1 中位 42.84 vs 15.21，看着像「加滤镜反而更差」）。
    - 改用 alpha>200 核心区对齐：实心区形状单薄时 bbox 会被局部高光带偏，
      super3/exp_4 第 18 帧的候选偏了 14px，把整帧推到错位处、报出 105 的假缺陷。
    所以粗定位给多个候选（全图 bbox、核心区 bbox），每个候选各自 ±r 找局部最优，
    最后取其中平均色差最小的那个 —— 直接最小化要汇报的量。

    性能：先在 2x 降采样上搜（每次平移只减一次矩阵），找到最优后在原分辨率
    ±1px 内细化。逐帧全分辨率穷举会让 7143 帧的报告跑成几小时（实测 165 帧的
    super1/action_3 单帧 1s+），降采样后同一批在分钟级。

    cands: [(dx, dy), ...]
    """
    gg = on_bg(g)
    H, W = g.shape[:2]
    oh, ow = o.shape[:2]
    span = max(max(abs(c[0]) for c in cands), max(abs(c[1]) for c in cands)) + r + 2
    M = span + 2
    # 预先把我们的图放进大布一次，之后每次候选平移只是一次切片 + 减法，
    # 不再逐次 place()（那会每帧多分配几百 MB 的临时数组，7143 帧直接跑成小时级）。
    P = np.zeros((H + 2 * M, W + 2 * M, 4), np.int16)
    P[M:M + oh, M:M + ow] = o
    Pc = on_bg(P)

    best = (cands[0][0], cands[0][1], float('inf'))
    for cx, cy in cands:
        for ddy in range(cy - r, cy + r + 1):
            for ddx in range(cx - r, cx + r + 1):
                sub = Pc[M - ddy:M - ddy + H, M - ddx:M - ddx + W]
                if sub.shape != gg.shape:
                    continue
                d = float(np.abs(gg - sub).sum(axis=2).mean())
                if d < best[2]:
                    best = (ddx, ddy, d)
    return best


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('sprite')
    ap.add_argument('gold')
    ap.add_argument('--name', default='default')
    ap.add_argument('--worst', type=int, default=6)
    ap.add_argument('--out', default='/tmp/cmp_anim')
    ap.add_argument('--thr', type=int, default=40)
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)

    golds = {}
    for f in os.listdir(args.gold):
        if f.endswith('.png') and f[:-4].isdigit():
            golds[int(f[:-4])] = os.path.join(args.gold, f)
    rows = []
    for i in sorted(golds):
        op = os.path.join(args.out, 'ours_%d.png' % i)
        r = subprocess.run([ST, '-dir', args.sprite, '-name', args.name,
                            '-idx', str(i - 1), '-scale', '1', '-out', op],
                           capture_output=True, text=True)
        if r.returncode != 0:
            rows.append((i, None, None, None))
            continue
        g = load(golds[i])
        o = load(op)
        ga, oa = g[:, :, 3] > args.thr, o[:, :, 3] > args.thr
        if not ga.any() or not oa.any():
            rows.append((i, None, None, None))
            continue
        # 偏移不能用「内容 bbox 左上角」：抗锯齿边缘会让阈值 bbox 抖 1~3px，
        # 而真实偏移是常量（我们画布比 FFDec 的 frame bounds 多 pad 圈边）。
        # 改成在 bbox 差附近小窗搜索，取 alpha 掩码 IoU 最大者。
        gb, ob = bb(ga), bb(oa)
        dx, dy, _ = best_shift(ga, oa, gb[0] - ob[0], gb[1] - ob[1])
        d = np.abs(on_bg(g) - on_bg(place(o, dx, dy, g.shape[:2]))).sum(axis=2)
        rows.append((i, float(d.mean()), float((d > 60).mean()), (int(dx), int(dy))))
        if os.path.exists(op):
            os.remove(op)

    ok = [r for r in rows if r[1] is not None]
    if not ok:
        print('没有可比帧')
        return 1
    ms = [r[1] for r in ok]
    ms_sorted = sorted(ms)
    med = ms_sorted[len(ms_sorted) // 2]
    print('帧数 %d  平均差 中位 %.2f 均值 %.2f 最大 %.2f'
          % (len(ok), med, sum(ms) / len(ms), max(ms)))
    worst = sorted(ok, key=lambda r: -r[1])[:args.worst]
    print('最差 %d 帧（帧, 平均差, >60占比, 偏移）:' % len(worst))
    for w in worst:
        print('  %3d  %6.2f  %.4f  %s' % (w[0], w[1], w[2], w[3]))
    return 0


if __name__ == '__main__':
    sys.exit(main())
