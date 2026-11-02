#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""swfshape.py —— 自研 SWF 形状解析器（SHAPERECORD → SVG path）。

为什么自己写（2026-10-25，代号 20261025-02）：
    1. 现有链路依赖 FFDec 导出 SVG，等于把「新建形态/表情」绑在 Flash 工具链上；
    2. FFDec 的 `-export shape` 不包含 morph（tag 46/84），必须另用 `-export morphshape`，
       两套输出格式还不完全一样，容易漏；
    3. idle 多方向重导要在 FFDec 里按 cid 逐个 select，脚本化很别扭。
    目标：SWF 只在导入时读一次，之后完全脱离 Flash。

输出口径与 FFDec 对齐（已实测锁定，见 变更工程/当前改动/20261025-02/说明.md）：
    d       —— 绝对 px 坐标（twips/20），与原版 shape 坐标一致
    base    —— translate(-bounds.xmin_px, -bounds.ymin_px)，即 FFDec 的 <g transform>
    width/  —— shapeBounds 的宽高（px）
    height
    fill-rule —— 统一 evenodd（FFDec 23461 条 path 全是 evenodd）

支持：
    DefineShape  (tag 2)   RGB 颜色、无 LINESTYLE2
    DefineShape2 (tag 22)  RGBA、LINESTYLE2
    DefineShape3 (tag 32)  RGBA、LINESTYLE2
    DefineShape4 (tag 83)  额外 EdgeBounds + winding 标志
    DefineMorphShape  (tag 46)  取 start 形状（ratio 0）
    DefineMorphShape2 (tag 84)  取 start 形状
    FILLSTYLE: solid / linear / radial / focal radial（bitmap 记为 unsupported）
    LINESTYLE: LINESTYLE / LINESTYLE2（含 fill 类型描边记为纯色降级）

不支持（遇到会记到 warnings，不静默）：
    - bitmap fill（0x40~0x43）：NONO 素材实测 0 个，但保留告警
    - LINESTYLE2 的 gradient fill 描边：降级为灰色
"""
import math
import os
import struct

# ---------- 位读取 ----------


class BR:
    """按位读取器。bit 是绝对位位置，方便和字节操作混用。"""

    def __init__(self, buf, byte_pos=0):
        self.buf = buf
        self.bit = byte_pos * 8

    def ub(self, n):
        v = 0
        for _ in range(n):
            i = self.bit >> 3
            v = (v << 1) | ((self.buf[i] >> (7 - (self.bit & 7))) & 1)
            self.bit += 1
        return v

    def sb(self, n):
        if n == 0:
            return 0
        v = self.ub(n)
        if v & (1 << (n - 1)):
            v -= 1 << n
        return v

    def align(self):
        self.bit = ((self.bit + 7) // 8) * 8

    @property
    def bytepos(self):
        return self.bit >> 3

    def seek(self, byte_pos):
        self.bit = byte_pos * 8

    def u8(self):
        p = self.bytepos
        v = self.buf[p]
        self.bit += 8
        return v

    def u16(self):
        p = self.bytepos
        v = struct.unpack('<H', self.buf[p:p + 2])[0]
        self.bit += 16
        return v

    def u32(self):
        p = self.bytepos
        v = struct.unpack('<I', self.buf[p:p + 4])[0]
        self.bit += 32
        return v

    def rgb(self):
        p = self.bytepos
        r, g, b = self.buf[p], self.buf[p + 1], self.buf[p + 2]
        self.bit += 24
        return (r, g, b)

    def rgba(self):
        p = self.bytepos
        r, g, b, a = self.buf[p], self.buf[p + 1], self.buf[p + 2], self.buf[p + 3]
        self.bit += 32
        return (r, g, b, a)


def read_rect(br):
    """RECT → (xmin, xmax, ymin, ymax)，单位 twips。"""
    n = br.ub(5)
    return (br.sb(n), br.sb(n), br.sb(n), br.sb(n))


def read_matrix(br):
    """MATRIX → dict(sx, r0, r1, sy, tx, ty)，tx/ty 为 twips。"""
    sx = sy = 1.0
    if br.ub(1):
        n = br.ub(5)
        sx = br.sb(n) / 65536.0 if n else 0.0
        sy = br.sb(n) / 65536.0 if n else 0.0
    r0 = r1 = 0.0
    if br.ub(1):
        n = br.ub(5)
        r0 = br.sb(n) / 65536.0 if n else 0.0
        r1 = br.sb(n) / 65536.0 if n else 0.0
    n = br.ub(5)
    tx = br.sb(n) if n else 0
    ty = br.sb(n) if n else 0
    br.align()
    return {'sx': sx, 'r0': r0, 'r1': r1, 'sy': sy, 'tx': tx, 'ty': ty}


def hexc(c):
    if len(c) == 3:
        return '#%02x%02x%02x' % c
    return '#%02x%02x%02x' % c[:3]


# ---------- 样式表 ----------


def read_gradient(br, shape_version, focal):
    """GRADIENT → dict。shape_version 1/2 用 RGB，3/4 用 RGBA。"""
    spread = br.ub(2)
    interp = br.ub(2)
    n = br.ub(4)
    stops = []
    for _ in range(n):
        ratio = br.u8()
        # 只有 DefineShape3/4（version>=3）的颜色是 RGBA；Shape1/2 一律 RGB。
        col = br.rgba() if shape_version >= 3 else br.rgb() + (255,)
        st = {'offset': round(ratio / 255.0, 4), 'color': hexc(col), 'opacity': round(col[3] / 255.0, 4)}
        stops.append(st)
    focal_point = None
    if focal:
        # FIXED8: SB[16] / 256，范围 -1..1
        focal_point = br.sb(16) / 256.0
    return {
        'spread': ['pad', 'reflect', 'repeat', 'pad'][spread],
        'interp': interp,
        'stops': stops,
        'focal': focal_point,
    }


def read_fillstyle(br, tag, morph=False):
    """FILLSTYLE → dict 或 None（bitmap 不支持）。morph 时把 end 部分读掉丢弃。"""
    sv = shape_version_of(tag)
    t = br.u8()
    if t == 0x00:  # solid
        c1 = br.rgba() if sv >= 3 else br.rgb() + (255,)
        if morph:
            br.rgba()
        return {'kind': 'solid', 'color': hexc(c1), 'alpha': round(c1[3] / 255.0, 4)}
    if t in (0x10, 0x12, 0x13):  # linear / radial / focal radial
        m = read_matrix(br)
        g = read_gradient(br, sv, focal=(t == 0x13))
        if morph:
            read_matrix(br)
            read_gradient(br, sv, focal=(t == 0x13))
        g['type'] = 'linear' if t == 0x10 else 'radial'
        # MATRIX 的 tx/ty 是 twips，SVG 的 gradientTransform 用 px。
        # 不除 20 会让渐变整体偏出形状（实测 cid 27-32 渲染全空）。
        g['matrix'] = [m['sx'], m['r0'], m['r1'], m['sy'],
                       m['tx'] / 20.0, m['ty'] / 20.0]
        if t == 0x13:
            g['focal_point'] = g.pop('focal')
        else:
            g.pop('focal', None)
        return {'kind': 'gradient', 'gradient': g}
    if t in (0x40, 0x41, 0x42, 0x43):
        br.u16()  # BitmapId
        read_matrix(br)
        if morph:
            read_matrix(br)
        return {'kind': 'bitmap', 'type': t}
    # 未知类型：无法安全跳过，抛错让上层记录（带位流位置，便于定位错位）
    raise ValueError('未知 FILLSTYLE 类型 0x%02x @byte %d bit %d tag %d'
                     % (t, br.bytepos, br.bit, tag))


def read_fillstylearray(br, tag, morph=False):
    sv = shape_version_of(tag)
    n = br.u8()
    if n == 0xFF and sv >= 2:
        n = br.u16()
    out = []
    for _ in range(n):
        out.append(read_fillstyle(br, tag, morph))
    return out


def read_linestyle(br, tag, morph=False):
    """LINESTYLE / LINESTYLE2 → dict。"""
    sv = shape_version_of(tag)
    if sv >= 4:
        # LINESTYLE2：Flags 是 bit-packed 的 UB 序列，不能当 u16 小端整体读。
        w1 = br.u16()
        if morph:
            br.u16()
        start_cap = br.ub(2)
        join = br.ub(2)
        has_fill = br.ub(1)
        br.ub(1)   # NoHScaleFlag
        br.ub(1)   # NoVScaleFlag
        br.ub(1)   # PixelHintingFlag
        br.ub(5)   # Reserved
        br.ub(1)   # NoCloseFlag
        br.ub(2)   # EndCapStyle
        br.align()
        if join == 2:
            br.u16()  # MiterLimitFactor (FIXED8)
        if has_fill:
            f = read_fillstyle(br, tag, morph=False)
            col = f.get('color', '#888888') if f else '#888888'
            alpha = f.get('alpha', 1.0) if f else 1.0
        else:
            c1 = br.rgba()
            if morph:
                br.rgba()
            col, alpha = hexc(c1), round(c1[3] / 255.0, 4)
        return {'color': col, 'width': round(w1 / 20.0, 3), 'alpha': alpha, 'cap': start_cap, 'join': join}
    w1 = br.u16()
    if morph:
        br.u16()
    # LINESTYLE 的颜色只有 DefineShape3/4（tag 32/83）才是 RGBA，
    # DefineShape1/2（tag 2/22）是 RGB。按「sv>=2 用 RGBA」会多读 1 字节，
    # 后面整条记录流错位——cid 124（tag 22、1 个 linestyle）在 byte 144 报
    # 「未知 FILLSTYLE 0x84」就是这个原因。linestyle 数为 0 的 shape 不会暴露。
    c1 = br.rgba() if sv >= 3 else br.rgb() + (255,)
    if morph:
        br.rgba() if sv >= 2 else br.rgb()
    return {'color': hexc(c1), 'width': round(w1 / 20.0, 3),
            'alpha': round(c1[3] / 255.0, 4), 'cap': 0, 'join': 0}


def read_linestylearray(br, tag, morph=False):
    sv = shape_version_of(tag)
    n = br.u8()
    if n == 0xFF and sv >= 2:
        n = br.u16()
    return [read_linestyle(br, tag, morph) for _ in range(n)]


def shape_version_of(tag):
    """把 tag 映射成「颜色/样式版本」：1=RGB 旧版, 2=RGBA, 3=RGBA+Shape3/4, 4=LINESTYLE2。

    tag 2   → Shape1（RGB，无 LINESTYLE2）
    tag 22  → Shape2（RGBA，有 LINESTYLE2 计数扩展）
    tag 32  → Shape3（RGBA）
    tag 83  → Shape4（RGBA + LINESTYLE2）
    tag 46/84 → morph，按 Shape3/4 处理（morph 只出现在 SWF8+）
    """
    return {2: 1, 22: 2, 32: 3, 83: 4, 46: 3, 84: 4}.get(tag, 3)


# ---------- SHAPERECORD ----------


def parse_shape_records(br, tag, cur_fills, cur_lines, warn):
    """解析 SHAPERECORD 序列 → [(fill, line, [segments])]。

    每条子路径归属「起始时的 fillStyle0」（SWF 里 fill0 = 左侧填充）；
    fill0 为 0 时退回 fillStyle1（右填充，FFDec 也这么处理）。
    segments 是 [(cmd, x, y, cx, cy)]，坐标单位 twips。

    StateNewStyles 出现时样式表整体替换（SWF 语义），这里用局部表 + 对象去重，
    保证输出里的 fill 引用不串味。
    """
    nfill = br.ub(4)
    nline = br.ub(4)
    pen = (0, 0)
    fill0 = fill1 = 0
    line = 0
    sub = None
    subs = []

    def cur_fill(idx):
        if 1 <= idx <= len(cur_fills):
            return cur_fills[idx - 1]
        return None

    def cur_line(idx):
        if 1 <= idx <= len(cur_lines):
            return cur_lines[idx - 1]
        return None

    def close_sub():
        nonlocal sub
        if sub and len(sub[3]) >= 2:
            subs.append(sub)
        sub = None

    while True:
        if br.ub(1):
            # EDGERECORD
            straight = br.ub(1)
            nb = br.ub(4) + 2
            sx, sy = pen           # 本段起点（子路径的 M 要用它，不是终点）
            if straight:
                if br.ub(1):
                    dx = br.sb(nb)
                    dy = br.sb(nb)
                elif br.ub(1):
                    dx, dy = 0, br.sb(nb)
                else:
                    dx, dy = br.sb(nb), 0
                pen = (pen[0] + dx, pen[1] + dy)
                cmd = 'L'
                cx = cy = None
            else:
                cdx, cdy = br.sb(nb), br.sb(nb)
                adx, ady = br.sb(nb), br.sb(nb)
                cx, cy = pen[0] + cdx, pen[1] + cdy
                pen = (pen[0] + cdx + adx, pen[1] + cdy + ady)
                cmd = 'Q'
            if sub is None:
                sub = (cur_fill(fill0), cur_fill(fill1), cur_line(line), [('M', sx, sy, None, None)])
            sub[3].append((cmd, pen[0], pen[1], cx, cy))
            continue

        # ---------- Non-edge (StyleChange) ----------
        state_new = br.ub(1)
        state_line = br.ub(1)
        state_fill1 = br.ub(1)
        state_fill0 = br.ub(1)
        state_move = br.ub(1)
        if not (state_new or state_line or state_fill1 or state_fill0 or state_move):
            break  # END record
        if state_new:
            # StateNewStyles 同时开启「新的子形状」：pen 回到原点，move delta 相对新原点。
            # 实测 cid 10（tag 32）第二段：不重置时 move delta (805,157) 会叠在上一段的
            # (649,-26) 上，整段路径偏出 bounds 12.7px；重置后与 FFDec 的
            # M40.25 7.85 完全一致。
            close_sub()
            pen = (0, 0)
        if state_move:
            mb = br.ub(5)
            # 实测口径（与 FFDec 一致）：MoveDelta 直接作为子路径起点（绝对），
            # 不是叠加到上一段 pen 上。
            # 证据：cid 42 第二个环 MoveBits=9 delta=(149,218)，FFDec 起点
            # M7.45 10.9 = (149,218)/20；若按「相对」累加会得到 (761,191)，
            # 整个环偏 612 twips（30.6px）。cid 10 第二段同理（delta=(805,157)）。
            pen = (br.sb(mb), br.sb(mb)) if mb else (0, 0)
            close_sub()
        new_f0 = br.ub(nfill) if state_fill0 else fill0
        new_f1 = br.ub(nfill) if state_fill1 else fill1
        new_line = br.ub(nline) if state_line else line
        if new_f0 != fill0 or new_f1 != fill1 or new_line != line:
            # 样式真的变了才断开。作为样式切换重复赋值（值没变）时断开，
            # 会把同一条边界切成碎片，各自闭合后 evenodd 互相挖空，
            # 面积反而变小（cid 69 从 0.447 掉到 0.322）。
            close_sub()
        fill0, fill1, line = new_f0, new_f1, new_line
        if state_new:
            # 样式表是字节型结构（count 为 UI8），必须先从 bit 流对齐到字节边界。
            # 实测 cid 10（tag 32）：不对齐会从 bit 475 开始读，实际 count 在 bit 480，
            # 差 5 位导致把 0x00 当成 0 个填充、后续全部错位（报「未知 FILLSTYLE 0x17」）。
            br.align()
            # 嵌套样式表必须用「原始 shape 的 tag」解析。写死 32 会让 tag 22 的
            # shape 按 Shape3 的 RGBA/LINESTYLE2 读，位流立刻错位
            # （cid 11 越界、cid 9 报未知 FILLSTYLE 0xd7）。
            cur_fills = read_fillstylearray(br, tag)
            cur_lines = read_linestylearray(br, tag)
            br.align()
            nfill = br.ub(4)
            nline = br.ub(4)
    close_sub()
    return subs


# ---------- 主入口 ----------



def parse_define_shape(payload, tag):
    """解析 DefineShape/2/3/4 → dict。payload 从 CharacterID 开始。"""
    cid = struct.unpack('<H', payload[:2])[0]
    br = BR(payload, 2)
    bounds = read_rect(br)
    fill_rule = 'evenodd'
    if tag == 83:
        # ShapeBounds 的 RECT 按位结束，EdgeBounds 从下一个字节边界开始。
        # 不先 align 会把半个字节当 EdgeBounds 的 NBits 读，整个位流立刻错位
        # （super/exp/2_3 的 cid 62 就是这样报「未知 FILLSTYLE 0x55」）。
        br.align()
        read_rect(br)          # EdgeBounds
        br.align()             # RECT 按位结束，flags 从下一个字节开始
        flags = br.u8()        # UB[5] Reserved + UsesFillWindingRule + UsesNonScaling + UsesScalarHints
        # 低 3 位依次是 ScalarHints / NonScaling / FillWindingRule。
        # UsesFillWindingRule=1 时填充用非零绕数，不是奇偶——一律按 evenodd 会
        # 少填一大块（cid 69 实测 120121px vs FFDec 266536px）。
        if (flags >> 2) & 1:
            fill_rule = 'nonzero'
    br.align()
    fill_pos = br.bytepos
    fills = read_fillstylearray(br, tag)
    lines = read_linestylearray(br, tag)
    br.align()
    warn = []
    subs = parse_shape_records(br, tag, fills, lines, warn)
    # Shape4 的 UsesFillWindingRule 位决定填充规则：bit=1 用 nonzero。
    # 必须把 fill_rule 显式传下去——不传会落到 _assemble 的默认 evenodd，
    # 让上面读到的 flags 完全失效（cid 69 的 15 个子路径互相重叠，evenodd
    # 下重叠区被挖空 47k px，IoU 0.816）。
    return _assemble(cid, tag, bounds, fills, lines, subs, warn, fill_rule)


def parse_define_morph(payload, tag):
    """解析 DefineMorphShape/2 → 只取 start（ratio 0）形状。"""
    cid = struct.unpack('<H', payload[:2])[0]
    br = BR(payload, 2)
    bounds = read_rect(br)
    br.align()
    read_rect(br)              # EndBounds
    br.align()
    if tag == 84:
        read_rect(br)          # StartEdgeBounds
        br.align()
        read_rect(br)          # EndEdgeBounds
        br.align()
        br.u8()                # Reserved UB[6]
    br.u32()                   # Offset（到 EndEdits）
    br.align()
    fills = read_fillstylearray(br, tag, morph=True)
    lines = read_linestylearray(br, tag, morph=True)
    br.align()
    warn = []
    subs = parse_shape_records(br, tag, fills, lines, warn)
    # morph 分支没有 Shape4 的 flags 字节，填充规则固定 evenodd（与 FFDec 一致）。
    return _assemble(cid, tag, bounds, fills, lines, subs, warn)


def _reverse_segs(segs):
    """把一条闭合子路径反向：M A L B Q(c)C  →  M C Q(c)B L A。

    SWF 的 fill1（右侧填充）在几何上是同一组边的反向环。
    注意必须回到环的起点（segs[0] 的 M 点），漏掉最后一段会让环缺一条边
    （实测 cid 9 偏差 5.13px 就是缺了 Q38.55 1.55 42.55 5.3 这一段的逆向）。
    """
    if len(segs) < 2:
        return segs
    start0 = (segs[0][1], segs[0][2])
    pts = list(segs[1:])
    out = [('M', pts[-1][1], pts[-1][2], None, None)]
    for i in range(len(pts) - 1, -1, -1):
        cmd, x, y, cx, cy = pts[i]
        ex, ey = (pts[i - 1][1], pts[i - 1][2]) if i > 0 else start0
        if cmd == 'Q':
            out.append(('Q', ex, ey, cx, cy))
        else:
            out.append(('L', ex, ey, None, None))
    return out


def _pt_of(seg):
    return (seg[1], seg[2])


def _faces(parts):
    """按平面图面遍历重建填充边界。

    返回 dict（d/used/total/area_px）或 None。**只有 used == total 才可用**：
    边没走完说明图不封闭（内部边没剔干净或存在未切分的交叉点），此时结果
    会缺块，必须回退旧串链（实测 40 个 shape 会因此退化）。
    """
    try:
        import faces
    except Exception:
        return None
    try:
        return faces.boundaries(parts)
    except Exception:
        return None


def _union_d(chains):
    """多边形并集（失败返回 None，调用方回退到原 evenodd 多环输出）。"""
    try:
        import polyunion
    except Exception:
        return None
    try:
        return polyunion.union_d(chains)
    except Exception:
        return None


def _chain_subpaths(parts, tol=0.01):
    """把同一填充的子路径按「上一个终点 == 下一个起点」串成链。

    为什么必须串：SWF 一条闭合边界常被多条 StyleChange 切成若干段，各段端点严格
    相接。不串的话每段各自闭合，evenodd 下互相挖空，面积塌掉一半
    （super/exp/2_3 cid 69：33 个独立 M 段 → 98350px，FFDec 13 个 M → 266536px）。
    串接只在端点吻合时进行，不吻合的一律保持独立，避免把无关环焊在一起。
    """
    chains = []

    def closed(ch):
        return len(ch) > 1 and abs(_pt_of(ch[0])[0] - _pt_of(ch[-1])[0]) <= tol and \
            abs(_pt_of(ch[0])[1] - _pt_of(ch[-1])[1]) <= tol

    for part in parts:
        if len(part) < 2:
            continue
        head, tail = _pt_of(part[0]), _pt_of(part[-1])
        joined = False
        for ch in chains:
            if closed(ch):
                # 已经闭合的链不能再焊新段，否则两个同心环会被连成一条边界，
                # evenodd 失去挖空效果、变成实心（cid 28 我的 5312px vs FFDec 1189px）。
                continue
            if abs(_pt_of(ch[-1])[0] - head[0]) <= tol and abs(_pt_of(ch[-1])[1] - head[1]) <= tol:
                ch.extend(part)
                joined = True
                break
            if abs(_pt_of(ch[0])[0] - tail[0]) <= tol and abs(_pt_of(ch[0])[1] - tail[1]) <= tol:
                ch[:0] = part
                joined = True
                break
        if not joined:
            chains.append(list(part))
    # 反复合并首尾相接的链（上面单向遍历可能漏掉后出现的接合点）
    merged = True
    while merged and len(chains) > 1:
        merged = False
        for i in range(len(chains)):
            if closed(chains[i]):
                continue
            for j in range(len(chains)):
                if i == j or closed(chains[j]):
                    continue
                a, b = chains[i], chains[j]
                if abs(_pt_of(a[-1])[0] - _pt_of(b[0])[0]) <= tol and \
                   abs(_pt_of(a[-1])[1] - _pt_of(b[0])[1]) <= tol:
                    a.extend(b)
                    chains.pop(j)
                    merged = True
                    break
            if merged:
                break
    return chains


def _assemble(cid, tag, bounds, fills, lines, subs, warn, fill_rule='evenodd'):
    """子路径按 fill 对象分组 → 每个填充一条 path（evenodd）。

    一条子路径同时属于 fill0（左侧，正向）和 fill1（右侧，反向）——
    SWF 的填充是「边的左右两侧」，只取 fill0 会丢掉右填充那一半。
    实测 cid 46：只取 fill0 时 path0 有 67 点、path1 只有 18 点，
    而 FFDec 是 49/40，差异就是 fill1 的环没反向收进来。
    """
    xmin, xmax, ymin, ymax = [v / 20.0 for v in bounds]

    # 只有 morph 需要扩展 bounds。
    # DefineMorphShape2 的 StartBounds 只覆盖起始轮廓，二次贝塞尔的控制点会伸出
    # bounds 之外（cid 100：bounds y 3.95~22.10，路径实际到 2.50，按 bounds 归零后
    # 显示坐标 26.05 超出 18.15 高的画布，尖刺尖端会被裁掉）。Flash 播放器按 line
    # 宽留余量渲染，不裁。
    # 普通 shape 不动：ShapeBounds 是精确几何边界，把控制点凸包并进去会把 w/h
    # 撑大、与 FFDec 的 width/height 对不上（实测 4_4.swf 68/71 → 42/71）。
    if tag in (46, 84):
        edge_pts = []
        for _f0, _f1, _ln, _segs in subs:
            for _cmd, _x, _y, _cx, _cy in _segs:
                edge_pts.append((_x / 20.0, _y / 20.0))
                if _cx is not None:
                    # 控制点纳入：贝塞尔曲线不会超出控制点凸包，收进来保证不裁。
                    edge_pts.append((_cx / 20.0, _cy / 20.0))
        if edge_pts:
            exs = [p[0] for p in edge_pts]
            eys = [p[1] for p in edge_pts]
            xmin, xmax = min(xmin, min(exs)), max(xmax, max(exs))
            ymin, ymax = min(ymin, min(eys)), max(ymax, max(eys))

    groups = {}   # id(fill) -> (fill, [segs])
    strokes = {}  # id(line) -> (line, [segs])
    interior = set()  # 两侧同填充的边（内部边）的 id(segs)
    for f0, f1, ln, segs in subs:
        if f0 is not None and f0 is f1:
            # 同一条边左右两侧是同一个填充样式：只收正向一次，
            # 否则正反两份环会叠在一起（cid 42 点数翻倍、形状偏差 30px）。
            groups.setdefault(id(f0), (f0, []))[1].append(segs)
            # 面遍历必须排除内部边：它只出现一次会让顶点出入度失衡，
            # 遍历走到死胡同（cid 69 面积从 266k 塌到 107k）。
            interior.add(id(segs))
            continue
        if f0 is not None:
            groups.setdefault(id(f0), (f0, []))[1].append(segs)
        if f1 is not None:
            # fill1（边的右侧）要反向：fill0 的边 + fill1 的反向边才首尾相接
            # 围成同一个环。不反向时两条开放折线各自闭合，细长条会变成两块
            # （plain/action/1 的 cid 12 面积 3213 vs FFDec 1443）。
            groups.setdefault(id(f1), (f1, []))[1].append(_reverse_segs(segs))
        # 描边与填充互相独立：SWF 里一条边可以同时有 fillStyle 和 lineStyle。
        # 只在「无填充」时才收描边，会漏掉有填充那部分的边框
        # （cid 42 我的 67408px vs FFDec 67935px，差的就是一圈 2px 描边）。
        # 描边与填充互相独立：SWF 里一条边可以同时有 fillStyle 和 lineStyle。
        # A/B 实测：只在「无填充」时收描边 → 89.6%；只要 ln 非 None 就收 → 90.9%。
        if ln is not None:
            strokes.setdefault(id(ln), (ln, []))[1].append(segs)
        elif f0 is None and f1 is None:
            warn.append(f'shape {cid}: 子路径既无填充也无描边，丢弃 {len(segs)} 段')
    paths = []
    lines_out = []
    for key in groups:
        f, parts = groups[key]
        # 面遍历是 Flash 填充语义的正确还原：SWF 的填充是「边的左右侧」，
        # 串链遇到 T 型接头会断（cid 69 出现 399px 抄近路线、缺 50k px²）。
        # 面遍历失败（边太少/退化）时回退旧串链，保证不退化。
        chains = _chain_subpaths(parts)
        # 串链的「首尾距离」就是补闭合直线的长度。正常闭合链 ≈ 0；
        # T 型接头断掉时会拉出长直线抄近路（cid 69 实测 399px，缺 50k px²）。
        # 只有出现这种断口才改用面遍历：面遍历会按面合并环，对本来就闭合的
        # 多环 shape（cid 5 的 55 个独立环）会合并过头、与 FFDec 不一致。
        gap = 0.0
        for ch in chains:
            if len(ch) > 1:
                gap = max(gap, math.hypot(ch[0][1] - ch[-1][1], ch[0][2] - ch[-1][2]) / 20.0)
        d = None
        if gap > 5.0 and os.getenv('SWF_FACES', '1') != '0':
            res = _faces([p for p in parts if id(p) not in interior])
            if res and res['used'] == res['total']:
                d = res['d']
        if not d:
            d = _segs_to_d(chains)
        if not d:
            continue
        item = {'d': d, 'fillRule': fill_rule}
        if f['kind'] == 'solid':
            item['fill'] = f['color']
            if f['alpha'] < 1.0:
                item['fillAlpha'] = f['alpha']
        elif f['kind'] == 'gradient':
            item['gradient'] = f['gradient']
        else:
            warn.append(f'shape {cid}: 跳过 bitmap 填充（type 0x{f.get("type", 0):02x}）')
            continue
        paths.append(item)
    for key in strokes:
        ln, parts = strokes[key]
        # 描边不串链：串接会在断开处补连接线，画出一堆 SWF 里不存在的笔划
        # （cid 42 加串接后 18302px vs FFDec 17198px）。
        d = ' '.join(_segs_to_d(pt, close=False) for pt in parts if len(pt) >= 2)
        if not d:
            continue
        item = {'d': d, 'fill': 'none', 'stroke': ln['color'],
                'strokeWidth': ln['width'] if ln['width'] > 0 else 1.0}
        if ln.get('alpha', 1.0) < 1.0:
            item['strokeAlpha'] = ln['alpha']
        lines_out.append(item)
    return {
        'id': cid,
        'tag': tag,
        'bounds': [round(xmin, 3), round(xmax, 3), round(ymin, 3), round(ymax, 3)],
        'width': round(xmax - xmin, 3),
        'height': round(ymax - ymin, 3),
        'base': [1.0, 0.0, 0.0, 1.0, round(-xmin, 4), round(-ymin, 4)],
        'paths': paths + lines_out,
        'lines': lines,
        'warnings': warn,
    }


def _fmt(v):
    s = '%.2f' % (v / 20.0)
    if s.endswith('.00'):
        s = s[:-3]
    return s


def _segs_to_d(segs, close=True):
    """segs 可以是单条 seg 列表，也可以是「链列表」（_chain_subpaths 的返回值）。"""
    if segs and isinstance(segs[0], list):
        return ' '.join(_segs_to_d(ch, close) for ch in segs if ch)
    """子路径 → SVG d。SWF 的填充边界允许不闭合，渲染时按「直线连回起点」处理；
    FFDec 输出里也是显式补一段回到起点的直线。这里同样补上，否则细长条形
    （宽 0.65px 的描边样式）会变成开放折线，点集比较和光栅填充都会差一截
    （实测 cid 33 的 11 个填充组各差 5px，就是这个原因）。"""
    if not segs:
        return ''
    out = []
    start = None
    cur = None
    first = True
    for cmd, x, y, cx, cy in segs:
        if cmd == 'M':
            # 链内后续段的 M 是「笔跳到下一段起点继续描」，不是新起一个环。
            # 已在 _chain_subpaths 里确认过端点相接，这里直接接上，
            # 不再新起子路径（否则一条边界会被拆成 33 个 M，evenodd 互相挖空）。
            if first:
                out.append('M%s %s' % (_fmt(x), _fmt(y)))
                start = (x, y)
                first = False
            elif (x, y) != cur:
                out.append('L%s %s' % (_fmt(x), _fmt(y)))
            cur = (x, y)
        elif cmd == 'L':
            out.append('L%s %s' % (_fmt(x), _fmt(y)))
            cur = (x, y)
        else:
            out.append('Q%s %s %s %s' % (_fmt(cx), _fmt(cy), _fmt(x), _fmt(y)))
            cur = (x, y)
    # 整条边界允许不闭合：渲染侧会自动连回起点，FFDec 也按此语义处理。
    if close and start is not None and cur is not None and cur != start:
        out.append('L%s %s' % (_fmt(start[0]), _fmt(start[1])))
    return ' '.join(out)


# ---------- 便捷：从 SWF 直接取 ----------


def parse_from_swf(swf, cid):
    """用 swfread.SWF 已收集的 payload 解析单个 shape。"""
    info = swf.shape_defs.get(cid)
    if info is None:
        return None
    tag, payload = info
    if tag in (2, 22, 32, 83):
        return parse_define_shape(payload, tag)
    if tag in (46, 84):
        return parse_define_morph(payload, tag)
    return None
