#!/usr/bin/env python3
"""mk_flychoice.py —— 从官方 choicePanel.swf 重导「飞行模式选择面板」底图 + 取消按钮三态。

为什么需要（2026-10-27 重建）：
    旧 assets/ui/flychoice.png 是 1001×537、alpha 全 255、四角 (153,153,153) 的图，
    面板圆角外面被填成了实心灰，上屏就是「面板外面一圈灰底盖住桌面」。
    官方 SWF 里这块是透明的（DefineSprite 61 的 4x 导出四角 (0,0,0,0)）。

口径：
    - DefineSprite 61 = 面板根（SymbolClass 的 item 类）。
    - 4x 导出画布 1311×681，可见 bbox (137,19)-(1138,555) → 1001×536；
      右侧裁 1px 凑成 1000×536（4:1 精确，损失 0.25 逻辑像素，肉眼不可见），
      运行时缩到 250×134。
    - 底部「取消飞行模式」按钮 = DefineButton2 25（官方 NonoController 的 _panelMc["down"]
      → onDownHandler → nonoFlyAction(0) + hidePanel）。
      三态共用同一裁剪区（三张态图可见 bbox 的并集），保证坐标一致：
        up   = 橙色小机器人图标本体
        over = 图标 + 下方黑胶囊提示条「取消飞行模式」（比面板底边还低 29px）
        down = 按下态
      4x 裁剪区 387×211 直出，运行时 uiFit 缩到 97×53（缩小 4 倍，锐利）；
      不要先缩成 1.5x —— 145×79 再放大到 97×53 会把胶囊里的字糊掉。
      命中区只认图标本体，由 Go 侧 uiBtn.HitX/HitY/HitW/HitH 限定。

用法：
    python3 tools/mk_flychoice.py            # 用默认 SWF，写默认输出
    python3 tools/mk_flychoice.py --dry-run  # 只统计，不覆盖
"""

import argparse
import os
import shutil
import subprocess
import sys
import tempfile

import numpy as np
from PIL import Image

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DFLT_SWF = os.path.join(
    ROOT, "..", "nono_deskpet_vec", "source_swf", "资源", "nono",
    "nonoFlyModel", "choicePanel.swf",
)
UI_DIR = os.path.join(ROOT, "assets", "ui")
DFLT_OUT = os.path.join(UI_DIR, "flychoice.png")
FFDEC = "toolchain/ffdec/ffdec-cli.jar"

SPRITE_ID = "61"   # 面板根 sprite
OFF_BTN_ID = "25"  # 底部「取消飞行模式」按钮
ZOOM = "4"
ALPHA_MIN = 8      # 与运行时 hasPixel / alphaAt 的判定口径一致


def _run(cmd):
    subprocess.run(cmd, check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)


def export_sprite(swf, outdir):
    """把 swf 的所有 sprite 以 ZOOM 倍导出，返回面板根 sprite 的 png 路径。"""
    _run(["java", "-jar", FFDEC, "-zoom", ZOOM, "-export", "sprite", outdir, swf])
    # ffdec 的目录名带类名后缀（DefineSprite_61_item），用前缀匹配
    for name in sorted(os.listdir(outdir)):
        if name.startswith("DefineSprite_%s" % SPRITE_ID):
            p = os.path.join(outdir, name, "1.png")
            if os.path.exists(p):
                return p
    raise SystemExit("找不到 DefineSprite %s 的导出结果（目录：%s）" % (SPRITE_ID, outdir))


def export_buttons(swf, outdir):
    """导出所有按钮（ffdec 会建 DefineButton2_<id>/ 子目录）。"""
    os.makedirs(outdir, exist_ok=True)
    _run(["java", "-jar", FFDEC, "-zoom", ZOOM, "-export", "button", outdir, swf])


def btn_state(btndir, bid, state):
    p = os.path.join(btndir, "DefineButton2_%s" % bid, "%s.png" % state)
    if not os.path.exists(p):
        raise SystemExit("按钮素材缺失：%s" % p)
    return Image.open(p).convert("RGBA")


def visible_bbox(img):
    a = np.array(img.convert("RGBA"))
    ys, xs = np.nonzero(a[:, :, 3] > ALPHA_MIN)
    if len(xs) == 0:
        raise SystemExit("图片整幅透明，SWF 可能下错了")
    return int(xs.min()), int(ys.min()), int(xs.max()) + 1, int(ys.max()) + 1


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--swf", default=DFLT_SWF)
    ap.add_argument("--out", default=DFLT_OUT)
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    if not os.path.exists(args.swf):
        raise SystemExit("SWF 不存在：%s" % args.swf)

    tmp = tempfile.mkdtemp(prefix="mk_flychoice_")
    try:
        # ---- 1) 面板底图 ----
        src = Image.open(export_sprite(args.swf, os.path.join(tmp, "sp")))
        crop, bbox = visible_bbox_crop(src)
        w, h = crop.size
        # 对齐到 4 的整数倍：保证 4x 素材缩到逻辑尺寸是整数比
        w4, h4 = (w // 4) * 4, (h // 4) * 4
        crop = crop.crop((0, 0, w4, h4))
        print("面板 %s  %s -> 可见 bbox=%s 裁切=%dx%d -> 输出 %dx%d（显示 %dx%d）"
              % (os.path.basename(args.swf), src.size, bbox, w, h, w4, h4, w4 // 4, h4 // 4))

        # ---- 2) 取消飞行模式按钮三态 ----
        btndir = os.path.join(tmp, "btn")
        export_buttons(args.swf, btndir)
        states = [("up", "1_up"), ("over", "2_over"), ("down", "3_down")]
        imgs = [btn_state(btndir, OFF_BTN_ID, st) for _, st in states]
        boxes = [visible_bbox(im) for im in imgs]
        x0 = min(b[0] for b in boxes)
        y0 = min(b[1] for b in boxes)
        x1 = max(b[2] for b in boxes)
        y1 = max(b[3] for b in boxes)
        # 4x 直出，不预先缩放：目标 97×53 是缩小 4 倍，比 1.5x 素材放大 1.5 倍清晰得多。
        tw = x1 - x0
        th = y1 - y0
        print("取消按钮 并集裁剪区(4x)=(%d,%d)-(%d,%d) %dx%d -> 输出 %dx%d（1x 显示 %.2fx%.2f）"
              % (x0, y0, x1, y1, tw, th, tw, th,
                 tw / 4.0, th / 4.0))
        icon_box = boxes[0]
        print("  图标本体(4x)=%s -> 相对按钮左上 1x=(%.2f,%.2f) 尺寸 %.2fx%.2f"
              % (icon_box, (icon_box[0] - x0) / 4.0, (icon_box[1] - y0) / 4.0,
                 (icon_box[2] - icon_box[0]) / 4.0, (icon_box[3] - icon_box[1]) / 4.0))

        if args.dry_run:
            return

        crop.save(args.out)
        print("已写入 %s（%d 字节）" % (args.out, os.path.getsize(args.out)))

        # 只写 over / down：up 态已经烘进面板底图，运行时再压一张会重影。
        for idx, name in ((1, "over"), (2, "down")):
            p = os.path.join(UI_DIR, "fly_cancel_%s.png" % name)
            imgs[idx].crop((x0, y0, x1, y1)).save(p)
            print("已写入 %s（%dx%d，%d 字节）" % (p, tw, th, os.path.getsize(p)))
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def visible_bbox_crop(src):
    """裁掉 alpha 全空的外框，返回 (image, bbox)。"""
    x0, y0, x1, y1 = visible_bbox(src)
    return src.crop((x0, y0, x1, y1)), (x0, y0, x1, y1)


if __name__ == "__main__":
    sys.exit(main())
