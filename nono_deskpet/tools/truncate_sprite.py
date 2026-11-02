#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""truncate_sprite.py —— 把指定 DefineSprite 截断到前 N 帧（默认 1）。

为什么需要：body/color 层里每个方向的段首帧放的是 2 帧 sprite（站姿/走姿）。
游戏用 gotoAndStop 把它钉在第 1 帧；我们用 ffdec 导出「冻结拉长」时，这个 2 帧
层会自己循环，导致导出的 17 帧出现半帧空白。先把它截成 1 帧，内层 17 帧动画
才是唯一在动的东西。

用法：python3 truncate_sprite.py <in.swf> <out.swf> <N> <id> [id...]
"""
import struct, sys, zlib


def read_swf(path):
    raw = open(path, "rb").read()
    if raw[:3] == b"CWS":
        return raw[:8] + zlib.decompress(raw[8:]), b"CWS"
    return raw, raw[:3]


def raw_tags(data, pos):
    while pos + 2 <= len(data):
        start = pos
        cl = struct.unpack("<H", data[pos:pos+2])[0]
        code, length = cl >> 6, cl & 0x3F
        pos += 2
        if length == 0x3F:
            length = struct.unpack("<I", data[pos:pos+4])[0]
            pos += 4
        payload = data[pos:pos+length]
        pos += length
        yield start, pos, code, payload
        if code == 0:
            return


def write_tag(out, code, payload):
    n = len(payload)
    if n < 0x3F:
        out += struct.pack("<H", (code << 6) | n) + payload
    else:
        out += struct.pack("<H", (code << 6) | 0x3F) + struct.pack("<I", n) + payload


def truncate(body, keep):
    sid, total = struct.unpack("<HH", body[0:4])
    out = bytearray(struct.pack("<HH", sid, min(keep, total)))
    frame = 0
    for start, end, code, payload in raw_tags(body, 4):
        if code == 0:
            continue
        if code == 1:
            frame += 1
            if frame >= keep:
                write_tag(out, 1, b"")
                break
            write_tag(out, 1, b"")
            continue
        if frame < keep:
            out += body[start:end]
    write_tag(out, 0, b"")
    return bytes(out)


def main():
    src, dst, keep = sys.argv[1], sys.argv[2], int(sys.argv[3])
    ids = {int(x) for x in sys.argv[4:]}
    data, sig = read_swf(src)
    nb = struct.unpack("<B", data[8:9])[0] >> 3
    hdr = 8 + (5 + nb * 4 + 7) // 8 + 4
    out = bytearray(data[:hdr])
    for start, end, code, payload in raw_tags(data, hdr):
        if code == 39 and len(payload) >= 4:
            sid = struct.unpack("<H", payload[0:2])[0]
            if sid in ids:
                nb2 = truncate(payload, keep)
                write_tag(out, 39, nb2)
                print(f"sprite {sid}: keep {keep} frames")
                continue
        out += data[start:end]
        if code == 0:
            break
    raw = bytes(out)
    final = raw[:8] + zlib.compress(raw[8:], 9) if sig == b"CWS" else raw
    open(dst, "wb").write(final)
    print("wrote", dst, len(final))


if __name__ == "__main__":
    main()
