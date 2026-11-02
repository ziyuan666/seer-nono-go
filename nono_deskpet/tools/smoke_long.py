#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""smoke_long.py —— 长动作（>240 帧）逐段确认全帧播完。

为什么单独跑：smoke_actions.py 统一等 13s，够 300 帧以内的动作，但
super5/action_4 有 315 帧（13.1s @24fps），加上加载 3.6s 就会「播放中」。
这里给 24s，只跑那几段，确认 idx == total。

用法：
    WINEPREFIX=/tmp/nono-wine DISPLAY=:98 python3 tools/smoke_long.py
"""
import os
import re
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LOG = os.path.join(HERE, 'nono_debug.log')
NAMED = {1: 'super1', 4: 'super2', 7: 'super3', 9: 'super4', 12: 'super5'}
CASES = [(1, 7), (4, 7), (7, 5), (7, 7), (9, 4), (9, 5), (9, 7), (12, 4), (12, 7)]


def run(level, act):
    subprocess.run(['pkill', '-x', 'wine'], capture_output=True)
    time.sleep(1.5)
    subprocess.run([sys.executable, os.path.join(HERE, 'tools', 'set_super.py'), str(level)],
                   capture_output=True, env=dict(os.environ))
    if os.path.exists(LOG):
        os.remove(LOG)
    env = dict(os.environ, NONO_LOG='1', NONO_TEST_ACTION=str(act))
    p = subprocess.Popen(['wine', 'nono_deskpet.exe'], cwd=HERE, env=env,
                         stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    time.sleep(24.0)
    p.terminate()
    try:
        out = p.communicate(timeout=6)[0].decode('utf-8', 'ignore')
    except subprocess.TimeoutExpired:
        p.kill()
        out = ''
    subprocess.run(['pkill', '-x', 'wine'], capture_output=True)
    txt = open(LOG, encoding='utf-8', errors='ignore').read() if os.path.exists(LOG) else ''
    crash = 'panic' in out.lower() or 'fatal error' in out.lower()
    m = re.search(r'idle\(\) 清动作 kind=1 id=%d idx=(\d+)/(\d+)' % act, txt)
    f = re.search(r'playAction %d: NFrames=(\d+)' % act, txt)
    nf = f.group(1) if f else '?'
    if crash:
        return '崩溃'
    if m:
        return '%s/%s %s' % (m.group(1), m.group(2), '✓' if m.group(1) == m.group(2) else '✗')
    miss = re.search(r'anim miss.*?共 (\d+) 次', txt)
    extra = (' miss=%s' % miss.group(1)) if miss else ''
    return '未播完(/%s)%s' % (nf, extra)


def main():
    bad = 0
    for lv, act in CASES:
        r = run(lv, act)
        print('%-7s action%-2d -> %s' % (NAMED[lv], act, r), flush=True)
        if '✗' in r or '崩溃' in r or '未播完' in r:
            bad += 1
    print('\n未通过 %d 段' % bad)


if __name__ == '__main__':
    main()
