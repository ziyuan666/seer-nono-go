#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""构造只含 shape_46 的最小 sprite，用于与 Ruffle 的 min46.swf 同坐标逐像素比对。

为什么需要：Ruffle 是唯一按 Flash 规范实现的参照。原 SWF 里星芒被 sprite54/47 两层
嵌套和子循环包住，截帧对不齐；min46.swf 把它隔离成单帧后，这里用「同一组 place
矩阵 + 同一份 shape 几何」喂给我们的光栅化，两边坐标系完全相同，差异就只剩渲染实现。
"""
import json
import os
import shutil
import sys

SRC_PARTS = '/app/working/workspaces/a2ZDuB/nono_deskpet/assets/sprites/plain/action_2/parts'


def main():
    places = json.load(open('/tmp/m47.json'))
    out_dir = '/tmp/min46_sprite'
    os.makedirs(os.path.join(out_dir, 'parts'), exist_ok=True)
    shutil.copy(os.path.join(SRC_PARTS, 'shape_46.json'),
                os.path.join(out_dir, 'parts', 'shape_46.json'))

    parts = []
    for p in places:
        m = p['matrix']
        parts.append({
            'part': 'shape_46',
            'layer': 'shape_46',
            'matrix': [m['sx'], m['r0'], m['r1'], m['sy'], m['tx'] / 20.0, m['ty'] / 20.0],
            'alpha': p.get('alpha', 1.0),
        })
    # 画布取 sprite47 的 bounds 量级（屏幕 960x560 里内容集中在左上 130x100）
    sprite = {
        'format': 1,
        'canvas': {'w': 200.0, 'h': 150.0, 'anchor': [0.0, 0.0], 'ballc': [0.0, 0.0]},
        'colors': {},
        'layers': [{'name': 'shape_46', 'z': 0, 'tintable': False}],
        'directions': {'default': {'frames': [{'parts': parts}]}},
    }
    json.dump(sprite, open(os.path.join(out_dir, 'sprite.json'), 'w'), indent=1)
    print('构造 %d 个 part -> %s' % (len(parts), out_dir))
    for i, p in enumerate(parts):
        print('  #%d tx=%.2f ty=%.2f sx=%.3f' % (i, p['matrix'][4], p['matrix'][5], p['matrix'][0]))


if __name__ == '__main__':
    main()
