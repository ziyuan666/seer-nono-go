#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""bake_idle17.py —— 按游戏原版的「内层 17 帧」重烘待机动画。

为什么必须换层：
    body/color 层的 63 帧是「8 方向 × 7~8 帧」的外层时间轴，游戏里被
    NonoModel.changedir() 用 gotoAndStop(方向) 停住；真正持续播放的是段首帧里
    嵌套的 17 帧子 sprite（char49/64/71/99 与 char4/16/24/34）。之前的素材取的是
    外层 7~8 帧，循环周期只有原版的 1/2.4，所以看着「快很多」。

做法：把内层 17 帧按 SWF 里的父级 scale 缩放后，用内容 bbox 对齐到父层，
再合成到 pet 画布，最后缩到 253×270（与既有 assets 一致）。

用法：
    python3 bake_idle17.py <导出目录> <输出目录>
"""
import json
import os
import sys
from PIL import Image

ZOOM = 8
CANVAS = (253, 270)

# 方向 → (body内层, color内层, 父级scaleX, 是否水平镜像)
DIRS = {
    'down':      (49, 4,  -0.708786, False),
    'leftdown':  (64, 16, -0.717621, False),
    'left':      (64, 16, -0.717621, False),
    'leftup':    (71, 24, -0.734360, False),
    'up':        (99, 34, -0.720398, False),
    'rightup':   (71, 24,  0.734360, True),
    'right':     (64, 16,  0.717621, True),
    'rightdown': (64, 16,  0.717621, True),
}

# 父层 sprite（用于给出 bbox 参照）
PARENT_BODY = 116
PARENT_COLOR = 39
PET = 127


def sprite_dir(root, chid):
    for name in sorted(os.listdir(root)):
        if name == f'DefineSprite_{chid}' or name.startswith(f'DefineSprite_{chid}_'):
            return os.path.join(root, name)
    raise SystemExit(f'找不到 sprite {chid}')


def frames(d, n):
    out = []
    for i in range(1, n + 1):
        p = os.path.join(d, f'{i}.png')
        if not os.path.exists(p):
            break
        out.append(Image.open(p).convert('RGBA'))
    return out


def content_box(im):
    bb = im.getbbox()
    if bb is None:
        raise SystemExit('空图')
    return bb


def scaled(im, s):
    w = max(1, round(im.width * s))
    h = max(1, round(im.height * s))
    return im.resize((w, h), Image.LANCZOS)


def align_to_parent(layer, parent, scale):
    """把 layer 按 scale 缩放后，用内容 bbox 对齐到 parent，返回 (img, dx, dy)。"""
    s = scaled(layer, scale)
    lb = content_box(s)
    pb = content_box(parent)
    dx = pb[0] - lb[0]
    dy = pb[1] - lb[1]
    return s, dx, dy


def main():
    root, out = sys.argv[1], sys.argv[2]
    dpet = sprite_dir(root, PET)
    dbody = sprite_dir(root, PARENT_BODY)
    dcolor = sprite_dir(root, PARENT_COLOR)
    pet = frames(dpet, 1)[0]
    body_parent = frames(dbody, 1)[0]
    color_parent = frames(dcolor, 1)[0]
    W, H = pet.size

    # 父层在 pet 中的落位
    # color39 -> pet：translate(2,2) twips ≈ 0，左上角对齐
    cb = content_box(color_parent)
    pb = content_box(pet)
    color_dx, color_dy = pb[0] - cb[0], pb[1] - cb[1]
    # body116 -> pet：水平居中、底部对齐
    bb = content_box(body_parent)
    body_dx = (pb[0] + pb[2]) / 2 - (bb[0] + bb[2]) / 2
    body_dy = pb[3] - bb[3]
    body_dx, body_dy = int(round(body_dx)), int(round(body_dy))
    print(f'pet={pet.size} color@{color_dx},{color_dy} body@{body_dx},{body_dy}')

    meta = {'canvas': list(CANVAS), 'zoom': ZOOM, 'dirs': {}, 'note': 'inner 17-frame idle'}
    os.makedirs(out, exist_ok=True)
    for name, (bid, cid, sx, mirror) in DIRS.items():
        bframes = frames(sprite_dir(root, bid), 17)
        cframes = frames(sprite_dir(root, cid), 17)
        if len(bframes) != 17 or len(cframes) != 17:
            raise SystemExit(f'{name}: 帧数不足 body={len(bframes)} color={len(cframes)}')
        scale = abs(sx)
        # 内层在父层中的落位（用第 1 帧求一次，整段复用，避免逐帧抖动）
        _, bdx, bdy = align_to_parent(bframes[0], body_parent, scale)
        _, cdx, cdy = align_to_parent(cframes[0], color_parent, scale)
        bdx += body_dx; bdy += body_dy
        cdx += color_dx; cdy += color_dy
        ddir = os.path.join(out, name)
        os.makedirs(ddir, exist_ok=True)
        for i in range(17):
            canvas = Image.new('RGBA', (W, H), (0, 0, 0, 0))
            canvas.alpha_composite(scaled(cframes[i], scale), (cdx, cdy))
            canvas.alpha_composite(scaled(bframes[i], scale), (bdx, bdy))
            if mirror:
                canvas = canvas.transpose(Image.FLIP_LEFT_RIGHT)
            canvas = canvas.resize(CANVAS, Image.LANCZOS)
            canvas.save(os.path.join(ddir, f'{i + 1:02d}.png'))
        meta['dirs'][name] = {'frames': 17, 'body': bid, 'color': cid,
                              'scale': round(scale, 4), 'mirror': mirror,
                              'off_body': [bdx, bdy], 'off_color': [cdx, cdy]}
        print(f'{name}: body={bid} color={cid} off_body={bdx},{bdy} off_color={cdx},{cdy}')
    with open(os.path.join(out, 'idle17.json'), 'w', encoding='utf-8') as f:
        json.dump(meta, f, ensure_ascii=False, indent=2)
    print('done', out)


if __name__ == '__main__':
    main()
