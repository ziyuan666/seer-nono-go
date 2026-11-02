#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""swf_bounds.py —— 解析 SWF 里每个 character 的 bounds，并输出

- shape: DefineShape/2/3/4 的 shapeBounds（SWF 里显式写着）
- sprite: 递归 = 其所有子 character 经 PlaceObject matrix 变换后的并集

有了 bounds，就能把 ffdec 导出的 sprite PNG（画布原点 = 该 sprite 的 bounds
左上角）精确摆回父层坐标系，不依赖模板匹配或 bbox 猜测。

用法：
    python3 swf_bounds.py <file.swf> [charId ...]
"""
import struct
import sys
import zlib


def read_swf(path):
    raw = open(path, 'rb').read()
    if raw[:3] == b'CWS':
        return raw[:8] + zlib.decompress(raw[8:])
    return raw


def iter_tags(data, pos):
    while pos + 2 <= len(data):
        cl = struct.unpack('<H', data[pos:pos + 2])[0]
        code, length = cl >> 6, cl & 0x3F
        pos += 2
        if length == 0x3F:
            length = struct.unpack('<I', data[pos:pos + 4])[0]
            pos += 4
        yield code, data[pos:pos + length]
        pos += length
        if code == 0:
            return


class BitReader:
    def __init__(self, data, pos):
        self.d = data
        self.i = pos * 8

    def read(self, n):
        v = 0
        for _ in range(n):
            byte = self.d[self.i // 8]
            bit = (byte >> (7 - (self.i % 8))) & 1
            v = (v << 1) | bit
            self.i += 1
        return v

    def read_signed(self, n):
        v = self.read(n)
        if n and v >> (n - 1):
            v -= 1 << n
        return v

    @property
    def bytepos(self):
        return (self.i + 7) // 8


def read_rect(br):
    nb = br.read(5)
    xmin = br.read_signed(nb) / 20.0
    xmax = br.read_signed(nb) / 20.0
    ymin = br.read_signed(nb) / 20.0
    ymax = br.read_signed(nb) / 20.0
    return (xmin, ymin, max(xmax, xmin), max(ymax, ymin))


def read_matrix(br):
    a = d = 1.0
    b = c = 0.0
    if br.read(1):
        n = br.read(5)
        a = br.read_signed(n) / 65536.0
        d = br.read_signed(n) / 65536.0
    if br.read(1):
        n = br.read(5)
        b = br.read_signed(n) / 65536.0
        c = br.read_signed(n) / 65536.0
    n = br.read(5)
    tx = br.read_signed(n) / 20.0
    ty = br.read_signed(n) / 20.0
    return (a, b, c, d, tx, ty)


def apply(m, x, y):
    a, b, c, d, tx, ty = m
    return (a * x + c * y + tx, b * x + d * y + ty)


def union(b1, b2):
    if b1 is None:
        return b2
    if b2 is None:
        return b1
    return (min(b1[0], b2[0]), min(b1[1], b2[1]), max(b1[2], b2[2]), max(b1[3], b2[3]))


def load(path):
    data = read_swf(path)
    nb = struct.unpack('<B', data[8:9])[0] >> 3
    hdr_end = 8 + (5 + nb * 4 + 7) // 8 + 4
    shapes, sprites, tags = {}, {}, []
    for code, payload in iter_tags(data, hdr_end):
        if code in (2, 22, 32, 83) and len(payload) >= 2:
            cid = struct.unpack('<H', payload[0:2])[0]
            br = BitReader(payload, 2)
            shapes[cid] = read_rect(br)
        elif code == 39 and len(payload) >= 4:
            cid, total = struct.unpack('<HH', payload[0:4])
            places = []
            for t, p in iter_tags(payload, 4):
                if t in (26, 70) and p:
                    flags = p[0]
                    q = 1
                    depth = struct.unpack('<H', p[q:q + 2])[0]; q += 2
                    chid = None
                    if (flags >> 1) & 1:
                        chid = struct.unpack('<H', p[q:q + 2])[0]; q += 2
                    m = (1.0, 0.0, 0.0, 1.0, 0.0, 0.0)
                    if (flags >> 2) & 1:
                        br = BitReader(p, q)
                        m = read_matrix(br)
                    if chid:
                        places.append((chid, m))
            sprites[cid] = {'frames': total, 'places': places}
        if code == 0:
            break
    return shapes, sprites


def char_bounds(cid, shapes, sprites, memo, depth=0):
    if cid in memo:
        return memo[cid]
    if depth > 8:
        return None
    b = None
    if cid in shapes:
        b = shapes[cid]
    elif cid in sprites:
        for chid, m in sprites[cid]['places']:
            cb = char_bounds(chid, shapes, sprites, memo, depth + 1)
            if cb is None:
                continue
            pts = [apply(m, cb[0], cb[1]), apply(m, cb[2], cb[1]),
                   apply(m, cb[0], cb[3]), apply(m, cb[2], cb[3])]
            xs = [p[0] for p in pts]; ys = [p[1] for p in pts]
            b = union(b, (min(xs), min(ys), max(xs), max(ys)))
    memo[cid] = b
    return b


def main():
    path = sys.argv[1]
    want = [int(x) for x in sys.argv[2:]]
    shapes, sprites = load(path)
    memo = {}
    if not want:
        want = [127, 116, 39, 52, 68, 94, 115, 49, 64, 71, 99, 9, 20, 26, 38, 4, 16, 24, 34]
    for cid in want:
        b = char_bounds(cid, shapes, sprites, memo)
        if b is None:
            print(cid, 'no bounds')
            continue
        w, h = b[2] - b[0], b[3] - b[1]
        print(f'char{cid}: bounds=({b[0]:.2f},{b[1]:.2f})-({b[2]:.2f},{b[3]:.2f}) '
              f'size={w:.2f}x{h:.2f}')


if __name__ == '__main__':
    main()
