#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""在 SWF 里直接给指定 DefineSprite 的每个方向段补 ShowFrameTag（二进制级，不重打包）。

为什么不用 xml2swf：实测 ffdec 的 xml2swf 会改变 sprite 的组合语义——
重打包后 sprite 51 帧 1 与原件 diff=58，且展开出来的 1..18 帧完全相同（嵌套
MC 不再推进），等于把动画拍平。所以改走二进制插入，只动 sprite 内部 tag 流。

用法：
  python3 expand_sprite_frames.py <in.swf> <out.swf> <spriteId> <目标帧数逗号分隔>
例：sprite 51 按段补到 18,90,18,18,18,18,18,90
"""
import struct
import sys
import zlib


class Reader:
    def __init__(self, data):
        self.d = data
        self.i = 0

    def u8(self):
        v = self.d[self.i]; self.i += 1; return v

    def u16(self):
        v = struct.unpack_from('<H', self.d, self.i)[0]; self.i += 2; return v

    def u32(self):
        v = struct.unpack_from('<I', self.d, self.i)[0]; self.i += 4; return v

    def bits(self, n, bits_state):
        """跨字节的 UB/SB 读取，简化为按位流读（用于 RECT）。"""
        val = 0
        for _ in range(n):
            byte_pos, bit_pos = bits_state
            v = (self.d[byte_pos] >> bit_pos) & 1
            val = (val << 1) | v
            bit_pos -= 1
            if bit_pos < 0:
                byte_pos += 1
                bit_pos = 7
            bits_state = (byte_pos, bit_pos)
        return val, bits_state


def skip_rect(data, off):
    """跳过 FrameSize RECT：5 bits nBits，再读 4*nBits 位。返回下一字节偏移。"""
    bitpos = off * 8
    def read(n):
        nonlocal bitpos
        v = 0
        for _ in range(n):
            byte = data[bitpos >> 3]
            v = (v << 1) | ((byte >> (7 - (bitpos & 7))) & 1)
            bitpos += 1
        return v
    nbits = read(5)
    for _ in range(4):
        read(nbits)
    return (bitpos + 7) >> 3


def parse_tags(data, off):
    """产出 (tag_start, tag_end, code, body_start, body_end)。"""
    out = []
    i = off
    n = len(data)
    while i < n - 1:
        h = struct.unpack_from('<H', data, i)[0]
        code = h >> 6
        length = h & 0x3F
        body = i + 2
        if length == 0x3F:
            length = struct.unpack_from('<I', data, body)[0]
            body += 4
        end = body + length
        if end > n:
            break
        out.append((i, end, code, body, end))
        if code == 0:  # End
            break
        i = end
    return out


def find_sprite(data, tags, sprite_id):
    for (ts, te, code, bs, be) in tags:
        if code != 39:
            continue
        sid = struct.unpack_from('<H', data, bs)[0]
        if sid == sprite_id:
            return bs, be
    return None


def main():
    src, dst, sid, targets_s = sys.argv[1:5]
    sid = int(sid)
    targets = [int(x) for x in targets_s.split(',')]

    raw = open(src, 'rb').read()
    magic = raw[:3]
    if magic == b'CWS':
        # 8 字节头 + zlib 正文；body 从第 8 字节开始（非压缩格式里第 8 字节是 tag 流起点）
        body = zlib.decompress(raw[8:])
        head = raw[:8]
        mode = 'CWS'
    elif magic == b'FWS':
        body = raw[8:]
        head = raw[:8]
        mode = 'FWS'
    else:
        raise SystemExit('不支持的 SWF 魔数 %r' % magic)

    # body 前是 RECT + frameRate(2) + frameCount(2)，tag 流在其后
    tag_off = skip_rect(body, 0) + 4
    tags = parse_tags(body, tag_off)
    found = find_sprite(body, tags, sid)
    if not found:
        raise SystemExit('找不到 sprite %d' % sid)
    sbs, sbe = found
    # 外层 DefineSprite tag 的 header：长度字段必须同步更新，否则整条 tag 流错位
    outer = next(t for t in tags if t[2] == 39 and t[3] == sbs)
    o_ts, o_be = outer[0], outer[4]
    h = struct.unpack_from('<H', body, o_ts)[0]
    if (h & 0x3F) != 0x3F:
        raise SystemExit('sprite %d 的 tag 是短格式，本脚本只处理长格式' % sid)
    old_tag_len = struct.unpack_from('<I', body, o_ts + 2)[0]
    if o_ts + 6 + old_tag_len != o_be:
        raise SystemExit('tag 长度自检失败：%d != %d' % (o_ts + 6 + old_tag_len, o_be))
    # frameCount 在 spriteId 之后
    fc_off = sbs + 2
    old_fc = struct.unpack_from('<H', body, fc_off)[0]

    inner_start = fc_off + 2
    inner = body[inner_start:sbe]
    itags = parse_tags(inner, 0)
    total_frames = sum(1 for t in itags if t[2] == 1)
    if total_frames != old_fc:
        print('警告：ShowFrame 计数 %d != frameCount %d' % (total_frames, old_fc), file=sys.stderr)

    if len(targets) != 8:
        raise SystemExit('需要 8 段目标帧数')

    # 按 FrameLabelTag(43) 分段；补帧用「字节插入点」表达，避免切块把 RemoveObject
    # 归错段（RemoveObject 属于下一段的第 1 帧开始，必须留在上一段最后一个 ShowFrame 之后）。
    lab = [i for i, t in enumerate(itags) if t[2] == 43]
    if len(lab) != 8:
        raise SystemExit('段标签数 %d != 8' % len(lab))
    lab.append(len(itags))

    SF = b'\x40\x00'
    inserts = []          # (byte_offset, count)
    added = 0
    for bi in range(8):
        lo, hi = lab[bi], lab[bi + 1]
        sfs = [t for t in itags[lo:hi] if t[2] == 1]
        have = len(sfs)
        need = targets[bi]
        if need < have:
            raise SystemExit('段 %d 现有 %d > 目标 %d' % (bi, have, need))
        if need > have:
            inserts.append((sfs[-1][1], need - have))
            added += need - have

    new_inner = bytearray(inner)
    for off, cnt in sorted(inserts, reverse=True):
        new_inner[off:off] = SF * cnt

    # End tag 保留（原始 sprite 内最后一个 tag 通常是 End）
    tail_code = itags[-1][2] if itags else None
    if tail_code != 0:
        new_inner += b'\x00\x00'

    new_fc = old_fc + added
    out_body = bytearray()
    out_body += body[:fc_off]
    out_body += struct.pack('<H', new_fc)
    out_body += new_inner
    out_body += body[sbe:]

    # 同步外层 DefineSprite tag 的长度字段
    delta = len(new_inner) - len(inner)
    struct.pack_into('<I', out_body, o_ts + 2, old_tag_len + delta)

    if mode == 'CWS':
        comp = zlib.compress(bytes(out_body), 9)
        newhead = bytearray(head)
        struct.pack_into('<I', newhead, 4, len(comp) + 8)
        out = bytes(newhead) + comp
    else:
        newhead = bytearray(head)
        struct.pack_into('<I', newhead, 4, len(out_body) + 8)
        out = bytes(newhead) + bytes(out_body)

    open(dst, 'wb').write(out)
    print('sprite %d: %d -> %d 帧（补 %d），文件 %d -> %d 字节'
          % (sid, old_fc, new_fc, added, len(raw), len(out)))


if __name__ == '__main__':
    main()
