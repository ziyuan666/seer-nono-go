#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""export_ref_all.py —— 对 source_swf 下每个 SWF 现场用 FFDec 导出参考 SVG。

为什么不用 /tmp/ffanim2：那批目录是早前按另一套素材导出的，命名与当前
`super/action/N_M.swf` 对不上（例如 super5_action_6 找不到对应 SWF），
用它做回归等于比对错素材。现场导出保证「每个 SWF 比的就是它自己的 SVG」。

morph（tag 46/84）FFDec 的 `-export shape` 不导出，必须再跑一次
`-export morphshape`；两次输出到同一目录，文件名都是 `<cid>.svg`，互不冲突。
"""
import argparse
import os
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
SWF_ROOT = os.path.join(ROOT, 'source_swf', '资源', 'nono')
OUT_ROOT = '/tmp/ffall'
FFDEC = '/app/working/workspaces/a2ZDuB/tools/ffdec/ffdec-cli.jar'


def safe(rel):
    return rel.replace('/', '__').replace('.swf', '')


def export_one(swf):
    out = os.path.join(OUT_ROOT, safe(swf))
    os.makedirs(out, exist_ok=True)
    full = os.path.join(SWF_ROOT, swf)
    for kind in ('shape', 'morphshape'):
        subprocess.run(['java', '-jar', FFDEC, '-export', kind, out, full],
                       capture_output=True, timeout=300)
    n = len([f for f in os.listdir(out) if f.endswith('.svg')])
    return swf, n


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--jobs', type=int, default=6)
    args = ap.parse_args()
    swfs = []
    for dirpath, _d, files in os.walk(SWF_ROOT):
        for f in files:
            if f.endswith('.swf'):
                swfs.append(os.path.relpath(os.path.join(dirpath, f), SWF_ROOT))
    swfs.sort()
    print('待导出 %d 个 SWF' % len(swfs))
    total = 0
    with ThreadPoolExecutor(max_workers=args.jobs) as ex:
        for swf, n in ex.map(export_one, swfs):
            total += n
            print('%-40s %d svg' % (swf, n))
            sys.stdout.flush()
    print('合计 SVG %d，输出目录 %s' % (total, OUT_ROOT))


if __name__ == '__main__':
    main()
