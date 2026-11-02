#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""dump_panel_buttons.py —— 列出面板 SWF 根 sprite 每一帧里 place 的按钮及其原生坐标。

用途：重建桌面面板的按钮命中区与三态贴图位置。坐标来自 place 的 matrix，
单位 twips（÷20 = 像素）。

用法: python3 dump_panel_buttons.py <swf> [根spriteId]
"""
import struct
import sys
import zlib


def read_swf(path):
    raw = open(path, 'rb').read()
    if raw[:3] == b'CWS':
        return raw[:8] + zlib.decompress(raw[8:])
    return raw


def tags(b, pos):
    while pos + 2 <= len(b):
        cl = struct.unpack('<H', b[pos:pos + 2])[0]
        code, ln = cl >> 6, cl & 0x3F
        pos += 2
        if ln == 0x3F:
            ln = struct.unpack('<I', b[pos:pos + 4])[0]
            pos += 4
        yield code, b[pos:pos + ln]
        pos += ln
        if code == 0:
            return


class Bits:
    def __init__(self, buf, pos=0):
        self.b = buf
        self.bit = pos * 8

    def u(self, n):
        v = 0
        for _ in range(n):
            v = (v << 1) | ((self.b[self.bit >> 3] >> (7 - (self.bit & 7))) & 1)
            self.bit += 1
        return v

    def s(self, n):
        if n == 0:
            return 0
        v = self.u(n)
        return v - (1 << n) if v & (1 << (n - 1)) else v


def read_matrix(buf, i):
    b = Bits(buf, i)
    hs = b.u(1)
    sx = sy = 1.0
    if hs:
        n = b.u(5)
        sx = b.s(n) / 65536.0 if n else 0
        sy = b.s(n) / 65536.0 if n else 0
    hr = b.u(1)
    if hr:
        n = b.u(5)
        b.s(n); b.s(n)
    n = b.u(5)
    tx = b.s(n) if n else 0
    ty = b.s(n) if n else 0
    return sx, sy, tx / 20.0, ty / 20.0, (b.bit + 7) // 8


def main():
    data = read_swf(sys.argv[1])
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
            ev = []
            fr = 0
            for c2, p2 in tags(pl, 4):
                if c2 == 1:
                    fr += 1
                elif c2 == 26 and p2 and not (p2[0] >> 7) and (p2[0] >> 1) & 1:
                    d = struct.unpack('<H', p2[1:3])[0]
                    cid = struct.unpack('<H', p2[3:5])[0]
                    i = 5
                    mtx = None
                    if (p2[0] >> 2) & 1:
                        mtx = read_matrix(p2, i)
                    ev.append((fr, d, cid, mtx))
            sprites[sid] = (total, ev)
    referenced = {e[2] for _t, ev in sprites.values() for e in ev}
    root = int(sys.argv[2]) if len(sys.argv) > 2 else None
    if root is None:
        cands = [s for s in sprites if s not in referenced]
        root = max(cands, key=lambda s: sprites[s][0]) if cands else None
    print(f'根 sprite = {root}  总帧={sprites[root][0] if root in sprites else "?"}  按钮={len(buttons)}')
    seen = set()
    for fr, d, cid, mtx in sprites.get(root, (0, []))[1]:
        tag = 'BTN' if cid in buttons else ('MC' if cid in sprites else 'SHP')
        key = (d, cid)
        if key in seen:
            continue
        seen.add(key)
        pos_s = f'({mtx[2]:.1f},{mtx[3]:.1f}) s=({mtx[0]:.2f},{mtx[1]:.2f})' if mtx else '-'
        print(f'  f{fr:<3d} d={d:<3d} cid={cid:<5d} {tag}  位置={pos_s}')


if __name__ == '__main__':
    main()
