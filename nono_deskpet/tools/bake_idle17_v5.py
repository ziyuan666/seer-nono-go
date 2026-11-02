#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""bake_idle17_v5.py —— 直接导出「容器 sprite 整层」的 17 帧，不再手工叠层。

v4 把 color(39) 与 body(116) 分别导出再按 bbox 对齐叠加，层间 1~2px 的锚点误差
会在侧向视角露出白缝（用户看到的"割裂"）。v5 换成：把容器 sprite（plain 的
species 127、超能形态的根 sprite）也一起冻结拉长到 17 帧，让 ffdec 一次性渲染
完整组合，深度/边缘/描边全部由播放器保证，不存在层间拼缝。

用法：
    python3 bake_idle17_v5.py <swf> <原sprite导出目录|-> <输出目录> <根sprite> [方向...]
"""
import glob
import json
import os
import shutil
import subprocess
import sys

from PIL import Image

ZOOM = int(os.environ.get('NONO_ZOOM', '8'))
DIRS = ['down', 'leftdown', 'left', 'leftup', 'up', 'rightup', 'right', 'rightdown']
TOOLS = os.path.dirname(os.path.abspath(__file__))
FFDEC = 'toolchain/ffdec/ffdec-cli.jar'


def run(cmd):
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        raise SystemExit(f'失败: {" ".join(cmd)}\n{r.stderr[-600:]}')
    return r.stdout


def sprite_dir(root, cid):
    for n in sorted(os.listdir(root)):
        if n == f'DefineSprite_{cid}' or n.startswith(f'DefineSprite_{cid}_'):
            return os.path.join(root, n)
    return None


def frames(root, cid, n=None):
    d = sprite_dir(root, cid)
    if not d:
        raise SystemExit(f'missing sprite {cid} in {root}')
    fs = sorted((f for f in os.listdir(d) if f.endswith('.png')), key=lambda x: int(x.split('.')[0]))
    if n:
        fs = fs[:n]
    return [Image.open(os.path.join(d, f)).convert('RGBA') for f in fs]


def box(im):
    b = im.getbbox()
    if b is None:
        raise SystemExit('empty frame')
    return b


def _paste(canvas, im, x, y):
    """把 im 贴到 canvas 的 (x, y)，允许负坐标（超出部分裁掉）而不抛异常。"""
    cx, cy = max(0, -x), max(0, -y)
    if cx or cy:
        im = im.crop((cx, cy, im.width, im.height))
        x, y = max(0, x), max(0, y)
    if x >= canvas.width or y >= canvas.height:
        return
    w = min(im.width, canvas.width - x)
    h = min(im.height, canvas.height - y)
    canvas.alpha_composite(im.crop((0, 0, w, h)), (x, y))


def analyze(swf):
    """返回 (spriteTags: {id: (frames, labels)}, places: {parent: [(depth, child)]})。"""
    sys.path.insert(0, TOOLS)
    from extend_hold_frames import read_swf, raw_tags
    data, _ = read_swf(swf)
    nb = data[8] >> 3
    pos = 8 + (5 + nb * 4 + 7) // 8 + 4
    sprites, parents = {}, {}
    for code, pl in ((t[2], t[3]) for t in raw_tags(data, pos)):
        if code == 39 and len(pl) >= 4:
            sid = int.from_bytes(pl[0:2], 'little')
            f, labels, kids = 0, {}, []
            for c2, p2 in ((t[2], t[3]) for t in raw_tags(pl, 4)):
                if c2 == 43:
                    labels[p2.split(b'\x00')[0].decode('utf-8', 'replace')] = f
                elif c2 == 1:
                    f += 1
                elif c2 in (26, 70) and p2:
                    fl = p2[0]
                    if (fl >> 1) & 1:
                        kids.append((int.from_bytes(p2[1:3], 'little'), int.from_bytes(p2[3:5], 'little')))
            sprites[sid] = (f, labels, kids)
        if code == 0:
            break
    for sid, (f, labels, kids) in sprites.items():
        for depth, child in kids:
            parents.setdefault(child, []).append(sid)
    return sprites, parents


def main():
    swf, orig_root, out = sys.argv[1], sys.argv[2], sys.argv[3]
    root_arg = sys.argv[4] if len(sys.argv) > 4 else 'auto'
    want = sys.argv[5:] or DIRS
    sprites, parents = analyze(swf)

    def desc(cid, seen=None):
        """递归取全部后代 sprite。

        2026-10-24 修：旧版 `if depth < 1` 只往下走两层，导致 super 形态里
        「根→foot→63帧层→8方向层」这种第 4 层的 8 方向 sprite（如 super2 的
        31/73/87）没被 hold，渲染 17 帧时它们照原时间轴推进，第 8 帧就跨到下一个
        方向段，表现为「前 7 帧正常、第 8 帧起变形/掉脸」。必须递归到底。
        """
        seen = seen if seen is not None else set()
        out = []
        for _d, c in sprites.get(cid, (0, {}, []))[2]:
            if c in seen:
                continue
            seen.add(c)
            out.append(c)
            out += desc(c, seen)
        return out

    # 根：未被任何 place 引用、且子孙里带最多「8 方向标签层」的 sprite。
    # super2 这类形态的 63 帧层不是根的直接子层，允许向下一层找。
    best = None
    for cand in sprites:
        if cand in parents:
            continue
        layers = [c for c in desc(cand) if c in sprites and len(sprites[c][1]) == 8]
        if layers and (best is None or len(layers) > len(best[1])):
            best = (cand, layers)
    if best is None:
        raise SystemExit('找不到根 sprite')
    root_id = best[0] if root_arg == 'auto' else int(root_arg)
    dir_layers = sorted(set(best[1]))
    only = set(desc(root_id))
    two_frame = [sid for sid, (f, labels, _) in sprites.items() if f == 2 and (sid in only or sid in dir_layers)]
    print(f'auto root={root_id} dirLayers={dir_layers}', file=sys.stderr)
    print(f'root={root_id} dirLayers={sorted(dir_layers)} twoFrame={sorted(two_frame)}')

    base = '/tmp/v5base.swf'
    run([sys.executable, os.path.join(TOOLS, 'truncate_sprite.py'), swf, base, '1',
         *[str(x) for x in sorted(two_frame)]] if two_frame else
        ['cp', swf, base])

    # 基准：原始 SWF 里根 sprite 的绝对 bounds（= down 段），以及它的导出画布尺寸。
    # ffdec 的 sprite 导出画布左上 ≈ bounds 左上 - 4.5px（描边外扩），用 bounds 差
    # 定位时这点误差在最终 253x270 缩放下小于 0.5px，不处理。
    import swf_bounds as SB
    oshapes, osprites = SB.load(swf)
    base_b = SB.char_bounds(root_id, oshapes, osprites, {})
    pet_ref = None
    if orig_root != '-':
        pet_ref = frames(orig_root, root_id, 1)[0]

    # 旧素材的 down 图是同一个「方向段首帧」，用它当基准把缩放与位置一次定下来，
    # 换素材后窗口里的落点不会跳。没有旧素材时退回 pet 原图 / bounds 推算。
    ref_old = None
    old_dir = os.environ.get('NONO_OLD_DIR', '')
    if old_dir:
        p = os.path.join(old_dir, 'down', '01.png')
        if os.path.exists(p):
            ref_old = Image.open(p).convert('RGBA')

    stage = []
    for name in want:
        hold = f'/tmp/v5hold_{name}.swf'
        args = [sys.executable, os.path.join(TOOLS, 'extend_hold_frames.py'), base, hold, '17']
        args += [f'{sid}:{name}' for sid in sorted(dir_layers)]
        args += [f'{root_id}:*']
        run(args)
        exp = f'/tmp/v5exp_{name}'
        shutil.rmtree(exp, ignore_errors=True)
        run(['java', '-jar', FFDEC, '-zoom', str(ZOOM), '-selectid', str(root_id),
             '-export', 'sprite', exp, hold])
        hshapes, hsprites = SB.load(hold)
        hb = SB.char_bounds(root_id, hshapes, hsprites, {})
        fr = frames(exp, root_id, 17)
        if len(fr) != 17:
            raise SystemExit(f'{name}: {len(fr)} 帧')
        stage.append((name, fr, hb))
        print(f'{name}: frames=17 bounds={[round(v, 2) for v in hb]}')

    name0, fr0, hb0 = stage[0]
    b0 = box(fr0[0])
    if ref_old is not None:
        rb = box(ref_old)
        k = ((rb[2] - rb[0]) / (b0[2] - b0[0]) + (rb[3] - rb[1]) / (b0[3] - b0[1])) / 2
        cw, ch = ref_old.size
        off0 = (rb[0] - b0[0] * k, rb[1] - b0[1] * k)
        print(f'scale={k:.4f} canvas={cw}x{ch} off0={off0}')
    else:
        k = 1.0
        cw, ch = (round((base_b[2] - base_b[0]) * ZOOM), round((base_b[3] - base_b[1]) * ZOOM))
        off0 = (0.0, 0.0)
    meta = {'root': root_id, 'scale': round(k, 4), 'canvas': [cw, ch], 'dirs': {}}
    os.makedirs(out, exist_ok=True)
    for name, fr, hb in stage:
        ox = off0[0] + (hb[0] - hb0[0]) * ZOOM * k
        oy = off0[1] + (hb[1] - hb0[1]) * ZOOM * k
        meta['dirs'][name] = {'bounds': [round(v, 2) for v in hb], 'off': [round(ox, 1), round(oy, 1)]}
        ddir = os.path.join(out, name)
        os.makedirs(ddir, exist_ok=True)
        for i, im in enumerate(fr):
            if abs(k - 1.0) > 1e-6:
                im = im.resize((max(1, round(im.width * k)), max(1, round(im.height * k))), Image.LANCZOS)
            canvas = Image.new('RGBA', (cw, ch), (0, 0, 0, 0))
            _paste(canvas, im, int(round(ox)), int(round(oy)))
            canvas.save(os.path.join(ddir, f'{i + 1:02d}.png'))
    with open(os.path.join(out, 'meta_v5.json'), 'w', encoding='utf-8') as f:
        json.dump(meta, f, ensure_ascii=False, indent=2)
    print('done', out)


if __name__ == '__main__':
    main()
