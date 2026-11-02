"""重新生成 assets/fly 的 4x 高分辨率飞行器图。

原版钉 1x 导出（84x110 等），桌宠按本体比例放大到 ~135px 显示时重采样发虚
运行时再按目标尺寸降采样，边缘由 4x 超采样平均，清晰度接近矢量。

dirMc / fireMc 的 chid 对应（20261023-08 说明.md + 本次 dumpSWF 复核）：
  style1 dir=51  fire=14
  style2 dir=84  fire=16
  style3 dir=29  fire=17
  style4 dir=29  fire=17
dirMc 每方向 8 帧，方向首帧 = 1/9/17/25/33/41/49/57
（帧标签顺序 down,leftdown,left,leftup,up,rightup,right,rightdown）
"""
import os
from PIL import Image

SRC = {
    1: (51, 14, "/tmp/flyspr"),
    2: (84, 16, "/tmp/fs2"),
    3: (29, 17, "/tmp/fs3"),
    4: (29, 17, "/tmp/fs4"),
}
DIR_FIRST = [1, 9, 17, 25, 33, 41, 49, 57]
OUT = "nono_deskpet/assets/fly"

os.makedirs(OUT, exist_ok=True)
total = 0
for style, (did, fid, root) in SRC.items():
    for idx, fr in enumerate(DIR_FIRST, start=1):
        src = os.path.join(root, "DefineSprite_%d" % did, "%d.png" % fr)
        dst = os.path.join(OUT, "%d_%d.png" % (style, idx))
        Image.open(src).save(dst, optimize=True)
        total += os.path.getsize(dst)
    for f in range(1, 9):
        src = os.path.join(root, "DefineSprite_%d" % fid, "%d.png" % f)
        dst = os.path.join(OUT, "fire_%d_%d.png" % (style, f))
        Image.open(src).save(dst, optimize=True)
        total += os.path.getsize(dst)
    print("style", style, "ok")

print("total MB", round(total / 1048576, 1))
