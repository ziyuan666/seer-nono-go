# -*- coding: utf-8 -*-
"""把 01 可读格式转成 02 量化格式（与 pkg/spec 的 02 字段严格对齐）。

02 schema（由 pkg/spec/spec.go 的 Normalize 折叠回 01）：

sprite.json:
    {"format":2, "name":..., "canvas":{...}, "colors":{...},
     "pt":[part 名表], "ly":[{"n":名字,"z":层序,"t":0/1 可染色}],
     "d":{"default":[[[partIdx, layerIdx, ma..mf, alpha255], ...], ...]}}

parts/shape_X.json:
    {"id":..., "paths":[{"v":[顶点],"m":[矩阵16.16],"r":1,"f":0xRRGGBB,
                        "g":{"t":"linear|radial","m":[...],"s":"pad",
                             "st":[[off1e4,color,op1e4],...],
                             "g":[几何 twip]},"fa":0..255,"k":0xRRGGBB,"w":twip}]}

顶点：0=M(x,y) 1=L(x,y) 2=Q(cx,cy,x,y) 3=Z，坐标 twip 整数（÷20 = px）。
已实测 px→twip 往返误差 0。
"""
import json, os, re, sys

NUM = re.compile(r'[-+]?\d*\.?\d+(?:[eE][-+]?\d+)?')
CMDMAP = {'M': 0, 'L': 1, 'Q': 2, 'Z': 3}


def tw(v):
    return int(round(float(v) * 20.0))


def fx16(v):
    return int(round(float(v) * 65536.0))


def fx6(v):
    """帧矩阵用 1e-6 微像素定点。

    01 的帧矩阵是 round(v, 6) 存的，16.16（1.5e-5 精度）比它粗 15 倍，
    量化后细长图形（星芒）的两条边重叠判定会翻转，evenodd alpha 折算
    从 0.72 跳到 0.40，肉眼可见 82 灰阶差。×1e6 与 01 的存储精度完全对齐。
    """
    return int(round(float(v) * 1000000.0))


def hex2int(c):
    """'#rrggbb' / '#rrggbbaa' → 0xRRGGBB。非 # 颜色（槽位名）返回 None。"""
    if not isinstance(c, str) or not c.startswith('#'):
        return None
    h = c[1:]
    if len(h) >= 6:
        return int(h[:6], 16)
    return None


def parse_d(d):
    """M/L/Q/Z → 顶点数组。全库实测只有这四种命令。"""
    out = []
    i, n = 0, len(d)
    while i < n:
        ch = d[i]
        if ch in ' ,\t\n\r':
            i += 1
            continue
        if ch.isalpha():
            cmd = ch.upper()
            if cmd not in CMDMAP:
                raise ValueError('不支持的命令 %r' % ch)
            out.append(CMDMAP[cmd])
            i += 1
            continue
        m = NUM.match(d, i)
        if not m:
            raise ValueError('无法解析 %r @%d' % (d[i:i+20], i))
        out.append(tw(float(m.group())))
        i = m.end()
    return out


def conv_gradient(g):
    out = {
        't': 'linear' if g['type'] == 'linear' else 'radial',
        # 渐变矩阵是 SWF 的 16.16 定点（2020/65536），x65536 才精确。
        'm': [fx16(x) for x in g['matrix']],
    }
    if g.get('spread'):
        out['s'] = g['spread']
    st = []
    for s in g['stops']:
        off = int(round(float(s['offset']) * 10000))
        ci = hex2int(s['color'])
        if ci is None:
            raise ValueError('渐变 stops 出现非 hex 颜色 %r' % s['color'])
        op = int(round(float(s['opacity']) * 10000))
        st.append([off, ci, op])
    out['st'] = st
    if g['type'] == 'linear':
        out['g'] = [tw(g['x1']), tw(g['y1']), tw(g['x2']), tw(g['y2'])]
    else:
        out['g'] = [tw(g['cx']), tw(g['cy']), tw(g['r']), tw(g['fx']), tw(g['fy'])]
    return out


def conv_path(p):
    out = {'v': parse_d(p['d'])}
    if p.get('matrix') and any(p['matrix']):
        out['m'] = [fx6(x) for x in p['matrix']]
    if p.get('fillRule') == 'evenodd':
        out['r'] = 1
    if p.get('gradient'):
        out['g'] = conv_gradient(p['gradient'])
    elif p.get('fill') and p['fill'] != 'none':
        ci = hex2int(p['fill'])
        if ci is not None:
            out['f'] = ci
        else:
            raise ValueError('填充色不是 #hex，02 无法表达：%r' % p['fill'])
    # fill 为 "none" 或空：02 里不写 f 即等价于不填充（与 01 的 r.color("none") 失败路径一致）
    # 01 里 fillAlpha / strokeAlpha 都是 3 位小数，用 ×1000 精确往返；
    # 用 65535 会留 6.5e-6 残差，严格比对过不去。
    if p.get('fillAlpha') is not None:
        out['fa'] = int(round(float(p['fillAlpha']) * 1000))
    if p.get('stroke'):
        s = p['stroke']
        ci = hex2int(s['color'])
        if ci is not None:
            out['k'] = ci
            out['w'] = tw(s.get('width', 1.0))
            # sa 必须写：alpha=0 表示「这条描边不画」，漏了会按不透明画成黑线。
            if p.get('strokeAlpha') is not None:
                out['sa'] = int(round(float(p['strokeAlpha']) * 1000))
    return out


def convert(anim_dir, out_dir):
    sj = json.load(open(os.path.join(anim_dir, 'sprite.json')))
    if sj.get('format') == 2:
        return None

    layers = sj.get('layers', [])
    layer_idx = {l['name']: i for i, l in enumerate(layers)}

    # parts：文件名即 part 名，保持一 shape 一文件（LoadLibrary 按文件加载）
    parts_dir = os.path.join(anim_dir, 'parts')
    names = []
    paths_by_name = {}
    if os.path.isdir(parts_dir):
        for fn in sorted(os.listdir(parts_dir)):
            if not fn.endswith('.json'):
                continue
            name = fn[:-5]
            pf = json.load(open(os.path.join(parts_dir, fn)))
            names.append(name)
            paths_by_name[name] = [conv_path(p) for p in pf.get('paths', [])]
    idx_of = {n: i for i, n in enumerate(names)}

    # 帧
    dout = {}
    for dname, d in sj.get('directions', {}).items():
        frames = []
        for fr in d.get('frames', []):
            row = []
            for p in fr.get('parts', []):
                pi = idx_of.get(p['part'], -1)
                if pi < 0:
                    continue
                li = layer_idx.get(p.get('layer', ''), -1)
                # alpha 走 1/256 定点：01 的 alpha 来自 SWF cxform mul（8.8 定点，
                # 256 = 1.0，见 swfread.read_cxform），用 255 归一会系统性偏大 0.4%，
                # 多层叠加后肉眼可见（星芒 7 层叠出 82 灰阶差）。
                # alpha 用 x10000 定点：01 里是 round(v,4)，而 v 可能是
                # 多级 place 的 cxform 相乘结果（0.734375 x 0.0703125 = 0.051635），
                # 用 x256 只剩 0.0508，80 层叠加后差出 255 灰阶。
                item = [pi, li] + [fx6(x) for x in p['matrix']] \
                       + [int(round(float(p.get('alpha', 1.0)) * 10000))]
                # cxform 的 RGB 部分。绝大多数 place 是恒等，所以按长度区分：
                #   9 个 = 无颜色变换；12 个 = 仅乘项；15 个 = 乘项 + 加项。
                # 乘项 x1e6、加项 x1e4，与 01 的 round(v,6)/round(v,4) 对齐。
                cm = p.get('cmul')
                ca = p.get('cadd')
                need_mul = bool(cm) and any(abs(v - 1.0) > 1e-9 for v in cm)
                need_add = bool(ca) and any(abs(v) > 1e-9 for v in ca)
                if need_mul or need_add:
                    mv = cm if need_mul else [1.0, 1.0, 1.0]
                    item += [int(round(v * 1000000)) for v in mv]
                if need_add:
                    item += [int(round(v * 10000)) for v in ca]
                row.append(item)
            frames.append(row)
        dout[dname] = frames

    v2 = {
        'f': 2,
        'n': sj.get('name', ''),
        'c': sj.get('canvas', {}),
        'col': sj.get('colors', {}),
        'pt': names,
        'ly': [{'n': l['name'], 'z': l.get('z', 0), 't': 1 if l.get('tintable') else 0}
               for l in layers],
        'd': dout,
    }

    os.makedirs(out_dir, exist_ok=True)
    os.makedirs(os.path.join(out_dir, 'parts'), exist_ok=True)

    old = os.path.getsize(os.path.join(anim_dir, 'sprite.json'))
    for fn in (os.listdir(parts_dir) if os.path.isdir(parts_dir) else []):
        old += os.path.getsize(os.path.join(parts_dir, fn))

    raw = json.dumps(v2, ensure_ascii=False, separators=(',', ':'))
    open(os.path.join(out_dir, 'sprite.json'), 'w', encoding='utf-8').write(raw)
    new = len(raw.encode('utf-8'))
    for name in names:
        pf = {'id': name, 'paths': paths_by_name[name]}
        r2 = json.dumps(pf, ensure_ascii=False, separators=(',', ':'))
        open(os.path.join(out_dir, 'parts', name + '.json'), 'w', encoding='utf-8').write(r2)
        new += len(r2.encode('utf-8'))

    return old, new


def main():
    src_dir = sys.argv[1]      # 01 动画目录（含 sprite.json + parts/）
    out_dir = sys.argv[2]      # 02 输出目录
    r = convert(src_dir, out_dir)
    if r is None:
        print('已是 02 格式，跳过')
        return
    old, new = r
    print('%s: 01=%.1fKB → 02=%.1fKB (%.0f%%)' % (
        os.path.basename(src_dir.rstrip('/')), old / 1024.0, new / 1024.0,
        new * 100.0 / max(old, 1)))


if __name__ == '__main__':
    main()
