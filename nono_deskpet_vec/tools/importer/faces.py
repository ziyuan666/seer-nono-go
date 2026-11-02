# -*- coding: utf-8 -*-
"""按平面图「面遍历」重建填充边界（Flash 填充语义的正确实现）。

## 为什么不能用串链

旧实现 `_chain_subpaths` 按「上段终点 == 下段起点」把边串成环，遇到已闭合的
链就拒绝再焊。SWF 里存在大量 **T 型接头**（三条以上边交于一点，例如主体轮廓
与尖刺相接处），此时：

    链 A 先闭合（在别处收口）→ 尖刺链 B 的端点本应接在 A 的中间点
    → 被「已闭合不焊」规则拒绝 → B 单独闭合、A 少走一段
    → A 用一条长直线抄近路补回起点

实测 super/exp/2_3 的 cid 69：环 0 里出现 `L(308.1,510.1)->(639.0,287.2)`
长 399px 的直线，顶部一整块 ≈ 50k px² 没有填充，IoU 只有 0.816。

## 正确语义

SWF 的填充不是「环」，而是「边的左右两侧各有一个填充样式」。把边集合当作
**有向平面图**，沿「填充始终在行进方向同侧」的规则走边界：到达终点后，从入射
方向的反向出发，按顺时针找第一条未使用的出边继续。T 型接头自然分叉，共享边
被两侧各走一次，外环与洞方向相反。

## 图必须封闭

面遍历的前提是每个顶点入度 == 出度。**两侧同填充的内部边只被收集一次**，
会让出度/入度失衡、遍历走进死胡同。调用方必须在传参前剔除内部边
（swfshape 的 `interior` 集合）。返回值的 `used < total` 就是「图不封闭」的
信号，此时结果不可信，应回退串链。

## 接口

    boundaries(parts, tol=0.02, min_area_px=0.3) -> dict | None
        d / used / total / area_px
    boundaries_d(parts, ...) -> str | None    兼容旧接口
"""
import math

import os

TAU = 2.0 * math.pi
_DIR = os.getenv('SWF_FACE_DIR', 'cw')


def _key(p, q):
    return (int(round(p[0] * q)), int(round(p[1] * q)))


def _edges_of(segs):
    """一条子路径 → 有向边列表 [(start, end, cmd, cx, cy)]，坐标 twips。

    segs[0] 是 M（起点），其后每段的终点即该段终点，起点由上一段给出。
    零长度段直接丢弃（SWF 里偶有重复点，会让出边角度排序出错）。
    """
    out = []
    for i in range(len(segs) - 1):
        cmd, x, y, cx, cy = segs[i + 1]
        s = (segs[i][1], segs[i][2])
        t = (x, y)
        if s == t:
            continue
        out.append((s, t, cmd, cx, cy))
    return out


def boundaries(parts, tol=0.02, min_area_px=0.3):
    """同一填充的所有子路径 → 面遍历结果（dict）或 None。

    used < total 表示有边走不到（图不封闭），调用方应据此回退。
    """
    q = 1.0 / tol
    edges = []
    for segs in parts:
        edges.extend(_edges_of(segs))
    if len(edges) < 3:
        return None

    # 出边表：按**起点**索引（按终点索引会让遍历在第一步就找不到出边）
    out = {}
    for i, (s, _t, _c, _cx, _cy) in enumerate(edges):
        out.setdefault(_key(s, q), []).append(i)

    def ang(i):
        s, t = edges[i][0], edges[i][1]
        return math.atan2(t[1] - s[1], t[0] - s[0])

    for v in out:
        out[v].sort(key=ang)

    used = [False] * len(edges)
    rings = []
    for i0 in range(len(edges)):
        if used[i0]:
            continue
        ring = []
        e = i0
        start_v = _key(edges[i0][0], q)
        for _guard in range(len(edges) + 4):
            if used[e]:
                break
            used[e] = True
            ring.append(e)
            v = _key(edges[e][1], q)
            if v == start_v:
                break
            cand = out.get(v)
            if not cand:
                break
            # 屏幕坐标 y 向下，atan2 增大即顺时针；取顺时针方向第一条未用出边，
            # 等价「填充始终在行进方向右侧」，外环与洞自然反号。
            a_ref = ang(e) + math.pi
            best = None
            bestd = None
            for j in cand:
                if used[j]:
                    continue
                # 方向：'cw' 取顺时针第一条，'ccw' 取逆时针第一条。
                # 选错会在 T 型接头把不该连的边界焊在一起（cid 5 的 55 个环
                # 被合并成 20 个），实测由 SWF_FACE_DIR 切换比对确定。
                if _DIR == 'ccw':
                    d = (a_ref - ang(j)) % TAU
                else:
                    d = (ang(j) - a_ref) % TAU
                if d < 1e-9:
                    d = TAU
                if bestd is None or d < bestd:
                    bestd = d
                    best = j
            if best is None:
                break
            e = best
        if len(ring) >= 2:
            rings.append(ring)

    if not rings:
        return None

    out_parts = []
    total_area = 0.0
    for ring in rings:
        area = 0.0
        for i in ring:
            s, t = edges[i][0], edges[i][1]
            area += s[0] * t[1] - t[0] * s[1]
        area_px = abs(area) / 2.0 / 400.0      # twips² → px²
        if area_px < min_area_px:
            continue
        total_area += area_px
        segs = []
        first = edges[ring[0]][0]
        segs.append('M%s %s' % (_f(first[0]), _f(first[1])))
        for i in ring:
            _s, t, cmd, cx, cy = edges[i]
            if cmd == 'Q':
                segs.append('Q%s %s %s %s' % (_f(cx), _f(cy), _f(t[0]), _f(t[1])))
            else:
                segs.append('L%s %s' % (_f(t[0]), _f(t[1])))
        out_parts.append(' '.join(segs))
    if not out_parts:
        return None
    return {'d': ' '.join(out_parts), 'used': sum(1 for u in used if u),
            'total': len(edges), 'area_px': total_area}


def boundaries_d(parts, tol=0.02, min_area_px=0.3):
    """兼容旧接口：只返回 d。"""
    r = boundaries(parts, tol, min_area_px)
    return r['d'] if r else None


def _f(v):
    s = '%.2f' % (v / 20.0)      # twips → px
    if s.endswith('.00'):
        s = s[:-3]
    return s
