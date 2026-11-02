#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""对比重导前后每个动画的 canvas（w/h/anchor）差异。

为什么关心：anchor 是运行时把精灵钉到舞台位置的参考点，重导若改变 anchor，
拿着旧存档的用户会看到整体位移，所以必须逐项核对差异幅度。
"""
import glob
import json
import os
import sys


def load(root):
    out = {}
    for p in glob.glob(os.path.join(root, '*', '*', 'sprite.json')):
        rel = '/'.join(p.split(os.sep)[-3:-1])
        with open(p, encoding='utf-8') as f:
            out[rel] = json.load(f).get('canvas') or {}
    return out


def main():
    old = load(sys.argv[1])
    new = load(sys.argv[2])
    common = sorted(set(old) & set(new))
    print('对比 %d 个动画（旧 %d / 新 %d）' % (len(common), len(old), len(new)))
    big = []
    for k in common:
        a, b = old[k], new[k]
        dw = b.get('w', 0) - a.get('w', 0)
        dh = b.get('h', 0) - a.get('h', 0)
        dan = max(abs(x - y) for x, y in zip(b.get('anchor', [0, 0]), a.get('anchor', [0, 0])))
        if abs(dw) > 0.01 or abs(dh) > 0.01 or dan > 0.01:
            big.append((dan, abs(dw) + abs(dh), k, dw, dh, a.get('anchor'), b.get('anchor')))
    big.sort(reverse=True)
    print('有变化的 %d 个，按 anchor 位移排序 top12：' % len(big))
    for dan, ds, k, dw, dh, oa, na in big[:12]:
        print('  %-22s Δanchor=%.2f  Δw=%+.2f Δh=%+.2f  %s -> %s'
              % (k, dan, dw, dh,
                 [round(v, 1) for v in oa], [round(v, 1) for v in na]))
    if big:
        vals = sorted(x[0] for x in big)
        print('  Δanchor 中位数 %.2f  最大 %.2f' % (vals[len(vals) // 2], vals[-1]))


if __name__ == '__main__':
    main()
