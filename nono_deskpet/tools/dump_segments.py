#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""dump_segments.py —— 帧数完整性审计：把「根 sprite → 8 方向层 → 子 sprite」的
每一帧事件全部列出来，证明没有漏帧、也没有跨段串帧。

输出每段的：段首帧号 / 段长 / 段内每帧发生的 place、remove、引用子 sprite 的帧数 /
内层是否 2 帧（站姿-走姿）或 N 帧（待机动画）。

用法: python3 dump_segments.py <swf> [根spriteId]
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


def parse(data):
    nb = data[8] >> 3
    pos = 8 + (5 + nb * 4 + 7) // 8 + 4
    sprites = {}
    for code, pl in tags(data, pos):
        if code == 0:
            break
        if code != 39 or len(pl) < 4:
            continue
        sid = struct.unpack('<H', pl[:2])[0]
        total = struct.unpack('<H', pl[2:4])[0]
        frames, labels, events, kids = 0, {}, [], set()
        for c2, p2 in tags(pl, 4):
            if c2 == 43:
                labels[p2.split(b'\x00')[0].decode('utf-8', 'replace')] = frames
            elif c2 == 1:
                frames += 1
            elif c2 == 26 and p2:
                fl = p2[0]
                d = struct.unpack('<H', p2[1:3])[0]
                if (fl >> 1) & 1:
                    cid = struct.unpack('<H', p2[3:5])[0]
                    kids.add(cid)
                    events.append((frames, f'place d={d} cid={cid}'))
                else:
                    events.append((frames, f'update d={d}'))
            elif c2 == 70 and p2:
                # PlaceObject3: Flags, Flags2, Depth, [ClassName], [CharacterId], ...
                fl, fl2 = p2[0], p2[1]
                d = struct.unpack('<H', p2[2:4])[0]
                i = 4
                if fl2 & 0x10:
                    i = p2.index(b'\x00', i) + 1
                if (fl >> 1) & 1:
                    cid = struct.unpack('<H', p2[i:i + 2])[0]
                    kids.add(cid)
                    events.append((frames, f'place3 d={d} cid={cid}'))
                else:
                    events.append((frames, f'update3 d={d}'))
            elif c2 == 5 and len(p2) >= 4:
                events.append((frames, f'del1 d={struct.unpack("<H", p2[2:4])[0]}'))
            elif c2 == 28 and len(p2) >= 2:
                events.append((frames, f'del d={struct.unpack("<H", p2[:2])[0]}'))
        sprites[sid] = {'frames': frames, 'labels': labels, 'events': events, 'kids': kids}
    return sprites


def main():
    path = sys.argv[1]
    data = read_swf(path)
    sprites = parse(data)
    root = int(sys.argv[2]) if len(sys.argv) > 2 else None
    if root is None:
        referenced = {k for s in sprites.values() for k in s['kids']}
        for sid, s in sprites.items():
            if sid not in referenced and len(s['labels']) == 8:
                root = sid
                break
    print(f'# {path.split("/")[-1]}  根 sprite = {root}')
    r = sprites[root]
    layers = sorted(r['kids'])
    print(f'根帧数={r["frames"]}  方向层={layers}')
    for lc in layers:
        s = sprites.get(lc)
        if not s:
            continue
        print(f'\n## sprite {lc}  总帧={s["frames"]}  标签={len(s["labels"])}')
        labs = sorted(s['labels'].items(), key=lambda kv: kv[1])
        for i, (name, fr) in enumerate(labs):
            end = labs[i + 1][1] if i + 1 < len(labs) else s['frames']
            ev = [e for e in s['events'] if fr <= e[0] < end]
            detail = '; '.join(f'f{e[0]}:{e[1]}' for e in ev) or '（段内无事件，沿用上一段状态）'
            refs = []
            for e in ev:
                if 'cid=' in e[1]:
                    cid = int(e[1].split('cid=')[1])
                    ch = sprites.get(cid)
                    refs.append(f'cid{cid}({ch["frames"]}帧)' if ch else f'cid{cid}(shape)')
            print(f'  {name:10s} f{fr:<3d}-{end-1:<3d} 段长={end-fr:<2d}  {detail}  引用={refs}')


if __name__ == '__main__':
    main()
