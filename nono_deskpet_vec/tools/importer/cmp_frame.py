#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""单帧对比图：金标准 | 我们的渲染 | 差异热力图（三联）。

用于「报告里某帧差得特别大」时肉眼定位。偏移默认 (-2,-2)（我们的画布比
FFDec frame bounds 多一圈 pad），也可显式传 --dx/--dy。

用法：
    python3 cmp_frame.py --sprite /tmp/sp_new2/super3/exp_4 --gold /tmp/gold_all/super3_exp_4 \
        --frame 23 --out /tmp/x.png [--dx -2 --dy -2] [--zoom 4]
"""
import argparse
import os
import subprocess
import sys

import numpy as np
from PIL import Image

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
from cmp_anim import best_shift, bb, load, on_bg, place, align_min_diff  # noqa: E402

ST = os.environ.get('SPRITETEST') or '/tmp/st'


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--sprite', required=True)
    ap.add_argument('--gold', required=True)
    ap.add_argument('--frame', type=int, required=True, help='1 起的金标准帧号')
    ap.add_argument('--name', default='default')
    ap.add_argument('--out', default='/tmp/cmp_frame.png')
    ap.add_argument('--dx', type=int, default=None)
    ap.add_argument('--dy', type=int, default=None)
    ap.add_argument('--zoom', type=int, default=0)
    args = ap.parse_args()

    gp = os.path.join(args.gold, '%d.png' % args.frame)
    g = load(gp)
    op = '/tmp/_cf.png'
    r = subprocess.run([ST, '-dir', args.sprite, '-name', args.name, '-idx',
                        str(args.frame - 1), '-scale', '1', '-out', op],
                       capture_output=True, text=True)
    if r.returncode != 0:
        raise SystemExit('渲染失败: %s' % r.stderr[-300:])
    o = load(op)
    ga, oa = g[:, :, 3] > 40, o[:, :, 3] > 40
    if args.dx is None or args.dy is None:
        gb, ob = bb(ga), bb(oa)
        dx, dy, iou = best_shift(ga, oa, gb[0] - ob[0], gb[1] - ob[1])
        # 与 gold_validate 同口径：全图 bbox 与实心区 bbox 各给一个候选，
        # 再在 ±3px 内取平均色差最小的平移（光晕会撑大全图 bbox；实心区在
        # 单薄帧上会被高光带偏，两者都不能单独信）。
        cands = [(gb[0] - ob[0], gb[1] - ob[1]), (dx, dy)]
        gac, oac = g[:, :, 3] > 200, o[:, :, 3] > 200
        if gac.sum() >= 60 and oac.sum() >= 60:
            gcb, ocb = bb(gac), bb(oac)
            cands.append((gcb[0] - ocb[0], gcb[1] - ocb[1]))
        dx, dy, _ = align_min_diff(g, o, cands, r=3)
        print('自动对齐 offset=(%d,%d) IoU=%.3f' % (dx, dy, iou))
    else:
        dx, dy = args.dx, args.dy
    gg = on_bg(g)
    oo = on_bg(place(o, dx, dy, g.shape[:2]))
    d = np.abs(gg - oo).sum(axis=2)
    hm = np.clip(d, 0, 255).astype(np.uint8)
    hm3 = np.stack([hm] * 3, axis=2)
    out = np.concatenate([gg, oo, hm3], axis=1).astype(np.uint8)
    im = Image.fromarray(out)
    z = args.zoom or max(1, min(4, 1500 // out.shape[1]))
    im.resize((out.shape[1] * z, out.shape[0] * z), Image.NEAREST).save(args.out)
    print('平均差 %.2f  >60 占比 %.4f  尺寸 %s  zoom %d  -> %s'
          % (float(d.mean()), float((d > 60).mean()), g.shape[:2], z, args.out))


if __name__ == '__main__':
    main()
