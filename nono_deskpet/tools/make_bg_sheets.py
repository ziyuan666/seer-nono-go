#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""make_bg_sheets.py —— 把每个形态的每一帧铺在红/黑/白三种底上，用于肉眼查素材问题。

为什么要三种底：
  - 白底：看黑色描边、白色外壳是否缺内色；
  - 黑底：看白色 fringe / 白缝（半透明白边在黑底上会发灰亮）；
  - 红底：红与白/青反差大，能看出 1px 的层间错位、缺角、半透明残留。

输出：<out>/<form>_<bg>.png，每个形态 3 张，8 行(方向) × 17 列(帧)。
格子按「球径 60px」归一（super4 长鳍太宽，按格宽 ≤260 收），保证同一张图里
帧与帧之间可以直接比较。

用法：
    python3 make_bg_sheets.py <assets根> <输出目录> [idle|action|exp|all]
"""
import json
import os
import sys

from PIL import Image, ImageDraw, ImageFont

FORMS = ['plain', 'super1', 'super2', 'super3', 'super4', 'super5']
DIRS = ['down', 'leftdown', 'left', 'leftup', 'up', 'rightup', 'right', 'rightdown']
BGS = [('white', (255, 255, 255)), ('black', (0, 0, 0)), ('red', (220, 30, 40))]
TARGET_BALL = 60      # 目标球径（像素）
MAX_CELL_W = 260      # 单格最大宽
GAP = 4
LABEL_W = 86
HEAD = 26


def font(size=12):
    for p in ('/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf',
              '/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf'):
        if os.path.exists(p):
            return ImageFont.truetype(p, size)
    return ImageFont.load_default()


def frames_of(d):
    if not os.path.isdir(d):
        return []
    return [Image.open(os.path.join(d, f)).convert('RGBA')
            for f in sorted(os.listdir(d)) if f.endswith('.png')]


def load_sets(root, form, part):
    out = {}
    if part in ('idle', 'all'):
        for d in DIRS:
            fr = frames_of(os.path.join(root, form, 'idle', d))
            if fr:
                out['idle/' + d] = fr
    if part in ('action', 'all'):
        base = os.path.join(root, form, 'action')
        if os.path.isdir(base):
            for n in sorted(os.listdir(base), key=lambda x: int(x) if x.isdigit() else 0):
                fr = frames_of(os.path.join(base, n))
                if fr:
                    out['action/' + n] = fr
    if part in ('exp', 'all'):
        base = os.path.join(root, form, 'exp')
        if os.path.isdir(base):
            for n in sorted(os.listdir(base), key=lambda x: int(x) if x.isdigit() else 0):
                fr = frames_of(os.path.join(base, n))
                if fr:
                    out['exp/' + n] = fr
    return out


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else 'assets'
    out = sys.argv[2] if len(sys.argv) > 2 else '/tmp/nono_bg_check'
    part = sys.argv[3] if len(sys.argv) > 3 else 'idle'
    os.makedirs(out, exist_ok=True)
    fnt = font(12)
    fnt_h = font(14)
    made = []
    for form in FORMS:
        meta = json.load(open(os.path.join(root, form, 'meta.json'), encoding='utf-8'))
        ball = meta.get('ball') or 98
        k = min(TARGET_BALL / float(ball), 1.0)
        sets = load_sets(root, form, part)
        if not sets:
            print(f'{form}: 无帧，跳过')
            continue
        maxw = max(im.width for fr in sets.values() for im in fr)
        maxh = max(im.height for fr in sets.values() for im in fr)
        k = min(k, MAX_CELL_W / float(maxw))
        cw, ch = int(round(maxw * k)), int(round(maxh * k))
        cols = max(len(fr) for fr in sets.values())
        rows = list(sets.items())
        W = LABEL_W + cols * (cw + GAP) + GAP
        H = HEAD + len(rows) * (ch + GAP + 14) + GAP
        print(f'{form}: {len(rows)} 组 × 最多 {cols} 帧, cell={cw}x{ch}, sheet={W}x{H}, k={k:.3f}')
        for name, color in BGS:
            sheet = Image.new('RGBA', (W, H), color + (255,))
            dr = ImageDraw.Draw(sheet)
            dr.text((8, 6), f'{form}  part={part}  bg={name}  ball={ball}  k={k:.2f}',
                    fill=(255, 255, 255) if name != 'white' else (0, 0, 0), font=fnt_h)
            y = HEAD
            for label, fr in rows:
                dr.text((6, y + 2), label, fill=(255, 255, 255) if name != 'white' else (0, 0, 0), font=fnt)
                x = LABEL_W
                for i, im in enumerate(fr):
                    w, h = max(1, int(round(im.width * k))), max(1, int(round(im.height * k)))
                    t = im.resize((w, h), Image.LANCZOS)
                    # 帧底部对齐、水平居中，方便看落脚点是否漂
                    sheet.alpha_composite(t, (x + (cw - w) // 2, y + (ch - h)))
                    if i == 0:
                        dr.text((x, y), '1', fill=(255, 255, 0, 255), font=fnt)
                    x += cw + GAP
                y += ch + GAP + 14
            p = os.path.join(out, f'{form}_{name}.png')
            sheet.convert('RGB').save(p, optimize=True)
            made.append((p, os.path.getsize(p)))
    print('\n生成:')
    for p, s in made:
        print(f'  {p}  {s/1024/1024:.2f}MB')
    print(f'共 {len(made)} 张')


if __name__ == '__main__':
    main()
