#!/usr/bin/env python3
"""生成「基本资料」面板的两张底图变体（去掉要由代码自己画的部分）。

  1. 扳手（修改昵称）和 √ 同时叠在上面 → 底图 info.png 里已经把扳手烘焙进去了，
     代码又叠了一层按钮贴图。要把底图上的扳手擦掉，图标全部由代码按状态画。
  2. 超能等级不是数字，是「超能等级: 图标(球+N翼)」整行，等级由 SWF 精灵
     DefineSprite_118 的第 N 帧决定（ps0Mc.gotoAndStop(superLevel)）。
     底图那行烘焙的是固定等级，必须擦掉后按当前等级重画。
  3. 左侧 NONO 头像要按当前形态换（faceMc: 普通 norMc / 超能 superMc）。
     超能形态要擦掉底图里的普通头像，改画 DefineSprite_66 的第 superStage 帧。

产物：
  assets/ui/info.png        普通形态用（擦扳手 + 擦超能等级行）
  assets/ui/info_super.png  超能形态用（再擦掉左侧普通头像）

坐标全部是 4x 底图坐标（面板原生坐标 × 4）。
用法：python3 tools/mk_info_ui.py
"""
import os
import shutil

from PIL import Image

UI = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'assets', 'ui')
SRC = os.path.join(UI, 'info.png')

# 扳手：面板 (249.6,42.1) 15x19 → 4x (998,168)-(1058,244)。
# 上半截在面板灰底上、下半截在白底上，中间横着昵称框蓝边框，分两段处理。
WRENCH_TOP = (994, 162, 1064, 170)
WRENCH_BODY = (994, 170, 1064, 252)
# 超能等级图标（球+N 翼）：面板 x 163..200 / y 68..90 → 4x (652,272)-(800,360)
#
# 只擦「图标」这一块，"超能等级:"四个字必须留在底图上。
# 字被放大糊掉了——文字是底图里的高清矢量，不该由贴图承担。
LVICON = (652, 272, 800, 360)
# 左侧头像：4x x 60..396 / y 148..596。
# y=145 那条深灰线是面板内壁轮廓线，必须保留；头像底下的背景是纯 #E6E6E6。
FACE = (60, 148, 396, 616)


def is_blue(p):
    r, g, b, a = p
    return b > 180 and r < 140 and (b - r) > 60


def fill_color(px, box, color):
    x0, y0, x1, y1 = box
    for y in range(y0, y1):
        for x in range(x0, x1):
            px[x, y] = color


def fill_white(px, box, keep_blue_lines=False):
    """白色填充。keep_blue_lines=True 时跳过「区域左右外侧是蓝色」的行，
    避免把昵称输入框的蓝色框线擦断（扳手正好压在框线旁边）。"""
    x0, y0, x1, y1 = box
    for y in range(y0, y1):
        for x in range(x0, x1):
            # 框线行只擦非蓝像素，保住昵称框的蓝色边框
            if keep_blue_lines and is_blue(px[x, y]):
                continue
            px[x, y] = (255, 255, 255, 255)


def restore_hlines(px, box, probe_x, x_end=None):
    """擦白后按区域外的水平蓝线重建框线（把被图标压过的框线补回来）。"""
    x0, y0, x1, y1 = box
    x_end = x1 if x_end is None else x_end
    y = y0
    while y < y1:
        if not is_blue(px[probe_x, y]):
            y += 1
            continue
        ya = y
        while y < y1 and is_blue(px[probe_x, y]):
            y += 1
        color = px[probe_x, (ya + y - 1) // 2]
        for yy in range(ya, y):
            for x in range(x0, x_end):
                px[x, yy] = color


def fill_vgrad(px, box):
    """用区域上下边界像素做垂直线性插值填充（背景是上浅下深的灰渐变）。"""
    x0, y0, x1, y1 = box
    for x in range(x0, x1):
        top = px[x, y0 - 1]
        bot = px[x, y1]
        span = y1 - y0 + 1
        for i, y in enumerate(range(y0, y1)):
            t = i / float(span)
            px[x, y] = tuple(
                int(round(top[c] * (1 - t) + bot[c] * t)) for c in range(4)
            )


def main():
    bak = os.path.join(UI, 'info_orig.png')
    if not os.path.exists(bak):
        shutil.copy(SRC, bak)
        print('backup → %s' % bak)

    base = Image.open(bak).convert('RGBA')

    # 普通形态底图：擦扳手 + 擦超能等级行
    normal = base.copy()
    px = normal.load()
    fill_white(px, LVICON)
    # 扳手压在两块背景上：y<170 是面板灰底(#E6E6E6)、下面白底，
    # 中间还横着昵称框的蓝边框 → 分两段擦，再按区域外重建框线。
    fill_color(px, WRENCH_TOP, (230, 230, 230, 255))
    fill_white(px, WRENCH_BODY)
    restore_hlines(px, WRENCH_BODY, probe_x=WRENCH_BODY[0] - 10)
    normal.save(SRC)
    print('wrote %s' % SRC)

    # 超能形态底图：再擦掉左侧普通头像
    sup = normal.copy()
    px = sup.load()
    fill_color(px, FACE, (230, 230, 230, 255))
    out = os.path.join(UI, 'info_super.png')
    sup.save(out)
    print('wrote %s' % out)


if __name__ == '__main__':
    main()
