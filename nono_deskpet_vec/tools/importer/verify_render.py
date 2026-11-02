#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""verify_render.py —— 用渲染结果比对自研解析 vs FFDec SVG（比点集比较更接近真实用途）。

为什么不用点集比：FFDec 会把同一填充的多个子路径连成一个大环，SWF 里可能是
多个独立小环。两种写法覆盖的填充区域相同，但点序列完全不同。渲染成像素后
用 IoU 比，才反映「画出来是不是同一个东西」。

用法：
    python3 verify_render.py --swf ../../source_swf/资源/nono/super/action/7_5.swf \
                             --svg-dir /tmp/ffanim2/super5_action_7 --scale 1
"""
import argparse
import io
import os
import sys

import cairosvg
import numpy as np
from PIL import Image

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from swfread import SWF       # noqa: E402
import swfshape               # noqa: E402


def shape_to_svg(shape):
    w, h = shape['width'], shape['height']
    tx, ty = shape['base'][4], shape['base'][5]
    pad = 2.0
    w2, h2 = w + pad * 2, h + pad * 2
    defs = ''
    body = []
    gid = 0
    for p in shape['paths']:
        if p.get('gradient'):
            g = p['gradient']
            stops = ''.join(
                '<stop offset="%s" stop-color="%s" stop-opacity="%s"/>' % (
                    s['offset'], s['color'], s.get('opacity', 1))
                for s in g['stops'])
            gt = g.get('matrix') or [1, 0, 0, 1, 0, 0]
            # 线性/径向必须分开：FFDec 线性用 x1=-819.2 x2=819.2，
            # 径向用 cx=0 cy=0 r=1638.4。用错了填充区域和颜色都会变
            # （cid 27 用 radial 渲染时 IoU 只有 0.58）。
            fill = 'url(#g)'
            if g.get('type') == 'linear':
                defs = ('<linearGradient id="g" gradientUnits="userSpaceOnUse" '
                        'spreadMethod="%s" x1="-819.2" x2="819.2" '
                        'gradientTransform="matrix(%s,%s,%s,%s,%s,%s)">%s</linearGradient>' % (
                            g.get('spread', 'pad'), gt[0], gt[1], gt[2], gt[3], gt[4], gt[5], stops))
            else:
                defs = ('<radialGradient id="g" gradientUnits="userSpaceOnUse" '
                        'spreadMethod="%s" cx="0" cy="0" r="819.2" '
                        'gradientTransform="matrix(%s,%s,%s,%s,%s,%s)">%s</radialGradient>' % (
                            g.get('spread', 'pad'), gt[0], gt[1], gt[2], gt[3], gt[4], gt[5], stops))
        else:
            fill = p.get('fill', 'none')
        if p.get('stroke'):
            body.append('<path d="%s" fill="none" stroke="%s" stroke-width="%s" '
                        'stroke-opacity="%s" stroke-linecap="round"/>' % (
                            p['d'], p['stroke'], p.get('strokeWidth', 1.0),
                            p.get('strokeAlpha', 1)))
        else:
            body.append('<path d="%s" fill="%s" fill-opacity="%s" fill-rule="%s" stroke="none"/>' % (
                p['d'], fill, p.get('fillAlpha', 1), p.get('fillRule', 'evenodd')))
    if defs:
        defs = '<defs>' + defs + '</defs>'
    return ('<svg xmlns="http://www.w3.org/2000/svg" width="%fpx" height="%fpx" '
            'viewBox="0 0 %f %f">%s<g transform="translate(%f,%f)">%s</g></svg>' % (
                w2, h2, w2, h2, defs, pad + tx, pad + ty, ''.join(body)))


def render(svg_text, scale):
    png = cairosvg.svg2png(bytestring=svg_text.encode('utf-8'), scale=scale,
                           background_color=None)
    return np.array(Image.open(io.BytesIO(png)).convert('RGBA'))


def _crop(a):
    ys, xs = np.nonzero(a[:, :, 3] > 40)
    if len(xs) == 0:
        return a[:1, :1]
    return a[ys.min():ys.max() + 1, xs.min():xs.max() + 1]


def iou(a, b):
    """裁到内容 bbox 再比：我的 SVG 加了 2px 边距防边缘裁切，FFDec 的没有，
    直接按画布比会因尺寸不同全判 0。"""
    a, b = _crop(a), _crop(b)
    ma = a[:, :, 3] > 40
    mb = b[:, :, 3] > 40
    if ma.sum() == 0 and mb.sum() == 0:
        return 1.0, 0, 0
    H = max(ma.shape[0], mb.shape[0])
    W = max(ma.shape[1], mb.shape[1])
    pa = np.zeros((H, W), bool)
    pb = np.zeros((H, W), bool)
    pa[:ma.shape[0], :ma.shape[1]] = ma
    pb[:mb.shape[0], :mb.shape[1]] = mb
    return (pa & pb).sum() / max(1, (pa | pb).sum()), int(ma.sum()), int(mb.sum())


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--swf', required=True)
    ap.add_argument('--svg-dir', required=True)
    ap.add_argument('--scale', type=int, default=1)
    ap.add_argument('--min-iou', type=float, default=0.95)
    ap.add_argument('--verbose', action='store_true')
    ap.add_argument('--json-out', default=None,
                    help='把完整结果（含全部失败 cid）写到该文件')
    args = ap.parse_args()

    swf = SWF(args.swf)
    ok = bad = nosvg = 0
    fails = []
    # 只验 shape：sprite 的 SVG 是整棵显示列表的合成结果，
    # 拿单个 shape 的解析去比必然偏低（super/exp/2_3 的 cid 69 就是 sprite）。
    sprite_ids = set(getattr(swf, 'sprites', {}))
    for cid in sorted(swf.shapes):
        if cid in sprite_ids:
            continue
        svg_file = os.path.join(args.svg_dir, '%d.svg' % cid)
        if not os.path.exists(svg_file):
            nosvg += 1
            continue
        try:
            shape = swfshape.parse_from_swf(swf, cid)
            if shape is None or not shape['paths']:
                nosvg += 1
                continue
            mine = render(shape_to_svg(shape), args.scale)
            theirs = render(open(svg_file, encoding='utf-8').read(), args.scale)
            v, na, nb = iou(mine, theirs)
        except Exception as e:
            bad += 1
            fails.append((cid, 0.0, '异常 %s' % str(e)[:60]))
            continue
        if v >= args.min_iou:
            ok += 1
            if args.verbose:
                print('  OK cid %-4d IoU %.4f  px %d/%d' % (cid, v, na, nb))
        else:
            bad += 1
            fails.append((cid, v, 'px %d/%d' % (na, nb)))
    print('%s: IoU>=%.2f 通过 %d / 失败 %d / 无SVG %d'
          % (os.path.basename(args.swf), args.min_iou, ok, bad, nosvg))
    for cid, v, extra in fails[:15]:
        print('  cid %-4d IoU %.3f  %s' % (cid, v, extra))
    if args.json_out:
        import json as _json
        _json.dump({'ok': ok, 'bad': bad, 'nosvg': nosvg,
                    'fails': {str(c): v for c, v, _e in fails}},
                   open(args.json_out, 'w'))
    return 0 if bad == 0 else 1


if __name__ == '__main__':
    sys.exit(main())
