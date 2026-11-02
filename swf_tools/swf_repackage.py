#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""swf_repackage.py —— 把任意 DefineSprite 提升成根时间轴，生成可直接逐帧拖动的 SWF。

为什么需要它：
    SWF 的时间轴是树。赛尔号动作 SWF（如 super/action/3_1.swf）的根时间轴往往只有
    1 帧，画面全部来自根第 0 帧挂上去的 DefineSprite 子 MC，而子 MC 有自己的播放头和
    帧序列。JPEXS / 大多数播放器的"上一帧/下一帧"只作用于根时间轴，根只有 1 帧就
    一帧也切不动。
    本工具把目标 DefineSprite 内部的 tag 序列（含每一帧的 ShowFrame）搬到文件根时间轴，
    于是新文件的根就有 N 帧，任何播放器都能逐帧（前提：只读时间轴，不依赖脚本）。

边界与已知限制：
    1. 默认丢弃脚本 tag（DoAction / DoInitAction / DoABC / SymbolClass）。
       理由：脚本里的 stop() 会让提升后的根停在末帧，破坏"逐帧拖到底"的观察目的；
       --keep-scripts 可保留原样（用于验证"脚本确实只做 stop"这类问题）。
    2. 提升会丢掉该 sprite 在父级中的 PlaceObject 矩阵。若该 sprite 在父级带位移/缩放，
       画面会偏移 —— 用 --list 看挂载矩阵，必要时用 --offset 手动补偿。
    3. 被提升 sprite 内部的嵌套子 MC 仍是独立播放头。逐帧拖动时它们的相位取决于播放器
       实现（Ruffle 的 seek 是"重放 N 次 tick"，相位正确；JPEXS 的帧滑块是静态重建，
       子 MC 恒在第 0 帧）。要连子 MC 一起定格，需要"烘焙模式"（后续迭代）。
    4. 只处理 FWS / CWS / ZWS，不重写 shape/bitmap 内容，全部原样搬运。

用法：
    python3 swf_repackage.py <in.swf> --list                # 打印时间轴树
    python3 swf_repackage.py <in.swf> --sprite 99 -o out.swf
    python3 swf_repackage.py <in.swf> --all -d out_dir/      # 每个 sprite 各出一个文件
    python3 swf_repackage.py <in.swf> --sprite 99 --keep-scripts -o out.swf
"""

import argparse
import lzma
import os
import struct
import sys
import zlib

# —— tag 分类 ——
# 时间轴控制类：只对"当前时间轴"生效，提升时原根上的这批必须丢弃（否则会污染新根）。
TIMELINE_TAGS = {
    0,    # End
    1,    # ShowFrame
    4,    # PlaceObject
    5,    # RemoveObject
    26,   # PlaceObject2
    28,   # RemoveObject2
    43,   # FrameLabel
    70,   # PlaceObject3
}
# 脚本类：默认丢弃（见文件头第 1 条限制）。
SCRIPT_TAGS = {
    12,   # DoAction (AS1/2)
    59,   # DoInitAction
    82,   # DoABC (AS3)
    76,   # SymbolClass（把类绑定到根，脚本丢了它就没意义）
}
TAG_NAMES = {
    0: 'End', 1: 'ShowFrame', 2: 'DefineShape', 4: 'PlaceObject', 5: 'RemoveObject',
    6: 'DefineBits', 9: 'SetBackgroundColor', 12: 'DoAction', 20: 'DefineBitsLossless',
    21: 'DefineBitsJPEG2', 22: 'DefineShape2', 26: 'PlaceObject2', 28: 'RemoveObject2',
    32: 'DefineShape3', 35: 'DefineBitsJPEG3', 36: 'DefineBitsLossless2',
    39: 'DefineSprite', 43: 'FrameLabel', 45: 'SoundStreamHead2', 46: 'DefineMorphShape',
    48: 'DefineFont2', 56: 'ExportAssets', 59: 'DoInitAction', 60: 'DefineVideoStream',
    69: 'FileAttributes', 70: 'PlaceObject3', 71: 'ImportAssets', 73: 'DefineFontAlignZones',
    74: 'CSMTextSettings', 75: 'DefineFont3', 76: 'SymbolClass', 82: 'DoABC',
    83: 'DefineShape4', 84: 'DefineMorphShape2', 86: 'DefineSceneAndFrameLabelData',
    87: 'DefineBinaryData', 88: 'DefineFontName', 89: 'StartSound2',
    90: 'DefineBitsJPEG4', 91: 'DefineFont4',
}


# --------------------------------------------------------------------------
# 容器读写
# --------------------------------------------------------------------------
def load_swf(path):
    """返回 (version, rect_bytes, fps_raw, frame_count, tags)。tags 为 [(code, body)]。"""
    raw = open(path, 'rb').read()
    sig = raw[:3]
    if sig == b'CWS':
        data = raw[:8] + zlib.decompress(raw[8:])
    elif sig == b'ZWS':
        # ZWS 头是 8 字节 + 4 字节 compressedLength，正文是 LZMA(带 5 字节属性头)
        data = raw[:8] + lzma.decompress(raw[12:])
    elif sig == b'FWS':
        data = raw
    else:
        raise SystemExit(f'不是 SWF 文件: {sig!r}')

    version = data[3]
    # RECT 的 Nbits 占前 5 bit（SWF>=8 仍是 5 bit，只是字段更宽）
    nbits = data[8] >> 3
    rect_len = (5 + nbits * 4 + 7) // 8
    rect = data[8:8 + rect_len]
    off = 8 + rect_len
    fps_raw = struct.unpack('<H', data[off:off + 2])[0]
    frame_count = struct.unpack('<H', data[off + 2:off + 4])[0]
    tags = list(iter_tags(data, off + 4))
    return version, rect, fps_raw, frame_count, tags


def iter_tags(buf, pos):
    while pos + 2 <= len(buf):
        code_len = struct.unpack('<H', buf[pos:pos + 2])[0]
        code, length = code_len >> 6, code_len & 0x3F
        pos += 2
        if length == 0x3F:
            if pos + 4 > len(buf):
                return
            length = struct.unpack('<I', buf[pos:pos + 4])[0]
            pos += 4
        body = buf[pos:pos + length]
        pos += length
        yield code, body
        if code == 0:
            return


def pack_tag(code, body):
    n = len(body)
    if n < 0x3F:
        return struct.pack('<H', (code << 6) | n) + body
    return struct.pack('<H', (code << 6) | 0x3F) + struct.pack('<I', n) + body


def build_swf(version, rect, fps_raw, frame_count, tags, compress=True):
    body = rect + struct.pack('<HH', fps_raw, frame_count)
    body += b''.join(pack_tag(c, b) for c, b in tags)
    if compress:
        payload = zlib.compress(body, 9)
        # 关键：SWF 头里的 FileLength 恒为「解压后」的总长度（8 字节头 + body），
        # 写成压缩后长度会让解析器以为文件被截断（FFDec: Premature end of stream）。
        return b'CWS' + bytes([version]) + struct.pack('<I', 8 + len(body)) + payload
    return b'FWS' + bytes([version]) + struct.pack('<I', 8 + len(body)) + body


# --------------------------------------------------------------------------
# PlaceObject 解析：只为 --list 显示父子关系与挂载矩阵
# --------------------------------------------------------------------------
class BitReader:
    """矩阵/CXFORM 是位对齐结构，按位读比按字节猜稳。"""

    def __init__(self, buf, pos=0):
        self.buf = buf
        self.pos = pos * 8

    def align(self):
        self.pos = (self.pos + 7) // 8 * 8

    def bits(self, n):
        v = 0
        for _ in range(n):
            byte = self.buf[self.pos // 8]
            v = (v << 1) | ((byte >> (7 - self.pos % 8)) & 1)
            self.pos += 1
        return v

    def sbits(self, n):
        v = self.bits(n)
        if n and v & (1 << (n - 1)):
            v -= 1 << n
        return v

    def ubits(self, n):
        return self.bits(n)


def read_matrix(br):
    """返回 (scaleX, scaleY, rot0, rot1, tx, ty)，单位 px。"""
    sx = sy = 1.0
    r0 = r1 = 0.0
    if br.ubits(1):
        nb = br.ubits(5)
        sx = br.sbits(nb) / 65536.0
        sy = br.sbits(nb) / 65536.0
    if br.ubits(1):
        nb = br.ubits(5)
        r0 = br.sbits(nb) / 65536.0
        r1 = br.sbits(nb) / 65536.0
    nb = br.ubits(5)
    tx = br.sbits(nb) / 20.0
    ty = br.sbits(nb) / 20.0
    return (sx, sy, r0, r1, tx, ty)


def parse_place(payload):
    """解析 PlaceObject2(26) / PlaceObject3(70)，返回 dict 或 None。

    只取 --list 需要的字段；解析失败一律返回 None，不让一个坏 tag 打断整个导出。
    """
    try:
        flags1 = payload[0]
        i = 1
        if flags1 & 0x80:  # HasClipActions(bit7) 实际是 Reserved，PlaceObject3 另有 flags2
            pass
        depth = struct.unpack('<H', payload[i:i + 2])[0]
        i += 2
        info = {'depth': depth, 'cid': None, 'name': None, 'remove': False,
                'move': False, 'matrix': None}
        if flags1 & 0x02:  # HasCharacter
            info['cid'] = struct.unpack('<H', payload[i:i + 2])[0]
            i += 2
        if flags1 & 0x04:  # HasMatrix
            br = BitReader(payload, i)
            info['matrix'] = read_matrix(br)
            br.align()
            i = br.pos // 8
        if flags1 & 0x08:  # HasColorTransform：跳过（--list 用不到）
            br = BitReader(payload, i)
            br.ubits(1)
            nb = br.ubits(4)
            for _ in range(4):
                if br.ubits(1):
                    br.sbits(nb)
            br.align()
            i = br.pos // 8
        if flags1 & 0x10:  # HasRatio
            i += 2
        if flags1 & 0x20:  # HasName
            j = payload.index(b'\x00', i)
            info['name'] = payload[i:j].decode('utf-8', 'replace')
            i = j + 1
        if flags1 & 0x40:  # HasClipDepth
            i += 2
        # PlaceObject3 多一个 flags2 字节（HasFilterList/HasBlendMode/HasCacheAsBitmap）
        if payload is not None and len(payload) > i and info['cid'] is None:
            pass
        return info
    except Exception:
        return None


# --------------------------------------------------------------------------
# 结构分析
# --------------------------------------------------------------------------
def collect_sprites(tags):
    """返回 {sid: (frame_count, inner_tags)}。inner_tags 不含 DefineSprite 头。"""
    out = {}
    for code, body in tags:
        if code == 39 and len(body) >= 4:
            sid, nframes = struct.unpack('<HH', body[:4])
            out[sid] = (nframes, list(iter_tags(body, 4)))
    return out


def refs_of(inner_tags):
    """从 sprite 内部 tag 提取 (frame, depth, cid) 引用，用于建父子关系。"""
    refs = []
    frame = 0
    for code, body in inner_tags:
        if code == 1:
            frame += 1
        elif code in (4, 26, 70):
            info = parse_place(body)
            if info and info['cid']:
                refs.append((frame, info['depth'], info['cid'], info['matrix'], info['name']))
    return refs


# --------------------------------------------------------------------------
# 舞台 RECT（视口）读写
# --------------------------------------------------------------------------
def parse_rect(buf):
    """RECT = 5 bit Nbits + 4 个 Nbits 有符号值（单位 twips，1px = 20）。"""
    bits = ''.join(f'{b:08b}' for b in buf)
    n = int(bits[:5], 2)
    vals = []
    for i in range(4):
        s = bits[5 + i * n:5 + (i + 1) * n]
        v = int(s, 2)
        if s and s[0] == '1':
            v -= 1 << n
        vals.append(v / 20.0)
    # SWF 的 RECT 字段顺序是 Xmin, Xmax, Ymin, Ymax（不是常见的 xmin/ymin/xmax/ymax）
    return (vals[0], vals[1], vals[2], vals[3])  # (xmin, xmax, ymin, ymax) px


def encode_rect(vals):
    twips = [int(round(v * 20)) for v in vals]
    n = 5
    for v in twips:
        need = v.bit_length() + 1 if v >= 0 else (~v).bit_length() + 1
        n = max(n, need)
    bits = f'{n:05b}'
    for v in twips:
        if v < 0:
            v += 1 << n
        bits += f'{v & ((1 << n) - 1):0{n}b}'
    bits += '0' * (-len(bits) % 8)
    return int(bits, 2).to_bytes(len(bits) // 8, 'big')


# --------------------------------------------------------------------------
# 累计位移：把 sprite 内部坐标映射回舞台坐标
# --------------------------------------------------------------------------
def pure_translate(m):
    """只认纯平移矩阵；带旋转/缩放的链路上无法用视口偏移补偿，需人工处理。"""
    if not m:
        return True
    sx, sy, r0, r1, tx, ty = m
    return abs(sx - 1) < 1e-6 and abs(sy - 1) < 1e-6 and abs(r0) < 1e-6 and abs(r1) < 1e-6


def chain_offset(tags, sprites, sid):
    """从 root 往下找 sid 的引用链，累加平移量。

    返回 (tx, ty, ok)。ok=False 表示链上有旋转/缩放，偏移只是近似值。
    注意：同一 sprite 可能在多处被引用（镜像左右两半），此时只能取第一条链。
    """
    parents = {}
    for pid, (nf, inner) in sprites.items():
        for f, d, cid, m, name in refs_of(inner):
            parents.setdefault(cid, []).append((pid, m))
    # root 的引用
    for f, d, cid, m, name in refs_of(tags):
        parents.setdefault(cid, []).append((None, m))

    def walk(cur, seen):
        if cur in seen or cur is None:
            return (0.0, 0.0, True)
        seen.add(cur)
        plist = parents.get(cur)
        if not plist:
            return (0.0, 0.0, True)
        pid, m = plist[0]
        tx, ty, ok = walk(pid, seen)
        if m:
            ok = ok and pure_translate(m)
            tx += m[4]
            ty += m[5]
        return (tx, ty, ok)

    return walk(sid, set())


def print_tree(tags, sprites):
    root_refs = refs_of(tags)
    print(f'root  帧数={sum(1 for c, _ in tags if c == 1)}')
    for f, d, cid, m, name in root_refs:
        print(f'  [frame {f}] depth={d} -> sprite/shape {cid}'
              + (f' name={name}' if name else '')
              + (f' matrix={m}' if m and m != (1.0, 1.0, 0.0, 0.0, 0.0, 0.0) else ''))
    print()
    parents = {}
    for sid, (nf, inner) in sprites.items():
        for f, d, cid, m, name in refs_of(inner):
            parents.setdefault(cid, []).append((sid, f, d, m, name))
    for sid in sorted(sprites):
        nf, inner = sprites[sid]
        labels = [b.split(b'\x00')[0].decode('utf-8', 'replace')
                  for c, b in inner if c == 43]
        kid_list = []
        for cid, lst in sorted(parents.items()):
            for (psid, f, d, m, name) in lst:
                if psid == sid:
                    kid_list.append(f'{cid}@depth{d}/frame{f}')
        pinfo = '; '.join(f'sprite {p[0]}@depth{p[2]}/frame{p[1]}'
                          + (f' matrix={p[3]}' if p[3] and p[3] != (1.0, 1.0, 0.0, 0.0, 0.0, 0.0) else '')
                          for p in parents.get(sid, []))
        print(f'sprite {sid:4d} 帧数={nf:4d}'
              + (f' 标签={labels}' if labels else '')
              + (f' 子级=[{", ".join(kid_list)}]' if kid_list else '')
              + (f' 被引用={pinfo}' if pinfo else ' (未被引用)'))


# --------------------------------------------------------------------------
# 提升
# --------------------------------------------------------------------------
def _strip_trailing_end(inner):
    """sprite 的 tag 序列以 End 收尾，搬到根上会把后面的帧全部截断，必须先摘掉。"""
    out = []
    for code, body in inner:
        if code == 0:
            continue
        out.append((code, body))
    return out


def promote(tags, sprites, sid, keep_scripts=False, keep_root_defs=True):
    if sid not in sprites:
        raise SystemExit(f'找不到 sprite {sid}')
    nf_target, inner = sprites[sid]

    defs = []
    for code, body in tags:
        if code in TIMELINE_TAGS:
            continue                      # 原根时间轴全部丢弃
        if code in SCRIPT_TAGS and not keep_scripts:
            continue                      # 默认去脚本，避免 stop() 卡住
        defs.append((code, body))

    seq = _strip_trailing_end(inner)
    frame_count = sum(1 for c, _ in seq if c == 1)
    if frame_count == 0:
        raise SystemExit(f'sprite {sid} 没有任何 ShowFrame，无法作为根时间轴')
    new_tags = defs + seq + [(0, b'')]
    return new_tags, frame_count


def build_one(tags, sprites, rect, version, fps_raw, sid, keep_scripts, offset_mode, compress):
    """生成一个提升后的 SWF；返回 (bytes, 帧数, 偏移 tx, ty, 是否纯平移链)。"""
    seq, nf = promote(tags, sprites, sid, keep_scripts)

    if offset_mode == 'none':
        tx = ty = 0.0
        ok = True
    elif offset_mode == 'auto':
        tx, ty, ok = chain_offset(tags, sprites, sid)
    else:
        tx, ty = [float(x) for x in offset_mode.split(',')]
        ok = True

    xmin, xmax, ymin, ymax = parse_rect(rect)
    # 视口反向平移：内容在 sprite 内部坐标 c 上，原舞台位置是 c+tx，
    # 要让 (c - xmin') == (c + tx - xmin)，即 xmin' = xmin - tx。
    new_rect = encode_rect((xmin - tx, xmax - tx, ymin - ty, ymax - ty))
    return build_swf(version, new_rect, fps_raw, nf, seq, compress), nf, tx, ty, ok


def main():
    ap = argparse.ArgumentParser(description='把 DefineSprite 提升成根时间轴，生成可逐帧拖动的 SWF')
    ap.add_argument('input')
    ap.add_argument('--list', action='store_true', help='打印时间轴树后退出')
    ap.add_argument('--sprite', type=int, help='要提升的 sprite id')
    ap.add_argument('--all', action='store_true', help='每个 sprite 各导出一个文件')
    ap.add_argument('-o', '--output', help='输出文件（--sprite 时用）')
    ap.add_argument('-d', '--outdir', help='输出目录（--all 时用）')
    ap.add_argument('--keep-scripts', action='store_true', help='保留 DoABC/DoAction/SymbolClass')
    ap.add_argument('--offset', default='auto',
                    help='视口补偿：auto(默认，按引用链累加平移) | none | X,Y')
    ap.add_argument('--no-compress', action='store_true', help='输出 FWS 未压缩')
    args = ap.parse_args()

    version, rect, fps_raw, root_frames, tags = load_swf(args.input)
    sprites = collect_sprites(tags)
    fps = fps_raw / 256.0
    stage = parse_rect(rect)
    compress = not args.no_compress

    if args.list or (not args.sprite and not args.all):
        print(f'文件: {args.input}')
        print(f'版本: {version}  fps: {fps}  根帧数: {root_frames}  定义 sprite 数: {len(sprites)}')
        sx0, sx1, sy0, sy1 = stage
        print(f'舞台: x[{sx0:.2f}, {sx1:.2f}] y[{sy0:.2f}, {sy1:.2f}] = {sx1-sx0:.2f} x {sy1-sy0:.2f} px')
        print()
        print_tree(tags, sprites)
        if not args.sprite and not args.all:
            print()
            print('用 --sprite <id> 提升某条子时间轴为根，或 --all 全部导出。')
        return

    stem = os.path.splitext(os.path.basename(args.input))[0]

    if args.all:
        outdir = args.outdir or f'{stem}_frames'
        os.makedirs(outdir, exist_ok=True)
        for sid in sorted(sprites):
            try:
                data, nf, tx, ty, ok = build_one(tags, sprites, rect, version, fps_raw,
                                                 sid, args.keep_scripts, args.offset, compress)
            except SystemExit as e:
                # 单帧 sprite 可能是纯占位/定义容器，跳过而不是中断整批导出
                print(f'跳过 sprite {sid}: {e}')
                continue
            path = os.path.join(outdir, f'{stem}_s{sid}.swf')
            open(path, 'wb').write(data)
            warn = '' if ok else '  [警告: 引用链含旋转/缩放，偏移为近似值]'
            print(f'{path}  ({nf} 帧, offset=({tx:.2f},{ty:.2f}), {len(data)} 字节){warn}')
        return

    data, nf, tx, ty, ok = build_one(tags, sprites, rect, version, fps_raw,
                                     args.sprite, args.keep_scripts, args.offset, compress)
    out = args.output or f'{stem}_s{args.sprite}.swf'
    open(out, 'wb').write(data)
    dur = nf / fps if fps else 0
    print(f'已写出 {out}')
    print(f'  根时间轴: {nf} 帧 @ {fps} fps ≈ {dur:.2f}s  |  {len(data)} 字节')
    print(f'  视口补偿: ({tx:.2f}, {ty:.2f})' + ('' if ok else '  [警告: 引用链含旋转/缩放，偏移为近似值]'))
    print(f'  脚本: {"保留" if args.keep_scripts else "已丢弃（不再有 stop()，可拖到末帧）"}')


if __name__ == '__main__':
    main()
