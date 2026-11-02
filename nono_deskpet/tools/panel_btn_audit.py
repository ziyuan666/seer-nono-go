#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""panel_btn_audit.py —— 面板按钮审计：对每个面板 SWF 找出「按钮在面板里的原生坐标」，
与 Go 里登记的命中区对比。

输出每个含按钮 place 的 sprite 的按钮清单（cid / 位置 / 缩放 / 实际像素尺寸）。

用法: python3 panel_btn_audit.py <swf> [...]
"""
import struct
import sys

from dump_panel_buttons import read_swf, tags, read_matrix


def audit(path):
    data = read_swf(path)
    nb = data[8] >> 3
    pos = 8 + (5 + nb * 4 + 7) // 8 + 4
    sprites, buttons = {}, set()
    for code, pl in tags(data, pos):
        if code == 0:
            break
        if code == 34 and len(pl) >= 2:
            buttons.add(struct.unpack('<H', pl[:2])[0])
        elif code == 39 and len(pl) >= 4:
            sid, total = struct.unpack('<HH', pl[0:4])
            ev, fr = [], 0
            for c2, p2 in tags(pl, 4):
                if c2 == 1:
                    fr += 1
                elif c2 == 26 and p2 and not (p2[0] >> 7) and (p2[0] >> 1) & 1:
                    d = struct.unpack('<H', p2[1:3])[0]
                    cid = struct.unpack('<H', p2[3:5])[0]
                    m = read_matrix(p2, 5) if (p2[0] >> 2) & 1 else None
                    ev.append((fr, d, cid, m))
            sprites[sid] = (total, ev)
    print(f'\n===== {path.split("/")[-1]}  按钮定义 {len(buttons)} 个')
    for sid, (total, ev) in sorted(sprites.items()):
        hits = [e for e in ev if e[2] in buttons]
        if not hits:
            continue
        print(f'  sprite {sid}（{total} 帧）里 place 的按钮：')
        for fr, d, cid, m in hits:
            if m:
                sx, sy, tx, ty, _ = m
                print(f'    cid={cid:<4d} 原生位置=({tx:7.1f},{ty:7.1f})  缩放=({sx:.2f},{sy:.2f})')
            else:
                print(f'    cid={cid:<4d} 原生位置=-  缩放=-')


if __name__ == '__main__':
    for p in sys.argv[1:]:
        audit(p)
