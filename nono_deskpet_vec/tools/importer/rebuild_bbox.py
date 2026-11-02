#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""rebuild_bbox.py —— 按 parts 实际几何重算每帧 bbox。

背景（2026-10-26，代号 20261026-01）：
    运行时素材的部分 frame.bbox 与同文件的 parts 不一致。
    例：super5/action_7 第 205 帧 bbox 报 [36.558, 53.18, 900.015, 858.817]，
    而 shape_144（缩放 4.511 的大光环）实际几何是 x[-39.1, 611.3]
    y[-63.7, 586.8]。RenderBbox 按 bbox 开窗，于是左侧光环被整段切掉。
    当前 build_sprite 重建同动作得到的是 [2, 2, 650, 650]，可见运行时这份
    是旧管线产物，改了画布口径但没重算 bbox。

做法：
    对每个 sprite.json 的每个方向、每一帧，取其 parts 里所有 alpha>0 的部件，
    用 part.matrix 变换 path 顶点，得到该帧真实包围盒，写回 frame.bbox。
    不碰矩阵、层、alpha、画布与 bodyc/ballc。

用法：
    python3 rebuild_bbox.py --dry-run   # 只统计变化
    python3 rebuild_bbox.py             # 写回
"""
import argparse
import json
import os
import re

HERE = os.path.dirname(os.path.abspath(__file__))
RUNTIME = os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(HERE))),
                       'nono_deskpet', 'assets', 'sprites')
NUM = re.compile(r'-?\d+\.?\d*')


def load_lib(d):
    lib = {}
    pdir = os.path.join(d, 'parts')
    if not os.path.isdir(pdir):
        return lib
    for fn in os.listdir(pdir):
        if fn.endswith('.json'):
            lib[fn[:-5]] = json.load(open(os.path.join(pdir, fn), encoding='utf-8'))
    return lib


def part_xy(pf, m):
    """返回该部件在画布坐标下的 (xs, ys)。"""
    xs, ys = [], []
    for pd in pf.get('paths', []):
        nums = [float(v) for v in NUM.findall(pd.get('d', ''))]
        for x, y in zip(nums[0::2], nums[1::2]):
            xs.append(m[0] * x + m[2] * y + m[4])
            ys.append(m[1] * x + m[3] * y + m[5])
    return xs, ys


def frame_bbox(lib, parts):
    xs, ys = [], []
    for p in parts:
        if p.get('alpha', 1.0) <= 0:
            continue
        pf = lib.get(p['part'])
        if not pf:
            continue
        m = p.get('matrix') or [1, 0, 0, 1, 0, 0]
        px, py = part_xy(pf, m)
        xs += px
        ys += py
    if not xs:
        return None
    return [round(min(xs), 3), round(min(ys), 3),
            round(max(xs) - min(xs), 3), round(max(ys) - min(ys), 3)]


def process(path, dry):
    sj = json.load(open(path, encoding='utf-8'))
    lib = load_lib(os.path.dirname(path))
    if not lib:
        return None
    changed = 0
    worse = 0
    grew = 0
    for dname, dv in (sj.get('directions') or {}).items():
        for fr in dv.get('frames', []):
            nb = frame_bbox(lib, fr.get('parts', []))
            if nb is None:
                continue
            ob = fr.get('bbox')
            if ob != nb:
                changed += 1
                # 旧 bbox 比真实内容小的帧 = 会被裁
                if ob and len(ob) == 4:
                    if ob[0] > nb[0] + 0.5 or ob[1] > nb[1] + 0.5:
                        worse += 1
                    if ob[2] < nb[2] - 0.5 or ob[3] < nb[3] - 0.5:
                        worse += 1
                    if nb[2] > ob[2] or nb[3] > ob[3]:
                        grew += 1
                fr['bbox'] = nb
    if changed and not dry:
        with open(path, 'w', encoding='utf-8') as f:
            json.dump(sj, f, ensure_ascii=False, separators=(',', ':'))
    return changed, worse, grew


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--dry-run', action='store_true')
    ap.add_argument('--only', default='')
    args = ap.parse_args()
    tot = w = g = 0
    files = 0
    for form in sorted(os.listdir(RUNTIME)):
        fd = os.path.join(RUNTIME, form)
        if not os.path.isdir(fd):
            continue
        for anim in sorted(os.listdir(fd)):
            rel = f'{form}/{anim}'
            if args.only and rel != args.only:
                continue
            p = os.path.join(fd, anim, 'sprite.json')
            if not os.path.exists(p):
                continue
            r = process(p, args.dry_run)
            if r is None:
                continue
            c, ww, gg = r
            if c:
                files += 1
                tot += c
                w += ww
                g += gg
                if args.only or c > 50:
                    print(f'  {rel}: {c} 帧重算（旧值偏小 {ww}，尺寸变大 {gg}）')
    print(f'共 {files} 个文件、{tot} 帧重算；其中旧 bbox 漏内容 {w} 处、bbox 变大 {g} 处'
          f'{"（dry-run 未写盘）" if args.dry_run else ""}')


if __name__ == '__main__':
    main()
