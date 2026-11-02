#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""重导素材后，把 ballc / bodyc 从旧素材迁移到新素材并修正坐标偏移。

背景：build_sprite.py 用「所有帧 bbox 并集 - padding」当画布原点，anchor 就是
世界原点在画布内的位置。修好 entry_frame（子 sprite 入场帧）后，部件不再被画到
镜像位置，bbox 并集收缩，画布变小，anchor 也随之改变。
ballc（面部黑窗口中心）/ bodyc（本体第 0 帧中心）是**画布内坐标**，
所以必须按 anchor 的位移整体平移：
    新 = 旧 + (新 anchor - 旧 anchor)
帧 0 的展开不受 entry_frame 修复影响（所有 sprite 在帧 0 入场，偏移恒为 0），
因此这两个点的世界坐标不变，公式成立。
"""
import glob
import json
import os
import sys


def main():
    old_root, new_root = sys.argv[1], sys.argv[2]
    dry = '--dry' in sys.argv
    fixed = 0
    skipped = 0
    for op in sorted(glob.glob(os.path.join(old_root, '*', '*', 'sprite.json'))):
        rel = os.path.join(*op.split(os.sep)[-3:-1])
        np_ = os.path.join(new_root, rel, 'sprite.json')
        if not os.path.exists(np_):
            skipped += 1
            continue
        with open(op, encoding='utf-8') as f:
            od = json.load(f)
        with open(np_, encoding='utf-8') as f:
            nd = json.load(f)
        oc = od.get('canvas') or {}
        nc = nd.get('canvas')
        if not nc:
            continue
        oa = oc.get('anchor') or [0.0, 0.0]
        na = nc.get('anchor') or [0.0, 0.0]
        dx, dy = na[0] - oa[0], na[1] - oa[1]
        touched = False
        for key in ('ballc', 'bodyc'):
            v = oc.get(key)
            if not v:
                continue
            nc[key] = [round(v[0] + dx, 3), round(v[1] + dy, 3)]
            touched = True
        if touched:
            fixed += 1
            if not dry:
                with open(np_, 'w', encoding='utf-8') as f:
                    json.dump(nd, f, separators=(',', ':'), ensure_ascii=False)
            if abs(dx) > 30 or abs(dy) > 30:
                print('  %-22s Δanchor=(%+.1f,%+.1f) ballc=%s bodyc=%s'
                      % (rel, dx, dy, nc.get('ballc'), nc.get('bodyc')))
    print('迁移完成：%d 个动画带上 ballc/bodyc（跳过 %d）%s'
          % (fixed, skipped, '（dry-run 未写入）' if dry else ''))


if __name__ == '__main__':
    main()
