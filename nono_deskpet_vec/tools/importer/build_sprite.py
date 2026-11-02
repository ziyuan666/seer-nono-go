#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""build_sprite.py —— 把 SWF 的嵌套显示列表展平成「层→方向→帧→部件」JSON。

用法：
    python3 build_sprite.py --swf nono.swf --root 127 --name plain \
        --svg-dir /tmp/ffvec --out ../../assets/sprites --anim-frames 17

产出：
    <out>/parts/shape_<cid>.json     每个 shape 的几何（path + fill + gradient + stroke）
    <out>/<name>/sprite.json         形态骨架（canvas / colors / layers / directions）

核心规则：
1. 方向段由「带 label 的 63 帧子 sprite」定义（down=0, leftdown=7, ...），
   外层一律冻结在该方向的段首帧，不让它跨段；
2. 帧数 == --anim-frames 的 sprite 视为动画层，按输出帧号推进；
3. 其余嵌套 sprite 冻结在第 0 帧（它们只是容器/挂点）；
4. 层名取「根 sprite 第一层 place 的实例名」（plain 的 color_2/body/color_1），
   一个层内可以有多个 shape，按展开顺序叠。
"""
import argparse
import json
import os
import re
import sys
import xml.etree.ElementTree as ET

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from swfread import SWF, mul_matrix, swf_matrix_to_svg  # noqa: E402
import swfshape  # noqa: E402

DIRS = ['down', 'leftdown', 'left', 'leftup', 'up', 'rightup', 'right', 'rightdown']
SVG_NS = '{http://www.w3.org/2000/svg}'
IDENT = [1.0, 0.0, 0.0, 1.0, 0.0, 0.0]

# 子时间轴（AnimGroup）每帧毫秒，main() 里按 SWF 头 frameRate 覆写。
frame_ms = 40
# 允许捕获子时间轴的层名（fireMc 的云团由官方 PNG 帧接管，不能重复成组）。
group_layers = ('dirMc',)
# 捕获时是否下钻到最内层（--anim-group-deep）。
group_deep = False


def has_multi_child(swf, cid):
    """该 sprite 的直接子角色里是否还有多帧 MC。

    用来决定「当前层能不能收成组」：如果它内部还有多帧子层（annual 的
    106 → 103/105），把它收成组会把内层按 21 帧展平，等于换个人冻结。
    返回 True 时跳过捕获、继续展开，让内层自己成组。
    """
    sp = swf.sprites.get(cid)
    if sp is None:
        return False
    seen = set()
    for fr in sp.frames:
        for p in fr:
            if not isinstance(p, dict):
                continue
            k = p.get('cid')
            if not k or k in seen:
                continue
            seen.add(k)
            ks = swf.sprites.get(k)
            if ks is not None and ks.nframes > 1:
                return True
    return False


def read_frame_rate(path):
    """读 SWF 头里的 frameRate（UI16，8.8 定点，低字节在前）。

    头 8 字节固定：签名 3 + 版本 1 + 文件长度 4；随后是变长 RECT
    （nbits 占 5 bit，再 4×nbits bit）。CWS/ZWS 的正文是 zlib 压缩的，
    必须先解压再解析（直接读偏移会得到垃圾帧率，例如 148fps）。
    读失败返回 0，调用方回退 40ms。
    """
    try:
        import zlib
        with open(path, 'rb') as f:
            head = f.read(4096)
        if len(head) < 12 or head[:3] not in (b'FWS', b'CWS', b'ZWS'):
            return 0.0
        body = head[8:]
        if head[:3] != b'FWS':
            body = zlib.decompressobj().decompress(body)
        i = 0
        nbits = body[i] >> 3
        i += (5 + 4 * nbits + 7) // 8
        raw = body[i] | (body[i + 1] << 8)
        fps = raw / 256.0
        return fps if 1.0 < fps < 121.0 else 0.0
    except Exception:
        return 0.0


# ---------- SVG ----------

def parse_transform(s):
    """解析 transform 属性，返回 2x3 矩阵 [a,b,c,d,e,f]。"""
    m = list(IDENT)
    if not s:
        return m
    for name, args in re.findall(r'(\w+)\s*\(([^)]*)\)', s):
        vals = [float(x) for x in re.split(r'[,\s]+', args.strip()) if x]
        if name == 'matrix' and len(vals) == 6:
            t = vals
        elif name == 'translate' and vals:
            t = [1.0, 0.0, 0.0, 1.0, vals[0], vals[1] if len(vals) > 1 else 0.0]
        elif name == 'scale' and vals:
            sy = vals[1] if len(vals) > 1 else vals[0]
            t = [vals[0], 0.0, 0.0, sy, 0.0, 0.0]
        elif name == 'rotate' and vals:
            import math
            a = math.radians(vals[0])
            t = [math.cos(a), math.sin(a), -math.sin(a), math.cos(a), 0.0, 0.0]
        else:
            continue
        m = mul_matrix(m, t)
    return m


def parse_svg(path):
    """返回 (paths, gradients, bbox_w, bbox_h)。

    paths 里的 d 保持 SVG 原始坐标；base 是「d 坐标 → SVG 显示坐标」的矩阵，
    顶层 <g> 的平移就是 FFDec 把 bounds 归零的补偿。
    bbox 用 svg 的 width/height —— 即 shape 在显示坐标系里的实际范围。
    """
    tree = ET.parse(path)
    root = tree.getroot()
    w = float(root.get('width', '0').replace('px', '') or 0)
    h = float(root.get('height', '0').replace('px', '') or 0)

    grads = {}
    for el in root.iter():
        tag = el.tag.replace(SVG_NS, '')
        if tag in ('linearGradient', 'radialGradient'):
            grads[el.get('id')] = parse_gradient(el, tag)

    paths = []
    missing_grad = []   # (文件名, gradient id, 原始 fill) —— 供上层打印

    def walk(node, mat):
        for ch in list(node):
            tag = ch.tag.replace(SVG_NS, '')
            if tag == 'g':
                walk(ch, mul_matrix(mat, parse_transform(ch.get('transform'))))
            elif tag == 'path':
                m = mul_matrix(mat, parse_transform(ch.get('transform')))
                d = ch.get('d')
                if not d:
                    continue
                fill = ch.get('fill') or 'none'
                stroke = ch.get('stroke')
                item = {'d': d, 'fillRule': ch.get('fill-rule') or 'nonzero', 'base': m}
                # FFDec 用 fill-opacity/stroke-opacity 表达半透明部件；丢掉会把
                # 0.2 的灰弧画成纯黑（action/1 帧 1 的球下黑弧）。
                fo = ch.get('fill-opacity')
                if fo is not None:
                    try:
                        item['fillAlpha'] = float(fo)
                    except ValueError:
                        pass
                so = ch.get('stroke-opacity')
                if so is not None:
                    try:
                        item['strokeAlpha'] = float(so)
                    except ValueError:
                        pass
                if fill.startswith('url('):
                    gid = fill[fill.index('#') + 1:fill.index(')')]
                    if gid in grads:
                        item['gradient'] = grads[gid]
                    else:
                        # 洋红是「静默失败」标记：渲染出来只是一块紫红斑，看不出
                        # 是渐变丢了。改成取文件里第一个渐变的颜色近似（视觉上
                        # 接近原图），同时记警告交给调用方打印，便于定位问题文件。
                        # 实测当前 3776 个 FFDec SVG 命中率为 100%，此处是兜底。
                        g = next(iter(grads.values()), None)
                        if g and g.get('stops'):
                            st = g['stops'][len(g['stops']) // 2]
                            item['fill'] = st['color']
                            if st.get('opacity', 1) < 1:
                                item['fillAlpha'] = st['opacity']
                        else:
                            item['fill'] = 'none'
                        missing_grad.append((os.path.basename(path), gid, fill))
                elif fill != 'none':
                    item['fill'] = fill
                if stroke and stroke != 'none' and not stroke.startswith('url('):
                    item['stroke'] = {
                        'color': stroke,
                        'width': float(ch.get('stroke-width') or 1),
                        'cap': ch.get('stroke-linecap') or 'round',
                        'join': ch.get('stroke-linejoin') or 'round',
                    }
                paths.append(item)

    walk(root, IDENT)
    return paths, grads, w, h, missing_grad


def parse_gradient(el, kind):
    g = {
        'type': kind,
        'matrix': parse_transform(el.get('gradientTransform')),
        'spread': el.get('spreadMethod') or 'pad',
        'stops': [],
    }
    for st in el:
        if st.tag.replace(SVG_NS, '') != 'stop':
            continue
        g['stops'].append({
            'offset': float(st.get('offset') or 0),
            'color': st.get('stop-color') or '#000000',
            'opacity': float(st.get('stop-opacity') or 1),
        })
    if kind == 'linear':
        g['x1'] = float(el.get('x1') or 0)
        g['y1'] = float(el.get('y1') or 0)
        g['x2'] = float(el.get('x2') or 1)
        g['y2'] = float(el.get('y2') or 0)
    else:
        g['cx'] = float(el.get('cx') or 0)
        g['cy'] = float(el.get('cy') or 0)
        g['r'] = float(el.get('r') or 1)
        g['fx'] = float(el.get('fx') if el.get('fx') is not None else g['cx'])
        g['fy'] = float(el.get('fy') if el.get('fy') is not None else g['cy'])
    return g


def parse_native(swf, cid):
    """用自研 SHAPERECORD 解析器取 shape，返回与 parse_svg 相同的四元组。

    与 parse_svg 的差异：
    - base 用 shape 自己的 [1,0,0,1,-xmin,-ymin]（SWF 坐标 → 显示坐标）
    - 渐变补上 FFDec 的固定口径：线性 x1=-409.6/x2=409.6，径向 cx=cy=0/r=409.6
      （FFDec 的 SVG 导出写的是 819.2，但那是 twips/10；按 twips/20→像素换算应为
       8192/20=409.6。用 Ruffle（按 Flash 规范实现）渲染 sprite47 实测：星芒直径
       约 25px（sprite 本地 1:1），而 r=819.2 时我们渲染出 45px，正好差 2 倍，
       改 409.6 后为 22px，与 Ruffle/FFDec PNG 一致。）
    """
    sh = swfshape.parse_from_swf(swf, cid)
    if sh is None:
        return [], {}, 0, 0, []
    paths = []
    grads = {}
    for i, p in enumerate(sh['paths']):
        item = {'d': p['d'], 'fillRule': p.get('fillRule', 'evenodd'), 'base': list(sh['base'])}
        if p.get('gradient'):
            g = p['gradient']
            ng = {
                'type': g['type'],
                'matrix': g['matrix'],
                'spread': g.get('spread', 'pad'),
                'stops': g['stops'],
            }
            if g['type'] == 'linear':
                ng.update({'x1': -409.6, 'y1': 0.0, 'x2': 409.6, 'y2': 0.0})
            else:
                ng.update({'cx': 0.0, 'cy': 0.0, 'r': 409.6, 'fx': 0.0, 'fy': 0.0})
            grads['native_g%d' % i] = ng
            # 直接挂结构化渐变，而不是只写 url(#id) 引用字符串。
            # 旧写法把定义留在 grads 里、path 只存引用，而落盘代码只认
            # path 上的 'gradient' 键 —— 结果 27 个文件 49 处渐变全部丢失，
            # 渲染时静默变成无填充（shape_46.json 的 url(#native_g0) 就是残留）。
            item['gradient'] = ng
        elif p.get('stroke'):
            # spec.StrokeDef 是结构体，不能写字符串（Go 侧 unmarshal 会报错）。
            item['fill'] = 'none'
            item['stroke'] = {
                'color': p['stroke'],
                'width': p.get('strokeWidth', 1.0) or 1.0,
                'cap': 'round',
                'join': 'round',
            }
            if p.get('strokeAlpha') is not None:
                item['strokeAlpha'] = p['strokeAlpha']
        else:
            item['fill'] = p.get('fill', 'none')
            if p.get('fillAlpha') is not None:
                item['fillAlpha'] = p['fillAlpha']
        paths.append(item)
    return paths, grads, sh['width'], sh['height'], []


# ---------- 展开 ----------

IDENT_CMUL = (1.0, 1.0, 1.0)
IDENT_CADD = (0.0, 0.0, 0.0)


def norm_filter(f):
    """把 swfread 解出的滤镜记录转成素材格式（颜色转 #rrggbbaa，去掉内部字段）。"""
    # SWF 里 RGBA 是按 R,G,B,A 顺序写的字节，swfread 用 '<I' 读成整数后
    # 最低字节才是 R（早期按 (c>>24) 取 R 会把颜色整体错一位，青色发光变白色）。
    c = f.get('color') or 0
    r, g, b, a = c & 255, (c >> 8) & 255, (c >> 16) & 255, (c >> 24) & 255
    out = {
        'type': f.get('type', 'glow'),
        'blurX': round(float(f.get('blurX', 0)), 4),
        'blurY': round(float(f.get('blurY', 0)), 4),
        'strength': round(float(f.get('strength', 1)), 4),
        'color': '#%02x%02x%02x%02x' % (r, g, b, a),
    }
    if f.get('inner'):
        out['inner'] = True
    if f.get('knockout'):
        out['knockout'] = True
    if f.get('compositeSource'):
        out['compositeSource'] = True
    if f.get('type') == 'shadow':
        out['angle'] = round(float(f.get('angle', 0)), 4)
        out['distance'] = round(float(f.get('distance', 0)), 4)
    return out

def filter_pad(filters):
    """一组滤镜在 1x 画布坐标下最多能把内容向外扩多少像素。

    渲染器把 blurX 换算成「三次盒滤波、每次半径 r=max(1, round(blur*0.25))」，
    三次盒滤波的支撑半径就是 3r；滤镜链上的支撑相加，投影再加 distance。
    不加这个 pad 的后果（实测）：帧 bbox 只按几何算，运行时 RenderClip 用
    bbox 当窗口，super1/action_1 这类带 glow 的帧四边 alpha 被硬切成 246
    （整帧边缘一圈直边光晕），看起来像「贴了张方框」。

    滤镜在链上串联，误差按支撑累加；再留 1px 余量吸收 1/64 像素覆盖采样。
    """
    pad = 0.0
    for fid, fs in (filters or {}).items():
        p = 0.0
        for f in fs:
            # x/y 取 max 而不是相加：扩散半径是两个方向支撑的最宽者，
            # 相加会把 super1/action_1 这类 blur(2,2)+glow(5,5) 算成 14px
            # 而不是 7px，帧窗口无谓地大一圈（真跑起来是 4x，多 56px）。
            bx = max(f.get('blurX', 0) or 0, f.get('blurY', 0) or 0)
            if bx > 0:
                p += 3 * max(1, int(round(bx * 0.25)))
            if f.get('type') == 'shadow':
                p += abs(float(f.get('distance', 0) or 0))
        pad = max(pad, p)
    return pad + 1.0 if pad > 0 else 0.0




def expand(swf, cid, frame_idx, mat, anim_frames, dirname, layer=None,
           depth=0, trail=(), dir_seg=False, off=0, fid=None, filters=None,
           seg_idx=None, free_child=False, groups=None, group_capture=False,
           place_z=0):
    """递归展开显示列表，产出 (shapeCid, 世界矩阵, 层名, alpha)。

    frame_idx 是「父时间轴上本帧实际显示的那一帧」，off 是本实例在自己时间轴上的
    起点偏移（父帧为单位）。本实例的局部帧 = (frame_idx - off) mod 自身帧数。

    seg_idx 是「当前方向段内的第几帧」（0..段长-1，由根循环的 i 提供并往下透传）。
    带方向 label 的容器必须用它取帧：这类容器是被 AS3 gotoAndStop(方向) 直接
    钉到段首的，跟父帧号没有比例关系；而根往往只有 1 帧（frame_idx 恒 0），
    拿父帧推段内偏移会算成回绕后的错帧。

    为什么不能逐层 modulo：Flash 里嵌套 sprite 的时间轴是各自一直往前跑的，
    只有它自己的帧数会回绕，不为父 sprite 的帧数所限。早期实现拿
    `frame_idx % sp.nframes` 当父帧再传给子层，遇到「1 帧的中间容器」
    （如 exp/1: 67 → 21(1帧) → 20(69帧)）时，父帧恒为 0，子层就被冻在第 0 帧，
    表现为整段动作的姿态一动不动（姿态本该逐帧旋转/缩放）。
    """
    is_switch = False   # 「2 帧 A/B 开关容器」标记，见下方 else 分支
    if seg_idx is None:
        seg_idx = frame_idx
    if depth > 16 or cid in trail:
        return
    if cid in swf.shapes:
        yield cid, mat, layer, 1.0, IDENT_CMUL, IDENT_CADD, fid
        return
    sp = swf.sprites.get(cid)
    if sp is None:
        return
    # 带方向 label 的是「方向段容器」，冻结在该方向段首帧，避免跨段；
    # 其余嵌套 sprite 按输出的帧号推进（原版里它们各自独立循环）。
    if dirname and dirname in sp.labels:
        start = sp.labels[dirname]
        # dir_seg=True 时按段内循环（飞行器每方向 8 帧）；否则冻结段首（形态 idle）
        f = start + (seg_idx % seg_len(sp, dirname)) if dir_seg else start
    else:
        # 「2 帧 A/B 开关容器」：两帧挂的子树 cid 集合不同，等于一个二选一开关。
        # 例如 plain 的 sprite 39(方向容器) → 9(2 帧) → 4(17 帧浮动动画) / 8(11 帧静止)，
        # 116 → 52 → 49/51，126 → 125 → 123/124 都是这个结构。
        #
        # 官方 AS（NonoModel.as 的 _bodyMC.getChildByName 那几行）只对命名层
        # color_1/color_2/body/foot/foot_2 调 gotoAndStop(direction)，从不驱动这层；
        # 真让它在 Flash 里播放，就是每帧替换子树 → 画面闪烁。
        #
        # 早期实现把它当普通容器跟帧号推进，结果 17 帧里 8 帧取到了 B 套（静止姿态），
        # down 的球 ty 变成 27.01/24.00/26.19/24.00 的锯齿，肉眼是 240ms 一次抖动。
        # 这里冻结在第 0 帧（A 套），段内帧号透传给子层（见下面 child_f）。
        is_switch = False
        if sp.nframes == 2:
            c0 = {o.get('cid') for o in sp.state_at(0).values() if o.get('cid') is not None}
            c1 = {o.get('cid') for o in sp.state_at(1).values() if o.get('cid') is not None}
            is_switch = bool(c0) and bool(c1) and c0 != c1
        if is_switch:
            f = 0
        else:
            f = (frame_idx - off) % sp.nframes if sp.nframes > 0 else 0
    state = sp.state_at(f)
    for d in sorted(state):
        op = state[d]
        if op.get('cid') is None:
            continue
        lname = layer
        if lname is None:
            lname = op.get('name') or f"shape_{op['cid']}"
        m2 = mul_matrix(mat, swf_matrix_to_svg(op.get('matrix')))
        # 子 sprite 的帧号要从它自己的入场帧起算：嵌套 sprite 是从被 place 到
        # 舞台那一刻开始播自己的第 0 帧，不是跟父时间轴同步。
        #
        # 这里必须传父「本帧实际显示的那一帧」f，不能传 frame_idx：根只有 1 帧，
        # frame_idx 恒为 0，而方向容器已被 gotoAndStop 到段首（如 leftdown=7）。
        # 传 frame_idx 时子层算 (0 - 7) % nframes 会回绕到别的帧上——实测
        # leftdown 段首帧的 cid40 被取成第 11 帧，环状光晕缩水一半（shape_26
        # 的世界缩放该是 1.0，却算成 0.592）。
        child_f = f
        if is_switch:
            # 开关层自身冻结在 A 套第 0 帧，但 A 套自己的 17 帧要照常播：
            # 段内帧号直接透传，子层再用 (child_f - off) % nframes 归一到自己的时间轴。
            child_f = seg_idx
        if free_child and dirname and dirname in sp.labels:
            # 父容器被 gotoAndStop 钉在段首，但它下面的子 MC 在 Flash 里是
            # 自己往前播的（父 stop 不冻结子层）。这里让子层按输出帧号推进，
            # 父容器的 state 仍取段首——这才是游戏里的实际画面。
            child_f = start + seg_idx
        child_off = off + sp.entry_frame(d, f)
        # 滤镜挂在整个 place 上（对它下面整棵子树一起做发光/阴影），所以子树
        # 里每个 part 都带同一个 fid，渲染时按 fid 成组先合成到临时层。
        child_fid = fid
        if op.get('filters') and filters is not None:
            child_fid = 'f%d' % len(filters)
            filters[child_fid] = [norm_filter(x) for x in op['filters']]
        alpha = 1.0
        cmul, cadd = IDENT_CMUL, IDENT_CADD
        cx = op.get('cxform')
        if cx:
            if cx['mul'][3] is not None:
                alpha = max(0.0, min(1.0, cx['mul'][3]))
            cmul = tuple(cx['mul'][:3])
            cadd = tuple(cx['add'][:3])
        # ---- 子时间轴捕获（--anim-groups）----
        # Flash 里嵌套 MC 各按自己的帧数循环，父容器 stop/gotoAndStop 不影响
        # 子层。把它展平进父帧会同时踩两个坑：
        #   1) 父帧只跑 anim_frames（=8）次，18 帧的光圈只导到前 8 帧（缺帧）；
        #   2) 被 1 帧包装器包住的子 MC，父帧恒 0 → 子层永远取第 0 帧（眼睛不眨）。
        # 这里把「自身帧数 > 1 且不是方向容器」的子树整体收成 AnimGroup，
        # 运行时按自己的时钟推进；父帧因此只剩静态部分。
        csp = swf.sprites.get(op['cid'])
        if (groups is not None and group_capture and csp is not None
                and csp.nframes > 1 and not (dirname and dirname in csp.labels)
                and (lname or '') in group_layers
                # 周期本身不超过输出帧数的子层不需要成组：展平时
                # f = (frame_idx - off) % nframes 已经能走完整一圈。
                # 判据来自 annual：它的 5 个层各挂一个 21 帧容器，展开成
                # 21 帧输出就够了；若也收成组，一个方向会凭空多出 5 组、
                # 每 tick 多做 5 次全帧合成。
                and csp.nframes > anim_frames
                and not (group_deep and has_multi_child(swf, op['cid']))):
            # z 用「这条组所在的合成位置」：子 MC 自己挂在 depth>1 上时就是它
            # 的 depth（光圈 10）；被 depth=1 的 1 帧包装器包住时取包装器的
            # 位置（眼睛 sprite25 在 depth5），否则会和底层的 depth1 混排。
            g = {'z': d if d > 1 else (place_z or d), 'ms': frame_ms, 'frames': []}
            base_filters = {}
            g_fid = fid
            if op.get('filters'):
                base_filters['f0'] = [norm_filter(x) for x in op['filters']]
                g_fid = 'f0'
            for gi in range(csp.nframes):
                gf = dict(base_filters)
                leaves = list(expand(swf, op['cid'], child_off + gi, m2, anim_frames,
                                     dirname, lname, depth + 1, trail + (cid,), dir_seg,
                                     child_off, g_fid, gf, gi, False,
                                     groups=None, group_capture=False, place_z=d))
                gparts = []
                for c, m, l, a, smul, sadd, cfid in leaves:
                    nmul = tuple(cmul[i] * smul[i] for i in range(3))
                    nadd = tuple(sadd[i] * cmul[i] + cadd[i] for i in range(3))
                    gparts.append({'cid': c, 'matrix': m, 'layer': l,
                                   'alpha': a * alpha, 'cmul': nmul, 'cadd': nadd,
                                   'cid_fid': cfid})
                g['frames'].append({'parts': gparts, 'filters': gf})
            groups.append(g)
            continue
        for item in expand(swf, op['cid'], child_f, m2, anim_frames, dirname,
                           lname, depth + 1, trail + (cid,), dir_seg, child_off,
                           child_fid, filters, seg_idx, free_child,
                           groups, group_capture, d):
            c, m, l, a, smul, sadd, cfid = item
            # 子层先作用、父层后作用：
            #   c1 = c*smul + sadd
            #   c2 = c1*cmul + cadd = c*(smul*cmul) + (sadd*cmul + cadd)
            # 注意 add 的传递方向：子 add 要被父 mul 缩放，不是反过来。
            nmul = tuple(cmul[i] * smul[i] for i in range(3))
            nadd = tuple(sadd[i] * cmul[i] + cadd[i] for i in range(3))
            yield c, m, l, a * alpha, nmul, nadd, cfid


def seg_len(sp, label):
    """方向段长度：从该 label 起点到下一个 label 起点（末尾段到总帧数）。

    飞行器的 dirMc/fireMc 每个方向段固定 8 帧，段与段之间在时间轴上首尾相接；
    用全局帧号推进会跨段（下载方向播着播着变成别的方向），必须按段内偏移取。
    """
    starts = sorted(sp.labels.values())
    start = sp.labels[label]
    nxt = [v for v in starts if v > start]
    end = nxt[0] if nxt else sp.nframes
    return max(1, end - start)


def find_dir_sprite(swf):
    for sid, sp in swf.sprites.items():
        if 'down' in sp.labels and 'rightdown' in sp.labels:
            return sid
    return None


def path_range(paths):
    """shape 内所有 path 的实际坐标范围 (x0,y0,x1,y1)。

    为什么要它：shape 的路径坐标并不从 0 开始（fly 的 shape_26 是 [-35,35]，
    即形状以局部原点为中心）。早期 bbox 一律按 [0,w]×[0,h] 算，等于给每个
    shape 多加了一个 (-dmin) 的平移，包围盒整体偏出去——fly1 的画布被撑到
    144 宽（实际内容 74），帧 bbox 偏右 35px，裁剪框把左边半个飞行器切掉了。
    """
    xs, ys = [], []
    for p in paths:
        n = [float(v) for v in re.findall(r'-?\d+\.?\d*', p['d'])]
        xs += n[0::2]
        ys += n[1::2]
    if not xs:
        return None
    return (min(xs), min(ys), max(xs), max(ys))


def xform_bbox(mat, x0, y0, x1, y1):
    """把 [x0,y0]-[x1,y1] 用矩阵变换后的四角包围盒。"""
    pts = [(x0, y0), (x1, y0), (x0, y1), (x1, y1)]
    xs, ys = [], []
    for x, y in pts:
        X = mat[0] * x + mat[2] * y + mat[4]
        Y = mat[1] * x + mat[3] * y + mat[5]
        xs.append(X)
        ys.append(Y)
    return min(xs), min(ys), max(xs), max(ys)


def refresh_bbox(out_frames, shape_dr):
    """按当前 matrix 重算每帧 bbox（ty 被对齐改过之后必须重算）。

    out_frames 里的 matrix 已经是「减去画布偏移」的最终放置矩阵，
    所以 xform_bbox 直接出来的就是渲染坐标系下的可见范围。
    漏掉这一步的后果：运行期 idleClip 用旧框，画面整体屏移，向上/斜上
    这类改动量大的方向会直接裁成空白窗口。
    """
    updated = 0
    for item in out_frames:
        x0 = y0 = 1e9
        x1 = y1 = -1e9
        for p in item['parts']:
            name = p['part']
            try:
                cid = int(name.rsplit('_', 1)[1])
            except (ValueError, IndexError):
                continue
            dr = shape_dr.get(cid)
            if not dr:
                continue
            bx0, by0, bx1, by1 = xform_bbox(p['matrix'], dr[0], dr[1], dr[2], dr[3])
            x0 = min(x0, bx0); y0 = min(y0, by0)
            x1 = max(x1, bx1); y1 = max(y1, by1)
        if x0 > 1e8 or x1 < -1e8:
            continue
        pad = filter_pad(item.get('filters'))
        item['bbox'] = [round(x0 - pad, 3), round(y0 - pad, 3),
                        round(x1 - x0 + pad * 2, 3), round(y1 - y0 + pad * 2, 3)]
        updated += 1
    print(f'    bbox 重算: {updated} 帧')


def sync_layers_vertical(out_frames):
    """把「球心组」各层的垂直曲线对齐到同一基准。

    背景：SWF 里 color_2（球壳/耳朵）与 body（黑脸/眼睛）是两套独立的
    17 帧时间轴。它们的第 0 帧并不总在同一相位——实测 down 差 0 帧，
    right 差 4 帧，leftup 差 9 帧，left 差 10 帧，up 差 14 帧；FFDec 用
    「gotoAndStop(帧) + 导出」得到的结果一样，说明相位差来自 SWF 数据本身，
    不是本工具的展平错误。直接叠层就会出现「球体沉下去、脸还留在上面」，
    也就是子渊反馈的穿帮。

    做法：以幅度最大的部件（实测都是球壳）为基准曲线，把幅度落在基准
    ±20% 以内的部件逐帧平移到基准曲线上，同时保留各自的平均位置。
    幅度明显更小的部件不动——耳朵（GIF 实测只动 0.9px，球动 16.5px）和
    地面影子（幅度≈0）本来就该独立，强行对齐反而破坏官方表现。
    帧间切换 shape 的槽位（眼睛闭/半/全睁）直接跳过，它们的 ty 本来就同步。
    """
    n = len(out_frames)
    if n == 0:
        return
    cols = []
    for ref_i in range(len(out_frames[0]['parts'])):
        part0 = out_frames[0]['parts'][ref_i]
        layer0 = part0['layer']
        curve = []
        ok = True
        for fr in out_frames:
            if ref_i >= len(fr['parts']) or fr['parts'][ref_i]['layer'] != layer0:
                ok = False
                break
            curve.append(fr['parts'][ref_i]['matrix'][5])
        if ok:
            cols.append((layer0, part0['part'], ref_i, curve))
    if not cols:
        return
    amps = [max(c[3]) - min(c[3]) for c in cols]
    # 基准幅度取所有部件的「中位数」：球壳、黑脸、眼睛这一大票刚性部件
    # 幅度相同，数量上占多数；耳朵（GIF 实测只动 0.9px）和地面影子
    # （幅度≈0）是少数派，中位数不会被它们带偏。
    amps_sorted = sorted(amps)
    base_amp = amps_sorted[len(amps_sorted) // 2]
    # 基准曲线优先从 color_2（球壳层）里挑，幅度最接近中位数的那条
    cand = [(abs(amps[i] - base_amp), i) for i, c in enumerate(cols) if c[0] == 'color_2']
    if not cand:
        cand = [(abs(amps[i] - base_amp), i) for i in range(len(cols))]
    base_i = min(cand)[1]
    base = cols[base_i][3]
    if base_amp < 1e-6:
        return
    mean_base = sum(base) / n
    moved = 0
    for layer, part_name, idx, curve in cols:
        amp = max(curve) - min(curve)
        if amp < base_amp * 0.5 or amp > base_amp * 1.6:
            continue                      # 耳朵 / 影子 / 特效：保留自己的节奏
        if curve == base:
            continue
        off = sum(curve) / n - mean_base  # 保留平均位置，只换曲线形状
        for fi in range(n):
            out_frames[fi]['parts'][idx]['matrix'][5] = round(base[fi] + off, 6)
        moved += 1
    print(f'    层间垂直对齐: 基准 {cols[base_i][0]}/{cols[base_i][1]} '
          f'幅度 {base_amp:.2f}，对齐 {moved} 个部件')


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--swf', required=True)
    ap.add_argument('--root', type=int, required=True)
    ap.add_argument('--name', required=True)
    ap.add_argument('--svg-dir', default='', help='FFDec SVG 目录；--native 时可省略')
    ap.add_argument('--out', required=True)
    ap.add_argument('--anim-frames', type=int, default=17)
    ap.add_argument('--auto-frames', action='store_true', help='自动探测动画层帧数')
    ap.add_argument('--dir-seg', action='store_true',
                    help='方向段内循环（飞行器：每方向 8 帧）；缺省冻结段首（形态 idle）')
    ap.add_argument('--free-child', action='store_true',
                    help='方向容器固定在段首，但其子层按输出帧推进（还原 Flash 父 stop/子自播）')
    ap.add_argument('--anim-groups', action='store_true',
                    help='把「多帧且非方向容器」的子 MC 导出成独立子时间轴（飞行器眼球/光圈），'
                         '不再是展平到父帧序列（否则 18 帧光圈只导 8 帧、1 帧包装器里的眼睛恒为第 0 帧）')
    ap.add_argument('--anim-group-deep', action='store_true',
                    help='捕获时跳过「内部还有多帧子 MC」的中间容器，直接收到最内层。'
                         'annual 的 106(21帧) 里还套着 103(150帧)/105(100帧)，'
                         '不收最内层的话那两层仍会被展平冻结。默认关（fly1/fly2 行为不变）')
    ap.add_argument('--anim-group-layers', default='dirMc',
                    help='只在这些层里捕获子时间轴（逗号分隔；尾焰 fireMc 必须排除，'
                         '它由官方 PNG 帧接管，重复画会双份）')
    ap.add_argument('--frame-ms', type=int, default=0,
                    help='子时间轴每帧毫秒；0 = 按 SWF 头 frameRate 自动算')
    ap.add_argument('--single-dir', action='store_true',
                    help='无方向动画（表情/动作）：全部帧放一个 default 方向')
    ap.add_argument('--canvas-pad', type=float, default=2.0)
    ap.add_argument('--canvas', default='', help='强制画布 w,h,x,y（跨形态统一用）')
    ap.add_argument('--native', action='store_true',
                    help='用自研 swfshape 解析器取 shape，不读 FFDec 的 SVG 目录')
    ap.add_argument('--sync-layers', action='store_true',
                    help='把球心组各层的垂直曲线对齐到同一基准（修各方向层间相位差导致的穿帮）')
    args = ap.parse_args()

    swf = SWF(args.swf)
    root = swf.sprites.get(args.root)
    if root is None:
        raise SystemExit(f'找不到根 sprite {args.root}')
    print(f'SWF: shapes={len(swf.shapes)} sprites={len(swf.sprites)}')
    # 子时间轴每帧毫秒：原版是 24/25fps，取 SWF 头 frameRate 自动算，
    # 免得把 720ms 的光圈硬按 40ms/tick 压成 320ms。
    fps = read_frame_rate(args.swf)
    global frame_ms, group_layers, group_deep
    frame_ms = args.frame_ms if args.frame_ms > 0 else (int(round(1000.0 / fps)) if fps > 1 else 40)
    group_layers = tuple(x.strip() for x in args.anim_group_layers.split(',') if x.strip())
    group_deep = args.anim_group_deep
    print(f'帧率: {fps:.2f}fps → 子时间轴 {frame_ms}ms/帧, 组层: {group_layers}')
    dsp = find_dir_sprite(swf)
    print(f'方向 sprite: {dsp}')

    if args.auto_frames:
        import collections
        c = collections.Counter(sp.nframes for sp in swf.sprites.values()
                                if sp.nframes not in (1, 63) and sp.nframes < 56)
        if c:
            args.anim_frames = c.most_common(1)[0][0]
            print(f'自动帧数: {args.anim_frames} (分布 {dict(c)})')

    form_dir = os.path.join(args.out, args.name)
    # 每个形态独立的 parts 目录：不同 SWF 的 shape id 会重复，共享会互相覆盖
    parts_dir = os.path.join(form_dir, 'parts')
    os.makedirs(parts_dir, exist_ok=True)
    os.makedirs(form_dir, exist_ok=True)

    dirs = ['default'] if args.single_dir else DIRS
    nframes = root.nframes if args.single_dir else args.anim_frames
    if args.single_dir:
        print(f'单方向模式：{nframes} 帧')

    # 1) 展开
    frames_by_dir = {}
    groups_by_dir = {}
    layer_order = []      # [名]，决定 z
    needed = {}
    for dirname in dirs:
        frames = []
        # 绝对帧基准：根自己若有方向 label，段内第 i 帧 = labels[dirname]+i。
        # expand 内部统一按绝对帧推进，嵌套层减自己的入场帧才对得上。
        base = 0
        if not args.single_dir:
            base = root.labels.get(dirname, 0)
        for i in range(nframes):
            fmap = {}
            glist = []
            leaves = list(expand(swf, args.root, base + i, IDENT, args.anim_frames,
                                 None if args.single_dir else dirname,
                                 dir_seg=args.dir_seg, filters=fmap, seg_idx=i,
                                 free_child=args.free_child,
                                 groups=glist, group_capture=args.anim_groups))
            parts = []
            for cid, m, lname, alpha, cmul, cadd, fid in leaves:
                needed[cid] = True
                if lname not in layer_order:
                    layer_order.append(lname)
                p = {'cid': cid, 'matrix': m, 'layer': lname, 'alpha': alpha,
                     'cmul': cmul, 'cadd': cadd}
                if fid:
                    p['cid_fid'] = fid   # 见下方写盘时改名，保持 JSON 短
                parts.append(p)
            frames.append({'parts': parts, 'filters': fmap})
            # 子时间轴只跟方向有关，与父段内第几帧无关；同一方向取第 0 帧那次
            # 捕获结果即可（组内容已从静态帧里剔除，不会再重复）。
            if glist and not groups_by_dir.get(dirname):
                groups_by_dir[dirname] = glist
                for g in glist:
                    for gf in g['frames']:
                        for p in gf['parts']:
                            needed[p['cid']] = True
                            if p['layer'] not in layer_order:
                                layer_order.append(p['layer'])
        frames_by_dir[dirname] = frames
        gn = sum(len(g['frames']) for g in groups_by_dir.get(dirname, []))
        print(f'  {dirname}: {len(frames)} 帧, 每帧 {len(frames[0]["parts"])} 个 part'
              f'{f", 子时间轴 {len(groups_by_dir.get(dirname, []))} 组 / 共 {gn} 帧" if gn else ""}')
    print(f'shape 数: {len(needed)}, 层: {layer_order}')

    # 2) 解析 SVG，写 parts
    shape_info = {}
    shape_dr = {}   # cid -> 路径实际坐标范围，bbox 用它而不是 [0,w]
    minx = miny = 1e18
    maxx = maxy = -1e18
    missing = []
    # 每帧自己的 bbox：动作/表情的单帧内容常常只占统一画布的一小块，
    # 渲染整张画布会白烧十几倍像素（super4/action_4 单帧 746x453 → 实际 ~200x200）。
    frame_bb = {}
    for cid in sorted(needed):
        if args.native:
            if cid not in swf.shape_defs:
                missing.append(cid)
                continue
            paths, grads, w, h, _mg = parse_native(swf, cid)
        else:
            sp = os.path.join(args.svg_dir, f'{cid}.svg')
            if not os.path.exists(sp):
                missing.append(cid)
                continue
            paths, grads, w, h, mg = parse_svg(sp)
            for _fn, _gid, _raw in mg:
                print('  ! gradient 引用缺失 %s: %s (%s) —— 用近似色替代'
                      % (_fn, _gid, _raw))
        shape_info[cid] = (paths, grads, w, h)
        dr = path_range(paths)
        if dr:
            shape_dr[cid] = dr
        # world bbox：显示坐标 [0,w]×[0,h] 经 place 矩阵（= part.matrix ∘ base⁻¹）
        for dirname, frames in frames_by_dir.items():
            for fi, fr in enumerate(frames):
                for p in fr['parts']:
                    if p['cid'] != cid:
                        continue
                    # alpha=0 的部件（cxform mul alpha=0）在 Flash 里完全不可见，
                    # 若把它们算进 bbox，画布会被远处/巨大的辅助图形撑到两倍大
                    # （super4/action/4：746x453 vs FFDec 383x238）。
                    if p.get('alpha', 1.0) <= 0:
                        continue
                    place = place_matrix_of(p['matrix'], shape_info[cid][0])
                    dr = shape_dr.get(cid)
                    if dr:
                        x0, y0, x1, y1 = xform_bbox(place, dr[0], dr[1], dr[2], dr[3])
                    else:
                        x0, y0, x1, y1 = xform_bbox(place, 0, 0, w, h)
                    minx, miny = min(minx, x0), min(miny, y0)
                    maxx, maxy = max(maxx, x1), max(maxy, y1)
                    k = (dirname, fi)
                    bb = frame_bb.get(k)
                    if bb is None:
                        frame_bb[k] = [x0, y0, x1, y1]
                    else:
                        bb[0] = min(bb[0], x0)
                        bb[1] = min(bb[1], y0)
                        bb[2] = max(bb[2], x1)
                        bb[3] = max(bb[3], y1)
        # 子时间轴的内容已经从静态帧里剔除，画布统计必须把它们补回来：
        # canvas 一旦缩水，part.matrix 的减原点量就变，vecFlyClip（常量表）
        # 会整体失配，飞行器落点漂移。组帧不写 frame_bb（帧级 bbox 只给
        # 动作/表情用，混进来会让 fly 的帧框被组内容撑大）。
        for dirname, glist in groups_by_dir.items():
            for g in glist:
                for gf in g['frames']:
                    for p in gf['parts']:
                        if p['cid'] != cid or p.get('alpha', 1.0) <= 0:
                            continue
                        place = place_matrix_of(p['matrix'], shape_info[cid][0])
                        dr = shape_dr.get(cid)
                        if dr:
                            x0, y0, x1, y1 = xform_bbox(place, dr[0], dr[1], dr[2], dr[3])
                        else:
                            x0, y0, x1, y1 = xform_bbox(place, 0, 0, w, h)
                        minx, miny = min(minx, x0), min(miny, y0)
                        maxx, maxy = max(maxx, x1), max(maxy, y1)
        # 只算 down 方向即可（其余方向同层同矩阵，尺寸差异靠 up 方向补）
    if missing:
        print(f'警告：缺少 SVG: {missing}')

    if args.canvas:
        cx, cy, cw, ch = [float(v) for v in args.canvas.split(',')]
    else:
        cx = minx - args.canvas_pad
        cy = miny - args.canvas_pad
        cw = (maxx - minx) + args.canvas_pad * 2
        ch = (maxy - miny) + args.canvas_pad * 2
    print(f'画布: ({cx:.3f},{cy:.3f}) {cw:.3f}x{ch:.3f}')

    for cid, (paths, grads, w, h) in shape_info.items():
        out_paths = []
        for p in paths:
            m = p['base']
            item = {
                'd': p['d'],
                # base 是 FFDec 的 <g transform> = translate(-bounds.xmin)，作用在
                # shape 绝对坐标上；part.matrix 才负责父坐标系与画布原点。
                # 两者不能混：早期版本这里写成 m[4]-cx（多加了一次画布原点），
                # 导致 idle 素材的 base 全部偏 anchor（6 形态 262/262，
                # shape_3 白球下移 14px 被裁），已由 fix_part_base.py 批量修正。
                'matrix': [round(m[0], 6), round(m[1], 6), round(m[2], 6),
                           round(m[3], 6), round(m[4], 6), round(m[5], 6)],
                'fillRule': p.get('fillRule', 'nonzero'),
            }
            if 'gradient' in p:
                g = dict(p['gradient'])
                gm = g['matrix']
                g['matrix'] = [gm[0], gm[1], gm[2], gm[3], gm[4] - cx, gm[5] - cy]
                item['gradient'] = g
            elif p.get('fill'):
                item['fill'] = p['fill']
            if 'stroke' in p:
                item['stroke'] = p['stroke']
            if 'fillAlpha' in p:
                item['fillAlpha'] = p['fillAlpha']
            if 'strokeAlpha' in p:
                item['strokeAlpha'] = p['strokeAlpha']
            out_paths.append(item)
        with open(os.path.join(parts_dir, f'shape_{cid}.json'), 'w') as f:
            json.dump({'id': f'shape_{cid}', 'paths': out_paths}, f, ensure_ascii=False,
                      separators=(',', ':'))

    # 3) sprite.json
    layers = [{'name': n, 'z': i, 'tintable': n.startswith('color') or n in ('color_1', 'color_2')}
              for i, n in enumerate(layer_order)]

    directions = {}

    def emit_parts(parts):
        """内部 part 列表 → sprite.json 的 parts（矩阵减画布原点）。

        静态帧和子时间轴帧共用同一套字段，避免两处口径漂移
        （cmul/cadd 只在非恒等时写，fid 保持组内编号）。
        """
        ps = []
        for p in parts:
            m = p['matrix']
            d = {
                'layer': p['layer'],
                'part': f'shape_{p["cid"]}',
                'matrix': [round(m[0], 6), round(m[1], 6), round(m[2], 6),
                           round(m[3], 6), round(m[4] - cx, 6), round(m[5] - cy, 6)],
                'alpha': round(p['alpha'], 4),
            }
            cm = p.get('cmul', IDENT_CMUL)
            ca = p.get('cadd', IDENT_CADD)
            if any(abs(v - 1.0) > 1e-9 for v in cm):
                d['cmul'] = [round(v, 6) for v in cm]
            if any(abs(v) > 1e-9 for v in ca):
                d['cadd'] = [round(v, 4) for v in ca]
            if p.get('cid_fid'):
                d['fid'] = p['cid_fid']
            ps.append(d)
        return ps
    for dirname, frames in frames_by_dir.items():
        out_frames = []
        for fi, fr in enumerate(frames):
            item = {'parts': emit_parts(fr['parts'])}
            if fr.get('filters'):
                item['filters'] = fr['filters']
            bb = frame_bb.get((dirname, fi))
            if bb:
                # 带滤镜的帧要把光晕/模糊的扩散量算进 bbox，否则运行时按 bbox
                # 开窗会把光晕四边切平（详见 filter_pad 注释）。
                fp = filter_pad(fr.get('filters'))
                item['bbox'] = [round(bb[0] - cx - fp, 3), round(bb[1] - cy - fp, 3),
                                round(bb[2] - bb[0] + fp * 2, 3),
                                round(bb[3] - bb[1] + fp * 2, 3)]
            out_frames.append(item)
        if args.sync_layers:
            sync_layers_vertical(out_frames)
            # bbox 是照原矩阵算好的，ty 一改就必须重算，否则运行时按旧框开窗，
            # 内容会整体偏移甚至被裁成空图（实测 up/leftup 直接显示不出来）。
            refresh_bbox(out_frames, shape_dr)
        d_out = {'frames': out_frames}
        glist = groups_by_dir.get(dirname)
        if glist:
            gout = []
            for g in glist:
                gout.append({
                    'z': g['z'],
                    'ms': g['ms'],
                    'frames': [({'parts': emit_parts(gf['parts']), 'filters': gf['filters']}
                                if gf.get('filters') else {'parts': emit_parts(gf['parts'])})
                               for gf in g['frames']],
                })
            d_out['groups'] = gout
        directions[dirname] = d_out

    sprite = {
        'format': 1,
        'name': args.name,
        'canvas': {'w': round(cw, 3), 'h': round(ch, 3),
                   'anchor': [round(-cx, 3), round(-cy, 3)]},
        'colors': {},
        'layers': layers,
        'directions': directions,
    }
    out_sprite = os.path.join(form_dir, 'sprite.json')
    with open(out_sprite, 'w') as f:
        json.dump(sprite, f, ensure_ascii=False, separators=(',', ':'))
    print(f'写入 {out_sprite}')


def place_matrix_of(full, paths):
    """返回用于 bbox 计算的矩阵。

    渲染器用的是 part.matrix（= place ∘ base），形状经 base 后落在 [0,w]×[0,h]，
    所以包围盒直接对 [0,w]×[0,h] 应用 full 即可。早期版本这里反解 base 求 place，
    与渲染路径不一致，会让 bbox 与内容错位（super1/action_6 因此整帧被裁成空图）。
    """
    return full


if __name__ == '__main__':
    main()
