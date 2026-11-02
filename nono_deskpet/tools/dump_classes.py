#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""dump_classes.py —— 输出 SWF 的 SymbolClass 映射（class 名 -> sprite id），
用于确认游戏里某个类名到底绑定哪个 sprite，防止素材张冠李戴。

用法: python3 dump_classes.py <swf> [关键字]
"""
import struct
import sys
import zlib


def read_swf(path):
    raw = open(path, 'rb').read()
    if raw[:3] == b'CWS':
        return raw[:8] + zlib.decompress(raw[8:])
    return raw


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
    path = sys.argv[1]
    kw = sys.argv[2] if len(sys.argv) > 2 else None
    data = read_swf(path)
    nb = data[8] >> 3
    pos = 8 + (5 + nb * 4 + 7) // 8 + 4
    for code, body in tags(data, pos):
        if code != 76:
            continue
        n = struct.unpack('<H', body[:2])[0]
        i = 2
        rows = []
        for _ in range(n):
            sid = struct.unpack('<H', body[i:i + 2])[0]
            i += 2
            j = body.index(b'\x00', i)
            cls = body[i:j].decode('utf-8', 'replace')
            i = j + 1
            rows.append((cls, sid))
        for cls, sid in sorted(rows):
            if kw and kw.lower() not in cls.lower():
                continue
            print(f'{sid:6d}  {cls}')


if __name__ == '__main__':
    main()
