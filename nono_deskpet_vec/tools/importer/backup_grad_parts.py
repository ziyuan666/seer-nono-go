#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""备份所有含渐变的 shape 文件（改动前留档）。

只备份会被 fix_grad_r.py 改到的文件，避免把 2782 个 parts 全拷一遍。
"""
import glob
import json
import os
import shutil
import sys

DEST = '/app/working/workspaces/a2ZDuB/变更工程/当前改动/20261026-02/备份/parts_grad'
ROOTS = ['/app/working/workspaces/a2ZDuB/nono_deskpet/assets/sprites',
         '/app/working/workspaces/a2ZDuB/nono_deskpet_vec/assets/sprites']


def main():
    os.makedirs(DEST, exist_ok=True)
    n = 0
    for root in ROOTS:
        tag = 'deskpet' if 'nono_deskpet/' in root else 'vec'
        for p in glob.glob(os.path.join(root, '*', '*', 'parts', '*.json')):
            with open(p, encoding='utf-8') as f:
                d = json.load(f)
            has = False
            for it in d.get('paths', []):
                g = it.get('gradient') or {}
                if g.get('r', 0) > 800 or abs(g.get('x1', 0)) > 800:
                    has = True
                    break
            if not has:
                continue
            rel = os.path.relpath(p, root).replace(os.sep, '_').replace('/', '_')
            shutil.copy2(p, os.path.join(DEST, tag + '_' + rel))
            n += 1
    print('已备份 %d 个含渐变的 shape 文件 -> %s' % (n, DEST))


if __name__ == '__main__':
    main()
