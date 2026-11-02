# -*- coding: utf-8 -*-
"""给动作/表情的 sprite.json 写入 canvas.bodyc（本体中心，帧 0）。

为什么需要：动作帧不能按「贴底居中」放进固定画布——各段动作的本体在各自
sprite 里的位置完全不同（super1 action_4 本体在 y≈250，action_1 在 y≈18），
贴底会让本体在开始播放的瞬间跳几十像素。运行时改为「本体中心对齐球心」，
这里负责把本体中心量出来。

本体中心的取法：tintable 层（color_*，即球壳/可染色部件）在所有帧里的 bbox
并集中心。不用整帧 bbox——特效（射线/爆炸）会把整帧 bbox 撑得很大，中心
会跑到画面外面去。

用法：
    python3 mark_bodyc.py            # 写回所有形态的 sprite.json
    python3 mark_bodyc.py --dry-run  # 只打印
"""
import json, os, re, sys

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', '..', '..', 'nono_deskpet', 'assets', 'sprites')
DRY = '--dry-run' in sys.argv


def load(d):
    sp = json.load(open(os.path.join(d, 'sprite.json'), encoding='utf-8'))
    lib = {}
    pdir = os.path.join(d, 'parts')
    for fn in os.listdir(pdir):
        if fn.endswith('.json'):
            lib[fn[:-5]] = json.load(open(os.path.join(pdir, fn), encoding='utf-8'))
    return sp, lib


def apply(m, x, y):
    a, b, c, d, e, f = m
    return a * x + c * y + e, b * x + d * y + f


def bbox(lib, frames, layers):
    xs = []
    ys = []
    for fr in frames:
        for p in fr.get('parts', []):
            if layers and p.get('layer') not in layers:
                continue
            pf = lib.get(p.get('part'))
            if not pf:
                continue
            m = p.get('matrix') or [1, 0, 0, 1, 0, 0]
            for pd in pf.get('paths', []):
                nums = [float(v) for v in re.findall(r'-?\d+\.?\d*', pd.get('d', ''))]
                for x, y in zip(nums[0::2], nums[1::2]):
                    X, Y = apply(m, x, y)
                    xs.append(X)
                    ys.append(Y)
    if not xs:
        return None
    return (min(xs), min(ys), max(xs), max(ys))


changed = 0
for key in sorted(os.listdir(ROOT)):
    base = os.path.join(ROOT, key)
    if not os.path.isdir(base):
        continue
    for sub in sorted(os.listdir(base)):
        subdir = os.path.join(base, sub)
        if not os.path.isdir(subdir) or not (sub.startswith('action_') or sub.startswith('exp_')):
            continue
        if not os.path.exists(os.path.join(subdir, 'sprite.json')):
            continue
        sp, lib = load(subdir)
        frames = sp['directions']['default']['frames']
        tint = [l['name'] for l in sp.get('layers', []) if l.get('tintable')]
        bb = bbox(lib, frames[:1], tint) or bbox(lib, frames, tint) or bbox(lib, frames[:1], None)
        if not bb:
            print('SKIP %s/%s（无几何）' % (key, sub))
            continue
        cx = round((bb[0] + bb[2]) / 2, 2)
        cy = round((bb[1] + bb[3]) / 2, 2)
        ov = sp['canvas'].get('bodyc')
        print('%-8s %-9s bodyc=[%7.2f,%7.2f] canvas=%.1fx%.1f tint=%s%s' % (
            key, sub, cx, cy, sp['canvas']['w'], sp['canvas']['h'], tint,
            '' if ov == [cx, cy] else '  <-更新'))
        if ov == [cx, cy]:
            continue
        sp['canvas']['bodyc'] = [cx, cy]
        if not DRY:
            with open(os.path.join(subdir, 'sprite.json'), 'w', encoding='utf-8') as f:
                json.dump(sp, f, ensure_ascii=False, separators=(',', ':'))
        changed += 1
print('更新 %d 个 sprite.json%s' % (changed, '（dry-run 未写盘）' if DRY else ''))
