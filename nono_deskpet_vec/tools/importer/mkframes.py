#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""把 SWF 的根时间轴拉长到指定帧数。

背景：NONO 的 action/exp SWF 根时间轴只有 1 帧，真正的动画在一个被 place
的 DefineSprite 里。FFDec 的 `-export frame` 只导根时间轴的帧，所以直接导出
只能拿到 1 张图。

做法：在根级最后一个 `ShowFrameTag` 之后补 (target - 现有) 个空 ShowFrameTag，
并同步改 `frameCount`。子 sprite 是普通 MovieClip，默认自动播放，根每推进一帧
它就前进一帧，于是第 N 张导出图就是该 sprite 的第 N 帧。

不碰任何 shape / sprite 定义，只改根时间轴长度，所以几何数据零改动。
"""
import re
import sys

# 根级 ShowFrameTag 缩进 4 空格；DefineSprite 内部的是 8 空格，靠这个区分，
# 不能用「第一个/最后一个 ShowFrameTag」这类位置假设。
ROOT_SF = '    <item type="ShowFrameTag" forceWriteAsLong="false"/>\n'


def main():
    if len(sys.argv) != 4:
        print('用法: mkframes.py <in.xml> <out.xml> <targetFrames>')
        return 2
    src, dst, target = sys.argv[1], sys.argv[2], int(sys.argv[3])

    s = open(src, encoding='utf-8').read()
    m = re.search(r'frameCount="(\d+)"', s)
    if not m:
        print('!! 找不到 frameCount')
        return 1
    cur = int(m.group(1))
    if target <= cur:
        print('现有 %d 帧 >= 目标 %d，无需补帧' % (cur, target))
        open(dst, 'w', encoding='utf-8').write(s)
        return 0

    idx = s.rfind(ROOT_SF)
    if idx < 0:
        print('!! 找不到根级 ShowFrameTag（缩进 4 空格）')
        return 1

    add = target - cur
    s = s[:idx + len(ROOT_SF)] + ROOT_SF * add + s[idx + len(ROOT_SF):]
    s = s.replace('frameCount="%d"' % cur, 'frameCount="%d"' % target, 1)
    open(dst, 'w', encoding='utf-8').write(s)
    print('补帧 %d -> %d（新增 %d 个 ShowFrameTag）' % (cur, target, add))
    return 0


if __name__ == '__main__':
    sys.exit(main())
