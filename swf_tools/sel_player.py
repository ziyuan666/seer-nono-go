#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""sel_player.py —— 赛尔号 SWF 逐帧播放器（自研 tick 引擎 + 浏览器渲染）。

为什么需要（而不是直接用 FFDec / Ruffle）：
    1. FFDec 的帧导出只按根时间轴重建，子 MC 最多推进一层，550 帧的深层动画看不到；
    2. Ruffle 官方二进制没有 Step Forward，且本机交叉编译不出 Windows 版；
    3. 赛尔号动作 SWF 的画面 100% 由 shape + 时间轴构成，AS3 脚本只做 stop()，
       不需要完整 AVM2 运行时。
    所以自己实现「显示列表 + tick」这一层，把每一 tick 的合成结果交前端渲染。

tick 语义（与 Flash 一致的部分）：
    - 时间轴是树，每个 DefineSprite 实例有独立播放头；
    - 每 tick，所有 playing 的实例各前进 1 帧，到末帧回 0（loop）；
    - 子 MC 从它被 place 到舞台上的那一帧开始从自己的第 0 帧播（Sprite.entry_frame）；
    - 父的 stop / 帧数不影响子 MC。
    不实现的部分：stop()/gotoAndPlay 等脚本控制（所以末帧后循环，而不是定格）。

用法：
    python3 sel_player.py 3_1.swf -o 3_1.html              # 自动选动画主体
    python3 sel_player.py 3_1.swf -o x.html --sprite 98    # 指定子时间轴
    python3 sel_player.py 3_1.swf --list                   # 只打印时间轴树
"""

import argparse
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
IMPORTER = os.path.abspath(os.path.join(HERE, '..', 'nono_deskpet_vec', 'tools', 'importer'))
sys.path.insert(0, IMPORTER)

from swfread import SWF, mul_matrix  # noqa: E402
import swfshape  # noqa: E402


# --------------------------------------------------------------------------
# 显示列表 → 每一 tick 的扁平绘制列表
# --------------------------------------------------------------------------
def matrix_to_svg(m):
    """swfread 的 matrix dict → canvas setTransform 的 [a,b,c,d,e,f]。

    SWF 的 tx/ty 单位是 twips，这里一并换成 px；其余是 16.16 定点已转好的倍数。
    """
    if not m:
        return [1.0, 0.0, 0.0, 1.0, 0.0, 0.0]
    return [m['sx'], m['r0'], m['r1'], m['sy'], m['tx'] / 20.0, m['ty'] / 20.0]


def cx_alpha(cx):
    """只用颜色变换里的 alpha 分量（RGB 变换在赛尔号动作里极少见）。"""
    if not cx:
        return 1.0
    return max(0.0, min(1.0, cx['mul'][3] + cx['add'][3] / 255.0))


def compose(parent, child):
    """canvas 语义的矩阵复合：先 child 后 parent。"""
    return mul_matrix(parent, child)


def flatten_with_alpha(swf, sprite, frame, m, alpha, out, budget=None):
    """带 alpha 继承版本的递归展开（flatten 的完整版）。"""
    if budget is None:
        budget = [96]
    if budget[0] <= 0:
        return
    budget[0] -= 1

    state = sprite.state_at(frame)
    for d, place in sorted(state.items(), key=lambda kv: kv[0]):
        cid = place.get('cid')
        if cid is None:
            continue
        pm = compose(m, matrix_to_svg(place.get('matrix')))
        pa = alpha * cx_alpha(place.get('cxform'))
        if cid in swf.shapes:
            out.append({'s': cid, 'm': [round(v, 4) for v in pm], 'a': round(pa, 4)})
        elif cid in swf.sprites:
            child = swf.sprites[cid]
            entry = sprite.entry_frame(d, frame)
            cf = (frame - entry) % child.nframes if child.nframes else 0
            flatten_with_alpha(swf, child, cf, pm, pa, out, budget)


def collect_shapes(swf, used):
    """把用到的 shape 转成前端可绘制的 path 列表，只解析一次。"""
    out = {}
    for cid in sorted(used):
        info = swfshape.parse_from_swf(swf, cid)
        if not info:
            continue
        paths = []
        for p in info['paths']:
            item = {'d': p['d']}
            if p.get('stroke'):
                item['st'] = p['stroke']
                item['sw'] = round(p.get('strokeWidth') or 1.0, 2)
            else:
                item['f'] = p.get('fill') or '#000000'
            if p.get('fillAlpha') is not None:
                item['fa'] = p['fillAlpha']
            if p.get('strokeAlpha') is not None:
                item['sa'] = p['strokeAlpha']
            if p.get('gradient'):
                g = p['gradient']
                # 渐变：前端用平均色近似（动作 SWF 里极少，先保证不崩）
                stops = g.get('stops') or []
                if stops:
                    item['f'] = stops[len(stops) // 2][1]
                    item['grad'] = {'type': g.get('type'), 'stops': stops,
                                    'matrix': g.get('matrix'), 'focal': g.get('focal_point')}
            paths.append(item)
        out[cid] = paths
    return out


def root_places(swf):
    """读根时间轴的 PlaceObject，用于判断「舞台上直接挂了谁」。

    swfread.SWF 只保留了 sprite 与 shape 定义，根时间轴的 place 记录没存，
    而自动选目标恰好需要它，所以这里单独扫一遍（只读，不影响其它逻辑）。
    """
    from swfread import iter_tags, parse_place2, parse_place3
    out = []
    for code, payload in iter_tags(swf.data, swf.pos):
        if code in (26, 70):
            p = parse_place2(payload) if code == 26 else parse_place3(payload)
            if p.get('cid'):
                out.append({'depth': p['depth'], 'cid': p['cid']})
        elif code in (4,):
            break
    return out


def pick_target(swf, want=None):
    """挑「动画主体」：优先用根时间轴直接挂的 sprite。

    为什么不是「帧数最多的」：3_1 里 550 帧的 20/23 只是左右两半的零件，
    真正的动画主体是根挂的 99（165 帧），里面再嵌 9/98/20/23。
    """
    if want is not None:
        if want not in swf.sprites:
            raise SystemExit(f'找不到 sprite {want}')
        return swf.sprites[want]

    for p in root_places(swf):
        if p['cid'] in swf.sprites:
            return swf.sprites[p['cid']]

    best = None
    for sid, sp in swf.sprites.items():
        if sp.nframes <= 1:
            continue
        if best is None or sp.nframes > best.nframes:
            best = sp
    return best


def describe(swf, sid=None):
    """打印时间轴树（给 --list 用）。"""
    lines = []
    rp = root_places(swf)
    lines.append(f'根时间轴直接挂载: {[(p["depth"], p["cid"]) for p in rp]}')
    for s, sp in sorted(swf.sprites.items()):
        kids = []
        for f, ops in enumerate(sp.frames):
            for op in ops:
                if op.get('cid') and op['cid'] in swf.sprites:
                    kids.append(f'{op["cid"]}@d{op["depth"]}/f{f}')
        labels = ','.join(sp.labels) if sp.labels else ''
        lines.append(f'  sprite {s:5d}  {sp.nframes:4d} 帧'
                     + (f'  标签[{labels}]' if labels else '')
                     + (f'  子={kids}' if kids else ''))
    return '\n'.join(lines)


def build_frames(swf, sprite, nframes):
    """跑 tick 模拟：返回每帧的叶子绘制列表 + 用到的 shape 集合。"""
    used = set()
    frames = []
    for t in range(nframes):
        out = []
        # 根实例就是目标 sprite 本身，t 直接从 0 数到 nframes-1
        flatten_with_alpha(swf, sprite, t % max(1, sprite.nframes),
                           [1.0, 0.0, 0.0, 1.0, 0.0, 0.0], 1.0, out)
        for o in out:
            used.add(o['s'])
        frames.append(out)
    return frames, used


HTML = r'''<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>__TITLE__</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin:0; background:#1b1e24; color:#d8dee9; font:13px/1.5 -apple-system,"Segoe UI",sans-serif;
         display:flex; flex-direction:column; height:100vh; overflow:hidden; }
  /* safe center：放得下时居中，放不下（放大后）退化成 flex-start，滚轮能滚到全图 */
  #stage { flex:1; display:flex; overflow:auto; align-items: safe center; justify-content: safe center;
           background:
             linear-gradient(45deg,#23262d 25%,transparent 25%) 0 0/24px 24px,
             linear-gradient(-45deg,#23262d 25%,transparent 25%) 0 12px/24px 24px,
             linear-gradient(45deg,transparent 75%,#23262d 75%) 12px -12px/24px 24px,
             linear-gradient(-45deg,transparent 75%,#23262d 75%) -12px 0/24px 24px,
             #1b1e24; }
  /* flex 容器里用 margin:auto 居中：放得下时居中，放不下时 margin 归零、
     从左上开始铺开，滚轮能滚到整张图。用 align-items:center 会让溢出的
     上/左半截被裁掉且滚动不到（400% 时画面只剩底部就是这么来的）。 */
  #wrap { padding:16px; flex-shrink:0; }
  canvas { display:block; flex-shrink:0; }
  #bar { display:flex; gap:10px; align-items:center; padding:10px 14px; background:#22262e;
         border-top:1px solid #2f3540; flex-wrap:wrap; }
  button { background:#2f3540; color:#d8dee9; border:1px solid #3c4450; border-radius:6px;
           padding:5px 11px; cursor:pointer; font-size:13px; }
  button:hover { background:#3a4150; }
  button.primary { background:#4c7899; border-color:#5d8dad; color:#fff; }
  input[type=range] { flex:1; min-width:180px; accent-color:#4c7899; }
  #info { font-variant-numeric:tabular-nums; color:#9aa5b1; min-width:150px; text-align:right; }
  #zoom { width:150px; }
  label { color:#9aa5b1; display:flex; align-items:center; gap:6px; }
  .sep { width:1px; height:20px; background:#3c4450; }
</style>
</head>
<body>
<div id="stage"><div id="wrap"><canvas id="cv"></canvas></div></div>
<div id="bar">
  <button id="first" title="首帧 (Home)">⏮</button>
  <button id="prev" title="上一帧 (←)">◀</button>
  <button id="play" class="primary" title="播放/暂停 (空格)">▶ 播放</button>
  <button id="next" title="下一帧 (→)">▶</button>
  <button id="last" title="末帧 (End)">⏭</button>
  <div class="sep"></div>
  <input type="range" id="seek" min="0" max="__LAST__" value="0">
  <span id="info">1 / __TOTAL__</span>
  <div class="sep"></div>
  <label><input type="checkbox" id="loop" checked> 循环</label>
  <label>缩放 <input type="range" id="zoom" min="25" max="800" value="100"></label>
  <span id="zoomval" style="min-width:48px;text-align:right">100%</span>
  <button id="fit">适应</button>
  <button id="export">导出 PNG</button>
</div>
<script>
const DATA = __DATA__;
const W = DATA.w, H = DATA.h, FPS = DATA.fps;
const cv = document.getElementById('cv');
const ctx = cv.getContext('2d');
let zoom = 1, cur = 0, playing = false, timer = null;

// path 只解析一次：Path2D 复用比每帧拼字符串快一个量级
const PATHS = {};
for (const cid in DATA.shapes) {
  PATHS[cid] = DATA.shapes[cid].map(p => {
    const o = { p: new Path2D(p.d) };
    if (p.f) o.f = p.f;
    if (p.st) { o.st = p.st; o.sw = p.sw || 1; }
    if (p.fa !== undefined) o.fa = p.fa;
    if (p.sa !== undefined) o.sa = p.sa;
    if (p.grad) o.grad = p.grad;
    return o;
  });
}

function drawArt(c) {
  const frame = DATA.frames[cur];
  for (const o of frame) {
    const list = PATHS[o.s];
    if (!list) continue;
    const m = o.m;
    c.save();
    // 直接把「对象矩阵 + 包围盒原点 + zoom」合成成绝对变换：
    // 外层不再做 translate/scale，避免两层变换叠加时互相抵消/错位。
    c.setTransform(m[0] * zoom, m[1] * zoom, m[2] * zoom, m[3] * zoom,
                   (m[4] - BOX[0]) * zoom, (m[5] - BOX[1]) * zoom);
    c.globalAlpha = o.a;
    for (const it of list) {
      if (it.f !== undefined) {
        c.globalAlpha = o.a * (it.fa !== undefined ? it.fa : 1);
        c.fillStyle = it.f;
        c.fill(it.p, 'evenodd');
      }
      if (it.st) {
        c.globalAlpha = o.a * (it.sa !== undefined ? it.sa : 1);
        c.strokeStyle = it.st;
        c.lineWidth = it.sw;
        c.stroke(it.p);
      }
    }
    c.restore();
  }
}

// 画布尺寸：按整段动画的并集包围盒，避免不同帧尺寸变化导致抖动
function computeBox() {
  // 关键：shape bounds 是局部坐标，必须用「该帧该对象的矩阵」变换后才是在画布上的位置。
  // 只按 bounds 取并集会得到原点和 0 尺寸，画面被裁到左上角（第一版就是这个 bug）。
  let x0 = 1e9, y0 = 1e9, x1 = -1e9, y1 = -1e9;
  for (const frame of DATA.frames) {
    for (const o of frame) {
      const b = DATA.bounds[o.s];
      if (!b) continue;
      const m = o.m;
      for (const [px, py] of [[b[0], b[2]], [b[1], b[2]], [b[0], b[3]], [b[1], b[3]]]) {
        const X = m[0] * px + m[2] * py + m[4];
        const Y = m[1] * px + m[3] * py + m[5];
        if (X < x0) x0 = X; if (X > x1) x1 = X;
        if (Y < y0) y0 = Y; if (Y > y1) y1 = Y;
      }
    }
  }
  if (x0 > x1) { x0 = 0; y0 = 0; x1 = W; y1 = H; }
  const pad = 8;
  return [x0 - pad, y0 - pad, x1 - x0 + pad * 2, y1 - y0 + pad * 2];
}
const BOX = computeBox();

function render() {
  // 位图按 zoom 真实放大（矢量重绘，放大不糊），布局尺寸同步，
  // 由 #stage 的 safe center 负责居中/滚动。
  const w = Math.max(1, Math.ceil(BOX[2] * zoom));
  const h = Math.max(1, Math.ceil(BOX[3] * zoom));
  if (cv.width !== w || cv.height !== h) {
    cv.width = w; cv.height = h;
    cv.style.width = w + 'px';
    cv.style.height = h + 'px';
  }
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.clearRect(0, 0, w, h);
  drawArt(ctx);
  document.getElementById('seek').value = cur;
  document.getElementById('info').textContent = (cur + 1) + ' / ' + DATA.frames.length;
}
// 当前帧实际内容的包围盒（画布按全帧并集，单帧内容可能只占一小块，
// 放大后必须滚到它身上，否则一屏全是空白）
const FB = {};
function frameBox(i) {
  if (FB[i]) return FB[i];
  let x0 = 1e9, y0 = 1e9, x1 = -1e9, y1 = -1e9;
  for (const o of DATA.frames[i]) {
    const b = DATA.bounds[o.s];
    if (!b) continue;
    const m = o.m;
    for (const [px, py] of [[b[0], b[2]], [b[1], b[2]], [b[0], b[3]], [b[1], b[3]]]) {
      const X = m[0] * px + m[2] * py + m[4], Y = m[1] * px + m[3] * py + m[5];
      x0 = Math.min(x0, X); x1 = Math.max(x1, X);
      y0 = Math.min(y0, Y); y1 = Math.max(y1, Y);
    }
  }
  if (x0 > x1) { x0 = BOX[0]; y0 = BOX[1]; x1 = BOX[0] + BOX[2]; y1 = BOX[1] + BOX[3]; }
  return (FB[i] = [x0, y0, x1, y1]);
}
function focusContent() {
  const st = document.getElementById('stage');
  const fb = frameBox(cur);
  const cx = ((fb[0] + fb[2]) / 2 - BOX[0]) * zoom;
  const cy = ((fb[1] + fb[3]) / 2 - BOX[1]) * zoom;
  const cr = cv.getBoundingClientRect(), sr = st.getBoundingClientRect();
  const offX = cr.left - sr.left + st.scrollLeft;
  const offY = cr.top - sr.top + st.scrollTop;
  st.scrollLeft = Math.max(0, offX + cx - st.clientWidth / 2);
  st.scrollTop = Math.max(0, offY + cy - st.clientHeight / 2);
}
function setZoom(z) {
  zoom = Math.max(0.1, Math.min(8, z));
  document.getElementById('zoom').value = Math.round(zoom * 100);
  document.getElementById('zoomval').textContent = Math.round(zoom * 100) + '%';
  render();
  focusContent();
}
function go(i) { cur = Math.max(0, Math.min(DATA.frames.length - 1, i)); render(); }
function play() {
  if (playing) return;
  if (cur >= DATA.frames.length - 1) cur = 0;
  playing = true;
  document.getElementById('play').textContent = '⏸ 暂停';
  timer = setInterval(() => {
    if (cur >= DATA.frames.length - 1) {
      if (document.getElementById('loop').checked) { go(0); return; }
      pause(); return;
    }
    go(cur + 1);
  }, 1000 / FPS);
}
function pause() {
  playing = false; clearInterval(timer); timer = null;
  document.getElementById('play').textContent = '▶ 播放';
}
document.getElementById('play').onclick = () => playing ? pause() : play();
document.getElementById('prev').onclick = () => { pause(); go(cur - 1); };
document.getElementById('next').onclick = () => { pause(); go(cur + 1); };
document.getElementById('first').onclick = () => { pause(); go(0); };
document.getElementById('last').onclick = () => { pause(); go(DATA.frames.length - 1); };
document.getElementById('seek').oninput = e => { pause(); go(+e.target.value); };
document.getElementById('zoom').oninput = e => setZoom(+e.target.value / 100);
document.getElementById('fit').onclick = () => {
  const r = document.getElementById('stage').getBoundingClientRect();
  setZoom(Math.min(8, Math.min(r.width / BOX[2], r.height / BOX[3]) * 0.85));
};
document.getElementById('export').onclick = () => {
  const a = document.createElement('a');
  a.download = '__STEM___f' + String(cur + 1).padStart(3, '0') + '.png';
  a.href = cv.toDataURL('image/png');
  a.click();
};
document.addEventListener('keydown', e => {
  if (e.code === 'Space') { e.preventDefault(); playing ? pause() : play(); }
  else if (e.code === 'ArrowLeft') { pause(); go(cur - 1); }
  else if (e.code === 'ArrowRight') { pause(); go(cur + 1); }
  else if (e.code === 'Home') { pause(); go(0); }
  else if (e.code === 'End') { pause(); go(DATA.frames.length - 1); }
});
document.getElementById('stage').addEventListener('wheel', e => {
  e.preventDefault();
  setZoom(Math.max(0.1, Math.min(8, zoom * (e.deltaY < 0 ? 1.12 : 0.89))));
}, { passive: false });

render();
document.getElementById('fit').click();
</script>
</body>
</html>'''


def main():
    ap = argparse.ArgumentParser(description='赛尔号 SWF 逐帧播放器')
    ap.add_argument('input')
    ap.add_argument('-o', '--output')
    ap.add_argument('--sprite', type=int)
    ap.add_argument('--list', action='store_true')
    ap.add_argument('--max-frames', type=int, default=600)
    args = ap.parse_args()

    swf = SWF(args.input)

    if args.list:
        # 根时间轴的 place 信息 SWF 类没存，这里补一遍（只读，不影响其它功能）
        print(describe(swf))
        return

    sprite = pick_target(swf, args.sprite)
    if sprite is None:
        raise SystemExit('没找到可播放的 sprite')

    n = min(sprite.nframes, args.max_frames)
    frames, used = build_frames(swf, sprite, n)
    shapes = collect_shapes(swf, used)
    # bounds 用 shape 的几何边界，前端据此算统一画布
    bounds = {}
    for cid in shapes:
        info = swfshape.parse_from_swf(swf, cid)
        if info:
            bounds[cid] = info['bounds']

    data = {'w': 960, 'h': 560, 'fps': 25.0,
            'shapes': shapes, 'bounds': bounds, 'frames': frames}

    out = args.output or (os.path.splitext(os.path.basename(args.input))[0] + '.html')
    stem = os.path.splitext(os.path.basename(out))[0]
    html = (HTML.replace('__TITLE__', f'{os.path.basename(args.input)} · sprite {sprite.id} ({n} 帧)')
                .replace('__STEM__', stem)
                .replace('__DATA__', json.dumps(data, separators=(',', ':'), ensure_ascii=False))
                .replace('__TOTAL__', str(n))
                .replace('__LAST__', str(n - 1)))
    open(out, 'w', encoding='utf-8').write(html)
    print(f'已生成 {out}')
    print(f'  sprite {sprite.id}: {n} 帧 @ 25fps, '
          f'{len(shapes)} 个 shape, {len(frames)} 帧绘制列表, {len(html)/1024:.1f} KB')


if __name__ == '__main__':
    main()
