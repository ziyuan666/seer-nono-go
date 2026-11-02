#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""scan_remove_leaks.py —— 扫描 SWF：找出「旧版 extend_hold_frames 漏掉 RemoveObject2
导致某个方向段残留了不属于该段的层」的所有位置。

判定：对每个带 8 方向标签的 sprite，分别用
  new_state = 正确处理 PlaceObject2 / RemoveObject2 / RemoveObject
  old_state = 只认 HasCharacter 的 place（旧工具的行为）
算出每段首帧的 depth->cid 映射；两者不同即「该段会被旧工具污染」。

用法: python3 scan_remove_leaks.py <swf>
"""
import struct
import sys
import zlib

DIRS = ['down', 'leftdown', 'left', 'leftup', 'up', 'rightup', 'right', 'rightdown']


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
    hits = 0
    for code, pl in tags(data, pos):
        if code == 0:
            break
        if code != 39 or len(pl) < 4:
            continue
        sid = struct.unpack('<H', pl[:2])[0]
        frame = 0
        labels = []
        ops = []      # (frame, kind, depth, cid)
        for c2, p2 in tags(pl, 4):
            if c2 == 43:
                labels.append((frame, p2.split(b'\x00')[0].decode('utf-8', 'replace')))
            elif c2 == 26 and p2:
                fl = p2[0]
                d = struct.unpack('<H', p2[1:3])[0]
                if fl >> 7:
                    ops.append((frame, 'rm', d, None))
                elif (fl >> 1) & 1:
                    ops.append((frame, 'pl', d, struct.unpack('<H', p2[3:5])[0]))
            elif c2 == 28 and len(p2) >= 2:
                ops.append((frame, 'rm', struct.unpack('<H', p2[:2])[0], None))
            elif c2 == 1:
                frame += 1
        if not labels:
            continue
        new_f, old_f = {}, {}
        diffs = []
        for fr, name in labels:
            for f, kind, d, cid in ops:
                if f > fr:
                    continue
                if kind == 'rm':
                    new_f.pop(d, None)
                else:
                    new_f[d] = cid
                    old_f[d] = cid
            a = {d: c for d, c in new_f.items()}
            b = {d: c for d, c in old_f.items()}
            if a != b:
                diffs.append((name, fr, dict(b), dict(a)))
        if diffs:
            hits += 1
            print(f'sprite {sid}: {len(diffs)} 段受影响')
            for name, fr, old, new in diffs:
                print(f'   {name:10s} f{fr:<3d} 旧(污染)={old}  正确={new}')
    if not hits:
        print('无受影响 sprite')


if __name__ == '__main__':
    main()
