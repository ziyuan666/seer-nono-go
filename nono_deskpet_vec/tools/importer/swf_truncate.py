#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""把 dump 出来的 SWF XML 的某个 DefineSprite 截断到第 N 帧，用于定格渲染。

目的：Ruffle 播放 SWF 时帧号无法外部控制，而逐像素对比必须在同一帧上做。
把 root sprite 截到目标帧并保证循环回到该帧，Ruffle 无论何时截图都是这一帧。

注意：只截断 subTags（删掉第 N 帧之后的所有标签）并同步 frameCount，
不动 shape / fillStyle / 其它 sprite，避免影响渲染结果本身。
"""
import sys
import xml.etree.ElementTree as ET


def truncate(xml_in, xml_out, sprite_id, keep_frames, hold_frames=0):
    tree = ET.parse(xml_in)
    root = tree.getroot()
    hit = False
    for e in root.iter('item'):
        if e.get('type') != 'DefineSpriteTag' or e.get('spriteId') != str(sprite_id):
            continue
        sub = [c for c in e if c.tag == 'subTags']
        if not sub:
            continue
        sub = sub[0]
        last = None
        seen = 0
        drop = False
        removed = 0
        for it in list(sub):
            if drop:
                sub.remove(it)
                removed += 1
                continue
            if it.get('type') == 'ShowFrameTag':
                seen += 1
                last = it
                if seen >= keep_frames:
                    drop = True
        # 追加空 ShowFrame：display list 不变，画面就停在第 keep_frames 帧。
        # 这样 Ruffle 播放时该帧会停留 hold_frames 帧的时间，外部截图必然命中。
        if last is not None:
            idx = list(sub).index(last)
            for k in range(hold_frames):
                blank = ET.Element('item', {'type': 'ShowFrameTag'})
                sub.insert(idx + 1 + k, blank)
        e.set('frameCount', str(keep_frames + hold_frames))
        hit = True
        print('sprite %s 截断到 %d 帧（删除后续 %d 个标签），追加空帧 %d 个，frameCount=%d'
              % (sprite_id, keep_frames, removed, hold_frames, keep_frames + hold_frames))
        break
    if not hit:
        print('未找到 sprite %s' % sprite_id)
        return 1
    tree.write(xml_out, encoding='utf-8', xml_declaration=True)
    print('已写', xml_out)
    return 0


if __name__ == '__main__':
    hold = int(sys.argv[5]) if len(sys.argv) > 5 else 0
    sys.exit(truncate(sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), hold))
