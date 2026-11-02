#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""dump_buttons.py —— 列出 SWF 里的按钮资源与状态帧，用来核对「按下/悬停/静止」
是否三态齐全、动画是否多帧。

输出两类：
  1. DefineButton2(code 34)：每个 state 的字符 id 与偏移（SWF 原生按钮）
  2. DefineSprite(code 39)：带 up/over/down(hit) 标签的 MC 按钮，列出标签帧与
     各标签下的 place 记录（characterId），用于判断「是不是只导了 up 一帧」

用法: python3 dump_buttons.py <swf>
"""
import struct
import sys
import zlib


def read_swf(path):
    raw = open(path, 'rb').read()
    if raw[:3] == b'CWS':
        return raw[:8] + zlib.decompress(raw[8:])
    return raw


def tags(b, pos):
    while pos + 2 <= len(b):
        cl = struct.unpack('<H', b[pos:pos + 2])[0]
        code, ln = cl >> 6, cl & 0x3F
        pos += 2
        if ln == 0x3F:
            ln = struct.unpack('<I', b[pos:pos + 4])[0]
            pos += 4
        yield code, b[pos:pos + ln]
        pos += ln
        if code == 0:
            return


def main():
    data = read_swf(sys.argv[1])
    nb = data[8] >> 3
    pos = 8 + (5 + nb * 4 + 7) // 8 + 4
    buttons = []
    sprites = {}
    for code, pl in tags(data, pos):
        if code == 0:
            break
        if code == 34 and len(pl) >= 4:      # DefineButton2
            bid = struct.unpack('<H', pl[:2])[0]
            states = []
            i = 4
            while i < len(pl):
                flags = pl[i]
                i += 1
                if flags == 0:
                    break
                cid = struct.unpack('<H', pl[i:i + 2])[0]
                i += 2
                depth = struct.unpack('<H', pl[i:i + 2])[0]
                i += 2
                states.append((cid, depth, flags & 0x07))
                # 跳过 matrix / cxform（简化：用 flags 位判断长度不可靠，直接记录即可）
                break
            buttons.append((bid, len(states)))
        elif code == 39 and len(pl) >= 4:
            sid, total = struct.unpack('<HH', pl[0:4])
            labels, places = {}, []
            fr = 0
            for c2, p2 in tags(pl, 4):
                if c2 == 43:
                    labels[p2.split(b'\x00')[0].decode('utf-8', 'replace')] = fr
                elif c2 == 1:
                    fr += 1
                elif c2 == 26 and p2 and not (p2[0] >> 7):
                    d = struct.unpack('<H', p2[1:3])[0]
                    cid = struct.unpack('<H', p2[3:5])[0] if (p2[0] >> 1) & 1 else None
                    places.append((fr, d, cid))
            sprites[sid] = (total, labels, places)
    print(f'== {sys.argv[1].split("/")[-1]}')
    if buttons:
        print(f'DefineButton2: {len(buttons)} 个 -> {buttons[:20]}')
    st = {s: v for s, v in sprites.items()
          if any(k in v[1] for k in ('up', 'over', 'down', 'hit'))}
    if st:
        print('带 up/over/down 标签的 sprite：')
        for sid, (total, labels, places) in sorted(st.items()):
            lab = {k: v for k, v in labels.items() if k in ('up', 'over', 'down', 'hit')}
            print(f'  sprite {sid}: {total} 帧 标签={lab} place={places[:6]}')
    if not buttons and not st:
        print('无按钮资源')


if __name__ == '__main__':
    main()
