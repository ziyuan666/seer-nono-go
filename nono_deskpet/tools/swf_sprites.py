#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""SWF DefineSprite / FrameLabel 结构速查。

用途：查 nono.swf 里 body/foot/color 这些子 MC 每个方向标签下到底多少帧，
以此判断「游戏原版是静态单帧还是循环动画」。只做只读解析，不改 SWF。

用法：
    python3 swf_sprites.py <file.swf> [--labels]
输出：每个 DefineSprite 的 spriteId、帧数(ShowFrame 数)、帧标签列表；
      --labels 时额外打印标签所在帧号。
"""
import sys
import zlib
import struct


def read_header(raw):
    if raw[:3] == b'CWS':
        body = zlib.decompress(raw[8:])
        head = raw[:8] + body
        # 用解压后数据做后续解析，前 8 字节 header 保持一致
        data = head
    else:
        data = raw
    nb = struct.unpack('<B', data[8:9])[0] >> 3
    rect_bytes = (5 + nb * 4 + 7) // 8
    off = 8 + rect_bytes
    fps = struct.unpack('<H', data[off:off + 2])[0] / 256.0
    frames = struct.unpack('<H', data[off + 2:off + 4])[0]
    return data, off + 4, fps, frames


def iter_tags(data, pos):
    while pos + 2 <= len(data):
        code_len = struct.unpack('<H', data[pos:pos + 2])[0]
        code = code_len >> 6
        length = code_len & 0x3F
        pos += 2
        if length == 0x3F:
            if pos + 4 > len(data):
                return
            length = struct.unpack('<I', data[pos:pos + 4])[0]
            pos += 4
        body = data[pos:pos + length]
        yield code, body
        pos += length
        if code == 0:
            return


TAG_NAMES = {
    0: 'End', 1: 'ShowFrame', 9: 'SetBackgroundColor', 12: 'DoAction',
    26: 'PlaceObject2', 39: 'DefineSprite', 43: 'FrameLabel',
    48: 'DefineFont2', 56: 'ExportAssets', 59: 'DoInitAction',
    69: 'FileAttributes', 70: 'PlaceObject3', 76: 'SymbolClass',
    82: 'DoABC',
}


def parse_sprite(sid, body):
    """返回 (frame_count, labels[(frame, name)])。"""
    frame = 0
    labels = []
    for code, b in iter_tags(body, 4):  # 前 4 字节是 spriteId+frameCount
        if code == 1:  # ShowFrame
            frame += 1
        elif code == 43 and len(b) >= 1:
            # FrameLabel: 以 0 结尾的字符串，可带 anchor 字节
            name = b.split(b'\x00')[0].decode('utf-8', 'replace')
            labels.append((frame, name))
    return frame, labels


def main():
    path = sys.argv[1]
    show_labels = '--labels' in sys.argv
    raw = open(path, 'rb').read()
    data, pos, fps, frames = read_header(raw)
    print(f'file={path} fps={fps} root_frames={frames}')
    counts = {}
    sprites = {}
    for code, body in iter_tags(data, pos):
        counts[code] = counts.get(code, 0) + 1
        if code == 39 and len(body) >= 4:
            sid = struct.unpack('<H', body[0:2])[0]
            sprites[sid] = parse_sprite(sid, body)
    print('tags:', ', '.join(f'{TAG_NAMES.get(k, k)}={v}' for k, v in sorted(counts.items())))
    print(f'sprites={len(sprites)}')
    for sid in sorted(sprites):
        fc, labels = sprites[sid]
        if not labels and fc <= 1:
            continue
        desc = ' '.join(f'{n}@{f}' for f, n in labels) if show_labels else ' '.join(n for _, n in labels)
        print(f'  sprite {sid}: frames={fc} labels=[{desc}]')


if __name__ == '__main__':
    main()
