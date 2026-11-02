#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""gen_ball_anchor_vec.py —— 按矢量素材口径生成 assets/ball_anchor.json。

## 为什么不用 measure_ballc.py

`measure_ballc.py` 通过像素测量旧 PNG 帧（`assets/<form>/idle/down/*.png`）来找
球心。切成矢量后这些 PNG 已经不再参与渲染，测出来的仍是旧素材（super4 271,186 /
画布 571x430）那一套——而矢量画布只有 269x286，锚点 271 直接落到画布外，实机表现
是「快捷栏图标飞到窗口外、球心偏到右边」。

矢量 spec 里本来就有权威数据：
    canvas.w / canvas.h      画布尺寸（1x 素材像素）
    canvas.ballc             球心（面部黑窗口中心，measure_vec_ballc.py 实测写入）

锚点口径（与旧 PNG 版一致，单位 = 素材像素）：
    anchor_x = max(ballc_x)                    所有形态里最大的左侧留白
    anchor_y = max(ballc_y)                    最大的上方留白
    win_w    = max(ballc_x) + max(w - ballc_x) 左右都要放得下
    win_h    = max(ballc_y) + max(h - ballc_y) 上下都要放得下
这样任何形态、任何方向都不会被窗口裁掉，且各形态球心落在窗口内同一位置。

用法：
    python3 gen_ball_anchor_vec.py assets [--scale 4] [--write]
"""
import argparse
import json
import os
import sys

FORMS = ['plain', 'super1', 'super2', 'super3', 'super4', 'super5']


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('root', nargs='?', default='assets')
    ap.add_argument('--scale', type=int, default=4, help='渲染倍率，与 assets.go 的 scale 一致')
    ap.add_argument('--write', action='store_true')
    args = ap.parse_args()

    rows = []
    for form in FORMS:
        p = os.path.join(args.root, 'sprites', form, 'sprite.json')
        if not os.path.exists(p):
            print('跳过（无 sprite.json）: %s' % form)
            continue
        d = json.load(open(p, encoding='utf-8'))
        c = d['canvas']
        w, h = c['w'], c['h']
        bc = c.get('ballc') or c.get('anchor')
        if not bc or (bc[0] == 0 and bc[1] == 0):
            print('⚠️ %s 缺 canvas.ballc，退回 anchor（换形态球心会跳）' % form)
        rows.append((form, w, h, bc[0], bc[1]))
        print('%-7s canvas %6.1fx%-6.1f ballc (%6.1f,%6.1f) 左%6.1f 右%6.1f 上%6.1f 下%6.1f'
              % (form, w, h, bc[0], bc[1], bc[0], w - bc[0], bc[1], h - bc[1]))
    if not rows:
        raise SystemExit('没有可用素材')

    max_l = max(r[3] for r in rows)
    max_r = max(r[1] - r[3] for r in rows)
    max_u = max(r[4] for r in rows)
    max_d = max(r[2] - r[4] for r in rows)
    s = args.scale
    # 单位必须是「素材像素」= 1x 画布坐标 × 渲染倍率。旧 PNG 时代 win_w=571 就是
    # 4x 素材像素（显示时再 ÷zoom）。只写 1x 值会让窗口比帧小 s 倍，帧被缩小显示，
    # 又糊回 25px 球径——这正是要修的问题本身。
    out = {
        # schema=2 标记「按矢量 canvas.ballc 生成」；1x 时代的文件没有这个字段，
        # 运行时据此告警（用户只覆盖 exe 时锚点会与素材不同步）。
        'schema': 2,
        'zoom': s,
        'anchor_x': int(round(max_l * s)),
        'anchor_y': int(round(max_u * s)),
        'win_w': int(round((max_l + max_r) * s)),
        'win_h': int(round((max_u + max_d) * s)),
        'margins': {'l': int(round(max_l * s)), 'r': int(round(max_r * s)),
                    'u': int(round(max_u * s)), 'd': int(round(max_d * s))},
    }
    print('\nUNION  l/r/u/d = %.1f/%.1f/%.1f/%.1f' % (max_l, max_r, max_u, max_d))
    print('ANCHOR(1x素材)=(%.1f,%.1f) WIN=%.1fx%.1f  zoom=%d' % (max_l, max_u, max_l + max_r, max_u + max_d, s))
    print('ANCHOR(显示px,scale=1)=(%.2f,%.2f) WIN=(%.2fx%.2f)'
          % (max_l / s, max_u / s, (max_l + max_r) / s, (max_u + max_d) / s))
    if args.write:
        dst = os.path.join(args.root, 'ball_anchor.json')
        json.dump(out, open(dst, 'w', encoding='utf-8'), ensure_ascii=False, indent=1)
        print('written: %s' % dst)
    return out


if __name__ == '__main__':
    sys.exit(0 if main() else 0)
