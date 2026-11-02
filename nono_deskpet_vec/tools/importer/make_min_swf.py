#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""从现有 dump 的 XML 裁剪出「只显示指定 shape 单帧」的最小 SWF。

用途：把「渐变坐标语义」这个争议点隔离出来单独验证。
原 SWF 里星芒藏在 sprite54 → sprite47 → shape46 三层嵌套里，子 sprite 有自己的
循环，Ruffle 外部截帧对不齐；而且不做 AS3 时也只能靠时序碰运气。
这里保留原 XML 的完整骨架（<swf>/<displayRect>/<tags> 以及 shape 自己的子元素），
只做「删掉无关 tag + 改根 tags 的 place 列表」，避免手工重建结构踩 FFDec
反射映射的坑（rgb / shapeBounds / item 这些字段必须有正确层级）。

用法：python3 make_min_swf.py <源XML> <输出XML> <shapeId> <引用spriteId> <帧号>
"""
import sys
import xml.etree.ElementTree as ET
from copy import deepcopy


def sprite_places(root, sprite_id, frame_no):
    """取某个 sprite 在第 frame_no 帧（0 起）时 display list 上的 place 列表。"""
    for e in root.iter('item'):
        if e.get('type') == 'DefineSpriteTag' and e.get('spriteId') == str(sprite_id):
            sub = [c for c in e if c.tag == 'subTags'][0]
            display = {}
            f = 0
            for it in sub:
                ty = it.get('type') or ''
                if ty == 'ShowFrameTag':
                    if f == frame_no:
                        return list(display.values())
                    f += 1
                    continue
                if f > frame_no:
                    break
                if 'PlaceObject' in ty:
                    d = it.get('depth')
                    if it.get('characterId') or it.get('cid'):
                        display[d] = deepcopy(it)
                elif ty == 'RemoveObject2Tag':
                    display.pop(it.get('depth'), None)
            if f == frame_no:
                return list(display.values())
    return []


def main():
    src, dst, shape_id, sprite_id, frame_no = sys.argv[1:6]
    frame_no = int(frame_no)
    tree = ET.parse(src)
    root = tree.getroot()

    shape_item = None
    for e in root.iter('item'):
        ty = e.get('type') or ''
        if ty.startswith('DefineShape') and e.get('shapeId') == str(shape_id):
            shape_item = e
            break
    if shape_item is None:
        print('未找到 shape', shape_id)
        return 1

    places = sprite_places(root, sprite_id, frame_no)
    keep = [p for p in places
            if (p.get('characterId') or p.get('cid')) == str(shape_id)]
    print('sprite %s 第 %d 帧 %d 个 place，指向 shape %s 的 %d 个'
          % (sprite_id, frame_no, len(places), shape_id, len(keep)))
    if not keep:
        return 1

    # 1) 根 <tags> 只留文件属性/背景色 + 该 shape + 新 place + ShowFrame
    tags = root.find('.//tags')
    keep_types_global = ('FileAttributesTag', 'SetBackgroundColorTag', 'MetadataTag')
    new_tags = []
    for it in list(tags):
        if (it.get('type') or '') in keep_types_global:
            new_tags.append(it)
    for it in list(tags):
        tags.remove(it)
    for it in new_tags:
        tags.append(it)
    tags.append(deepcopy(shape_item))
    for i, p in enumerate(keep, start=1):
        q = deepcopy(p)
        q.set('depth', str(i))
        if 'cid' in q.attrib:
            q.set('cid', str(shape_id))
        if 'characterId' in q.attrib:
            q.set('characterId', str(shape_id))
        tags.append(q)
    ET.SubElement(tags, 'item', {'type': 'ShowFrameTag'})
    root.set('frameCount', '1')

    tree.write(dst, encoding='utf-8', xml_declaration=True)
    print('已写', dst)
    return 0


if __name__ == '__main__':
    sys.exit(main())
