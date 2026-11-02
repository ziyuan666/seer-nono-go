#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""smoke_exps.py —— 逐个表情冒烟截图（4x 改版后核对落位与画质）。

表情在原版里由计时随机触发，冒烟无法确定命中时机，所以用 main.go 的
NONO_TEST_EXP 调试钩子在 tick 60 播指定表情。

用法：WINEPREFIX=/tmp/nono-wine DISPLAY=:98 python3 tools/smoke_exps.py [输出目录]
"""
import os
import subprocess
import sys
import time

OUT = sys.argv[1] if len(sys.argv) > 1 else '/tmp/exps'
os.makedirs(OUT, exist_ok=True)
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LOG = os.path.join(HERE, 'nono_debug.log')

rows = []
for e in (1, 2, 3, 4):
    if os.path.exists(LOG):
        os.remove(LOG)
    logf = open(os.path.join(OUT, 'run_%d.log' % e), 'w', encoding='utf-8')
    env = dict(os.environ, NONO_LOG='1', NONO_TEST_EXP=str(e))
    p = subprocess.Popen(['timeout', '15', 'wine', os.path.join(HERE, 'nono_deskpet.exe')],
                         stdout=logf, stderr=subprocess.STDOUT, env=env, cwd=HERE)
    # tick 60 ≈ 启动后 2.5s 触发，表情本身 60~70 帧 ≈ 2.5~3s。7s 才截会正好
    # 落在播完之后（实测 exp1/2/4 三张逐像素相同）。改在 3.4s 起连截两张。
    time.sleep(3.4)
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
        im.crop(box).save(os.path.join(OUT, 'exp_%d.png' % e))
        size = (int(xs.max() - xs.min() + 1), int(ys.max() - ys.min() + 1))
        pos = (int(xs.min()), int(ys.min()))
    except Exception as ex:  # noqa: BLE001
        size, pos = None, None
        print('截图失败 exp=%d: %r' % (e, ex))
    time.sleep(0.9)
    try:
        import mss
        import numpy as np
        from PIL import Image
        with mss.MSS() as sct:
            shot = sct.grab(sct.monitors[1])
        im2 = Image.frombytes('RGB', shot.size, shot.rgb)
        a2 = np.array(im2)
        m2 = a2.sum(axis=2) > 40
        ys2, xs2 = np.nonzero(m2)
        keep2 = ys2 > 60
        ys2, xs2 = ys2[keep2], xs2[keep2]
        box2 = (max(0, xs2.min() - 6), max(0, ys2.min() - 6),
                min(im2.width, xs2.max() + 7), min(im2.height, ys2.max() + 7))
        im2.crop(box2).save(os.path.join(OUT, 'exp_%d_b.png' % e))
    except Exception as ex:  # noqa: BLE001
        print('第二张截图失败 exp=%d: %r' % (e, ex))
    p.wait()
    logf.close()
    rows.append({'exp': e, 'content': size, 'pos': pos})
    print('exp_%d content=%s at=%s' % (e, size, pos))
    time.sleep(1)

print('\n汇总:', rows)
