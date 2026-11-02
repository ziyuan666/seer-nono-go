#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""bake_idle17_matrix.py —— 用 SWF 的矩阵链精确合成「内层 17 帧」待机动画。

为什么不能只靠 bbox 对齐：不同方向的 sprite 画布尺寸与内容分布差别很大
（如 up 方向 char99 是 890×1749 的画布，内容只有顶部弧 + 底部阴影），
按 bbox 对齐会把部件放错位。正确做法是沿 pet → 外层 → 2 帧层 → 内层
逐级复合 PlaceObject 的 MATRIX，得到内层 PNG 在 pet 坐标中的仿射变换。

用法：
    python3 bake_idle17_matrix.py <nono.xml> <sprite导出目录> <输出目录>
"""
import json
import os
import sys
import xml.etree.ElementTree as ET

from PIL import Image

ZOOM = 8
CANVAS = (253, 270)
STAGE = {
    'pet': 127, 'body': 116, 'color2': 39,
}
# 方向 → (body 2帧层, color 2帧层, 镜像)
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


def parse_matrix(el):
    a = float(el.get('scaleX', 1))
    d = float(el.get('scaleY', 1))
    b = float(el.get('rotateSkew0', 0))
    c = float(el.get('rotateSkew1', 0))
    tx = float(el.get('translateX', 0)) / 20.0
    ty = float(el.get('translateY', 0)) / 20.0
    return (a, b, c, d, tx, ty)


IDENT = (1.0, 0.0, 0.0, 1.0, 0.0, 0.0)


def mmul(m1, m2):
    """m1 · m2（先 m2 后 m1）。"""
    a1, b1, c1, d1, t1x, t1y = m1
    a2, b2, c2, d2, t2x, t2y = m2
    return (
        a1 * a2 + c1 * b2,
        b1 * a2 + d1 * b2,
        a1 * c2 + c1 * d2,
        b1 * c2 + d1 * d2,
        a1 * t2x + c1 * t2y + t1x,
        b1 * t2x + d1 * t2y + t1y,
    )


def place_of(sprites, sid, frame, depth=None, char=None):
    sp = sprites[sid]
    for pl in sp['places']:
        if pl['frame'] != frame:
            continue
        if depth is not None and pl['depth'] != depth:
            continue
        if char is not None and pl['chid'] != char:
            continue
        return pl
    return None


def parse(xml):
    out = {}
    for it in ET.parse(xml).getroot().iter('item'):
        if it.get('type') != 'DefineSpriteTag':
            continue
        sid = int(it.get('spriteId'))
        labels, places = [], []
        frame = 0
        subs = it.find('subTags')
        if subs is None:
            continue
        for s in subs:
            t = s.get('type')
            if t == 'ShowFrameTag':
                frame += 1
            elif t == 'FrameLabelTag':
                labels.append((frame + 1, s.get('name')))
            elif t in ('PlaceObject2Tag', 'PlaceObject3Tag'):
                m = s.find('matrix')
                places.append({
                    'frame': frame + 1,
                    'depth': int(s.get('depth', 0)),
                    'chid': int(s.get('characterId')) if s.get('characterId') else None,
                    'name': s.get('name') or '',
                    'm': parse_matrix(m) if m is not None else IDENT,
                })
        out[sid] = {'frames': int(it.get('frameCount')), 'labels': labels, 'places': places}
    return out


def sprite_dir(root, chid):
    for name in sorted(os.listdir(root)):
        if name == f'DefineSprite_{chid}' or name.startswith(f'DefineSprite_{chid}_'):
            return os.path.join(root, name)
    raise SystemExit(f'找不到 sprite {chid}')


def load_frames(root, chid, n=None):
    d = sprite_dir(root, chid)
    files = sorted((f for f in os.listdir(d) if f.endswith('.png')),
                   key=lambda x: int(x.split('.')[0]))
    if n:
        files = files[:n]
    return [Image.open(os.path.join(d, f)).convert('RGBA') for f in files]


def main():
    xml, root, out = sys.argv[1], sys.argv[2], sys.argv[3]
    sprites = parse(xml)
    pet_im = load_frames(root, STAGE['pet'], 1)[0]
    W, H = pet_im.size

    # 静态链：pet -> 层 -> 2帧层 -> 内层
    pet_body = place_of(sprites, STAGE['pet'], 1, depth=3)['m']
    pet_color = place_of(sprites, STAGE['pet'], 1, depth=1)['m']

    meta = {'canvas': list(CANVAS), 'zoom': ZOOM, 'dirs': {}, 'source': 'matrix chain'}
    os.makedirs(out, exist_ok=True)
    for name, (bid2, cid2, mirror) in DIRS.items():
        # body: pet -> 116 -> 2帧层 -> 内层
        sp116 = sprites[STAGE['body']]
        label_frame = next(f for f, n in sp116['labels'] if n == name)
        outer = place_of(sprites, STAGE['body'], label_frame)
        if outer is None:
            # 该段沿用上一段的 place，向前找最近的
            cand = [p for p in sp116['places'] if p['frame'] <= label_frame and p['chid']]
            outer = cand[-1]
        inners_b = [x for x in sprites[outer['chid']]['places']
                    if x['frame'] == 1 and x['chid'] in sprites]
        body_layers = [(x['chid'], mmul(pet_body, mmul(outer['m'], x['m'])))
                       for x in sorted(inners_b, key=lambda z: z['depth'])]
        body_inner_id = [x[0] for x in body_layers]

        sp39 = sprites[STAGE['color2']]
        label_frame_c = next(f for f, n in sp39['labels'] if n == name)
        outer_c = place_of(sprites, STAGE['color2'], label_frame_c)
        if outer_c is None:
            cand = [p for p in sp39['places'] if p['frame'] <= label_frame_c and p['chid']]
            outer_c = cand[-1]
        inners_c = [x for x in sprites[outer_c['chid']]['places']
                    if x['frame'] == 1 and x['chid'] in sprites]
        color_layers = [(x['chid'], mmul(pet_color, mmul(outer_c['m'], x['m'])))
                        for x in sorted(inners_c, key=lambda z: z['depth'])]
        color_inner_id = [x[0] for x in color_layers]

        bseq = [(load_frames(root, cid, 17), M) for cid, M in body_layers]
        cseq = [(load_frames(root, cid, 17), M) for cid, M in color_layers]
        for tag, seq in (('body', bseq), ('color', cseq)):
            for frames, _m in seq:
                if len(frames) != 17:
                    raise SystemExit(f'{name}: {tag} 帧数 {len(frames)}')

        ddir = os.path.join(out, name)
        os.makedirs(ddir, exist_ok=True)
        for i in range(17):
            canvas = Image.new('RGBA', (W, H), (0, 0, 0, 0))
            for seq in (cseq, bseq):
                for fr, M in seq:
                    im = fr[i]
                # PIL 的 transform 需要把目标像素反算回源：用逆矩阵
                    inv = invert(M)
                # 源图坐标单位是 ZOOM 倍，先把矩阵左乘缩放
                    inv_zoom = mmul((1.0 / ZOOM, 0, 0, 1.0 / ZOOM, 0, 0), inv)
                    a, b, c, d, tx, ty = inv_zoom
                    warped = im.transform(
                        (W, H), Image.AFFINE,
                        (a, b, tx, c, d, ty), resample=Image.BICUBIC)
                    canvas.alpha_composite(warped)
            if mirror:
                canvas = canvas.transpose(Image.FLIP_LEFT_RIGHT)
            canvas = canvas.resize(CANVAS, Image.LANCZOS)
            canvas.save(os.path.join(ddir, f'{i + 1:02d}.png'))
        meta['dirs'][name] = {'body': body_inner_id, 'color': color_inner_id,
                              'mirror': mirror}
        print(f'{name}: body={body_inner_id} color={color_inner_id} mirror={mirror}')

    with open(os.path.join(out, 'idle17.json'), 'w', encoding='utf-8') as f:
        json.dump(meta, f, ensure_ascii=False, indent=2)
    print('done', out)


def invert(m):
    a, b, c, d, tx, ty = m
    det = a * d - b * c
    if abs(det) < 1e-9:
        return IDENT
    ia, ib, ic, id_ = d / det, -b / det, -c / det, a / det
    itx = -(ia * tx + ic * ty)
    ity = -(ib * tx + id_ * ty)
    return (ia, ib, ic, id_, itx, ity)


if __name__ == '__main__':
    main()
