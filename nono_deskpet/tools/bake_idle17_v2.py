#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""bake_idle17_v2.py —— 用「逐层模板匹配」把内层 17 帧放回 pet 画布。

为什么不用 bbox 对齐：部分方向（up / leftup）由多个部件 sprite 叠加而成，
各部件画布尺寸差异极大（char99 是 890×1749，父层 char115 只有 401×580），
bbox 对齐会整体错位。这里改为：
    1. 子层按 XML 里该 PlaceObject 的 matrix scale 缩放；
    2. 在父层第 1 帧的渲染图里做模板匹配求偏移；
    3. 逐级累加到 pet 坐标；
    4. 叠加全部子层（按 depth），得到 17 帧。

用法：
    python3 bake_idle17_v2.py <nono.xml> <sprite导出目录> <输出目录>
"""
import json
import os
import sys
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
                sx = float(m.get('scaleX', 1)) if m is not None else 1.0
                sy = float(m.get('scaleY', 1)) if m is not None else 1.0
                places.append({'frame': frame + 1, 'depth': int(s.get('depth', 0)),
                               'chid': int(s.get('characterId')) if s.get('characterId') else None,
                               'sx': sx, 'sy': sy})
        out[sid] = {'frames': int(it.get('frameCount')), 'labels': labels, 'places': places}
    return out


def sprite_dir(root, chid):
    for name in sorted(os.listdir(root)):
        if name == f'DefineSprite_{chid}' or name.startswith(f'DefineSprite_{chid}_'):
            return os.path.join(root, name)
    raise SystemExit(f'找不到 sprite {chid}')


def frames(root, chid, n=None, start=1):
    d = sprite_dir(root, chid)
    files = sorted((f for f in os.listdir(d) if f.endswith('.png')),
                   key=lambda x: int(x.split('.')[0]))
    files = files[start - 1:]
    if n:
        files = files[:n]
    return [Image.open(os.path.join(d, f)).convert('RGBA') for f in files]


def arr(im):
    return np.asarray(im).astype(np.int16)


def match(ref_im, layer_im, step=1):
    """在 ref 中找 layer，返回 layer 左上角在 ref 坐标中的 (dx,dy)。"""
    R, L = arr(ref_im), arr(layer_im)
    swap = False
    if L.shape[0] > R.shape[0] or L.shape[1] > R.shape[1]:
        R, L = L, R
        swap = True
    H, W = R.shape[:2]
    h, w = L.shape[:2]
    mask = L[:, :, 3] > 128
    if mask.sum() < 20:
        return None
    ys, xs = np.where(mask)
    lcol = L[ys, xs, :3].astype(np.int32)
    best = None
    for dy in range(0, H - h + 1, step):
        for dx in range(0, W - w + 1, step):
            win = R[dy:dy + h, dx:dx + w]
            if int(win[:, :, 3].max()) < 20:
                continue
            err = np.abs(win[ys, xs, :3].astype(np.int32) - lcol).mean()
            if best is None or err < best[0]:
                best = (err, dx, dy)
    if best is None:
        return None
    if swap:
        return (-best[1], -best[2])
    return (best[1], best[2])


def scaled(im, s):
    """按 matrix scale 变换：负值表示水平镜像（SWF 常用 -scaleX 做左右对称）。"""
    if s < 0:
        im = im.transpose(Image.FLIP_LEFT_RIGHT)
    k = abs(s)
    if abs(k - 1.0) < 1e-6:
        return im
    return im.resize((max(1, round(im.width * k)), max(1, round(im.height * k))), Image.LANCZOS)


def main():
    xml, root, out = sys.argv[1], sys.argv[2], sys.argv[3]
    sp = parse(xml)
    pet1 = frames(root, PET, 1)[0]
    W, H = pet1.size
    body1 = frames(root, BODY, 1)[0]
    color1 = frames(root, COLOR2, 1)[0]

    # 外层在 pet 中的偏移（用 body 层匹配；color 层用同样偏移）
    off_body = match(down4(pet1), down4(body1))
    off_color = match(down4(pet1), down4(color1))
    print('pet<-body', off_body, 'pet<-color', off_color)
    if off_body is None or off_color is None:
        raise SystemExit('外层匹配失败')

    meta = {'canvas': list(CANVAS), 'dirs': {}, 'method': 'template-match chain'}
    os.makedirs(out, exist_ok=True)
    for name, (b2, c2, mirror) in DIRS.items():
        def chain(layer2, parent1, off_parent, sprite_outer):
            """返回 [(frames列表, (dx,dy) 在 pet 8x 坐标)]"""
            sub = sp[layer2]
            kids = [p for p in sub['places'] if p['frame'] == 1 and p['chid'] in sp]
            ref = frames(root, layer2, 1)[0]
            res = []
            for k in sorted(kids, key=lambda z: z['depth']):
                kid1 = frames(root, k['chid'], 1)[0]
                kl = scaled(kid1, k['sx'])
                off = match(down4(ref), down4(kl))
                if off is None:
                    print(f'  warn {name}: 匹配失败 chid={k["chid"]}')
                    continue
                dx = (off[0] + off_parent[0]) * 4
                dy = (off[1] + off_parent[1]) * 4
                res.append((frames(root, k['chid'], 17), (dx, dy), k['sx']))
            return res

        # outer: 116 -> 2帧层
        os_ = [p for p in sp[BODY]['places'] if p['chid'] == b2]
        if not os_:
            raise SystemExit(f'{name}: body 2帧层 {b2} 不在 116 里')
        out_sx = os_[0]['sx']
        b2img = frames(root, b2, 1)[0]
        off_b2 = match(down4(body1), down4(scaled(b2img, out_sx)))
        if off_b2 is None:
            raise SystemExit(f'{name}: body 2帧层匹配失败')
        off_b2 = (off_b2[0] + off_body[0] // 4, off_b2[1] + off_body[1] // 4)

        oc_ = [p for p in sp[COLOR2]['places'] if p['chid'] == c2]
        if not oc_:
            raise SystemExit(f'{name}: color 2帧层 {c2} 不在 39 里')
        out_sx_c = oc_[0]['sx']
        c2img = frames(root, c2, 1)[0]
        off_c2 = match(down4(color1), down4(scaled(c2img, out_sx_c)))
        if off_c2 is None:
            raise SystemExit(f'{name}: color 2帧层匹配失败')
        off_c2 = (off_c2[0] + off_color[0] // 4, off_c2[1] + off_color[1] // 4)

        bl = chain(b2, body1, off_b2, b2)
        cl = chain(c2, color1, off_c2, c2)
        ddir = os.path.join(out, name)
        os.makedirs(ddir, exist_ok=True)
        for i in range(17):
            canvas = Image.new('RGBA', (W, H), (0, 0, 0, 0))
            for seq, off, sx in cl + bl:
                if i >= len(seq):
                    continue
                im = scaled(seq[i], sx)
                canvas.alpha_composite(im, (off[0], off[1]))
            if mirror:
                canvas = canvas.transpose(Image.FLIP_LEFT_RIGHT)
            canvas.resize(CANVAS, Image.LANCZOS).save(os.path.join(ddir, f'{i + 1:02d}.png'))
        meta['dirs'][name] = {'color_layers': len(cl), 'body_layers': len(bl),
                              'offsets': [list(map(int, o)) for _s, o, _x in cl + bl],
                              'mirror': mirror}
        print(f'{name}: color层{len(cl)} body层{len(bl)} off_b2={off_b2} off_c2={off_c2}')

    with open(os.path.join(out, 'idle17.json'), 'w', encoding='utf-8') as f:
        json.dump(meta, f, ensure_ascii=False, indent=2)
    print('done')


def down4(im):
    """降采样到 1/4，模板匹配提速（仍保留足够纹理）。"""
    return im.resize((max(1, im.width // 4), max(1, im.height // 4)), Image.LANCZOS)


if __name__ == '__main__':
    main()
