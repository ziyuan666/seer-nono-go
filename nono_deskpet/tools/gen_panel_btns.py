#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""gen_panel_btns.py —— 生成面板按钮三态贴图。

  1. 源图用 ffdec -zoom 4 导出的高分辨率图，缩到目标尺寸时降采样，保证清晰；
  2. 三态共用「联合 alpha bbox」，保证 up/over/down 同原点，叠加不跳；
  3. 输出一律 RGBA，透明区保持透明，不填任何底色；
  4. 目标矩形来自面板底图实测（模板匹配 / 像素扫描），不用 SWF place 坐标。

用法: python3 gen_panel_btns.py
"""
import glob
import os

from PIL import Image, ImageChops

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
UI = os.path.join(ROOT, 'assets', 'ui')


def load_state(src_dir, st):
    fs = glob.glob(os.path.join(src_dir, '*_%s.png' % st))
    if not fs:
        raise SystemExit('缺状态图 %s %s' % (src_dir, st))
    return Image.open(fs[0]).convert('RGBA')


def union_bbox(ims):
    box = None
    for im in ims:
        b = im.getbbox()
        if b is None:
            continue
        box = b if box is None else (min(box[0], b[0]), min(box[1], b[1]),
                                     max(box[2], b[2]), max(box[3], b[3]))
    return box


def fit_canvas(im, w, h):
    """等比缩放进 w×h 画布并居中，保持 alpha。"""
    iw, ih = im.size
    if iw == 0 or ih == 0:
        return Image.new('RGBA', (w, h), (0, 0, 0, 0))
    s = min(w / iw, h / ih)
    nw, nh = max(1, int(round(iw * s))), max(1, int(round(ih * s)))
    r = im.resize((nw, nh), Image.LANCZOS)
    out = Image.new('RGBA', (w, h), (0, 0, 0, 0))
    out.paste(r, ((w - nw) // 2, (h - nh) // 2))
    return out


def gen(src_dir, prefix, w, h, mirror=False, states=('up', 'over', 'down'), bbox_from='union'):
    ims = {st: load_state(src_dir, st) for st in states}
    # fly 这类按钮的 over 态带 tooltip，联合 bbox 会把图标缩得很小；
    # 用 up 的 bbox 定位图标本体，tooltip 单独出图（见 gen_fly_tip）。
    box = ims['up'].getbbox() if bbox_from == 'up' else union_bbox(ims.values())
    if box is None:
        raise SystemExit('空图 %s' % src_dir)
    for st, im in ims.items():
        c = im.crop(box)
        if mirror:
            c = c.transpose(Image.FLIP_LEFT_RIGHT)
        out = fit_canvas(c, w, h)
        p = os.path.join(UI, '%s_%s.png' % (prefix, st))
        out.save(p)
        print('  %-22s %s  %s' % (os.path.basename(p), out.size, 'RGBA' if out.mode == 'RGBA' else out.mode))


def tint(im, k):
    """按亮度缩放 k 倍（保留 alpha，透明区不动）。"""
    r, g, b, a = im.split()
    f = lambda ch: ch.point(lambda v: max(0, min(255, int(v * k))))
    return Image.merge('RGBA', (f(r), f(g), f(b), a))


def gen_fly_tip(src_dir, prefix, out_w):
    """裁 over 态里的 tooltip 黑框（图标下方那块），单独做 hover 提示图。"""
    im = load_state(src_dir, 'over')
    w, h = im.size
    box = im.getbbox()
    tip = im.crop((0, int(h * 0.45), w, box[3] if box else h))
    tw, th = tip.size
    out = tip.resize((out_w, max(1, int(round(th * out_w / tw)))), Image.LANCZOS)
    p = os.path.join(UI, '%s.png' % prefix)
    out.save(p)
    print('  %-22s %s' % (os.path.basename(p), out.size))


def gen_from_scene(scene_path, rect, prefix):
    """从面板底图裁切一块，做 over=提亮 / down=压暗 三态。"""
    sc = Image.open(scene_path).convert('RGBA')
    x, y, w, h = rect
    up = sc.crop((x, y, x + w, y + h))
    for st, im in (('up', up), ('over', tint(up, 1.20)), ('down', tint(up, 0.80))):
        p = os.path.join(UI, '%s_%s.png' % (prefix, st))
        im.save(p)
        print('  %-22s %s' % (os.path.basename(p), im.size))


def gen_drawn_close(prefix, w, h):
    """面板本身没有关闭按钮时（soul），自绘一个圆形 X，三态用亮度区分。"""
    from PIL import ImageDraw
    def make(alpha_mul, fill):
        im = Image.new('RGBA', (w, h), (0, 0, 0, 0))
        d = ImageDraw.Draw(im)
        d.ellipse([0, 0, w - 1, h - 1], fill=(30, 90, 160, int(210 * alpha_mul)),
                  outline=(150, 220, 255, int(255 * alpha_mul)), width=2)
        k = w * 0.28
        d.line([k, k, w - k, h - k], fill=fill, width=3)
        d.line([w - k, k, k, h - k], fill=fill, width=3)
        return im
    for st, a, f in (('up', 1.0, (235, 245, 255, 255)),
                     ('over', 1.0, (255, 255, 190, 255)),
                     ('down', 0.75, (200, 210, 220, 255))):
        im = make(a, f)
        p = os.path.join(UI, '%s_%s.png' % (prefix, st))
        im.save(p)
        print('  %-22s %s' % (os.path.basename(p), im.size))


def main():
    B4 = '/tmp/b4_mix'
    print('[lab] 底图 1x，矩形来自 lab.png 模板匹配')
    gen(f'{B4}/DefineButton2_26', 'lab_close', 32, 32)
    gen(f'{B4}/DefineButton2_40', 'lab_start', 84, 37)
    gen(f'{B4}/DefineButton2_44', 'lab_reset', 84, 37)
    gen(f'{B4}/DefineButton2_62', 'lab_tpl1', 55, 55)
    gen(f'{B4}/DefineButton2_65', 'lab_tpl2', 55, 55)
    gen(f'{B4}/DefineButton2_68', 'lab_tpl3', 55, 55)
    gen(f'{B4}/DefineButton2_71', 'lab_pageL', 27, 31)
    gen(f'{B4}/DefineButton2_71', 'lab_pageR', 26, 29, mirror=True)

    print('[storage] 底图 4x，逻辑矩形来自 storage.png 实测')
    gen('/tmp/b4_machine/DefineButton2_165', 'storage_tab1', 90, 21)
    gen('/tmp/b4_machine/DefineButton2_169', 'storage_tab2', 86, 21)
    gen('/tmp/b4_machine/DefineButton2_176', 'storage_close', 20, 20)
    gen('/tmp/b4_machine/DefineButton2_173', 'storage_pageL', 17, 20)
    gen('/tmp/b4_machine/DefineButton2_173', 'storage_pageR', 17, 20, mirror=True)

    print('[info] 底图 4x')
    gen('/tmp/b4_info/DefineButton2_133', 'info_close', 20, 20)
    gen('/tmp/b4_info/DefineButton2_88', 'info_rename', 20, 20)

    print('[flychoice] 显示坐标 = 原生 1.5x')
    gen('/tmp/b4_fly/DefineButton2_20', 'fly_close', 38, 38)
    for bid, name in (('32', 'fly_1'), ('47', 'fly_2'), ('54', 'fly_3'), ('60', 'fly_4')):
        gen('/tmp/b4_fly/DefineButton2_' + bid, name, 56, 56, bbox_from='up')
        gen_fly_tip('/tmp/b4_fly/DefineButton2_' + bid, name + '_tip', 210)

    print('[shell] 从底图裁切 + 提亮/压暗')
    shells = {
        'molecule': (236, 6, 28, 28),
        'expadm': (549, 10, 24, 24),
        'petlist': (261, 8, 28, 28),
        'train': (335, 11, 26, 26),
        'petstorage': (879, 33, 38, 38),
        'track': (370, 58, 40, 40),
        'soul': (833, 8, 30, 30),
    }
    for name, rect in shells.items():
        if name == 'soul':
            gen_drawn_close('shell_soul_close', 30, 30)
            continue
        gen_from_scene(os.path.join(UI, 'shell', name + '.png'), rect, 'shell_%s_close' % name)


if __name__ == '__main__':
    main()
