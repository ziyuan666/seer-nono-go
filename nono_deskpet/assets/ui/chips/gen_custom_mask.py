#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""生成自定义芯片图标的两张底图（700313 自定义颜色 / 700314 自定义渐变）。

思路：原版每种变色芯片都是「外壳 + 内部色块」已经合成好的 PNG，颜色写死。
本地新增的两枚颜色由玩家随时改，所以拆成：
  _custom_base.png = 原样复制 700301.png（外壳、描边、白高光、内部条纹全保留）
  _custom_mask.png = 内部色块区域（255=按目标色重新上色，0=保留外壳原样）

mask 的判定：
  1. 先取 700301.png 里饱和度高的粉色像素，得到色块包围盒；
  2. 包围盒内、亮度 >= 120 且不是近白（min < 235）的像素算「可上色」——
     近白是内部那条高光/分隔线，保留原色才不会把芯片涂成一块死色；
  3. 深色描边（亮度 < 120）留在包围盒外，保持外壳轮廓。

运行：python3 gen_custom_mask.py   （在 assets/ui/chips/ 目录下执行）
"""
from pathlib import Path

import numpy as np
from PIL import Image

HERE = Path(__file__).resolve().parent
SRC = HERE / "700301.png"


def main() -> None:
    src = Image.open(SRC).convert("RGBA")
    a = np.array(src).astype(int)
    r, g, b, al = a[..., 0], a[..., 1], a[..., 2], a[..., 3]

    pink = (r > 200) & (g < 210) & (b > 110) & (al > 30)
    ys, xs = np.where(pink)
    x0, x1, y0, y1 = xs.min(), xs.max(), ys.min(), ys.max()

    box = np.zeros_like(pink)
    box[y0 : y1 + 1, x0 : x1 + 1] = True

    mx = np.maximum(np.maximum(r, g), b)
    mn = np.minimum(np.minimum(r, g), b)
    inner = box & (mx >= 120) & (mn < 235) & (al > 30)

    Image.fromarray((inner * 255).astype(np.uint8), "L").save(HERE / "_custom_mask.png")
    src.save(HERE / "_custom_base.png")
    print(f"色块包围盒 x[{x0},{x1}] y[{y0},{y1}]，可上色 {int(inner.sum())} 像素")


if __name__ == "__main__":
    main()
