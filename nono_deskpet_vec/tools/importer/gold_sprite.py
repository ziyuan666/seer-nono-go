#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""用 FFDec 直接导出「动画根 sprite」的整段帧，作为金标准 PNG。

为什么不用 `-export frame`：那条路只能导根时间轴，动画 SWF 的根只有 1 帧，
必须先把根帧数拉长（mkframes）+ 改 displayRect + xml2swf 才能导出；而 sprite
导出走的是「该 sprite 自己的时间轴 + 自己的 frame bounds」，不需要改任何结构，
也不会有 displayRect 扩不够导致的裁切（曾出现内容贴 0,0 被切）。

根 sprite 的判定与 build_anims.find_root 完全一致（没被任何 sprite 引用、
帧数最多），保证金标准和素材是同一颗根。

用法：
    python3 gold_sprite.py <swf> <outdir> [--frames 1-248] [--root N]
输出：<outdir>/<n>.png（1 起），并打印 root/帧数。
"""
import argparse
import glob
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
VEC_ROOT = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, HERE)
from swfread import SWF  # noqa: E402
from build_anims import find_root  # noqa: E402

CLI = os.environ.get('FFDEC_CLI') or os.path.join(
    os.path.dirname(VEC_ROOT), 'tools', 'ffdec', 'ffdec-cli.jar')


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('swf')
    ap.add_argument('outdir')
    ap.add_argument('--frames', default='', help='如 1-248 / 205；留空=全部')
    ap.add_argument('--root', type=int, default=0)
    args = ap.parse_args()

    swf = SWF(args.swf)
    root = args.root or find_root(swf)
    if not root:
        raise SystemExit('找不到根 sprite')
    n = swf.sprites[root].nframes
    sel = args.frames or ('1-%d' % n)

    tmp = args.outdir.rstrip('/') + '_raw'
    os.makedirs(tmp, exist_ok=True)
    cmd = ['java', '-jar', CLI, '-format', 'sprite:png', '-select', '%d:%s' % (root, sel),
           '-export', 'sprite', tmp, args.swf]
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        raise SystemExit('ffdec 失败: %s' % r.stderr[-400:])

    # FFDec 输出目录名是 DefineSprite_<id>[_<cls>]，按 root 精确定位
    cands = [d for d in glob.glob(os.path.join(tmp, 'DefineSprite_%d*' % root))]
    if not cands:
        raise SystemExit('没找到导出目录 DefineSprite_%d*，实际: %s'
                         % (root, os.listdir(tmp)[:10]))
    srcdir = cands[0]
    os.makedirs(args.outdir, exist_ok=True)
    got = 0
    for p in glob.glob(os.path.join(srcdir, '*.png')):
        base = os.path.basename(p)
        os.replace(p, os.path.join(args.outdir, base))
        got += 1
    print('root=%d 帧数=%d 导出=%d -> %s' % (root, n, got, args.outdir))


if __name__ == '__main__':
    main()
