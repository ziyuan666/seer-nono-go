#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""按层导出飞行素材（colorMc / fireMc / dirMc 分开），供运行时分层合成。

1. **颜色**：原版 `NonoFlyModel` 调 `DisplayUtil.FillColor(this._colorMc, _info.color)`，
   把 colorMc 用 `ColorTransform.color` 染成玩家色（DisplayUtil.as:31）。烤进整帧
   就永久是灰色，运行时没法换色。
2. **周期**：三层各自有独立子 MC 周期——fireMc 内 `DefineSprite_12` 是 8 帧，
   dirMc 的 down 段 `DefineSprite_28` 是 18 帧。整帧合成会把它们强行同步成方向段长
   （down 段只有 7 帧），尾焰动到 7/8 就被拉回，轨迹和节奏都对不上。
   分层后各层按自己的帧数循环，才回到原版观感。

输出：
  assets/fly/c_<style>_<dir>.png            颜色层，每方向 1 张（段内无变化，实测帧1..7一致）
  assets/fly/f_<style>_<dir>_<k>.png        尾焰层，k = 1..段长
  assets/fly/d_<style>_<dir>_<k>.png        本体层，k = 1..段长
  assets/fly/metrics.json                   每层尺寸/偏移/帧数 + 本体实际 bbox
  assets/fly/offsets.json                   层偏移标定（calibrate_fly_offsets.py 产出）

用法：
  python3 tools/gen_fly_layers.py            # 全量
  python3 tools/gen_fly_layers.py --style 1  # 单个 style
"""
import json
import os
import sys
import tempfile

import numpy as np
from PIL import Image

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from gen_fly_composite import (OUT, STYLE_LAYERS, ZOOM, _load_offsets,
                               load_frame, prepare, sprite_dir,
                               sprite_segments)

# 层名 -> 输出前缀。bgMc 与 colorMc 必须分开：style2/3/4 里二者都存在
# （depth 1 和 5），早期都映射成 "c" 会互相覆盖，合成时露出错位的青色底板。
PREFIX = {"bgMc": "b", "colorMc": "c", "fireMc": "f", "dirMc": "d"}
# colorMc 会被 FillColor 染成玩家色，只有它参与 tint；bgMc 保持原色。
STATIC_LAYERS = ("bgMc", "colorMc")
# 根 sprite 的 place matrix 对某层做过缩放时，ffdec 导出的层 PNG 仍是 sprite bbox
# 原尺寸（不会带根 place 的缩放），必须由运行期补缩放，否则该层会大一圈。
# 数值来源：SWF 根 sprite 里 PlaceObject 的 matrix.scaleX/scaleY——
#   style3/style4 的 bgMc（characterId 10）在根里是 0.807。
LAYER_SCALE = {3: {"bgMc": 0.807}, 4: {"bgMc": 0.807}}


def content_bbox(arr, thresh=8):
    a = arr[:, :, 3]
    ys, xs = np.nonzero(a > thresh)
    if len(ys) == 0:
        return None
    return int(xs.min()), int(ys.min()), int(xs.max()), int(ys.max())


def build(style, tmp, metrics):
    root_id, layers = STYLE_LAYERS[style]
    spr, xml = prepare(style, tmp)
    root_dir = sprite_dir(spr, root_id)
    if root_dir is None:
        raise SystemExit("style %d: 找不到 root %d" % (style, root_id))
    full = load_frame(root_dir, 1)
    size = (full.shape[1], full.shape[0])

    calib = _load_offsets(style)
    info = {}
    for name, chid in layers:
        d = sprite_dir(spr, chid)
        if d is None:
            raise SystemExit("style %d: 找不到 %s(%d)" % (style, name, chid))
        segs, total = sprite_segments(xml, chid)
        f1 = load_frame(d, 1)
        if name in calib:
            off = tuple(int(v) for v in calib[name])
        elif (f1.shape[1], f1.shape[0]) == size:
            off = (0, 0)
        else:
            raise SystemExit("style %d layer %s 缺标定偏移，先跑 calibrate_fly_offsets.py"
                             % (style, name))
        info[name] = {"dir": d, "segs": segs, "total": total, "off": off}

    # 方向主轴以 dirMc 为准（三层帧标签实测完全一致）
    dir_segs = info["dirMc"]["segs"]
    written = 0
    layer_meta = {}
    body_union = None

    # style1/3 的 fireMc 是同一个火苗 sprite 复制到 8 个方向段，实测各方向
    # 首帧像素只差 8.37 的量化抖动、bbox 完全一致——它跟朝向无关，只是"火焰在闪"。
    # down 段时间轴只有 7 帧，按 7 帧循环等于每轮少闪一拍，视觉上就是
    fire_unified = {}
    if 1 == style or 3 == style:
        fi = info.get("fireMc")
        if fi:
            seg = None
            for nm, st, ct in dir_segs:
                if nm == "leftdown":
                    seg = (st, ct)
                    break
            if seg and seg[1] >= 8:
                fire_unified = {"start": seg[0], "count": seg[1], "dir": fi["dir"]}

    for lname, chid in layers:
        li = info[lname]
        pre = PREFIX[lname]
        per_dir = []
        for name_dir, start, cnt in dir_segs:
            if lname in STATIC_LAYERS:
                # 底板层段内实测无变化，只留段首帧，省体积
                arr = load_frame(li["dir"], start)
                if arr is None:
                    raise SystemExit("style %d color %s 缺帧" % (style, name_dir))
                dst = os.path.join(OUT, "%s_%d_%s.png" % (pre, style, name_dir))
                Image.fromarray(arr, "RGBA").save(dst, optimize=True)
                written += 1
                per_dir.append({"name": name_dir, "frames": 1})
                continue

            if lname == "fireMc" and fire_unified:
                start = fire_unified["start"]
                cnt = fire_unified["count"]
            k_list = []
            for k in range(cnt):
                arr = load_frame(li["dir"], start + k)
                if arr is None:
                    raise SystemExit("style %d %s %s frame %d 缺" % (style, lname, name_dir, k))
                dst = os.path.join(OUT, "%s_%d_%s_%d.png" % (pre, style, name_dir, k + 1))
                Image.fromarray(arr, "RGBA").save(dst, optimize=True)
                written += 1
                k_list.append(arr)
            per_dir.append({"name": name_dir, "frames": cnt})
            if lname == "dirMc":
                # 本体实际范围：取该方向所有帧 bbox 的并集，再换算到整帧坐标（逻辑像素）
                for a in k_list:
                    bb = content_bbox(a)
                    if bb is None:
                        continue
                    x0, y0, x1, y1 = bb
                    x0 += li["off"][0]; x1 += li["off"][0]
                    y0 += li["off"][1]; y1 += li["off"][1]
                    cur = body_union or (x0, y0, x1, y1)
                    body_union = (min(cur[0], x0), min(cur[1], y0),
                                  max(cur[2], x1), max(cur[3], y1))
        layer_meta[lname] = {
            "prefix": pre,
            "off": list(li["off"]),
            "dirs": per_dir,
            "w": load_frame(li["dir"], 1).shape[1],
            "h": load_frame(li["dir"], 1).shape[0],
        }
        sc = LAYER_SCALE.get(style, {}).get(lname)
        if sc and abs(sc - 1.0) > 1e-6:
            layer_meta[lname]["scale"] = sc

    bx0, by0, bx1, by1 = body_union
    metrics[style] = {
        "fullW": size[0] // ZOOM,
        "fullH": size[1] // ZOOM,
        # 本体在整帧里的真实范围（逻辑像素），flyBodyBottomInset 靠它算抬升补偿；
        # 旧值用 dirMc 导出画布高度（含透明边），bodyBottom 恒超 FullH 导致补偿一直返回 0。
        "bodyLeft": bx0 // ZOOM,
        "bodyTop": by0 // ZOOM,
        "bodyRight": bx1 // ZOOM,
        "bodyBottom": by1 // ZOOM,
        "bodyW": (bx1 - bx0) // ZOOM,
        "bodyH": (by1 - by0) // ZOOM,
        "layers": layer_meta,
        "dirs": [{"name": s[0], "frames": s[2]} for s in dir_segs],
    }
    print("style %d: 写出 %d 张  整帧 %dx%d  本体 %dx%d @ (%d,%d)-(%d,%d)"
          % (style, written, size[0] // ZOOM, size[1] // ZOOM,
             (bx1 - bx0) // ZOOM, (by1 - by0) // ZOOM,
             bx0 // ZOOM, by0 // ZOOM, bx1 // ZOOM, by1 // ZOOM))
    return written


def main():
    only = None
    if "--style" in sys.argv:
        only = int(sys.argv[sys.argv.index("--style") + 1])
    os.makedirs(OUT, exist_ok=True)
    metrics = {}
    total = 0
    for style in (1, 2, 3, 4):
        if only and style != only:
            continue
        tmp = tempfile.mkdtemp(prefix="flylayer_%d_" % style)
        print("== style", style)
        total += build(style, tmp, metrics)
    print("本轮写出 %d 张" % total)
    if metrics:
        p = os.path.join(OUT, "metrics.json")
        old = {}
        if os.path.exists(p):
            try:
                old = json.load(open(p, encoding="utf-8"))
            except ValueError:
                old = {}
        old.update({str(k): v for k, v in metrics.items()})
        with open(p, "w", encoding="utf-8") as fh:
            json.dump(old, fh, ensure_ascii=False, indent=1, sort_keys=True)
        print("metrics.json 已更新")


if __name__ == "__main__":
    main()
