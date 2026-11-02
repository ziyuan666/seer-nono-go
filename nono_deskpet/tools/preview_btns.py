#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""preview_btns.py —— 把 assets/ui 下的三态按钮拼成核对图（up/over/down 三列）。

用法: python3 preview_btns.py <out.png> <前缀1> <前缀2> ...
"""
import os
import sys

from PIL import Image, ImageDraw

UI = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'assets', 'ui')


def main():
    out = sys.argv[1]
    prefixes = sys.argv[2:]
    rows = []
    for p in prefixes:
        ims = {}
        for st in ('up', 'over', 'down'):
            f = os.path.join(UI, '%s_%s.png' % (p, st))
            if os.path.exists(f):
                ims[st] = Image.open(f).convert('RGBA')
        if ims:
            rows.append((p, ims))
    cw = max(max(i.width for i in ims.values()) for _, ims in rows) + 10
    ch = max(max(i.height for i in ims.values()) for _, ims in rows) + 20
    W = cw * 3 + 150
    H = ch * len(rows) + 8
    sheet = Image.new('RGB', (W, H), (35, 35, 40))
    d = ImageDraw.Draw(sheet)
    for k, (name, ims) in enumerate(rows):
        y = 4 + k * ch
        d.text((4, y + 4), name, fill=(255, 235, 120))
        for j, st in enumerate(('up', 'over', 'down')):
            x = 150 + j * cw
            d.text((x, y), st, fill=(150, 210, 255))
            if st in ims:
                im = ims[st]
                bg = Image.new('RGBA', im.size, (200, 200, 205, 255))
                bg.alpha_composite(im)
                sheet.paste(bg.convert('RGB'), (x, y + 14))
    sheet.save(out)
    print(out, sheet.size, len(rows))


if __name__ == '__main__':
    main()
