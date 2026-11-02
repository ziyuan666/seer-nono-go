#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""批量把「我们渲染的每一帧」和「FFDec 金标准 sprite 帧」逐帧比一遍。

为什么要批量：单个动画看着没问题，不代表 57 个都对齐；曾经出现过
「渐变半径被全局减半」这种一件事影响多个动画的 bug，只有全量对比能抓到。

金标准走 gold_sprite.py（FFDec sprite 导出，自己的 frame bounds，不裁切）；
对比口径与 cmp_anim.py 完全一致（alpha 掩码搜偏移 + 背景 51 灰合成后算差）。

用法：
    python3 gold_validate.py [--only super5/action_7] [--limit 5] [--redo]
                            [--gold-dir /tmp/gold_all] [--report docs/渲染对齐报告.md]
输出：每个动画一行；有 --report 时写 markdown 表。
"""
import argparse
import os
import subprocess
import sys

import numpy as np

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
from cmp_anim import bb, best_shift, load, on_bg, place, align_min_diff  # noqa: E402

VEC_ROOT = os.path.dirname(os.path.dirname(HERE))
SRC = os.path.join(VEC_ROOT, 'source_swf', '资源', 'nono')
FORMS = ['super1', 'super2', 'super3', 'super4', 'super5']
ST = os.environ.get('SPRITETEST') or '/tmp/st'


def jobs():
    out = []
    for n in (1, 2):
        p = os.path.join(SRC, 'action', '%d.swf' % n)
        if os.path.exists(p):
            out.append((p, 'plain', 'action_%d' % n))
    for n in (1, 2, 3, 4):
        p = os.path.join(SRC, 'exp', '%d.swf' % n)
        if os.path.exists(p):
            out.append((p, 'plain', 'exp_%d' % n))
    for k, form in enumerate(FORMS, start=1):
        for n in range(1, 8):
            p = os.path.join(SRC, 'super', 'action', '%d_%d.swf' % (n, k))
            if os.path.exists(p):
                out.append((p, form, 'action_%d' % n))
        for n in range(1, 5):
            p = os.path.join(SRC, 'super', 'exp', '%d_%d.swf' % (n, k))
            if os.path.exists(p):
                out.append((p, form, 'exp_%d' % n))
    return out


def gold_for(swf, gold_dir, redo=False):
    if not redo and os.path.isdir(gold_dir):
        n = len([f for f in os.listdir(gold_dir) if f.endswith('.png')])
        if n:
            return n
    r = subprocess.run([sys.executable, os.path.join(HERE, 'gold_sprite.py'), swf, gold_dir],
                       capture_output=True, text=True)
    if r.returncode != 0:
        print('  金标准失败:', r.stdout[-200:], r.stderr[-200:])
        return 0
    return len([f for f in os.listdir(gold_dir) if f.endswith('.png')])


def compare(sprite_dir, gold_dir, name='default', thr=40):
    golds = {}
    for f in os.listdir(gold_dir):
        if f.endswith('.png') and f[:-4].isdigit():
            golds[int(f[:-4])] = os.path.join(gold_dir, f)
    mean = []
    worst = []
    tmp = '/tmp/_gv.png'
    for i in sorted(golds):
        r = subprocess.run([ST, '-dir', sprite_dir, '-name', name, '-idx', str(i - 1),
                            '-scale', '1', '-out', tmp], capture_output=True, text=True)
        if r.returncode != 0:
            continue
        g = load(golds[i])
        o = load(tmp)
        ga, oa = g[:, :, 3] > thr, o[:, :, 3] > thr
        if not ga.any() or not oa.any():
            continue
        # 对齐：粗定位给两个候选（全图 bbox、实心区 bbox；光晕会撑大全图 bbox，
        # 实心区在单薄帧上又会被高光带偏，两者都不可单独信），各自 ±3px 找
        # 平均色差最小的平移，再取其中更优的。理由详见 align_min_diff 注释。
        gb, ob = bb(ga), bb(oa)
        cands = [(gb[0] - ob[0], gb[1] - ob[1])]
        gac, oac = g[:, :, 3] > 200, o[:, :, 3] > 200
        if gac.sum() >= 60 and oac.sum() >= 60:
            gcb, ocb = bb(gac), bb(oac)
            cands.append((gcb[0] - ocb[0], gcb[1] - ocb[1]))
        dx, dy, _ = align_min_diff(g, o, cands, r=3)
        d = np.abs(on_bg(g) - on_bg(place(o, dx, dy, g.shape[:2]))).sum(axis=2)
        m = float(d.mean())
        mean.append(m)
        worst.append((m, i))
    worst.sort(reverse=True)
    return mean, worst


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--only', default='')
    ap.add_argument('--limit', type=int, default=0)
    ap.add_argument('--redo', action='store_true')
    ap.add_argument('--gold-dir', default='/tmp/gold_all')
    ap.add_argument('--report', default='')
    ap.add_argument('--sprites-root', default='',
                    help='指定素材根目录，默认优先运行时副本')
    args = ap.parse_args()

    js = jobs()
    if args.only:
        js = [j for j in js if '%s/%s' % (j[1], j[2]) == args.only]
    if args.limit:
        js = js[:args.limit]
    rows = []
    for swf, form, anim in js:
        key = '%s/%s' % (form, anim)
        # 优先校验运行时真正读的那份（nono_deskpet/assets/sprites），
        # 再退回生成器输出（nono_deskpet_vec/assets/sprites）。
        if args.sprites_root:
            sprite_dir = os.path.join(args.sprites_root, key)
        else:
            sprite_dir = os.path.join(os.path.dirname(VEC_ROOT), 'nono_deskpet',
                                      'assets', 'sprites', key)
            if not os.path.isdir(sprite_dir):
                sprite_dir = os.path.join(VEC_ROOT, 'assets', 'sprites', key)
        if not os.path.isdir(sprite_dir):
            print('%-18s 缺素材目录' % key)
            continue
        gd = os.path.join(args.gold_dir, '%s_%s' % (form, anim))
        n = gold_for(swf, gd, args.redo)
        if not n:
            print('%-18s 金标准 0 帧' % key)
            continue
        mean, worst = compare(sprite_dir, gd)
        if not mean:
            print('%-18s 无可比帧' % key)
            continue
        s = sorted(mean)
        med = s[len(s) // 2]
        p95 = s[int(len(s) * 0.95) - 1]
        line = '%-18s 帧=%3d 中位=%6.2f p95=%6.2f 最大=%6.2f@%d' % (
            key, len(s), med, p95, worst[0][0], worst[0][1])
        print(line)
        rows.append((key, len(s), med, p95, worst[0][0], worst[0][1]))

    if args.report and rows:
        with open(args.report, 'w', encoding='utf-8') as f:
            f.write('| 动画 | 帧数 | 中位差 | p95 | 最大差 | 最大帧 |\n')
            f.write('|---|---:|---:|---:|---:|---:|\n')
            for r in rows:
                f.write('| %s | %d | %.2f | %.2f | %.2f | %d |\n'
                        % (r[0], r[1], r[2], r[3], r[4], r[5]))
        print('报告写入', args.report)
    return 0


if __name__ == '__main__':
    sys.exit(main())
