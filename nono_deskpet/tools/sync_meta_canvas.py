#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""sync_meta_canvas.py —— 按 assets/<form>/idle/<dir> 里的实际 PNG 尺寸回写
meta.json 的 canvas / ball 字段。用于重烘素材后保持 meta 与文件一致。

用法: python3 sync_meta_canvas.py <assets根目录> [ball]
"""
import json
import os
import sys

from PIL import Image

FORMS = ['plain', 'super1', 'super2', 'super3', 'super4', 'super5']
DIRS = ['down', 'leftdown', 'left', 'leftup', 'up', 'rightup', 'right', 'rightdown']


def main():
    root = sys.argv[1]
    ball = int(sys.argv[2]) if len(sys.argv) > 2 else None
    for form in FORMS:
        p = os.path.join(root, form, 'meta.json')
        if not os.path.exists(p):
            continue
        m = json.load(open(p, encoding='utf-8'))
        sizes = set()
        for d in DIRS:
            dd = os.path.join(root, form, 'idle', d)
            if not os.path.isdir(dd):
                continue
            for f in sorted(os.listdir(dd)):
                if f.endswith('.png'):
                    sizes.add(Image.open(os.path.join(dd, f)).size)
        if len(sizes) != 1:
            raise SystemExit(f'{form}: 帧尺寸不一致 {sizes}')
        w, h = sizes.pop()
        old = dict(m.get('canvas', {}))
        m['canvas'] = {'w': w, 'h': h}
        if ball is not None:
            m['ball'] = ball
        m.setdefault('idle_frames', len(os.listdir(os.path.join(root, form, 'idle', DIRS[0]))))
        json.dump(m, open(p, 'w', encoding='utf-8'), ensure_ascii=False, indent=2)
        print(f'{form}: canvas {old} -> {{"w": {w}, "h": {h}}}  ball={m.get("ball")}')


if __name__ == '__main__':
    main()
