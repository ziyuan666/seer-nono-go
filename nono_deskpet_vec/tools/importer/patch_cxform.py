#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""patch_cxform.py —— 把 SWF cxform 的 RGB 部分注入运行时素材（不改画布/几何）。

背景（2026-10-26，代号 20261026-01）：
    早期的 build_sprite.expand 只累积了 cxform 的 alpha（mul[3]），
    把 RGB 的 mul/add 整段丢弃。结果 super5/action_7 第 205 帧的
    黄色光球被画成青色——因为 place 上的 `mul=0.2617, add=[0,189,189]`
    完全没生效。全库 2379/5749 个 place 受影响。

为什么不用重新构建整包素材：
    运行时素材 `nono_deskpet/assets/sprites` 是历史构建产物
    （画布口径与当前 build_sprite 不同，例如 super5/action_7 是
    938.6x914.0 而当前构建出 654.5x654.5），重建会把已验证的
    画布/bodyc/ballc 全部冲掉。而 cxform 只依赖 SWF 结构，
    与画布无关，所以按 (帧号, part 序号) 原地补齐即可。
    已实测两份素材逐帧 parts 数量与 part 名 100% 一致。

用法：
    python3 patch_cxform.py --dry-run   # 只统计，不写
    python3 patch_cxform.py             # 写回
"""
import argparse
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from swfread import SWF                      # noqa: E402
from build_sprite import expand, IDENT       # noqa: E402
from build_anims import find_root, SRC, FORMS  # noqa: E402

RUNTIME = os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(HERE))),
                       'nono_deskpet', 'assets', 'sprites')


def jobs():
    """(swf 路径, form/anim 目录名)，与 build_anims 的命名保持一致。"""
    out = []
    for n in (1, 2):
        p = os.path.join(SRC, 'action', f'{n}.swf')
        if os.path.exists(p):
            out.append((p, f'plain/action_{n}'))
    for n in (1, 2, 3, 4):
        p = os.path.join(SRC, 'exp', f'{n}.swf')
        if os.path.exists(p):
            out.append((p, f'plain/exp_{n}'))
    for k, form in enumerate(FORMS, start=1):
        for n in range(1, 8):
            p = os.path.join(SRC, 'super', 'action', f'{n}_{k}.swf')
            if os.path.exists(p):
                out.append((p, f'{form}/action_{n}'))
        for n in range(1, 5):
            p = os.path.join(SRC, 'super', 'exp', f'{n}_{k}.swf')
            if os.path.exists(p):
                out.append((p, f'{form}/exp_{n}'))
    return out


def patch_one(swf_path, rel, dry):
    sj_path = os.path.join(RUNTIME, rel, 'sprite.json')
    if not os.path.exists(sj_path):
        return None, 'no-sprite'
    sj = json.load(open(sj_path, encoding='utf-8'))
    dirs = sj.get('directions') or {}
    if 'default' not in dirs:
        return None, 'no-default'
    frames = dirs['default']['frames']

    swf = SWF(swf_path)
    root = find_root(swf)
    if root is None:
        return None, 'no-root'
    n = swf.sprites[root].nframes
    if n != len(frames):
        return None, f'frames {len(frames)}!={n}'

    touched = 0
    mismatch = 0
    for i in range(n):
        leaves = list(expand(swf, root, i, IDENT, None, None, dir_seg=False))
        parts = frames[i].get('parts', [])
        if len(leaves) != len(parts):
            mismatch += 1
            continue
        for (cid, _m, _l, _a, cm, ca), p in zip(leaves, parts):
            if p.get('part') != f'shape_{cid}':
                mismatch += 1
                continue
            has_mul = any(abs(v - 1.0) > 1e-9 for v in cm)
            has_add = any(abs(v) > 1e-9 for v in ca)
            if has_mul:
                p['cmul'] = [round(v, 6) for v in cm]
                touched += 1
            elif 'cmul' in p:
                del p['cmul']
            if has_add:
                p['cadd'] = [round(v, 4) for v in ca]
            elif 'cadd' in p:
                del p['cadd']

    if not dry:
        with open(sj_path, 'w', encoding='utf-8') as f:
            json.dump(sj, f, ensure_ascii=False, separators=(',', ':'))
    return (touched, mismatch), 'ok'


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--dry-run', action='store_true')
    ap.add_argument('--only', default='')
    args = ap.parse_args()
    js = jobs()
    if args.only:
        js = [j for j in js if j[1] == args.only]
    print(f'共 {len(js)} 个动画，运行时根 {RUNTIME}')
    total = 0
    bad = 0
    for swf_path, rel in js:
        r, msg = patch_one(swf_path, rel, args.dry_run)
        if r is None:
            print(f'  !! {rel}: {msg}')
            bad += 1
            continue
        touched, mismatch = r
        total += touched
        flag = '' if mismatch == 0 else f'  匹配失败 {mismatch}'
        print(f'  {rel}: 注入 {touched} 个 part{flag}')
        if mismatch:
            bad += 1
    print(f'合计注入 {total} 个 part，异常动画 {bad} 个'
          f'{"（dry-run 未写盘）" if args.dry_run else ""}')


if __name__ == '__main__':
    main()
