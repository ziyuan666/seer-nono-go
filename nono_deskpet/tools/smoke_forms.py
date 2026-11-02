#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""smoke_forms.py —— 6 形态冒烟：逐个切 superLevel，截图 + 记录球心屏幕坐标。

为什么要有它：形态切换「球心屏幕位置必须不动」是硬要求，而球心由
ball_anchor.json 的 anchor 与各形态 canvas.ballc 共同决定，任何一边口径写错
（例如锚点按 1x 写入、画布按 4x 渲染）都会让球心偏移几十像素，只能靠实机
屏幕坐标来验，离线算不出来。

用法（必须显式带 WINEPREFIX，否则写到 ~/.wine 的旧存档，改了没反应）：
    WINEPREFIX=/tmp/nono-wine DISPLAY=:98 python3 tools/smoke_forms.py [输出目录]
"""
import json
import os
import re
import subprocess
import sys
import time

OUT = sys.argv[1] if len(sys.argv) > 1 else '/tmp/forms'
os.makedirs(OUT, exist_ok=True)
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LOG = os.path.join(HERE, 'nono_debug.log')


def grab(tag):
    """截全屏，裁出内容区域（跳过顶部 60px 桌面面板区）。"""
    import mss
    import numpy as np
    from PIL import Image
    with mss.MSS() as sct:
        shot = sct.grab(sct.monitors[1])
    im = Image.frombytes('RGB', shot.size, shot.rgb)
    a = np.array(im)
    mask = a.sum(axis=2) > 40
    ys, xs = np.nonzero(mask)
    keep = ys > 60
    ys, xs = ys[keep], xs[keep]
    if len(xs) == 0:
        return None
    box = (max(0, xs.min() - 5), max(0, ys.min() - 5),
           min(im.width, xs.max() + 6), min(im.height, ys.max() + 6))
    im.crop(box).save(os.path.join(OUT, 'form_%s.png' % tag))
    return (int(xs.max() - xs.min() + 1), int(ys.max() - ys.min() + 1))


# 采样值必须按服务端映射取：1-3→super1, 4-6→super2, 7-8→super3, 9-11→super4,
# 12→super5（form.go:superLevelToForm）。直接 0..5 只会跑到 plain/super1/super2 三种。
LEVELS = [0, 1, 4, 7, 9, 12]
rows = []
for lv in LEVELS:
    subprocess.run(['python3', os.path.join(HERE, 'tools', 'set_super.py'), str(lv)],
                   capture_output=True)
    if os.path.exists(LOG):
        os.remove(LOG)
    out = open(os.path.join(OUT, 'run_%d.log' % lv), 'w', encoding='utf-8')
    env = dict(os.environ, NONO_LOG='1')
    p = subprocess.Popen(['timeout', '17', 'wine', os.path.join(HERE, 'nono_deskpet.exe')],
                         stdout=out, stderr=subprocess.STDOUT, env=env, cwd=HERE)
    time.sleep(11)
    size = grab(str(lv))
    p.wait()
    out.close()
    # 日志每轮覆盖，必须先落盘再解析，否则只能看到最后一轮（踩过）
    try:
        import shutil
        shutil.copyfile(LOG, os.path.join(OUT, 'run_%d.debug.log' % lv))
    except FileNotFoundError:
        pass
    center, form = None, None
    try:
        txt = open(LOG, encoding='utf-8', errors='replace').read()
        m = re.findall(r'ball center form=(\S+) screen=(-?\d+),(-?\d+)', txt)
        if m:
            form = m[-1][0]
            center = (int(m[-1][1]), int(m[-1][2]))
    except FileNotFoundError:
        pass
    rows.append({'level': lv, 'form': form, 'center': center, 'content': size})
    print('superLevel=%d form=%s center=%s content=%s' % (lv, form, center, size))
    time.sleep(2)

ok = [r for r in rows if r['center']]
if ok:
    xs = [r['center'][0] for r in ok]
    ys = [r['center'][1] for r in ok]
    print('\n球心屏幕坐标跨度: x %d..%d (Δ%d)  y %d..%d (Δ%d)'
          % (min(xs), max(xs), max(xs) - min(xs), min(ys), max(ys), max(ys) - min(ys)))
json.dump(rows, open(os.path.join(OUT, 'summary.json'), 'w'), ensure_ascii=False, indent=1)
