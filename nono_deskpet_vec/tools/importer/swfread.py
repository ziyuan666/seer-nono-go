#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""swfread.py —— 最小 SWF 读取器，只做「拿到显示列表」这件事。

为什么不复用 nono_deskpet/tools 下的脚本：那边是烘焙流水线的一次性脚本，
互相 import 会把两套工程绑死。这里独立一份，接口稳定，后续导入器只依赖它。

支持的 tag：DefineShape(2/3/4)、DefineSprite、PlaceObject2、PlaceObject3、
RemoveObject/RemoveObject2、ShowFrame、FrameLabel、End。
其余 tag 一律跳过（不解析内容，只按长度跳过）。
"""
import struct
import zlib


def read_swf(path):
    raw = open(path, 'rb').read()
    if raw[:3] == b'CWS':
        return raw[:8] + zlib.decompress(raw[8:])
    if raw[:3] == b'ZWS':
        raise SystemExit('ZWS (LZMA) 暂不支持，请先用 ffdec 转成 CWS/FWS')
    return raw


def iter_tags(data, pos=0):
    while pos + 2 <= len(data):
        cl = struct.unpack('<H', data[pos:pos + 2])[0]
        code, length = cl >> 6, cl & 0x3F
        pos += 2
        if length == 0x3F:
            length = struct.unpack('<I', data[pos:pos + 4])[0]
            pos += 4
        payload = data[pos:pos + length]
        pos += length
        yield code, payload
        if code == 0:
            return


class _Bits:
    def __init__(self, buf, byte_pos=0):
        self.b = buf
        self.bit = byte_pos * 8

    def u(self, n):
        v = 0
        for _ in range(n):
            v = (v << 1) | ((self.b[self.bit >> 3] >> (7 - (self.bit & 7))) & 1)
            self.bit += 1
        return v

    def s(self, n):
        if n == 0:
            return 0
        v = self.u(n)
        if v & (1 << (n - 1)):
            v -= 1 << n
        return v

    def align(self):
        return (self.bit + 7) // 8


def read_matrix(buf, i):
    """返回 (matrix dict, 下一字节位置)。sx/sy 为倍数，tx/ty 为 twips。"""
    b = _Bits(buf, i)
    sx = sy = 1.0
    if b.u(1):
        n = b.u(5)
        sx = b.s(n) / 65536.0 if n else 0.0
        sy = b.s(n) / 65536.0 if n else 0.0
    r0 = r1 = 0.0
    if b.u(1):
        n = b.u(5)
        r0 = b.s(n) / 65536.0 if n else 0.0
        r1 = b.s(n) / 65536.0 if n else 0.0
    n = b.u(5)
    tx = b.s(n) if n else 0
    ty = b.s(n) if n else 0
    return {'sx': sx, 'r0': r0, 'r1': r1, 'sy': sy, 'tx': tx, 'ty': ty}, b.align()


def read_cxform(buf, i):
    """返回 (cxform dict, 下一字节位置)。mul 为倍数，add 为 -255..255。"""
    b = _Bits(buf, i)
    has_add = b.u(1)
    has_mul = b.u(1)
    n = b.u(4)
    mul = [1.0, 1.0, 1.0, 1.0]
    add = [0.0, 0.0, 0.0, 0.0]
    if has_mul:
        mul = [b.s(n) / 256.0 if n else 1.0 for _ in range(4)]
    if has_add:
        add = [float(b.s(n)) if n else 0.0 for _ in range(4)]
    return {'mul': mul, 'add': add}, b.align()


def parse_place2(payload):
    flags = payload[0]
    p = {
        'flags': flags,
        'depth': struct.unpack('<H', payload[1:3])[0],
        'cid': None, 'name': None, 'matrix': None, 'cxform': None,
        'ratio': None, 'clip': None, 'remove': bool(flags >> 7),
    }
    if p['remove']:
        return p
    i = 3
    if (flags >> 1) & 1:
        p['cid'] = struct.unpack('<H', payload[i:i + 2])[0]
        i += 2
    if (flags >> 2) & 1:
        p['matrix'], i = read_matrix(payload, i)
    if (flags >> 3) & 1:
        p['cxform'], i = read_cxform(payload, i)
    if (flags >> 4) & 1:
        p['ratio'] = struct.unpack('<H', payload[i:i + 2])[0]
        i += 2
    if (flags >> 5) & 1:
        j = payload.index(b'\x00', i)
        p['name'] = payload[i:j].decode('utf-8', 'replace')
        i = j + 1
    if (flags >> 6) & 1:
        p['clip'] = struct.unpack('<H', payload[i:i + 2])[0]
    return p


def parse_place3(payload):
    """PlaceObject3：Flags(1) Flags2(1) Depth(2) [ClassName] [CharId] [Matrix] [Cxform] ..."""
    flags, flags2 = payload[0], payload[1]
    p = {
        'flags': flags, 'flags2': flags2,
        'depth': struct.unpack('<H', payload[2:4])[0],
        'cid': None, 'name': None, 'matrix': None, 'cxform': None,
        'ratio': None, 'clip': None,
        # PlaceObject3 用 Flags2 的 bit7 表示「有 character」
        'remove': bool(flags >> 7) and not (flags >> 1) & 1,
    }
    i = 4
    if (flags2 >> 4) & 1:  # HasClassName
        i = payload.index(b'\x00', i) + 1
    if (flags >> 1) & 1:   # HasCharacter
        p['cid'] = struct.unpack('<H', payload[i:i + 2])[0]
        i += 2
    if (flags >> 2) & 1:
        p['matrix'], i = read_matrix(payload, i)
    if (flags >> 3) & 1:
        p['cxform'], i = read_cxform(payload, i)
    if (flags >> 4) & 1:
        p['ratio'] = struct.unpack('<H', payload[i:i + 2])[0]
        i += 2
    if (flags >> 5) & 1:
        j = payload.index(b'\x00', i)
        p['name'] = payload[i:j].decode('utf-8', 'replace')
        i = j + 1
    if (flags >> 6) & 1:
        p['clip'] = struct.unpack('<H', payload[i:i + 2])[0]
        i += 2
    # Flags2 bit0 = HasFilterList。带滤镜的 PlaceObject3 是「发光/阴影」的唯一来源，
    # 早期实现停在 clipDepth，把这些效果整块丢掉了（40/57 个动画有 GLOWFILTER）。
    if (flags2 >> 0) & 1:
        p['filters'], i = read_filter_list(payload, i)
    return p


def read_filter_list(buf, i):
    """解析 FILTERLIST：count(1) + 每个 filter(id 1 + 载荷)。

    载荷长度按 SWF 规范实测：Glow = color4+blurX4+blurY4+strength2+flags1（15 字节，
    没有 reserved 尾字节；用 3_5.swf 的实测 tag 长度反推验证过：24 字节 =
    flags/flags2/depth/cid/matrix 7 + count/id 17）。Blur = blurX4+blurY4+flags1。
    DropShadow = color4+blurX4+blurY4+angle4+distance4+strength2+flags1。其余
    （Bevel/Convolution/ColorMatrix…）长度依赖内部结构，遇到即停止，不猜。
    """
    if i >= len(buf):
        return [], i
    n = buf[i]
    i += 1
    out = []
    for _ in range(n):
        if i >= len(buf):
            break
        fid = buf[i]
        i += 1
        if fid == 0:  # DropShadowFilter
            if i + 23 > len(buf):
                break
            col = struct.unpack('<I', buf[i:i + 4])[0]
            bx, by, ang, dist = struct.unpack('<IIII', buf[i + 4:i + 20])
            strength = struct.unpack('<H', buf[i + 20:i + 22])[0] / 256.0
            fl = buf[i + 22]
            i += 23
            out.append({'type': 'shadow', 'color': col,
                        'blurX': bx / 65536.0, 'blurY': by / 65536.0,
                        'angle': ang / 65536.0, 'distance': dist / 65536.0,
                        'strength': strength, 'flags': fl,
                        'inner': bool(fl & 0x80), 'knockout': bool(fl & 0x40),
                        'compositeSource': bool(fl & 0x20),
                        'passes': fl & 0x1F})
        elif fid == 1:  # BlurFilter
            if i + 9 > len(buf):
                break
            bx, by = struct.unpack('<II', buf[i:i + 8])
            fl = buf[i + 8]
            i += 9
            out.append({'type': 'blur', 'color': 0,
                        'blurX': bx / 65536.0, 'blurY': by / 65536.0,
                        'strength': 1.0, 'flags': fl,
                        'passes': (fl >> 3) & 0x07})
        elif fid == 2:  # GlowFilter
            if i + 15 > len(buf):
                break
            col = struct.unpack('<I', buf[i:i + 4])[0]
            bx, by = struct.unpack('<II', buf[i + 4:i + 12])
            strength = struct.unpack('<H', buf[i + 12:i + 14])[0] / 256.0
            fl = buf[i + 14]
            i += 15
            out.append({'type': 'glow', 'color': col,
                        'blurX': bx / 65536.0, 'blurY': by / 65536.0,
                        'strength': strength, 'flags': fl,
                        'inner': bool(fl & 0x80), 'knockout': bool(fl & 0x40),
                        'compositeSource': bool(fl & 0x20),
                        'passes': fl & 0x1F})
        else:
            break
    return out, i


class Sprite:
    """一个 DefineSprite：帧列表，每帧是该帧发生的操作列表。"""

    def __init__(self, sid, nframes):
        self.id = sid
        self.nframes = nframes
        self.frames = []   # list[list[op]]
        self.labels = {}   # name -> frame index

    def state_at(self, frame_idx):
        """累积到 frame_idx（含）的 display list 快照：depth -> placement。"""
        state = {}
        for f in range(frame_idx + 1):
            if f >= len(self.frames):
                break
            for op in self.frames[f]:
                if op['op'] == 'remove':
                    state.pop(op['depth'], None)
                else:
                    d = op['depth']
                    if d in state:
                        merged = dict(state[d])
                        for k, v in op.items():
                            if k in ('cid', 'matrix', 'cxform', 'name', 'clip', 'filters') and v is not None:
                                merged[k] = v
                        state[d] = merged
                    else:
                        state[d] = dict(op)
        return state


    def entry_frame(self, depth, at_frame):
        """该 depth 上的物件「最近一次入场」的帧号（相对本 sprite 的帧轴）。

        为什么需要：嵌套 sprite 不是从父时间轴第 0 帧开始同步播放的，
        而是从它被 place 到舞台上的那一刻开始从自己的第 0 帧播。
        例如 action/2 里 sprite53 只在 sprite54 的第 55 帧才入场，它有 4 帧，
        所以 sprite54 的第 57 帧对应 sprite53 的第 (57-55)%4=2 帧。
        早期实现直接用父帧号取模（57%4=1），会取到入场前的帧号——
        表现就是本该在右侧的黑色月牙被画到了左侧。
        remove 后重新 place 会重置播放，这里按最近一次带 cid 的 place 计。
        """
        last = 0
        for f in range(min(at_frame + 1, len(self.frames))):
            for op in self.frames[f]:
                if op['op'] == 'remove':
                    if op['depth'] == depth:
                        last = None
                    continue
                if op['depth'] == depth and op.get('cid') is not None:
                    last = f
        return at_frame if last is None else last


class SWF:
    def __init__(self, path):
        self.data = read_swf(path)
        d = self.data
        nb = d[8] >> 3
        self.pos = 8 + (5 + nb * 4 + 7) // 8 + 4
        self.shapes = set()
        self.sprites = {}
        self.shape_defs = {}   # cid -> (tag, payload)；形状原始字节，供 swfshape 解析
        self.other_chars = set()
        self._parse()

    def _parse(self):
        for code, payload in iter_tags(self.data, self.pos):
            if code in (2, 22, 32, 83):  # DefineShape / 2 / 3 / 4
                if len(payload) >= 2:
                    cid = struct.unpack('<H', payload[:2])[0]
                    self.shapes.add(cid)
                    self.shape_defs[cid] = (code, payload)
            elif code in (46, 84):  # DefineMorphShape / DefineMorphShape2
                # morph 也是可渲染形状（取 start 形状）。旧版把它当 other_chars
                # 丢掉，导致引用链在 cid 100 处断掉、super4/action/4 少一块。
                if len(payload) >= 2:
                    cid = struct.unpack('<H', payload[:2])[0]
                    self.shapes.add(cid)
                    self.shape_defs[cid] = (code, payload)
            elif code == 39 and len(payload) >= 4:
                sid, n = struct.unpack('<HH', payload[:4])
                sp = Sprite(sid, n)
                self.sprites[sid] = sp
                cur = []
                for c2, p2 in iter_tags(payload, 4):
                    if c2 == 43:
                        sp.labels[p2.split(b'\x00')[0].decode('utf-8', 'replace')] = len(sp.frames)
                    elif c2 in (26, 70):
                        p = parse_place2(p2) if c2 == 26 else parse_place3(p2)
                        if p['remove'] and p['cid'] is None:
                            cur.append({'op': 'remove', 'depth': p['depth']})
                        else:
                            op = {'op': 'place', 'depth': p['depth'], 'cid': p['cid'],
                                  'matrix': p['matrix'], 'cxform': p['cxform'],
                                  'name': p['name'], 'clip': p['clip'],
                                  'filters': p.get('filters')}
                            cur.append(op)
                    elif c2 == 5 and len(p2) >= 4:  # RemoveObject
                        cur.append({'op': 'remove',
                                    'depth': struct.unpack('<H', p2[2:4])[0]})
                    elif c2 == 28 and len(p2) >= 2:  # RemoveObject2
                        cur.append({'op': 'remove',
                                    'depth': struct.unpack('<H', p2[:2])[0]})
                    elif c2 == 1:  # ShowFrame
                        sp.frames.append(cur)
                        cur = []
                    elif c2 == 0:
                        break
                if len(sp.frames) < n:
                    sp.frames.append(cur)
            elif code in (6, 7, 20, 21, 36, 37, 46, 48, 60, 75, 76, 82, 84, 88):
                # DefineBits / DefineBitsJPEG / DefineButton 等：记录 id，不当 shape
                if len(payload) >= 2:
                    self.other_chars.add(struct.unpack('<H', payload[:2])[0])
            elif code == 0:
                return


def mul_matrix(a, b):
    """矩阵复合：先 b 后 a（a∘b），SVG 语义 [a c e; b d f]。

    a = (a0,a1,a2,a3,a4,a5)，返回 a∘b。
    """
    a0, a1, a2, a3, a4, a5 = a
    b0, b1, b2, b3, b4, b5 = b
    return [
        a0 * b0 + a2 * b1,
        a1 * b0 + a3 * b1,
        a0 * b2 + a2 * b3,
        a1 * b2 + a3 * b3,
        a0 * b4 + a2 * b5 + a4,
        a1 * b4 + a3 * b5 + a5,
    ]


def swf_matrix_to_svg(m):
    """SWF matrix → SVG 矩阵 [a,b,c,d,e,f]，平移 twips 转 px。

    SWF MATRIX 的字段是 ScaleX / ScaleY / RotateSkew0 / RotateSkew1，语义为：
        x' = ScaleX*x + RotateSkew1*y + Tx
        y' = RotateSkew0*x + ScaleY*y + Ty
    而 SVG 的 [a,b,c,d] 语义为 x' = a*x + c*y、y' = b*x + d*y，
    所以 b 取 RotateSkew0、c 取 RotateSkew1。早期版本把两者写反，
    带旋转的 place（super4 动作的月牙/尖刺，旋转 ~72°）整体歪掉、高度只剩一半。
    """
    if m is None:
        return [1.0, 0.0, 0.0, 1.0, 0.0, 0.0]
    return [m['sx'], m['r0'], m['r1'], m['sy'], m['tx'] / 20.0, m['ty'] / 20.0]
