#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""set_super.py —— 冒烟用：把存档里的 superLevel 改成指定值。

同时写两处：Linux 侧 ~/.config/NonoDeskpet/save.json 与 Wine 的
drive_c/users/<user>/AppData/Roaming/NonoDeskpet/save.json。Wine 下
os.UserConfigDir() 落在后者，只改前者会出现「改了没反应」。
"""
import glob
import json
import os
import sys

paths = {os.path.expanduser('~/.config/NonoDeskpet/save.json')}
for p in glob.glob(os.path.expanduser('~/.wine/drive_c/users/*/AppData/Roaming/NonoDeskpet/save.json')):
    paths.add(p)
# WINEPREFIX 指定的 prefix 优先：冒烟脚本用 /tmp/nono-wine，只改 ~/.wine 会
# 出现「改了没反应」（2026-10-25 实测：6 形态冒烟全程 superLevel=0）。
wp = os.environ.get('WINEPREFIX')
if wp:
    for p in glob.glob(os.path.join(wp, 'drive_c/users/*/AppData/Roaming/NonoDeskpet/save.json')):
        paths.add(p)
# 目录不存在时也建一份，保证首次运行前就写入
for p in list(paths):
    os.makedirs(os.path.dirname(p), exist_ok=True)

lv = int(sys.argv[1]) if len(sys.argv) > 1 else 0
for p in paths:
    d = {}
    if os.path.exists(p):
        try:
            d = json.load(open(p))
        except Exception:
            d = {}
    # 必须用小写驼峰：Go 侧 json tag 是 superLevel/super。
    # 历史版本写过 "SuperLevel"/"Super"，Go 的 json 解码大小写不敏感，
    # 两个键同时存在时后出现的会覆盖前面的，表现为「存档写 0、程序读 9」
    # （2026-10-25 实测：6 形态冒烟全部跑成 super4）。这里顺手清掉旧键。
    for stale in ('SuperLevel', 'Super', 'Fly_Style', 'fly_style'):
        d.pop(stale, None)
    d['superLevel'] = lv
    d['super'] = lv > 0
    json.dump(d, open(p, 'w'))
    print('wrote', p, '->', lv)
