#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""dump_places.py —— 只读解析指定 sprite 的显示列表：每帧的 PlaceObject2 记录
（depth / characterId / 实例名 / Move / Remove / alpha 等），用于确认层级关系。

用法: python3 dump_places.py <swf> <spriteId> [--frames]
"""
import struct
import sys
import zlib


def read_swf(path):
    raw = open(path, 'rb').read()
    if raw[:3] == b'CWS':
        return raw[:8] + zlib.decompress(raw[8:])
    return raw


def header(data):
    nb = data[8] >> 3
    rect = (5 + nb * 4 + 7) // 8
    off = 8 + rect
    return off + 4


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


def sprites(data):
    for code, body in iter_tags(data, header(data)):
        if code == 39 and len(body) >= 4:
            sid = struct.unpack('<H', body[:2])[0]
            yield sid, body


def parse_place(payload):
    """返回 dict；解析失败返回 None。"""
    if not payload:
        return None
    flags = payload[0]
    i = 1
    depth = struct.unpack('<H', payload[i:i + 2])[0]
    i += 2
    if flags & 0x01:  # Move：可能只带属性
        pass
    info = {'flags': flags, 'depth': depth, 'cid': None, 'name': None,
            'remove': bool(flags >> 7), 'move': bool(flags & 1)}
    if flags >> 7:  # RemoveObject2
        return info
    if (flags >> 1) & 1:  # HasCharacter
        if i + 2 > len(payload):
            return None
        info['cid'] = struct.unpack('<H', payload[i:i + 2])[0]
        i += 2
    if (flags >> 2) & 1:  # HasMatrix
        nb = payload[i] >> 3
        rect = (5 + nb * 4 + 7) // 8
        i += rect
    if (flags >> 3) & 1:  # HasColorTransform
        i += 2
        nb = payload[i] >> 3
        i += 1 + ((nb * 4 + 7) // 8)
    if (flags >> 4) & 1:  # HasRatio
        i += 2
    if (flags >> 5) & 1:  # HasName
        j = payload.index(b'\x00', i)
        info['name'] = payload[i:j].decode('utf-8', 'replace')
    return info


def main():
    path, sid = sys.argv[1], int(sys.argv[2])
    data = read_swf(path)
    for s, body in sprites(data):
        if s != sid:
            continue
        total = struct.unpack('<H', body[2:4])[0]
        print(f'sprite {sid}: {total} frames')
        frame = 0
        seen = {}
        for code, payload in iter_tags(body, 4):
            if code == 43:
                nm = payload.split(b'\x00')[0].decode('utf-8', 'replace')
                print(f'  [frame {frame}] label={nm}')
            elif code in (26, 70):
                p = parse_place(payload)
                if p:
                    seen[p['depth']] = p
                    print(f'  [frame {frame}] place depth={p["depth"]} cid={p["cid"]} '
                          f'name={p["name"]} move={p["move"]} remove={p["remove"]}')
            elif code == 1:
                frame += 1
        print('  final depths:', {d: (v['cid'], v['name']) for d, v in sorted(seen.items())})
        return
    print('not found')


if __name__ == '__main__':
    main()
