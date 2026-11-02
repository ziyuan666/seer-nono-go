#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""btn_sheet.py —— 把 ffdec 导出的 DefineButton2 目录拼成「按钮图鉴」：
每个按钮一行，up / over / down 三列，标注 id 与尺寸，用来肉眼认按钮。

用法: python3 btn_sheet.py <导出目录> <输出png> [最大按钮数]
"""
import os
import sys

from PIL import Image, ImageDraw, ImageFont


def font(sz=12):
    for p in ('/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf',):
        if os.path.exists(p):
            return ImageFont.truetype(p, sz)
    return ImageFont.load_default()


def main():
    root, out = sys.argv[1], sys.argv[2]
    limit = int(sys.argv[3]) if len(sys.argv) > 3 else 40
    dirs = sorted([d for d in os.listdir(root) if d.startswith('DefineButton2_')],
                  key=lambda d: int(d.split('_')[1]))
    f = font(12)
    rows = []
    for d in dirs[:limit]:
        p = os.path.join(root, d)
        ims = {}
        for s in ('up', 'over', 'down'):
            for fn in os.listdir(p):
                if fn.endswith(f'_{s}.png'):
                    ims[s] = Image.open(os.path.join(p, fn)).convert('RGBA')
        if 'up' not in ims:
            continue
        rows.append((d, ims))
    if not rows:
        raise SystemExit('没有按钮')
    cw = max(max(i.width for i in ims.values()) for _, ims in rows) + 8
    chh = max(max(i.height for i in ims.values()) for _, ims in rows) + 20
    W = cw * 3 + 190
    H = chh * len(rows) + 10
    sheet = Image.new('RGB', (W, H), (40, 40, 44))
    dr = ImageDraw.Draw(sheet)
    for k, (name, ims) in enumerate(rows):
        y = 6 + k * chh
        u = ims['up']
        dr.text((4, y + 4), f'{name}  {u.width}x{u.height}', fill=(255, 235, 120), font=f)
        for j, s in enumerate(('up', 'over', 'down')):
            x = 190 + j * cw
            dr.text((x, y), s, fill=(160, 220, 255), font=f)
            if s in ims:
                im = ims[s]
                bg = Image.new('RGBA', im.size, (210, 210, 215, 255))
                bg.alpha_composite(im)
                sheet.paste(bg.convert('RGB'), (x, y + 16))
    sheet.save(out)
    print(out, sheet.size, len(rows), 'buttons')


if __name__ == '__main__':
    main()
