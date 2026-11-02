#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""build_anims.py —— 批量把「动作 / 表情」SWF 导入成矢量精灵 JSON。

与形态（idle）不同，动作/表情是单方向时间轴，每个 SWF 只有一个根 sprite，
帧数就等于动画帧数（与旧 PNG 素材逐帧一一对应，导入时用帧数做交叉校验）。

命名规则（来自资源包实测，不是猜的）：
  nono/action/{n}.swf            → plain 动作 n
  nono/exp/{n}.swf               → plain 表情 n
  super/action/{n}_{k}.swf       → 超能形态 k 的动作 n
  super/exp/{n}_{k}.swf          → 超能形态 k 的表情 n
  其中 1_2.swf 这类"动作1形态2"只有 1 帧，是占位（对应旧素材 super2/action/1 也是 1 帧）。

输出：assets/sprites/<form>/<anim>/sprite.json + parts/
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

CLI = os.environ.get('FFDEC_CLI') or os.path.join(os.path.dirname(VEC_ROOT), 'tools', 'ffdec', 'ffdec-cli.jar')
SRC = os.path.join(VEC_ROOT, 'source_swf', '资源', 'nono')
FORMS = ['super1', 'super2', 'super3', 'super4', 'super5']


def find_root(swf):
    refs = set()
    for sp in swf.sprites.values():
        for fr in sp.frames:
            for op in fr:
                if op.get('cid'):
                    refs.add(op['cid'])
    roots = [(sid, swf.sprites[sid].nframes) for sid in swf.sprites if sid not in refs]
    if not roots:
        return None
    return max(roots, key=lambda x: x[1])[0]


def build_one(swf_path, form, anim, out_root, tmp_root, do_export=True, native=False):
    name = f'{form}/{anim}'
    svg_dir = os.path.join(tmp_root, f'{form}_{anim}')
    swf = SWF(swf_path)
    root = find_root(swf)
    if root is None:
        print(f'  !! {name}: 找不到根 sprite，跳过')
        return False
    nframes = swf.sprites[root].nframes
    if do_export:
        os.makedirs(svg_dir, exist_ok=True)
        r = subprocess.run(['java', '-jar', CLI, '-zoom', '1', '-export', 'shape', svg_dir, swf_path],
                           capture_output=True, text=True)
        if r.returncode != 0:
            print(f'  !! {name}: ffdec 失败 {r.stderr[-200:]}')
            return False
    nsvg = len(glob.glob(os.path.join(svg_dir, '*.svg')))
    if nsvg < len(swf.shapes):
        print(f'  !! {name}: SVG 缺 {len(swf.shapes) - nsvg} 个（{nsvg}/{len(swf.shapes)}）')
    cmd = [sys.executable, os.path.join(HERE, 'build_sprite.py'),
           '--swf', swf_path, '--root', str(root), '--name', name,
           '--svg-dir', svg_dir, '--out', out_root, '--single-dir']
    if native:
        cmd.append('--native')
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        print(f'  !! {name}: build 失败\n{r.stdout[-300:]}\n{r.stderr[-300:]}')
        return False
    ok = '写入' in r.stdout
    print(f'  {name}: root={root} {nframes}帧 shapes={len(swf.shapes)} svg={nsvg} {"OK" if ok else "FAIL"}')
    return ok


def main():
    ap = argparse.ArgumentParser()
    # 默认输出到**运行时**那份素材（nono_deskpet/assets/sprites）。
    # 以前默认写 VEC_ROOT/assets/sprites，导致 vec 侧和运行时侧各有一份、越走越偏：
    # 2026-10-26 排查时 vec 侧 62 个 sprite.json 全是旧画布（plain/action_2 82.6×90.6
    # vs 运行时 71.8×81.1），vec 侧那份已归档删除。只保留唯一一份，避免再分叉。
    ap.add_argument('--out', default=os.path.join(os.path.dirname(VEC_ROOT), 'nono_deskpet', 'assets', 'sprites'))
    ap.add_argument('--tmp', default='/tmp/ffanim2')
    ap.add_argument('--only', default='', help='只跑其中一个，如 super1/action_2')
    ap.add_argument('--form', default='', help='只跑某个形态，如 annual')
    ap.add_argument('--no-export', action='store_true', help='复用已有 SVG')
    ap.add_argument('--native', action='store_true',
                    help='用自研 swfshape 解析器，不跑 ffdec/不读 SVG')
    args = ap.parse_args()
    os.makedirs(args.tmp, exist_ok=True)

    jobs = []
    # plain：动作 1~2 / 表情 1~4
    for n in (1, 2):
        p = os.path.join(SRC, 'action', f'{n}.swf')
        if os.path.exists(p):
            jobs.append((p, 'plain', f'action_{n}'))
    for n in (1, 2, 3, 4):
        p = os.path.join(SRC, 'exp', f'{n}.swf')
        if os.path.exists(p):
            jobs.append((p, 'plain', f'exp_{n}'))
    # 超能 5 形态：动作 1~7 / 表情 1~4（文件是 动作_形态）
    for k, form in enumerate(FORMS, start=1):
        for n in range(1, 8):
            p = os.path.join(SRC, 'super', 'action', f'{n}_{k}.swf')
            if os.path.exists(p):
                jobs.append((p, form, f'action_{n}'))
        for n in range(1, 5):
            p = os.path.join(SRC, 'super', 'exp', f'{n}_{k}.swf')
            if os.path.exists(p):
                jobs.append((p, form, f'exp_{n}'))

    # 至尊 NONO（annual）：动作 1~7 / 表情 1~4（文件名无形态后缀）
    # 资源实测：annual/action/{2,3,4,6,7}.swf、annual/exp/{1,2,3,4}.swf
    for n in range(1, 8):
        p = os.path.join(SRC, 'annual', 'action', f'{n}.swf')
        if os.path.exists(p):
            jobs.append((p, 'annual', f'action_{n}'))
    for n in range(1, 5):
        p = os.path.join(SRC, 'annual', 'exp', f'{n}.swf')
        if os.path.exists(p):
            jobs.append((p, 'annual', f'exp_{n}'))

    if args.form:
        jobs = [j for j in jobs if j[1] == args.form]
    if args.only:
        jobs = [j for j in jobs if f'{j[1]}/{j[2]}' == args.only]
    print(f'共 {len(jobs)} 个动画')
    okc = 0
    for swf, form, anim in jobs:
        if build_one(swf, form, anim, args.out, args.tmp, not args.no_export, args.native):
            okc += 1
    print(f'完成 {okc}/{len(jobs)}')


if __name__ == '__main__':
    main()
