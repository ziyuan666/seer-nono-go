# -*- coding: utf-8 -*-
"""同一填充样式的多条边界环 → 多边形并集。

## 为什么需要

SWF 的填充语义是「边有左右两侧的样式」，一条轮廓常被切成若干段；串链接回
之后仍可能得到**多条几何上重叠、但端点不共享**的闭合环。把这些环一股脑塞进
一个 evenodd path，重叠区会被挖空——super/exp/2_3 的 cid 69 就是这样：
自研解析出 15 个独立子环，`#02/#07/#08` 互相重叠，渲染 221118px，
FFDec 266536px（IoU 0.816）。

FFDec 的做法是先把它们并成一条边界（它输出 13 个 M、主环 424 点，自研 269 点），
本模块做同一件事：曲线离散 → 整数域 union → 简化 → 输出折线 path。

## 精度取舍

曲线按弧长离散（默认步长 0.3px），并集后在 1/2000 px 网格上做，最后用
Douglas-Peucker 以 0.02px 容差简化。1x 渲染 + 3x 超采样下这个量级的折线化
看不出来；换来的是并集运算的稳健（曲线求交是真正的麻烦来源）。

## 接口

    union_d(chains) -> str
        chains 是 `_chain_subpaths` 的返回值（每项为 segs 列表，
        seg 形如 (cmd, x, y, cx, cy)，坐标单位 twips）。
        返回 SVG path 的 d；并集失败时返回 None，调用方应退回旧行为。
"""
import math

import pyclipper

TWIP = 20.0        # 1px = 20 twips
GRID = 100         # 在 twips 上再乘 100 → 1/2000 px 精度
CURVE_STEP = 0.3 * TWIP   # 曲线离散步长（twips）

SCALE = TWIP * GRID


def _dist(a, b):
    return math.hypot(a[0] - b[0], a[1] - b[1])


def sample_chain(chain, step=CURVE_STEP):
    """把一条链（M/L/Q）离散成折线点列，单位 twips。

    二次贝塞尔按控制多边形长度估弧长，再按 step 取段数，保证小曲线不至于
    退化成两条直线、大圆弧也不会太粗糙。
    """
    pts = []
    cur = None
    for seg in chain:
        cmd, x, y = seg[0], seg[1], seg[2]
        if cmd == 'M':
            if cur is None:
                pts.append((x, y))
            elif (x, y) != cur:
                pts.append((x, y))
            cur = (x, y)
        elif cmd == 'L':
            pts.append((x, y))
            cur = (x, y)
        else:  # Q：控制点 cx,cy
            cx, cy = seg[3], seg[4]
            if cur is None:
                pts.append((x, y))
                cur = (x, y)
                continue
            x0, y0 = cur
            rough = _dist((x0, y0), (cx, cy)) + _dist((cx, cy), (x, y))
            n = max(4, int(rough / step) + 1)
            for i in range(1, n + 1):
                t = i / n
                u = 1.0 - t
                px = u * u * x0 + 2 * u * t * cx + t * t * x
                py = u * u * y0 + 2 * u * t * cy + t * t * y
                pts.append((px, py))
            cur = (x, y)
    return pts


def _to_int_path(pts):
    """折线 → pyclipper 的整数路径；去掉相邻重复点。"""
    out = []
    for x, y in pts:
        ix, iy = int(round(x * GRID)), int(round(y * GRID))
        if not out or (out[-1][0] != ix or out[-1][1] != iy):
            out.append([ix, iy])
    if len(out) > 2 and out[0] == out[-1]:
        out.pop()
    return out


def union_d(chains, min_area_px=0.5):
    """多条环 → 并集后的 path d（折线，单位 px）。失败返回 None。"""
    if not chains:
        return None
    if len(chains) == 1:
        return None  # 单环无需并集，交给调用方走原路径（保留曲线）

    paths = []
    for ch in chains:
        pts = sample_chain(ch)
        if len(pts) < 3:
            continue
        ip = _to_int_path(pts)
        if len(ip) < 3:
            continue
        paths.append(ip)
    if len(paths) < 2:
        return None

    pc = pyclipper.Pyclipper()
    try:
        pc.AddPaths(paths, pyclipper.PT_SUBJECT, True)
        res = pc.Execute(pyclipper.CT_UNION, pyclipper.PFT_NONZERO, pyclipper.PFT_NONZERO)
    except Exception:
        return None
    if not res:
        return None

    # 简化（容差 0.02px，单位与 pyclipper 一致 = 1/2000 px）
    tol = 0.02 * SCALE
    out_parts = []
    for path in res:
        simp = pyclipper.SimplifyPolygon(path, pyclipper.PFT_NONZERO)
        for sp in simp:
            area_px = abs(pyclipper.Area(sp)) / (SCALE * SCALE)
            if area_px < min_area_px:
                continue
            sp = pyclipper.CleanPolygon(sp, tol)
            if not sp or len(sp) < 3:
                continue
            area_px = abs(pyclipper.Area(sp)) / (SCALE * SCALE)
            if area_px < min_area_px:
                continue
            out_parts.append(sp)
    if not out_parts:
        return None

    segs = []
    for sp in out_parts:
        x0, y0 = sp[0][0] / SCALE, sp[0][1] / SCALE
        segs.append('M%s %s' % (_fmt(x0), _fmt(y0)))
        for x, y in sp[1:]:
            segs.append('L%s %s' % (_fmt(x / SCALE), _fmt(y / SCALE)))
        segs.append('Z')
    return ' '.join(segs)


def _fmt(v):
    s = '%.2f' % v
    if s.endswith('.00'):
        s = s[:-3]
    return s
