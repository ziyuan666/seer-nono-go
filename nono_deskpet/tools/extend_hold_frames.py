#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""extend_hold_frames.py —— 把指定方向段「冻结在第 1 帧并拉长到 N 帧」。

目的：ffdec 的 sprite 导出是按「父层每帧推进一次所有嵌套 MC」渲染的。父层段只有
7~8 帧时，内层 17 帧动画只能拿到前 7~8 帧。把目标方向的段首帧原地重复 N 次，
父层就有 N 帧，内层就会一路推进到第 N 帧 —— 从而导出完整的 17 帧、且部件相对
位置由 ffdec 自己保证，不需要我们手工叠层。

用法：
    python3 extend_hold_frames.py <in.swf> <out.swf> <N> <spriteId:方向> [...]
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


def extend(body, direction, n):
    """把 direction 段的段首帧状态复制成 1 帧并接 n 个 ShowFrame。

    place 一律原样复制原始字节（只清 Move 位）。早先版本手工重建 place
    （flags+depth+cid+matrix）会漏掉 HasRatio / HasColorTransform 字段，导致
    matrix 读偏 2~6 字节——位置与缩放全错，画面表现为部件散开、层间白缝。
    """
    sid, total = struct.unpack('<HH', body[0:4])
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
    if direction == '*':
        target = 0
    elif target is None:
        raise SystemExit(f'sprite {sid}: 无标签 {direction}')

    state = {}   # depth -> (tagCode, 原始 place 字节)
    for _s, _e, code, payload, fr in tags:
        if fr > target or not payload:
            continue
        # RemoveObject2(code 28)：color_1(126) 在 up/right 段先 Remove 再什么都不 place，
        # 漏掉它会把这个方向本不该显示的层（leftup 的高光 cid125）带进来。
        # 2026-10-24 实测踩到。
        if code == 28 and len(payload) >= 2:
            state.pop(struct.unpack('<H', payload[0:2])[0], None)
            continue
        # RemoveObject(code 5)：老格式删除，payload = CharacterId(2) + Depth(2)
        if code == 5 and len(payload) >= 4:
            state.pop(struct.unpack('<H', payload[2:4])[0], None)
            continue
        if code == 26:
            flags = payload[0]
            depth = struct.unpack('<H', payload[1:3])[0]
            # 注意：PlaceObject2 没有 remove 位。bit7 是 HasClipActions，
            # 早先版本把 bit7 当 RemoveObject 是错的（会把带 clipActions 的层丢掉）。
            if (flags >> 1) & 1:          # HasCharacter：属性齐全，直接留原文
                state[depth] = (26, payload)
            continue
        if code == 70:
            # PlaceObject3：payload[0]=Flags, payload[1]=Flags2, [2:4]=Depth,
            # 若 Flags2.HasClassName(bit0) 则紧跟类名字符串。
            flags, flags2 = payload[0], payload[1]
            depth = struct.unpack('<H', payload[2:4])[0]
            i = 4
            if flags2 & 0x01:
                i = payload.index(b'\x00', i) + 1
            if (flags >> 1) & 1:
                state[depth] = (70, payload)
            continue

    out = bytearray(struct.pack('<HH', sid, n))
    for depth in sorted(state):
        code, payload = state[depth]
        p = bytearray(payload)
        if code == 26:
            p[0] = (p[0] & ~0x01) & 0xFF   # 清 Move，保留其余 flag 位
        elif code == 70:
            p[0] = (p[0] & ~0x01) & 0xFF
        _write(out, code, bytes(p))
    for _i in range(n):
        _write(out, 1, b'')
    _write(out, 0, b'')
    return bytes(out), n


def _write(out, code, payload):
    n = len(payload)
    if n < 0x3F:
        out += struct.pack('<H', (code << 6) | n) + payload
    else:
        out += struct.pack('<H', (code << 6) | 0x3F) + struct.pack('<I', n) + payload


def main():
    src, dst, n = sys.argv[1], sys.argv[2], int(sys.argv[3])
    specs = {}
    for s in sys.argv[4:]:
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
                nb2, _ = extend(payload, specs[sid], n)
                _write(out, 39, nb2)
                print(f'sprite {sid}: hold {specs[sid]} x{n}')
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
