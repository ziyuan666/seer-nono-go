#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""dump_display.py —— 严格按 SWF 规范解析 PlaceObject2/3，输出显示列表：
depth / characterId / 实例名 / matrix(scale,translate) / colorTransform(mulAlpha) /
clipDepth / ratio。用途：确认某方向帧里到底有哪些层、哪些被移除、实例名是什么。

用法: python3 dump_display.py <swf> <spriteId> [--frame N]
"""
import struct
import sys
import zlib


class Bits:
    def __init__(self, buf, pos=0):
        self.b = buf
        self.bit = pos * 8

    def u(self, n):
        v = 0
        for _ in range(n):
            byte = self.b[self.bit >> 3]
            v = (v << 1) | ((byte >> (7 - (self.bit & 7))) & 1)
            self.bit += 1
        return v

    def s(self, n):
        if n == 0:
            return 0
        v = self.u(n)
        if v & (1 << (n - 1)):
            v -= (1 << n)
        return v


def read_matrix(buf, i):
    """返回 (matrix_dict, 下一个字节位置)。"""
    b = Bits(buf, i)
    has_scale = b.u(1)
    sx = sy = 1.0
    if has_scale:
        n = b.u(5)
        sx = b.s(n) / 65536.0 if n else 0
        sy = b.s(n) / 65536.0 if n else 0
    has_rot = b.u(1)
    r0 = r1 = 0.0
    if has_rot:
        n = b.u(5)
        r0 = b.s(n) / 65536.0 if n else 0
        r1 = b.s(n) / 65536.0 if n else 0
    n = b.u(5)
    tx = b.s(n) if n else 0
    ty = b.s(n) if n else 0
    return {'sx': sx, 'sy': sy, 'r0': r0, 'r1': r1, 'tx': tx, 'ty': ty}, (b.bit + 7) // 8


def read_cxform(buf, i):
    b = Bits(buf, i)
    has_add = b.u(1)
    has_mul = b.u(1)
    n = b.u(4)
    mul = [1.0] * 4
    add = [0] * 4
    if has_mul:
        mul = [b.s(n) / 256.0 if n else 1.0 for _ in range(4)]
    if has_add:
        add = [b.s(n) for _ in range(4)]
    return {'mul': mul, 'add': add}, (b.bit + 7) // 8


def parse_place(payload):
    flags = payload[0]
    p = {'flags': flags, 'depth': struct.unpack('<H', payload[1:3])[0],
         'cid': None, 'name': None, 'matrix': None, 'cxform': None,
         'ratio': None, 'clip': None, 'remove': bool(flags >> 7)}
    if p['remove']:
        return p
    i = 3
    if (flags >> 1) & 1:
        p['cid'] = struct.unpack('<H', payload[i:i + 2])[0]
        i += 2
    if (flags >> 2) & 1:
        p['matrix'], i = read_matrix(payload, i)
    if (flags >> 3) & 1:
        p['cxform'], i = read_cxform(payload, i)
    if (flags >> 4) & 1:
        p['ratio'] = struct.unpack('<H', payload[i:i + 2])[0]
        i += 2
    if (flags >> 5) & 1:
        j = payload.index(b'\x00', i)
        p['name'] = payload[i:j].decode('utf-8', 'replace')
        i = j + 1
    if (flags >> 6) & 1:
        p['clip'] = struct.unpack('<H', payload[i:i + 2])[0]
    return p


def read_swf(path):
    raw = open(path, 'rb').read()
    return raw[:8] + zlib.decompress(raw[8:]) if raw[:3] == 'CWS'.encode() else raw


def tags(data, pos):
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


def main():
    path, sid = sys.argv[1], int(sys.argv[2])
    only = None
    if '--frame' in sys.argv:
        only = int(sys.argv[sys.argv.index('--frame') + 1])
    data = read_swf(path)
    nb = data[8] >> 3
    off = 8 + (5 + nb * 4 + 7) // 8
    pos = off + 4
    for code, body in tags(data, pos):
        if code != 39 or len(body) < 4:
            continue
        if struct.unpack('<H', body[:2])[0] != sid:
            continue
        total = struct.unpack('<H', body[2:4])[0]
        print(f'sprite {sid}: {total} frames')
        frame = 0
        state = {}
        for code2, payload in tags(body, 4):
            if code2 == 43:
                nm = payload.split(b'\x00')[0].decode('utf-8', 'replace')
                print(f'  [f{frame}] label={nm}')
            elif code2 == 26:
                p = parse_place(payload)
                if p['remove']:
                    state.pop(p['depth'], None)
                else:
                    if p['cid'] is None and p['depth'] in state:
                        merge = dict(state[p['depth']])
                        merge.update({k: v for k, v in p.items() if v is not None})
                        state[p['depth']] = merge
                    else:
                        state[p['depth']] = p
                if only is None or frame == only:
                    print(f'  [f{frame}] place d={p["depth"]} cid={p["cid"]} name={p["name"]} '
                          f'rm={p["remove"]} m={p["matrix"]} cx={p["cxform"]} clip={p["clip"]}')
            elif code2 == 70:
                # PlaceObject3: Flags(1) Flags2(1) Depth(2) [ClassName] [CharacterId]
                # [Matrix] [Cxform] [Ratio] [Name] [ClipDepth] [Filters] [BlendMode] [Cache]
                flags, flags2 = payload[0], payload[1]
                depth = struct.unpack('<H', payload[2:4])[0]
                i = 4
                if flags2 & 0x10:
                    i = payload.index(b'\x00', i) + 1
                cid = None
                if flags & 0x02:
                    cid = struct.unpack('<H', payload[i:i + 2])[0]
                    i += 2
                mtx = cx = ratio = name = None
                if flags & 0x04:
                    mtx, i = read_matrix(payload, i)
                if flags & 0x08:
                    cx, i = read_cxform(payload, i)
                if flags & 0x10:
                    ratio = struct.unpack('<H', payload[i:i + 2])[0]
                    i += 2
                if flags & 0x20:
                    j = payload.index(b'\x00', i)
                    name = payload[i:j].decode('utf-8', 'replace')
                    i = j + 1
                if only is None or frame == only:
                    print(f'  [f{frame}] place3 d={depth} cid={cid} name={name} '
                          f'flags=0x{flags:02x} m={mtx} cx={cx} ratio={ratio}')
                if cid is not None:
                    state[depth] = {'depth': depth, 'cid': cid, 'name': name,
                                    'matrix': mtx, 'cxform': cx, 'clip': None,
                                    'remove': False}
            elif code2 == 5 and len(payload) >= 4:
                d = struct.unpack('<H', payload[2:4])[0]
                state.pop(d, None)
                if only is None or frame == only:
                    print(f'  [f{frame}] RemoveObject d={d}')
            elif code2 == 28 and len(payload) >= 2:
                d = struct.unpack('<H', payload[0:2])[0]
                state.pop(d, None)
                if only is None or frame == only:
                    print(f'  [f{frame}] RemoveObject2 d={d}')
            elif code2 == 1:
                if only is not None and frame == only:
                    print(f'  [f{frame}] state:')
                    for d, v in sorted(state.items()):
                        print(f'    d={d} cid={v["cid"]} name={v["name"]} m={v["matrix"]}')
                frame += 1
        return
    print('sprite not found')


if __name__ == '__main__':
    main()
