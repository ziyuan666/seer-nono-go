# -*- coding: utf-8 -*-
"""01（可读 SVG path JSON）→ 02（量化数组 JSON）。

为什么要两套：
- 01 留在源码库里做 diff / 排查，人和工具都看得懂；
- 02 是构建产物，给运行时直接吃，体积小、解析快。

量化口径（全部无损，不是近似）：
- 坐标：01 的 d 由 `%.2f % (twip/20)` 生成，twip 是整数，
  除以 20 后小数位最多两位（0.05 的倍数），乘回 20 必为整数。
- 矩阵：SVG 的 a,b,c,d,e,f 原值是 16.16 定点，乘 65536 取整无损。
- 渐变 stop 偏移与不透明度：0..1 浮点，乘 65535 取整。
- 颜色：解析成 0xRRGGBB 整数。

顶点指令编码（与运行时约定一致）：
  0 x y           moveTo
  1 x y           lineTo
  2 cx cy x y     quadTo
  3               closePath
"""
import json
import os
import re
import sys

CMD_RE = re.compile(r'([MLQZ])([^MLQZ]*)')
NUM_RE = re.compile(r'-?\d+(?:\.\d+)?')


def parse_d(d):
    """SVG d → 顶点整数数组（twip）。"""
    out = []
    for cmd, body in CMD_RE.findall(d):
        nums = [float(v) for v in NUM_RE.findall(body)]
        if cmd == 'M':
            out.extend([0, int(round(nums[0] * 20)), int(round(nums[1] * 20))])
        elif cmd == 'L':
            out.extend([1, int(round(nums[0] * 20)), int(round(nums[1] * 20))])
        elif cmd == 'Q':
            out.extend([2,
                        int(round(nums[0] * 20)), int(round(nums[1] * 20)),
                        int(round(nums[2] * 20)), int(round(nums[3] * 20))])
        elif cmd == 'Z':
            out.append(3)
    return out


def qmat(m):
    return [int(round(v * 65536)) for v in m]


def qcolor(c):
    """'#rrggbb' / '#rrggbbaa' → 0xRRGGBB（alpha 单独走 opacity，这里丢弃 AA 后缀）。"""
    if not isinstance(c, str) or not c.startswith('#'):
        return 0
    h = c[1:]
    if len(h) == 8:
        h = h[:6]
    if len(h) != 6:
        return 0
    return int(h, 16)


def quantize_shape(obj):
    paths = []
    for it in obj.get('paths', []):
        p = {'v': parse_d(it['d']) if it.get('d') else []}
        p['r'] = 1 if it.get('fillRule') == 'evenodd' else 0
        if 'matrix' in it:
            p['m'] = qmat(it['matrix'])
        if 'gradient' in it:
            g = it['gradient']
            qg = {'t': g['type'], 'm': qmat(g['matrix'])}
            if g.get('spread') and g['spread'] != 'pad':
                qg['s'] = g['spread']
            qg['st'] = [[int(round(s['offset'] * 65535)), qcolor(s['color']),
                         int(round(s.get('opacity', 1) * 65535))] for s in g['stops']]
            # 渐变几何：线性用 x1/y1/x2/y2，径向用 cx/cy/r/fx/fy（twip）
            if g['type'] == 'linear':
                qg['g'] = [int(round(g['x1'] * 20)), int(round(g['y1'] * 20)),
                           int(round(g['x2'] * 20)), int(round(g['y2'] * 20))]
            else:
                qg['g'] = [int(round(g['cx'] * 20)), int(round(g['cy'] * 20)),
                           int(round(g['r'] * 20)),
                           int(round(g.get('fx', 0) * 20)), int(round(g.get('fy', 0) * 20))]
            p['g'] = qg
        elif it.get('fill'):
            p['f'] = qcolor(it['fill'])
        if it.get('stroke'):
            p['k'] = qcolor(it['stroke'])
            if it.get('strokeWidth') is not None:
                p['w'] = int(round(it['strokeWidth'] * 20))
        # fillAlpha / strokeAlpha 必须带过去：SVG 用它表达半透明填充，
        # 丢了下场就是「0.2 的灰弧被画成纯黑」（action/1、action/2 都有）。
        if it.get('fillAlpha') is not None:
            p['fa'] = int(round(it['fillAlpha'] * 65535))
        if it.get('strokeAlpha') is not None:
            p['sa'] = int(round(it['strokeAlpha'] * 65535))
        paths.append(p)
    return {'id': obj.get('id', ''), 'paths': paths}


def walk(base_in, base_out):
    total_in = total_out = 0
    n = 0
    for root, _dirs, files in os.walk(base_in):
        for fn in files:
            if not fn.endswith('.json'):
                continue
            src = os.path.join(root, fn)
            rel = os.path.relpath(src, base_in)
            obj = json.load(open(src, encoding='utf-8'))
            if 'paths' not in obj or 'id' not in obj:
                continue
            q = quantize_shape(obj)
            dst = os.path.join(base_out, rel)
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            txt = json.dumps(q, ensure_ascii=False, separators=(',', ':'))
            with open(dst, 'w', encoding='utf-8') as f:
                f.write(txt)
            total_in += os.path.getsize(src)
            total_out += len(txt.encode('utf-8'))
            n += 1
    return n, total_in, total_out


if __name__ == '__main__':
    src = sys.argv[1]
    dst = sys.argv[2]
    n, a, b = walk(src, dst)
    print('量化 %d 个文件：%.2fMB → %.2fMB（%.1f%%）' % (n, a / 1e6, b / 1e6, b * 100.0 / a if a else 0))
