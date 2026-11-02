#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""strip_white_preview.py —— 预览：把指定方向帧里的「纯白」像素去掉。

背景：plain 的 up/leftup/rightup 三方向，body(sprite 116) 这三段完全没有白色
像素（实测 pure-white=0），但 color_2(sprite 39) 在这三段分别 place 了
cid 26/38/26，形状是整颗白球 + 两弧。合成后本体会多出一圈白。

只处理 >阈值 的纯白（默认 >=250 且 alpha>200），其余颜色（青边/灰块/黑内壁）不动。

用法: python3 strip_white_preview.py <形态> <方向1,方向2,...> <输出目录>
"""
import os
import sys

from PIL import Image, ImageDraw

FORMS_DIR = 'assets'


def strip(path, th=250):
    im = Image.open(path).convert('RGBA')
    px = im.load()
    w, h = im.size
    n = 0
    for y in range(h):
        for x in range(w):
            r, g, b, a = px[x, y]
            if a > 200 and r >= th and g >= th and b >= th:
                px[x, y] = (r, g, b, 0)
                n += 1
    return im, n


def main():
    form = sys.argv[1]
    dirs = sys.argv[2].split(',')
    out = sys.argv[3]
    os.makedirs(out, exist_ok=True)
    for d in dirs:
        src = os.path.join(FORMS_DIR, form, 'idle', d)
        dst = os.path.join(out, d)
        os.makedirs(dst, exist_ok=True)
        tot = 0
        for f in sorted(os.listdir(src)):
            if not f.endswith('.png'):
                continue
            im, n = strip(os.path.join(src, f))
            tot += n
            im.save(os.path.join(dst, f))
        print(f'{form}/{d}: 17 帧去白像素合计 {tot}')

    # 三底对比图
    for bgname, color in (('white', (255, 255, 255)), ('black', (0, 0, 0)), ('red', (220, 30, 40))):
        tiles = []
        for d in dirs:
            ims = [Image.open(os.path.join(out, d, f)).convert('RGBA')
                   for f in sorted(os.listdir(os.path.join(out, d))) if f.endswith('.png')]
            w = sum(i.width for i in ims[:9])
            stripimg = Image.new('RGBA', (ims[0].width * 9, ims[0].height * 2), color + (255,))
            for k, im in enumerate(ims):
                stripimg.alpha_composite(im, ((k % 9) * im.width, (k // 9) * im.height))
            tiles.append((d, stripimg))
        W = max(t.width for _, t in tiles)
        H = sum(t.height + 16 for _, t in tiles)
        sheet = Image.new('RGBA', (W, H), color + (255,))
        dr = ImageDraw.Draw(sheet)
        y = 0
        for name, t in tiles:
            dr.text((2, y + 2), f'{form}/{name} (no-white) bg={bgname}',
                    fill=(255, 255, 0, 255))
            y += 16
            sheet.alpha_composite(t, (0, y))
            y += t.height
        p = os.path.join(out, f'{form}_nowhite_{bgname}.png')
        sheet.convert('RGB').save(p)
        print('sheet:', p)


if __name__ == '__main__':
    main()
