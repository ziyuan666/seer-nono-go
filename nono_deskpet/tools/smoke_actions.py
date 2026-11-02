#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""smoke_actions.py —— 6 形态 × 动作 1~7 全量冒烟。

为什么要有它：动作是按 eid 逐段实现的，任何一段出问题（帧数不符、渲染报错、
绘制不进）都必须单独暴露，不能靠「跑一个 action_7 看起来没事」蒙混。

判据（每段）：
  - 有资源目录且进程没崩 → 必须出现 `anim place kind=1 id=N`
  - 有资源且播完 → `idle() 清动作` 的 idx 必须等于 total（全帧播完）
  - 无资源目录 → 正常走表情 fallback，记「无」

踩过的坑（务必保留）：
  1. `set_super.py` 按 WINEPREFIX 定位存档，必须把 WINEPREFIX 传给它；
     只传给 wine 会让程序读到 superLevel=1，全程停在 super1，
     super1/action_7 的 263 帧被误当成 super5 的 248 帧。
  2. 等待时间要 ≥ 加载(3.6s) + 帧数/24；300 帧动作需 12.5s+。
  3. 冷启动（pkill wine 之后第一次）比热启动慢 2~3s。

用法：
    WINEPREFIX=/tmp/nono-wine DISPLAY=:98 python3 tools/smoke_actions.py
"""
import os
import re
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LOG = os.path.join(HERE, 'nono_debug.log')
SPRITES = os.path.join(HERE, 'assets', 'sprites')
LEVELS = [0, 1, 4, 7, 9, 12]
NAMES = {0: 'plain', 1: 'super1', 4: 'super2', 7: 'super3', 9: 'super4', 12: 'super5'}
ACTIONS = [1, 2, 3, 4, 5, 6, 7]


def has_action(form, act):
    return os.path.isdir(os.path.join(SPRITES, form, 'action_%d' % act))


def run_one(level, act):
    form = NAMES[level]
    if not has_action(form, act):
        return '无资源', 'skip'
    subprocess.run(['pkill', '-x', 'wine'], capture_output=True)
    time.sleep(1.5)
    subprocess.run([sys.executable, os.path.join(HERE, 'tools', 'set_super.py'), str(level)],
                   capture_output=True, env=dict(os.environ))
    if os.path.exists(LOG):
        os.remove(LOG)
    env = dict(os.environ, NONO_LOG='1', NONO_TEST_ACTION=str(act))
    p = subprocess.Popen(['wine', 'nono_deskpet.exe'], cwd=HERE, env=env,
                         stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    # 13s = 加载 3.6s + 300 帧/24fps 12.5s 的余量（最长 super4/action_4）
    time.sleep(13.0)
    p.terminate()
    try:
        out = p.communicate(timeout=6)[0].decode('utf-8', 'ignore')
    except subprocess.TimeoutExpired:
        p.kill()
        out = ''
    subprocess.run(['pkill', '-x', 'wine'], capture_output=True)
    txt = open(LOG, encoding='utf-8', errors='ignore').read() if os.path.exists(LOG) else ''
    if 'panic' in out.lower() or 'fatal error' in out.lower():
        return '崩溃', 'crash'
    if ('anim place kind=1 id=%d' % act) not in txt:
        # 再看有没有误判：可能形态没切成功
        m = re.search(r'form loaded: (\S+)', txt)
        got = m.group(1) if m else '?'
        return '未进绘制(实际形态=%s)' % got, 'bad'
    m = re.search(r'idle\(\) 清动作 kind=1 id=%d idx=(\d+)/(\d+)' % act, txt)
    if m:
        a, b = int(m.group(1)), int(m.group(2))
        return ('%d/%d' % (a, b)), ('ok' if a == b else 'partial')
    m2 = re.search(r'playAction %d: NFrames=(\d+)' % act, txt)
    n = m2.group(1) if m2 else '?'
    return '播放中(/%s)' % n, 'partial'


def main():
    rows, bad = [], []
    for lv in LEVELS:
        line = []
        for act in ACTIONS:
            tag, kind = run_one(lv, act)
            line.append('%d:%s' % (act, tag))
            print('%-7s action%d -> %s' % (NAMES[lv], act, tag), flush=True)
            if kind in ('bad', 'crash'):
                bad.append((NAMES[lv], act, tag))
        rows.append((NAMES[lv], line))
    print('\n=== 汇总 ===')
    for name, line in rows:
        print('%-7s %s' % (name, '  '.join(line)))
    print('\n异常 %d 段' % len(bad))
    for n, a, t in bad:
        print('  %s action%d: %s' % (n, a, t))


if __name__ == '__main__':
    main()
