#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""dump_sprite_states.py —— 逐帧追踪 DefineSprite 的 place 状态（含 scaleX 符号）。

用途：确认每个方向标签段首帧里放的是哪个子 sprite、是否镜像。
只读。

用法：
    python3 dump_sprite_states.py <swf> <spriteId> [--labels]
"""
import struct
import sys
import zlib


def read_swf(path):
    raw = open(path, 'rb').read()
    data = raw[:8] + zlib.decompress(raw[8:]) if raw[:3] == b'CWS' else raw
    nb = struct.unpack('<B', data[8:9])[0] >> 3
    return data, 8 + (5 + nb * 4 + 7) // 8 + 4


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


def matrix_scale(body, p):
    """返回 (scaleX, ty_px)。仅取 HasScale 的 scaleX 与 translateY。"""
    bits = ''.join(f'{b:08b}' for b in body[p:p + 12])
    i = 0

    def take(n):
        nonlocal i
        s = bits[i:i + n]
        i += n
        return int(s, 2) if s else 0

    hs = take(1)
    ns = take(5)
    sx = None
    if hs:
        raw = take(ns)
        if raw >> (ns - 1):
            raw -= 1 << ns
        sx = raw / 65536.0
    hr = take(1)
    nr = take(5)
    if hr:
        take(nr); take(nr)
    nt = take(5)
    tx = take(nt); ty = take(nt)
    if nt and ty >> (nt - 1):
        ty -= 1 << nt
    return sx, ty / 20.0


def main():
    path, sid = sys.argv[1], int(sys.argv[2])
    data, pos = read_swf(path)
    sprites = {}
    for code, body in iter_tags(data, pos):
        if code == 39 and len(body) >= 4:
            sprites[struct.unpack('<H', body[0:2])[0]] = body
    b = sprites[sid]
    total = struct.unpack('<H', b[2:4])[0]
    frame = 0
    state = {}
    print(f'sprite {sid}: {total} frames')
    for code, body in iter_tags(b, 4):
        if code == 1:
            frame += 1
            continue
        if code == 43 and body:
            name = body.split(b'\x00')[0].decode('utf-8', 'replace')
            print(f'  label {name} @frame{frame}: {sorted(state.items())}')
            continue
        if code in (26, 70):
            if not body:
                continue
            flags = body[0]
            q = 1
            depth = struct.unpack('<H', body[q:q + 2])[0]; q += 2
            cid = None
            if (flags >> 1) & 1:
                cid = struct.unpack('<H', body[q:q + 2])[0]; q += 2
            sx = ty = None
            if (flags >> 2) & 1 and cid:
                sx, ty = matrix_scale(body, q)
            if cid:
                state[depth] = (cid, sx, ty)
    print(f'  end @frame{frame}: {sorted(state.items())}')


if __name__ == '__main__':
    main()
