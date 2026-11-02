#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""bake_idle17_v3.py —— 内层 17 帧合成（matrix 相对定位 + 并集归位）。

为什么不用绝对 bounds：ffdec 导出的 sprite PNG 画布含约 4.5px 的描边外扩，
不同 sprite 的 bounds 与画布不完全对齐，绝对定位会累积误差。

做法：
  1. 同一父层内，以 depth 最小的子层为基准，其余层按 PlaceObject matrix
     的**相对**平移放置（sprite 级误差在差值里抵消）；
  2. 叠加后取并集 bbox，整体平移到父层第 1 帧的实际内容 bbox（一次校正）；
  3. body / color 两个父层在 pet 里的位置，用第 1 帧模板匹配实测；
  4. 输出 17 帧 × 8 方向，缩放到 253×270。

用法：
    python3 bake_idle17_v3.py <nono.xml> <nono.swf> <sprite导出目录> <输出目录>
"""
import json
import os
import sys
import glob
import xml.etree.ElementTree as ET

import numpy as np
from PIL import Image

ZOOM = 8
CANVAS = (253, 270)
PET, BODY, COLOR2 = 127, 116, 39
DIRS = {
    'down':      (52, 9,  False),
    'leftdown':  (68, 20, False),
    'left':      (68, 20, False),
    'leftup':    (94, 26, False),
    'up':        (115, 38, False),
    'rightup':   (94, 26, True),
    'right':     (68, 20, True),
    'rightdown': (68, 20, True),
}


def sprite_dir(root, cid):
    for n in sorted(os.listdir(root)):
        if n == f'DefineSprite_{cid}' or n.startswith(f'DefineSprite_{cid}_'):
            return os.path.join(root, n)
    raise SystemExit(f'missing sprite {cid}')


def frames(root, cid, n=None):
    d = sprite_dir(root, cid)
    fs = sorted((f for f in os.listdir(d) if f.endswith('.png')), key=lambda x: int(x.split('.')[0]))
    if n:
        fs = fs[:n]
    return [Image.open(os.path.join(d, f)).convert('RGBA') for f in fs]


def load_swf(path):
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    import swf_bounds as S
    shapes, sprites = S.load(path)
    return S, shapes, sprites


def box(im):
    b = im.getbbox()
    if b is None:
        raise SystemExit('empty image')
    return b


def scaled(im, s):
    if s < 0:
        im = im.transpose(Image.FLIP_LEFT_RIGHT)
    k = abs(s)
    if abs(k - 1.0) < 1e-6:
        return im
    return im.resize((max(1, round(im.width * k)), max(1, round(im.height * k))), Image.LANCZOS)


def parse_places(xml_path):
    """spriteId -> [{frame, depth, chid, m(a,b,c,d,tx,ty)}]（ffdec 的 swf2xml）。"""
    import xml.etree.ElementTree as ET
    out = {}
    for it in ET.parse(xml_path).getroot().iter('item'):
        if it.get('type') != 'DefineSpriteTag':
            continue
        sid = int(it.get('spriteId'))
        places, frame = [], 0
        subs = it.find('subTags')
        if subs is None:
            out[sid] = places
            continue
        for s in subs:
            t = s.get('type')
            if t == 'ShowFrameTag':
                frame += 1
            elif t in ('PlaceObject2Tag', 'PlaceObject3Tag'):
                m = s.find('matrix')
                mm = (1.0, 0.0, 0.0, 1.0, 0.0, 0.0)
                if m is not None:
                    mm = (float(m.get('scaleX', 1)), float(m.get('rotateSkew0', 0)),
                          float(m.get('rotateSkew1', 0)), float(m.get('scaleY', 1)),
                          float(m.get('translateX', 0)) / 20.0,
                          float(m.get('translateY', 0)) / 20.0)
                places.append({'frame': frame + 1, 'depth': int(s.get('depth', 0)),
                               'chid': int(s.get('characterId')) if s.get('characterId') else None,
                               'm': mm})
        out[sid] = places
    return out


def first_frame_places(PL, sid):
    """取 sprite 第 1 帧生效的 place（按 depth 状态机）。"""
    st = {}
    for pl in PL.get(sid, []):
        if pl['frame'] > 1 or not pl['chid']:
            continue
        st[pl['depth']] = (pl['chid'], pl['m'])
    return [(d, st[d][0], st[d][1]) for d in sorted(st)]


def main():
    xml_path, swf_path, root, out = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
    S, shapes, sprites = load_swf(swf_path)
    PL = parse_places(xml_path)

    def bounds(cid):
        return S.char_bounds(cid, shapes, sprites, {})

    pet1 = frames(root, PET, 1)[0]
    body1 = frames(root, BODY, 1)[0]
    color1 = frames(root, COLOR2, 1)[0]
    W, H = pet1.size

    # body / color 在 pet 中的位置：vw/height 相同画布族，直接按内容 bbox 关系对齐
    pb, bb, cb = box(pet1), box(body1), box(color1)
    # pet 画布 = 两层并集的渲染；用 body 的底边与 pet 底边对齐、水平居中
    bPet, bBody, bColor = bounds(PET), bounds(BODY), bounds(COLOR2)
    body_dx = (bBody[0] - bPet[0]) * ZOOM
    body_dy = (bBody[1] - bPet[1]) * ZOOM
    color_dx = (bColor[0] - bPet[0]) * ZOOM
    color_dy = (bColor[1] - bPet[1]) * ZOOM
    print(f'pet{pb} body@({body_dx:.1f},{body_dy:.1f}) color@({color_dx:.1f},{color_dy:.1f})')

    meta = {'canvas': list(CANVAS), 'dirs': {}, 'method': 'matrix-relative + union fit'}
    os.makedirs(out, exist_ok=True)
    for name, (b2, c2, mirror) in DIRS.items():
        def build(two_frame, outer_s, parent_png, pdx, pdy, tag):
            kids = first_frame_places(PL, two_frame)
            kids = [(d, c, m) for d, c, m in kids if c in sprites]
            if not kids:
                raise SystemExit(f'{name}: {two_frame} 无子 sprite')
            ref_c, ref_m = kids[0][1], kids[0][2]
            ref_b = bounds(ref_c)
            b0x = ref_m[4] * ZOOM + 0
            layers = []
            for d, c, m in kids:
                im1 = frames(root, c, 1)[0]
                s = outer_s * m[0]
                # 相对基准层的平移（1x），乘 ZOOM
                dx = ((m[4] - ref_m[4])) * ZOOM
                dy = ((m[5] - ref_m[5])) * ZOOM
                layers.append((c, s, dx, dy, im1))
            # 先按相对位置摆到临时画布，求并集
            tW, tH = 2000, 2000
            tmp = Image.new('RGBA', (tW, tH), (0, 0, 0, 0))
            placed = []
            for c, s, dx, dy, im1 in layers:
                im = scaled(frames(root, c, 1)[0], s)
                x = int(round(1000 + dx)); y = int(round(1000 + dy))
                tmp.alpha_composite(im, (x, y))
                placed.append((c, s, x, y))
            tb = box(tmp)
            # 整体归位：并集左上 → 父层内容 bbox 左上
            sdx = parent_png[0] - tb[0]
            sdy = parent_png[1] - tb[1]
            return [(c, s, x + sdx, y + sdy) for c, s, x, y in placed]

        def seg_frame(sprite_id, target):
            fr = 1
            for it in ET.parse(xml_path).getroot().iter('item'):
                if it.get('type') == 'DefineSpriteTag' and it.get('spriteId') == str(sprite_id):
                    subs = it.find('subTags'); f = 0
                    for tag in subs:
                        if tag.get('type') == 'ShowFrameTag':
                            f += 1
                        elif tag.get('type') == 'FrameLabelTag' and tag.get('name') == target:
                            return f + 1
            return fr
        fb = seg_frame(BODY, name)
        fc = seg_frame(COLOR2, name)
        body_ref = frames(root, BODY, fb)[fb - 1] if fb > 1 else body1
        color_ref = frames(root, COLOR2, fc)[fc - 1] if fc > 1 else color1
        outer_sb = next(p['m'][0] for p in PL[BODY] if p['chid'] == b2)
        outer_sc = next(p['m'][0] for p in PL[COLOR2] if p['chid'] == c2)
        bl = build(b2, outer_sb, box(body_ref), body_dx, body_dy, 'body')
        cl = build(c2, outer_sc, box(color_ref), color_dx, color_dy, 'color')
        # body/color 层在 pet 中的整体偏移
        bl = [(c, s, x + body_dx, y + body_dy) for c, s, x, y in bl]
        cl = [(c, s, x + color_dx, y + color_dy) for c, s, x, y in cl]

        ddir = os.path.join(out, name)
        os.makedirs(ddir, exist_ok=True)
        for i in range(17):
            canvas = Image.new('RGBA', (W, H), (0, 0, 0, 0))
            for c, s, x, y in cl + bl:
                seq = frames(root, c, 17)
                if i >= len(seq):
                    continue
                canvas.alpha_composite(scaled(seq[i], s), (int(x), int(y)))
            if mirror:
                canvas = canvas.transpose(Image.FLIP_LEFT_RIGHT)
            canvas.resize(CANVAS, Image.LANCZOS).save(os.path.join(ddir, f'{i + 1:02d}.png'))
        meta['dirs'][name] = {'body_layers': len(bl), 'color_layers': len(cl), 'mirror': mirror}
        print(f'{name}: body{len(bl)} color{len(cl)}')

    with open(os.path.join(out, 'idle17.json'), 'w', encoding='utf-8') as f:
        json.dump(meta, f, ensure_ascii=False, indent=2)
    print('done', out)


if __name__ == '__main__':
    main()
