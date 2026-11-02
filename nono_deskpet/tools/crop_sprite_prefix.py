#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""crop_sprite_prefix.py —— 把指定方向段「转正」成第 1 段（二进制裁剪前导帧）。

背景：nono.swf 的 body/color 层是 63 帧、8 个方向标签。外层被游戏
gotoAndStop(方向) 停住，真正在播的是段首帧里嵌套的 17 帧子 sprite。

做法：把目标方向之前的所有帧整段删掉。段与段之间用 Move 复用同一个子 sprite，
直接删会把 characterId 定义删掉（渲染空白），所以先把「目标段之前各 depth
最后一次带 char 的 place」补成 frame0 的初始 place，再原样输出目标段字节。

关键：所有 tag 直接按原始字节切片复制（短 header/tag 长度形式都不动），
只重写 sprite header 的 frameCount 和补的那几条 PlaceObject。

用法：
    python3 crop_sprite_prefix.py <in.swf> <out.swf> <spriteId:方向> [更多...]
"""
import struct
import sys
import zlib


def read_swf(path):
    raw = open(path, 'rb').read()
    if raw[:3] == b'CWS':
        return raw[:8] + zlib.decompress(raw[8:]), b'CWS'
    return raw, raw[:3]


def raw_tags(data, pos):
    """产出 (start, end, code, payload)。"""
    while pos + 2 <= len(data):
        start = pos
        cl = struct.unpack('<H', data[pos:pos + 2])[0]
        code, length = cl >> 6, cl & 0x3F
        pos += 2
        if length == 0x3F:
            length = struct.unpack('<I', data[pos:pos + 4])[0]
            pos += 4
        payload = data[pos:pos + length]
        pos += length
        yield start, pos, code, payload
        if code == 0:
            return


def place_info(payload):
    if not payload:
        return None
    flags = payload[0]
    p = 1
    depth = struct.unpack('<H', payload[p:p + 2])[0]; p += 2
    cid = None
    if (flags >> 1) & 1:
        cid = struct.unpack('<H', payload[p:p + 2])[0]; p += 2
    mstart = p
    if (flags >> 2) & 1:
        bits = ''.join(f'{b:08b}' for b in payload[p:p + 12])
        i = 0

        def take(n):
            nonlocal i
            s = bits[i:i + n]; i += n
            return int(s, 2) if s else 0

        if take(1):
            n = take(5); take(n); take(n)
        if take(1):
            n = take(5); take(n); take(n)
        n = take(5); take(n); take(n)
        p += (i + 7) // 8
    return depth, cid, payload[mstart:p], flags


def write_tag(code, payload):
    n = len(payload)
    if n < 0x3F:
        return struct.pack('<H', (code << 6) | n) + payload
    return struct.pack('<H', (code << 6) | 0x3F) + struct.pack('<I', n) + payload


def crop(body, direction):
    sid = struct.unpack('<H', body[0:2])[0]
    total = struct.unpack('<H', body[2:4])[0]
    tags = []
    frame = 0
    target = None
    for start, end, code, payload in raw_tags(body, 4):
        tags.append((start, end, code, payload, frame))
        if code == 43:
            name = payload.split(b'\x00')[0].decode('utf-8', 'replace')
            if name == direction and target is None:
                target = frame
        elif code == 1:
            frame += 1
    if target is None:
        raise SystemExit(f'sprite {sid}: 无标签 {direction}')
    if target == 0:
        return body, total

    last_char = {}
    for _s, _e, code, payload, fr in tags:
        if code in (26, 70) and fr < target:
            info = place_info(payload)
            if info and info[1] is not None:
                last_char[info[0]] = info[1]
    first_matrix = {}
    for _s, _e, code, payload, fr in tags:
        if code in (26, 70) and fr == target:
            info = place_info(payload)
            if info:
                first_matrix[info[0]] = info[2]

    out = bytearray(struct.pack('<HH', sid, total - target))
    for depth in sorted(last_char):
        cid = last_char[depth]
        matrix = first_matrix.get(depth, b'')
        flags = 0b00000110 if matrix else 0b00000010
        out += write_tag(26, bytes([flags]) + struct.pack('<H', depth) + struct.pack('<H', cid) + matrix)
    for start, end, code, payload, fr in tags:
        if fr < target:
            continue
        if code == 0:
            continue  # End 标签在最后统一补
        if code == 43:
            name = payload.split(b'\x00')[0].decode('utf-8', 'replace')
            if name == direction:
                out += write_tag(43, direction.encode() + b'\x00')
                continue
        out += body[start:end]
    out += write_tag(0, b'')
    return bytes(out), total - target


def main():
    src, dst = sys.argv[1], sys.argv[2]
    specs = {}
    for s in sys.argv[3:]:
        sid, direction = s.split(':')
        specs[int(sid)] = direction
    data, sig = read_swf(src)
    nb = struct.unpack('<B', data[8:9])[0] >> 3
    hdr_end = 8 + (5 + nb * 4 + 7) // 8 + 4
    out = bytearray(data[:hdr_end])
    for start, end, code, payload in raw_tags(data, hdr_end):
        if code == 39 and len(payload) >= 4:
            sid = struct.unpack('<H', payload[0:2])[0]
            if sid in specs:
                nb2, _ = crop(payload, specs[sid])
                out += write_tag(39, nb2)
                print(f'sprite {sid}: -> {specs[sid]}, frames={struct.unpack("<H", nb2[2:4])[0]}')
                continue
        out += data[start:end]
        if code == 0:
            break
    raw = bytes(out)
    final = raw[:8] + zlib.compress(raw[8:], 9) if sig == b'CWS' else raw
    open(dst, 'wb').write(final)
    print('wrote', dst, len(final))


if __name__ == '__main__':
    main()
