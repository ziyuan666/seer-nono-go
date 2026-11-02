#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""把已生成素材里的渐变半径从 819.2 改成 409.6。

背景：FFDec 导出的 SVG 把 Flash 渐变的单位圆写成 r=819.2（twips/10），但按
SWF 规范的 twips/20 → 像素换算应为 8192/20 = 409.6。写错 2 倍会让渐变覆盖范围
翻倍：以 shape_46（星芒）为例，r=819.2 时我们渲染出 45x43 本地像素，r=409.6 时
22x20，而 Ruffle（按规范实现的开源播放器）渲染同一 place 下是 25x23。

这个脚本只改 gradient.r / x1 / x2 这三个标量，不动几何、颜色、矩阵，
所以可以和重导等价，但不用重跑 2782 个 shape（重导还会覆盖 canvas.ballc）。
"""
import glob
import json
import os
import sys


def fix_file(path, dry_run=False):
    with open(path, encoding='utf-8') as f:
        d = json.load(f)
    changed = 0
    for it in d.get('paths', []):
        g = it.get('gradient')
        if not g:
            continue
        if abs(g.get('r', 0) - 819.2) < 1e-6:
            if not dry_run:
                g['r'] = 409.6
            changed += 1
        if abs(g.get('x1', 0) + 819.2) < 1e-6:
            if not dry_run:
                g['x1'] = -409.6
            changed += 1
        if abs(g.get('x2', 0) - 819.2) < 1e-6:
            if not dry_run:
                g['x2'] = 409.6
            changed += 1
    if changed and not dry_run:
        with open(path, 'w', encoding='utf-8') as f:
            json.dump(d, f, separators=(',', ':'), ensure_ascii=False)
    return changed


def main():
    dry = '--dry' in sys.argv
    roots = [r for r in sys.argv[1:] if not r.startswith('--')]
    total_files = 0
    total_vals = 0
    for root in roots:
        fs = glob.glob(os.path.join(root, '*', '*', 'parts', '*.json'))
        nf = 0
        nv = 0
        for p in fs:
            c = fix_file(p, dry)
            if c:
                nf += 1
                nv += c
        print('  %-45s 文件 %d 个，改 %d 条渐变' % (root, nf, nv))
        total_files += nf
        total_vals += nv
    print('合计：%d 个文件，%d 条渐变%s' % (total_files, total_vals, '（dry-run 未写入）' if dry else ''))


if __name__ == '__main__':
    main()
