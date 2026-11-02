#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""frame_audit.py —— 普通 NONO（sprite 127 = class pet）逐帧/逐段审计表。

输出一张对照大图：
  行 = color_2(39) / body(116) / color_1(126) / 桌宠现用合成
  列 = down leftdown left leftup up rightup right rightdown
每格标注该段首帧实际生效的 characterId（从 place 记录解析）。

用法: python3 frame_audit.py <39导出目录> <116导出目录> <126导出目录> <合成assets目录> <输出png>
"""
import glob
import os
import sys

from PIL import Image, ImageDraw

DIRS = ['down', 'leftdown', 'left', 'leftup', 'up', 'rightup', 'right', 'rightdown']
FIRST = {'down': 1, 'leftdown': 8, 'left': 16, 'leftup': 24, 'up': 32,
         'rightup': 40, 'right': 48, 'rightdown': 56}
# 从 dump_display 解析得到的每段实际生效 cid
CID = {
    39: {'down': 9, 'leftdown': 20, 'left': '20(继承)', 'leftup': 26, 'up': 38,
         'rightup': 26, 'right': 20, 'rightdown': '20(继承)'},
    116: {'down': 52, 'leftdown': 68, 'left': '68(继承)', 'leftup': 94, 'up': 115,
          'rightup': 94, 'right': 68, 'rightdown': '68(继承)'},
    126: {'down': '无(空)', 'leftdown': '无(空)', 'left': '无(空)', 'leftup': 125,
          'up': '125(继承 leftup)', 'rightup': 125, 'right': '125(继承)',
          'rightdown': '125(继承)'},
}
CELL = 190
PAD = 6


def find(root, cid):
    for n in sorted(os.listdir(root)):
        if n.startswith(f'DefineSprite_{cid}_') or n == f'DefineSprite_{cid}':
            return os.path.join(root, n)
    raise SystemExit(f'missing {cid} in {root}')


def load(path):
    return Image.open(path).convert('RGBA')


def cell(im, bg=(214, 26, 38, 255)):
    g = Image.new('RGBA', im.size, bg)
    g.alpha_composite(im)
    k = min(CELL / g.width, (CELL - 22) / g.height)
    return g.convert('RGB').resize((max(1, int(g.width * k)), max(1, int(g.height * k))), Image.LANCZOS)


def main():
    d39, d116, d126, syn, out = sys.argv[1:6]
    roots = {39: find(d39, 39), 116: find(d116, 116), 126: find(d126, 126)}
    rows = [(39, 'color_2 (sprite 39)'), (116, 'body (sprite 116)'),
            (126, 'color_1 (sprite 126)'), (None, '桌宠现用合成 (17帧首帧)')]
    W = PAD + len(DIRS) * (CELL + PAD)
    H = 40 + len(rows) * (CELL + 34)
    sheet = Image.new('RGB', (W, H), (28, 28, 30))
    dr = ImageDraw.Draw(sheet)
    for j, d in enumerate(DIRS):
        dr.text((PAD + j * (CELL + PAD) + 4, 8), f'{d}  f{FIRST[d]}', fill=(255, 235, 120))
    for i, (cid, label) in enumerate(rows):
        y = 30 + i * (CELL + 34)
        dr.text((PAD, y + 2), label, fill=(160, 240, 255))
        yy = y + 18
        for j, d in enumerate(DIRS):
            x = PAD + j * (CELL + PAD)
            if cid is None:
                p = os.path.join(syn, d, '01.png')
            else:
                p = os.path.join(roots[cid], f'{FIRST[d]}.png')
            im = cell(load(p))
            sheet.paste(im, (x, yy))
            txt = f'cid={CID[cid][d]}' if cid else d
            dr.text((x, yy + CELL - 16), str(txt), fill=(255, 255, 255))
    sheet.save(out)
    print(out, sheet.size)


if __name__ == '__main__':
    main()
