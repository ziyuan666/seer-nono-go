#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""fix_part_base.py —— 修正 idle 形态素材里 parts 的 base 平移（一次性）。

背景（2026-10-25 实测，代号 20261025-02）：
    早期 build_sprite.py 写 parts 的 path.matrix 时用了 `m[4] - cx`，比 FFDec 原始
    base（= translate(-bounds.xmin)）多加了一次画布原点 anchor。渲染时
    m = part.matrix ∘ path.base，part 已减过 cx，于是每个部件整体再平移一个 anchor。

    实测（比对 SWF shapeBounds 的 -xmin/-ymin）：
      idle 6 形态  plain 50/50、super1 44/44、super2 48/48、
                   super3 28/28、super4 67/67、super5 25/25  → 全部偏 anchor
      action/exp/fly 全部 0 命中（base 已是 -bounds.xmin，正确）
    为什么分叉：action/exp 在 20261025-01 用修好的 build 重导过，idle 没重导。

    修正：path.matrix 平移 += (cx, cy) = −anchor，即 base 回到 -bounds.xmin。
    d / 填充 / 描边 / 渐变一律不动（idle 6 形态渐变数为 0，无需处理）。

幂等：修过的 parts JSON 顶层写入 "baseAnchorFixed": 1，重复运行跳过。
    Go 侧 spec.PartFile 只解 id/paths，未知字段被忽略。

用法：
    python3 fix_part_base.py --root ../../assets/sprites --dry-run
    python3 fix_part_base.py --root ../../assets/sprites
"""
import argparse
import glob
import json
import os
import sys

# 只处理 idle 形态：action/exp/fly 的 base 已经是对的，动了就破坏。
IDLE_FORMS = ['plain', 'super1', 'super2', 'super3', 'super4', 'super5']


def fix_one(part_path, cx, cy, dry_run):
    with open(part_path) as f:
        data = json.load(f)
    if data.get('baseAnchorFixed') == 1:
        return 'skip', 0
    paths = data.get('paths') or []
    if not paths:
        return 'empty', 0
    n = 0
    for p in paths:
        m = p.get('matrix')
        if m and len(m) == 6:
            m[4] = round(m[4] + cx, 6)
            m[5] = round(m[5] + cy, 6)
            n += 1
    data['baseAnchorFixed'] = 1
    if not dry_run:
        with open(part_path, 'w') as f:
            json.dump(data, f, ensure_ascii=False, separators=(',', ':'))
    return 'fixed', n


def main():
    ap = argparse.ArgumentParser()
    # 本脚本没有全局 VEC_ROOT（历史遗留），按 __file__ 现算：
    # tools/importer/ → 上两级 = nono_deskpet_vec → 同级 = nono_deskpet。
    _vec_root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    ap.add_argument('--root', default=os.path.join(os.path.dirname(_vec_root), 'nono_deskpet', 'assets', 'sprites'),
                    help='素材根目录，默认指向运行时那份（唯一一份）')
    ap.add_argument('--forms', default=','.join(IDLE_FORMS))
    ap.add_argument('--dry-run', action='store_true')
    args = ap.parse_args()

    total = {'fixed': 0, 'skip': 0, 'empty': 0}
    for form in args.forms.split(','):
        sp_path = os.path.join(args.root, form, 'sprite.json')
        if not os.path.exists(sp_path):
            print(f'{form:8s} 缺少 sprite.json，跳过')
            continue
        with open(sp_path) as f:
            sp = json.load(f)
        anchor = (sp.get('canvas') or {}).get('anchor') or [0, 0]
        # 断言是 idle 形态：必须有 8 个方向
        ndir = len(sp.get('directions') or {})
        if ndir != 8:
            print(f'{form:8s} 方向数 {ndir} != 8，不是 idle 形态，拒绝处理')
            continue
        cx, cy = -anchor[0], -anchor[1]
        files = sorted(glob.glob(os.path.join(args.root, form, 'parts', '*.json')))
        st = {'fixed': 0, 'skip': 0, 'empty': 0}
        np_ = 0
        for pf in files:
            s, n = fix_one(pf, cx, cy, args.dry_run)
            st[s] += 1
            np_ += n
        for k in total:
            total[k] += st[k]
        print(f'{form:8s} anchor=({anchor[0]:8.3f},{anchor[1]:8.3f}) parts={len(files):3d} '
              f'修={st["fixed"]:3d} 跳过={st["skip"]:3d} path={np_:4d}')
    print(f'\n合计：修 {total["fixed"]} / 跳过 {total["skip"]} / 空 {total["empty"]}'
          + ('（dry-run，未写盘）' if args.dry_run else ''))
    return 0


if __name__ == '__main__':
    sys.exit(main())
