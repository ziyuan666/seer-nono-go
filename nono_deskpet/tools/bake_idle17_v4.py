#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""bake_idle17_v4.py —— 用 ffdec 的「冻结拉长」导出完整 17 帧，再合成到 pet 画布。

流程（每个方向）：
  1. 基础 SWF：把所有 2 帧层（站姿/走姿）截成 1 帧，避免半帧空白；
  2. 按方向 hold：body(116)/color(39) 冻结在目标段首帧并拉长到 17 帧；
  3. ffdec -export sprite 导出，得到该方向 17 帧的 **body 层整层渲染** 与
     **color 层整层渲染**（部件相对位置由 ffdec 保证，手工不再拼）；
  4. 用「hold 第 1 帧 ↔ 原 SWF 对应段首帧」的 bbox 差求回原坐标系偏移；
  5. 叠加到 pet 画布并缩放到 253×270。

用法：
    python3 bake_idle17_v4.py <原swf> <原sprite导出目录> <输出目录> [方向...]
"""
import glob
import json
import os
import shutil
import struct
import subprocess
import sys
import zlib

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
TOOLS = os.path.dirname(os.path.abspath(__file__))
FFDEC = 'toolchain/ffdec/ffdec-cli.jar'
JAVA = 'java'


def run(cmd):
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        raise SystemExit(f'命令失败: {" ".join(cmd)}\n{r.stderr[-800:]}')
    return r.stdout


def read_swf(path):
    raw = open(path, 'rb').read()
    if raw[:3] == b'CWS':
        return raw[:8] + zlib.decompress(raw[8:]), b'CWS'
    return raw, raw[:3]


def load_swf_bounds(path):
    sys.path.insert(0, TOOLS)
    import swf_bounds as S
    shapes, sprites = S.load(path)
    return S, shapes, sprites


def sprite_dir(root, cid):
    for n in sorted(os.listdir(root)):
        if n == f'DefineSprite_{cid}' or n.startswith(f'DefineSprite_{cid}_'):
            return os.path.join(root, n)
    raise SystemExit(f'missing {cid}')


def frames(root, cid, n=None):
    d = sprite_dir(root, cid)
    fs = sorted((f for f in os.listdir(d) if f.endswith('.png')), key=lambda x: int(x.split('.')[0]))
    if n:
        fs = fs[:n]
    return [Image.open(os.path.join(d, f)).convert('RGBA') for f in fs]


def box(im):
    b = im.getbbox()
    if b is None:
        raise SystemExit('empty')
    return b


def fit(im, calib):
    k, dx, dy = calib['k'], calib['dx'], calib['dy']
    if abs(k - 1.0) > 1e-6:
        im = im.resize((max(1, round(im.width * k)), max(1, round(im.height * k))), Image.LANCZOS)
    out = Image.new('RGBA', im.size, (0, 0, 0, 0))
    out.alpha_composite(im, (int(round(dx)), int(round(dy))))
    return out


def seg_frame(xml_labels, name):
    return xml_labels.get(name, 1)


def parse_labels(swf_path):
    """直接扫 SWF 的 FrameLabel，返回 116 与 39 的 {方向: 帧号}。"""
    data, _ = read_swf(swf_path)
    nb = struct.unpack('<B', data[8:9])[0] >> 3
    pos = 8 + (5 + nb * 4 + 7) // 8 + 4

    def tags(p, d):
        while p + 2 <= len(d):
            cl = struct.unpack('<H', d[p:p + 2])[0]
            code, ln = cl >> 6, cl & 0x3F
            p += 2
            if ln == 0x3F:
                ln = struct.unpack('<I', d[p:p + 4])[0]
                p += 4
            yield code, d[p:p + ln]
            p += ln
            if code == 0:
                return

    # root 帧标签
    root_labels, f = {}, 0
    for code, pl in tags(pos, data):
        if code == 43:
            root_labels.setdefault(pl.split(b'\x00')[0].decode(), f)
        elif code == 1:
            f += 1
    out = {}
    for code, pl in tags(pos, data):
        if code == 39 and len(pl) >= 4:
            sid = struct.unpack('<H', pl[0:2])[0]
            if sid in (116, 39):
                lab, f = {}, 0
                for c2, p2 in tags(4, pl):
                    if c2 == 43:
                        lab[p2.split(b'\x00')[0].decode()] = f
                    elif c2 == 1:
                        f += 1
                out[sid] = lab
        if code == 0:
            break
    return out


def main():
    swf, orig_root, out = sys.argv[1], sys.argv[2], sys.argv[3]
    want = sys.argv[4:] or list(DIRS)
    S, shapes, sprites = load_swf_bounds(swf)
    bPet = S.char_bounds(PET, shapes, sprites, {})
    bBody = S.char_bounds(BODY, shapes, sprites, {})
    bColor = S.char_bounds(COLOR2, shapes, sprites, {})
    pet_off_body = ((bBody[0] - bPet[0]) * ZOOM, (bBody[1] - bPet[1]) * ZOOM)
    pet_off_color = ((bColor[0] - bPet[0]) * ZOOM, (bColor[1] - bPet[1]) * ZOOM)
    labels = parse_labels(swf)
    pet1 = frames(orig_root, PET, 1)[0]
    W, H = pet1.size
    print('pet', pet1.size, 'off_body', pet_off_body, 'off_color', pet_off_color)

    skip_export = os.environ.get('SKIP_EXPORT') == '1'
    base = '/tmp/v4_base.swf'
    run([sys.executable, os.path.join(TOOLS, 'truncate_sprite.py'), swf, base, '1',
         '52', '68', '94', '115', '9', '20', '26', '38'])

    meta = {'canvas': list(CANVAS), 'dirs': {}}
    os.makedirs(out, exist_ok=True)
    # 先用 down 建立起绝对基准：把合成结果的内容 bbox 校准到 pet 第 1 帧
    calib = {'k': 1.0, 'dx': 0.0, 'dy': 0.0}
    if 'down' in DIRS:
        pass
    for name in want:
        b2, c2, mirror = DIRS[name]
        hold = f'/tmp/v4_hold_{name}.swf'
        if not os.path.exists(hold) or os.environ.get('REGEN_HOLD') == '1':
            run([sys.executable, os.path.join(TOOLS, 'extend_hold_frames.py'), base, hold, '17',
                 f'{BODY}:{name}', f'{COLOR2}:{name}'])
        exp = f'/tmp/v4_exp_{name}'
        if not skip_export:
            shutil.rmtree(exp, ignore_errors=True)
            run([JAVA, '-jar', FFDEC, '-zoom', str(ZOOM), '-export', 'sprite', exp, hold])
        hb = frames(exp, BODY, 17)
        hc = frames(exp, COLOR2, 17)
        if len(hb) != 17 or len(hc) != 17:
            raise SystemExit(f'{name}: hold 帧数 body={len(hb)} color={len(hc)}')
        ob = frames(orig_root, BODY, 99)[seg_frame(labels[BODY], name)]
        oc = frames(orig_root, COLOR2, 99)[seg_frame(labels[COLOR2], name)]
        ddx = box(ob)[0] - box(hb[0])[0]
        ddy = box(ob)[1] - box(hb[0])[1]
        cdx2 = box(oc)[0] - box(hc[0])[0]
        cdy2 = box(oc)[1] - box(hc[0])[1]
        bx = int(round(pet_off_body[0] + ddx)); by = int(round(pet_off_body[1] + ddy))
        cx = int(round(pet_off_color[0] + cdx2)); cy = int(round(pet_off_color[1] + cdy2))
        ddir = os.path.join(out, name)
        os.makedirs(ddir, exist_ok=True)
        for i in range(17):
            # 原版深度：pet 下 color(39) 在外层、body(116) 在内层。color 层中间是
            # 镂空，body 的黑面板从镂空透出；反过来会把内部零件盖在外壳上。
            canvas = Image.new('RGBA', (W, H), (0, 0, 0, 0))
            canvas.alpha_composite(hb[i], (bx, by))
            canvas.alpha_composite(hc[i], (cx, cy))
            if mirror:
                canvas = canvas.transpose(Image.FLIP_LEFT_RIGHT)
            if name == 'down' and i == 0 and calib['k'] == 1.0:
                mb = box(canvas); pb2 = box(pet1)
                k = ((pb2[2] - pb2[0]) / (mb[2] - mb[0]) + (pb2[3] - pb2[1]) / (mb[3] - mb[1])) / 2
                calib['k'] = k
                calib['dx'] = pb2[0] - mb[0] * k
                calib['dy'] = pb2[1] - mb[1] * k
                print('calib', calib)
            canvas = fit(canvas, calib)
            canvas.resize(CANVAS, Image.LANCZOS).save(os.path.join(ddir, f'{i + 1:02d}.png'))
        meta['dirs'][name] = {'off_body': [bx, by], 'off_color': [cx, cy], 'mirror': mirror}
        print(f'{name}: body@{bx},{by} color@{cx},{cy} seg={seg_frame(labels[BODY], name)}')

    with open(os.path.join(out, 'idle17.json'), 'w', encoding='utf-8') as f2:
        json.dump(meta, f2, ensure_ascii=False, indent=2)
    print('done', out)


if __name__ == '__main__':
    main()
