#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""SWF 嵌套时间轴侦查：某个 DefineSprite 每一帧放置了哪些子角色，以及
这些子角色自己的帧数。

用途：判断「父 MC 被 gotoAndStop 停住后，里面嵌套的子 MC 是否仍在播放」。
只读，不改 SWF。

用法：
    python3 swf_nested.py <file.swf> <spriteId> [--frames N]
"""
import sys
import zlib
import struct


def read_swf(path):
    raw = open(path, 'rb').read()
    if raw[:3] == b'CWS':
        data = raw[:8] + zlib.decompress(raw[8:])
    else:
        data = raw
    nb = struct.unpack('<B', data[8:9])[0] >> 3
    off = 8 + (5 + nb * 4 + 7) // 8 + 4
    return data, off


def iter_tags(data, pos):
    while pos + 2 <= len(data):
        cl = struct.unpack('<H', data[pos:pos + 2])[0]
        code, length = cl >> 6, cl & 0x3F
        pos += 2
        if length == 0x3F:
            length = struct.unpack('<I', data[pos:pos + 4])[0]
            pos += 4
        body = data[pos:pos + length]
        yield code, body
        pos += length
        if code == 0:
            return


def skip_matrix(b, p):
    if p + 1 > len(b):
        return p
    v = b[p]; p += 1
    has_scale = v >> 7
    nbits = (v >> 1) & 0x07  # 实际读取顺序: HasScale(1), NScale(5), HasRotate(1), NRotate(5)
    # 正确拆位
    pos = 0
    def rb(n):
        nonlocal p, pos
        if p >= len(b):
            return 0
        v = 0
        start = b[p]
        # 简化：按位读取（SWF 是小端位序，从高到低）
        pass
    # 用位缓冲区稳妥实现
    bits = ''.join(f'{x:08b}' for x in b[p:p + 16])
    bi = 0
    def take(n):
        nonlocal bi
        s = bits[bi:bi + n]
        bi += n
        return int(s, 2) if s else 0
    has_scale = take(1)
    nscale = take(5)
    if has_scale:
        take(nscale); take(nscale)
    has_rot = take(1)
    nrot = take(5)
    if has_rot:
        take(nrot); take(nrot)
    ntrans = take(5)
    take(ntrans); take(ntrans)
    used = (bi + 7) // 8
    return p + used


def parse_place2(body):
    """返回 (depth, characterId, name)。"""
    if not body:
        return None
    flags = body[0]
    p = 1
    depth = struct.unpack('<H', body[p:p + 2])[0]; p += 2
    cid = None
    has_char = (flags >> 1) & 1
    has_matrix = (flags >> 2) & 1
    has_cx = (flags >> 3) & 1
    has_ratio = (flags >> 4) & 1
    has_name = (flags >> 5) & 1
    has_clipdepth = (flags >> 6) & 1
    if has_char:
        cid = struct.unpack('<H', body[p:p + 2])[0]; p += 2
    if has_matrix:
        p = skip_matrix(body, p)
    if has_cx:
        # CXFORMWITHALPHA: HasAdd(1) HasMult(1) Nbits(4)
        v = body[p]; p += 1
        has_add = v >> 7
        has_mult = (v >> 6) & 1
        nb = v & 0x0F
        cnt = (4 if has_mult else 0) + (4 if has_add else 0)
        p += (nb * cnt + 7) // 8
    if has_ratio:
        p += 2
    name = None
    if has_name:
        end = body.index(b'\x00', p)
        name = body[p:end].decode('utf-8', 'replace')
        p = end + 1
    if has_clipdepth:
        p += 2
    return depth, cid, name


def parse_place3(body):
    # PlaceObject3: flags1, flags2, depth, [classname], [characterid], ...
    if len(body) < 3:
        return None
    f1 = body[0]; f2 = body[1]
    p = 2
    depth = struct.unpack('<H', body[p:p + 2])[0]; p += 2
    if f2 & 0x08:  # HasClassName
        end = body.index(b'\x00', p)
        p = end + 1
    cid = None
    if f1 & 0x02:  # HasCharacter
        cid = struct.unpack('<H', body[p:p + 2])[0]; p += 2
    return depth, cid, None


def main():
    path, sid = sys.argv[1], int(sys.argv[2])
    data, pos = read_swf(path)
    sprites = {}
    for code, body in iter_tags(data, pos):
        if code == 39 and len(body) >= 4:
            sprites[struct.unpack('<H', body[0:2])[0]] = body
    if sid not in sprites:
        print(f'sprite {sid} not found')
        return
    body = sprites[sid]
    print(f'sprite {sid}: {struct.unpack("<H", body[2:4])[0]} frames')
    frame = 0
    for code, b in iter_tags(body, 4):
        if code == 1:
            frame += 1
            continue
        if code == 26:
            r = parse_place2(b)
            if r and r[1]:
                depth, cid, name = r
                sub = sprites.get(cid)
                n = f'{struct.unpack("<H", sub[2:4])[0]} 帧' if sub else '非 sprite'
                print(f'  frame{frame} place2 depth={depth} char={cid} name={name} ({n})')
        elif code == 70:
            r = parse_place3(b)
            if r and r[1]:
                depth, cid, name = r
                sub = sprites.get(cid)
                n = f'{struct.unpack("<H", sub[2:4])[0]} 帧' if sub else '非 sprite'
                print(f'  frame{frame} place3 depth={depth} char={cid} ({n})')


if __name__ == '__main__':
    main()
