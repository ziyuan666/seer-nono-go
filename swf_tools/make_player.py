#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""make_player.py —— 把「一个帧序列目录」打包成单文件 HTML 播放器。

为什么这样做：
    SWF 的逐帧控制（JPEXS 只能拖根时间轴）在纯离线环境里没有理想的内核。
    但"逐帧观察 + 导出"这个需求，本质只需要「已渲染好的 N 张画面 + 一个能播放/暂停/步进/缩放
    的壳」。所以把帧序列（可由 FFDec 导出、也可由自研渲染器产出）烘进一个 HTML，
    双击即可用，零依赖、跨平台。

输入：
    <frames_dir>/1.png … <frames_dir>/N.png  （数字命名，按数值排序）

输出：
    单文件 HTML，帧以 base64 内嵌，纯色背景自动转透明（便于导出带 alpha 的 PNG）。

用法：
    python3 make_player.py /tmp/f99 -o 3_1_s99_player.html --title "3_1 · sprite99 (165帧)"
"""

import argparse
import base64
import io
import os
import re

import numpy as np
from PIL import Image

TEMPLATE = '''<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>__TITLE__</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin:0; background:#1b1e24; color:#d8dee9; font:13px/1.5 -apple-system,"Segoe UI",sans-serif;
         display:flex; flex-direction:column; height:100vh; overflow:hidden; }
  #stage { flex:1; display:flex; align-items:center; justify-content:center; overflow:hidden;
           background:
             linear-gradient(45deg,#23262d 25%,transparent 25%) 0 0/24px 24px,
             linear-gradient(-45deg,#23262d 25%,transparent 25%) 0 12px/24px 24px,
             linear-gradient(45deg,transparent 75%,#23262d 75%) 12px -12px/24px 24px,
             linear-gradient(-45deg,transparent 75%,#23262d 75%) -12px 0/24px 24px,
             #1b1e24; cursor:grab; }
  #stage.dragging { cursor:grabbing; }
  canvas { image-rendering:pixelated; transform-origin:center center; }
  #bar { display:flex; gap:10px; align-items:center; padding:10px 14px; background:#22262e;
         border-top:1px solid #2f3540; flex-wrap:wrap; }
  button { background:#2f3540; color:#d8dee9; border:1px solid #3c4450; border-radius:6px;
           padding:5px 11px; cursor:pointer; font-size:13px; }
  button:hover { background:#3a4150; }
  button:active { transform:translateY(1px); }
  button.primary { background:#4c7899; border-color:#5d8dad; color:#fff; }
  input[type=range] { flex:1; min-width:180px; accent-color:#4c7899; }
  #info { font-variant-numeric:tabular-nums; color:#9aa5b1; min-width:150px; text-align:right; }
  #zoom { width:150px; }
  label { color:#9aa5b1; display:flex; align-items:center; gap:6px; }
  .sep { width:1px; height:20px; background:#3c4450; }
</style>
</head>
<body>
<div id="stage"><canvas id="cv"></canvas></div>
<div id="bar">
  <button id="first" title="首帧 (Home)">⏮</button>
  <button id="prev" title="上一帧 (←)">◀</button>
  <button id="play" class="primary" title="播放/暂停 (空格)">▶ 播放</button>
  <button id="next" title="下一帧 (→)">▶</button>
  <button id="last" title="末帧 (End)">⏭</button>
  <div class="sep"></div>
  <input type="range" id="seek" min="0" max="__LAST__" value="0">
  <span id="info">0 / __TOTAL__</span>
  <div class="sep"></div>
  <label>缩放 <input type="range" id="zoom" min="25" max="800" value="100"></label>
  <span id="zoomval" style="min-width:48px;text-align:right">100%</span>
  <button id="fit">适应</button>
  <button id="export">导出 PNG</button>
</div>
<script>
const FRAMES = __FRAMES__;
const W = __W__, H = __H__;
const cv = document.getElementById('cv');
cv.width = W; cv.height = H;
const ctx = cv.getContext('2d');
// 必须等图片解码完成再画：data URI 不是同步可用的，onload 前 drawImage 会画空白
const imgs = FRAMES.map((src, i) => {
  const im = new Image();
  im.onload = () => { if (i === cur) draw(); };
  im.src = src;
  return im;
});
let cur = 0, playing = false, zoom = 1, timer = null, fps = __FPS__;

function draw() {
  ctx.clearRect(0, 0, W, H);
  const im = imgs[cur];
  if (im && im.complete) ctx.drawImage(im, 0, 0, W, H);
  document.getElementById('seek').value = cur;
  document.getElementById('info').textContent = (cur + 1) + ' / ' + FRAMES.length;
}
function setZoom(z) {
  zoom = z;
  cv.style.transform = 'scale(' + zoom + ')';
  document.getElementById('zoom').value = Math.round(zoom * 100);
  document.getElementById('zoomval').textContent = Math.round(zoom * 100) + '%';
}
function go(i) {
  cur = Math.max(0, Math.min(FRAMES.length - 1, i));
  draw();
}
function play() {
  if (playing) return;
  if (cur >= FRAMES.length - 1) cur = 0;
  playing = true;
  document.getElementById('play').textContent = '⏸ 暂停';
  timer = setInterval(() => {
    if (cur >= FRAMES.length - 1) { pause(); return; }
    go(cur + 1);
  }, 1000 / fps);
}
function pause() {
  playing = false;
  clearInterval(timer); timer = null;
  document.getElementById('play').textContent = '▶ 播放';
}
document.getElementById('play').onclick = () => playing ? pause() : play();
document.getElementById('prev').onclick = () => { pause(); go(cur - 1); };
document.getElementById('next').onclick = () => { pause(); go(cur + 1); };
document.getElementById('first').onclick = () => { pause(); go(0); };
document.getElementById('last').onclick = () => { pause(); go(FRAMES.length - 1); };
document.getElementById('seek').oninput = e => { pause(); go(+e.target.value); };
document.getElementById('zoom').oninput = e => setZoom(+e.target.value / 100);
document.getElementById('fit').onclick = () => {
  const r = document.getElementById('stage').getBoundingClientRect();
  // 上限 4x：像素放大再多只会更糊，留 0.8 边距免得贴边
  setZoom(Math.min(4, Math.min(r.width / W, r.height / H) * 0.8));
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
  else if (e.code === 'End') { pause(); go(FRAMES.length - 1); }
});
// 滚轮缩放
document.getElementById('stage').addEventListener('wheel', e => {
  e.preventDefault();
  setZoom(Math.max(0.1, Math.min(8, zoom * (e.deltaY < 0 ? 1.12 : 0.89))));
}, { passive: false });
// 拖动平移
let drag = null;
const stage = document.getElementById('stage');
stage.addEventListener('mousedown', e => { drag = { x: e.clientX, y: e.clientY }; stage.classList.add('dragging'); });
window.addEventListener('mouseup', () => { drag = null; stage.classList.remove('dragging'); });
window.addEventListener('mousemove', e => {
  if (!drag) return;
  stage.scrollLeft -= e.clientX - drag.x;
  stage.scrollTop -= e.clientY - drag.y;
  drag = { x: e.clientX, y: e.clientY };
});
setZoom(1);
draw();
document.getElementById('fit').click();
</script>
</body>
</html>
'''


def load_frames(frames_dir):
    pat = re.compile(r'^(\d+)\.png$', re.I)
    items = []
    for name in os.listdir(frames_dir):
        m = pat.match(name)
        if m:
            items.append((int(m.group(1)), os.path.join(frames_dir, name)))
    items.sort()
    if not items:
        raise SystemExit(f'{frames_dir} 里没有 1.png 这种数字命名的 PNG')
    return [p for _, p in items]


def to_transparent(img):
    """FFDec 导出的是不透明纯色背景，转成 alpha 便于叠在棋盘格上/导出透明 PNG。"""
    arr = np.asarray(img.convert('RGBA')).astype(np.int16)
    bg = arr[8, 8, :3]
    diff = np.abs(arr[:, :, :3] - bg).sum(axis=2)
    mask = diff < 18
    arr[mask, 3] = 0
    return Image.fromarray(arr.astype(np.uint8), 'RGBA')


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('frames_dir')
    ap.add_argument('-o', '--output', required=True)
    ap.add_argument('--title', default='SWF 帧播放器')
    ap.add_argument('--fps', type=float, default=25.0)
    args = ap.parse_args()

    paths = load_frames(args.frames_dir)
    imgs = [to_transparent(Image.open(p)) for p in paths]

    # 统一裁剪框：所有帧的内容并集，避免播放时画面抖动
    union = None
    for im in imgs:
        bbox = im.getbbox()
        if not bbox:
            continue
        union = bbox if union is None else (min(union[0], bbox[0]), min(union[1], bbox[1]),
                                            max(union[2], bbox[2]), max(union[3], bbox[3]))
    if union is None:
        raise SystemExit('所有帧都是空的')
    pad = 12
    box = (max(0, union[0] - pad), max(0, union[1] - pad),
           min(imgs[0].width, union[2] + pad), min(imgs[0].height, union[3] + pad))

    data = []
    for im in imgs:
        buf = io.BytesIO()
        im.crop(box).save(buf, 'PNG', optimize=True)
        data.append('data:image/png;base64,' + base64.b64encode(buf.getvalue()).decode())

    stem = os.path.splitext(os.path.basename(args.output))[0]
    html = (TEMPLATE
            .replace('__TITLE__', args.title)
            .replace('__STEM__', stem)
            .replace('__FRAMES__', '[' + ','.join(f'"{d}"' for d in data) + ']')
            .replace('__W__', str(box[2] - box[0]))
            .replace('__H__', str(box[3] - box[1]))
            .replace('__TOTAL__', str(len(data)))
            .replace('__LAST__', str(len(data) - 1))
            .replace('__FPS__', str(args.fps)))
    open(args.output, 'w', encoding='utf-8').write(html)
    print(f'已生成 {args.output}')
    print(f'  {len(data)} 帧, 画布 {box[2]-box[0]}x{box[3]-box[1]}, 文件 {len(html)/1024:.1f} KB')


if __name__ == '__main__':
    main()
