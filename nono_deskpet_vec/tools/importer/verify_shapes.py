#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""verify_shapes.py —— 自研 swfshape 解析结果 vs FFDec 导出 SVG 的交叉验证。

比对项：
    width/height      shapeBounds 宽高（容差 0.05px）
    base              FFDec 的 <g transform> 平移（容差 0.05px）
    path 数量
    每条 path 的点序列（M/L 的终点、Q 的控制点+终点），逐点最大距离
    fill 颜色 / fill-opacity

用法：
    python3 verify_shapes.py --swf ../../source_swf/资源/nono/super/action/4_4.swf \
                             --svg-dir /tmp/ffanim2/super4_action_4
    python3 verify_shapes.py --swf ... --svg-dir ... --verbose
"""
import argparse
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from swfread import SWF          # noqa: E402
import swfshape                  # noqa: E402

NUM = r'[-+]?(?:\d+\.?\d*|\.\d+)(?:[eE][-+]?\d+)?'
TOK = re.compile(r'([MLQCZmlqcz])|(%s)' % NUM)


def parse_d(d):
    """SVG path d → [(cmd, [(x,y), ...])]。只支持 M/L/Q/Z（FFDec 只输出这几种）。"""
    toks = []
    for m in TOK.finditer(d):
        toks.append(m.group(1) if m.group(1) else float(m.group(2)))
    out = []
    i = 0
    cur = None
    cmd = None
    while i < len(toks):
        t = toks[i]
        if isinstance(t, str):
            cmd = t.upper()
            i += 1
            if cmd == 'Z':
                out.append(('Z', []))
                continue
            if cmd == 'M':
                # 后续隐式 L
                cmd = 'M'
        if cmd == 'M':
            if i + 1 >= len(toks) or isinstance(toks[i], str):
                break
            out.append(('M', [(toks[i], toks[i + 1])]))
            i += 2
            cmd = 'L'
        elif cmd == 'L':
            out.append(('L', [(toks[i], toks[i + 1])]))
            i += 2
        elif cmd == 'Q':
            out.append(('Q', [(toks[i], toks[i + 1]), (toks[i + 2], toks[i + 3])]))
            i += 4
        else:
            break
    return out


def flat_points(segs):
    pts = []
    for cmd, ps in segs:
        pts.extend(ps)
    return pts


def svg_paths(svg_file):
    """返回 [(d, fill, fill_opacity, transform_tx, transform_ty, w, h)]"""
    s = open(svg_file, encoding='utf-8').read()
    w = float(re.search(r'width="([\d.]+)px"', s).group(1))
    h = float(re.search(r'height="([\d.]+)px"', s).group(1))
    g = re.search(r'<g transform="matrix\(([^)]+)\)"', s)
    tx = ty = 0.0
    if g:
        parts = [float(v) for v in g.group(1).split(',')]
        tx, ty = parts[4], parts[5]
    out = []
    for m in re.finditer(r'<path([^>]*)/>', s):
        attrs = m.group(1)
        d = re.search(r'\bd="([^"]+)"', attrs)
        if not d:
            continue
        fill = re.search(r'fill="([^"]+)"', attrs)
        fo = re.search(r'fill-opacity="([^"]+)"', attrs)
        out.append({
            'd': d.group(1),
            'fill': fill.group(1) if fill else None,
            'opacity': float(fo.group(1)) if fo else 1.0,
        })
    return out, (w, h, tx, ty)


def _dist(a, b):
    return ((a[0] - b[0]) ** 2 + (a[1] - b[1]) ** 2) ** 0.5


def sample_points(segs, step=0.8):
    """把路径采样成点集：直线按 step 插值，二次曲线固定 10 段。
    用点集而不是点序列，是为了容忍「方向相反 / 起点不同」——同一个闭合环
    从不同起点、不同绕行方向写出，点序列完全不同但形状一致。"""
    pts = []
    cur = None
    for cmd, ps in segs:
        if cmd == 'M':
            cur = ps[0]
            pts.append(cur)
        elif cmd == 'L':
            p = ps[0]
            if cur is None:
                cur = p
                pts.append(p)
                continue
            n = max(1, int(_dist(cur, p) / step))
            for i in range(1, n + 1):
                t = i / n
                pts.append((cur[0] + (p[0] - cur[0]) * t, cur[1] + (p[1] - cur[1]) * t))
            cur = p
        elif cmd == 'Q':
            c, a = ps[0], ps[1]
            if cur is None:
                cur = a
                pts.append(a)
                continue
            n = 10
            for i in range(1, n + 1):
                t = i / n
                mt = 1 - t
                pts.append((mt * mt * cur[0] + 2 * mt * t * c[0] + t * t * a[0],
                            mt * mt * cur[1] + 2 * mt * t * c[1] + t * t * a[1]))
            cur = a
    return pts


def hausdorff(a, b):
    """双向最大最近邻距离（近似 Hausdorff）。"""
    if not a or not b:
        return float('inf')

    def one_way(p, q):
        worst = 0.0
        for x in p:
            d = min(_dist(x, y) for y in q)
            if d > worst:
                worst = d
        return worst

    # 点集较大时抽样，避免 O(n²) 拖死
    sa = a if len(a) <= 400 else a[::max(1, len(a) // 400)]
    sb = b if len(b) <= 400 else b[::max(1, len(b) // 400)]
    return max(one_way(sa, sb), one_way(sb, sa))


def group_by_fill(paths):
    """按 fill+opacity 归并点集：同一填充的多个子路径合成一组。"""
    groups = {}
    for p in paths:
        key = (p.get('fill'), round(p.get('opacity', 1.0), 2))
        groups.setdefault(key, []).extend(sample_points(parse_d(p['d'])))
    return groups


def compare(shape, svg_paths_list, svg_meta, tol=0.6):
    errs = []
    w, h, tx, ty = svg_meta
    if abs(shape['width'] - w) > 0.05 or abs(shape['height'] - h) > 0.05:
        errs.append('尺寸 %sx%s vs svg %sx%s' % (shape['width'], shape['height'], w, h))
    if abs(shape['base'][4] - tx) > 0.05 or abs(shape['base'][5] - ty) > 0.05:
        errs.append('base %s vs svg (%s,%s)' % (shape['base'][4:6], tx, ty))

    mine = group_by_fill([{'d': p['d'], 'fill': p.get('fill'), 'opacity': p.get('fillAlpha', 1.0)}
                          for p in shape['paths']])
    theirs = group_by_fill(svg_paths_list)
    if set(mine) != set(theirs):
        errs.append('填充组不一致 我=%s 他=%s' % (sorted(map(str, mine)), sorted(map(str, theirs))))
    worst = 0.0
    for key in set(mine) & set(theirs):
        d = hausdorff(mine[key], theirs[key])
        worst = max(worst, d)
        if d > tol:
            errs.append('fill %s 形状偏差 %.2fpx' % (key[0], d))
    return errs, worst


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--swf', required=True)
    ap.add_argument('--svg-dir', required=True)
    ap.add_argument('--ids', default='')
    ap.add_argument('--verbose', action='store_true')
    args = ap.parse_args()

    swf = SWF(args.swf)
    ids = [int(x) for x in args.ids.split(',')] if args.ids else sorted(swf.shapes)
    ok = bad = nosvg = 0
    fails = []
    for cid in ids:
        svg = os.path.join(args.svg_dir, '%d.svg' % cid)
        if not os.path.exists(svg):
            nosvg += 1
            continue
        try:
            shape = swfshape.parse_from_swf(swf, cid)
        except Exception as e:
            bad += 1
            fails.append((cid, ['解析异常: %s' % e], 0))
            continue
        if shape is None:
            nosvg += 1
            continue
        pl, meta = svg_paths(svg)
        errs, worst = compare(shape, pl, meta)
        if errs:
            bad += 1
            fails.append((cid, errs, worst))
        else:
            ok += 1
            if args.verbose:
                print('  OK  cid %-4d paths=%d 最大偏差 %.3f' % (cid, len(shape['paths']), worst))
    print(f'\n{os.path.basename(args.swf)}: 通过 {ok} / 失败 {bad} / 无SVG {nosvg}')
    for cid, errs, worst in fails[:20]:
        print(f'  cid {cid} (偏差 {worst:.2f}): ' + '; '.join(errs))
    return 0 if bad == 0 else 1


if __name__ == '__main__':
    sys.exit(main())
