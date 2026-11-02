#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""smoke_fly.py —— 4 种飞行载具 style 逐个冒烟（4x 改版后核对尺寸/落脚/是否出屏）。

为什么要有它：飞行器窗口尺寸不走 ball_anchor，而是按「原版整帧高 × Save.Scale」
独立计算；4x 之后 flyH 从 146/165/149/170 变成 4 倍，任何一处换算写错都会
让窗口飞出屏幕（第一版 y0 -= flyH 就是这么挂的，实机只剩尾焰灰点）。

用法：WINEPREFIX=/tmp/nono-wine DISPLAY=:98 python3 tools/smoke_fly.py [输出目录]
"""
import json
import os
import subprocess
import sys
import time

OUT = sys.argv[1] if len(sys.argv) > 1 else '/tmp/flys'
os.makedirs(OUT, exist_ok=True)
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LOG = os.path.join(HERE, 'nono_debug.log')
SAVE = os.path.join(os.environ['WINEPREFIX'],
                    'drive_c/users/root/AppData/Roaming/NonoDeskpet/save.json')


def set_style(style):
    d = json.load(open(SAVE, encoding='utf-8'))
    d['flyStyle'] = style
    json.dump(d, open(SAVE, 'w', encoding='utf-8'), ensure_ascii=False, indent=1)


rows = []
for st in (1, 2, 3, 4):
    set_style(st)
    if os.path.exists(LOG):
        os.remove(LOG)
    logf = open(os.path.join(OUT, 'run_%d.log' % st), 'w', encoding='utf-8')
    env = dict(os.environ, NONO_LOG='1', NONO_AUTOFLY='1')
    p = subprocess.Popen(['timeout', '22', 'wine', os.path.join(HERE, 'nono_deskpet.exe')],
                         stdout=logf, stderr=subprocess.STDOUT, env=env, cwd=HERE)
    time.sleep(9)
    try:
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
        box = (max(0, xs.min() - 6), max(0, ys.min() - 6),
               min(im.width, xs.max() + 7), min(im.height, ys.max() + 7))
        im.crop(box).save(os.path.join(OUT, 'fly_style%d.png' % st))
        size = (int(xs.max() - xs.min() + 1), int(ys.max() - ys.min() + 1))
        pos = (int(xs.min()), int(ys.min()))
    except Exception as ex:  # noqa: BLE001
        size, pos = None, None
        print('截图失败 style=%d: %r' % (st, ex))
    p.wait()
    logf.close()
    # 记录窗口尺寸日志：resize w=.. 出现在飞行切换之后
    win = None
    try:
        txt = open(os.path.join(OUT, 'run_%d.log' % st), encoding='utf-8', errors='replace').read()
        print('  style%d stdout 末行: %s' % (st, txt.strip().split('\n')[-1][:70] if txt.strip() else '-'))
    except Exception:  # noqa: BLE001
        pass
    rows.append({'style': st, 'content': size, 'pos': pos, 'win': win})
    print('style=%d content=%s at=%s' % (st, size, pos))
    time.sleep(1)

print('\n汇总:', rows)
