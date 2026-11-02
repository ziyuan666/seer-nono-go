#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""用 SWF 的 shapeBounds + place matrix 递归算出各层在根坐标里的精确偏移。

为什么换这条路线（2026-10-24）：图像匹配依赖 ffdec 导出的 root 帧做基准，
但 ffdec 对多层嵌套 sprite 的 root 渲染会漏层（style3 的 dirMc 完全没画出来），
拿这种残缺基准标定必然错。XML 里的 shapeBounds 是 SWF 原始数据，不受渲染器影响。

偏移定义：层 PNG 左上角在「整帧画像素坐标」中的位置（4x，与 offsets.json 同量纲）。
  层 PNG 左上角 = 该 sprite 内容的 bbox 左上角（ffdec 按 bbox 裁切导出）
  offset = place.translate - spriteContentBBox.topleft   （twips → px，再 ×ZOOM）
"""
import json
import os
import re
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from gen_fly_composite import OUT, STYLE_LAYERS, ZOOM, prepare

TWIPS = 20.0


class Swf:
    def __init__(self, xml_text):
        self.s = xml_text
        self.shapes = {}
        self.sprites = {}
        self._parse()

    def _blocks(self):
        """切成 (type, id, body_text) 的列表，按出现顺序。"""
        out = []
        for m in re.finditer(r'<item type="(DefineShape\d?Tag|DefineSpriteTag)"([^>]*)>', self.s):
            typ = m.group(1)
            attrs = m.group(2)
            sid = re.search(r'shapeId="(\d+)"', attrs) or re.search(r'spriteId="(\d+)"', attrs)
            if not sid:
                continue
            cid = int(sid.group(1))
            start = m.start()
            # 找该块结束：下一个 Define* 块开始处（XML 是平铺的 item 列表）
            nxt = re.search(r'<item type="DefineShape\d?Tag"|<item type="DefineSpriteTag"',
                            self.s[m.end():])
            end = m.end() + nxt.start() if nxt else len(self.s)
            out.append((typ, cid, self.s[start:end]))
        return out

    def _parse(self):
        for typ, cid, body in self._blocks():
            if typ.startswith("DefineShape"):
                b = re.search(r'<shapeBounds type="RECT"([^/]*)/>', body)
                if not b:
                    continue
                a = dict(re.findall(r'(\w+)="(-?\d+)"', b.group(1)))
                self.shapes[cid] = (int(a["Xmin"]), int(a["Ymin"]),
                                    int(a["Xmax"]), int(a["Ymax"]))
            else:
                places = []
                for m in re.finditer(r'<item type="PlaceObject\d?Tag"([^>]*)>(.*?)</item>',
                                     body, re.S):
                    at = m.group(1)
                    ch = re.search(r'characterId="(\d+)"', at)
                    if not ch:
                        continue
                    mm = re.search(r'<matrix type="MATRIX"([^>]*?)/?>', m.group(2))
                    a, b, c, dd = 1.0, 0.0, 0.0, 1.0
                    tx = ty = 0.0
                    if mm:
                        q = dict(re.findall(r'(\w+)="(-?[\d.eE+-]+)"', mm.group(1)))
                        def num(k, dflt=0.0):
                            if k not in q:
                                return dflt
                            v = float(q[k])
                            # ffdec 对定点数会直接输出浮点；个别版本输出原始 16.16 整数
                            if abs(v) > 100.0 and k not in ("translateX", "translateY"):
                                v /= 65536.0
                            return v
                        a = num("scaleX", 1.0)
                        dd = num("scaleY", 1.0)
                        b = num("rotateSkew0", 0.0)
                        c = num("rotateSkew1", 0.0)
                        tx = num("translateX")
                        ty = num("translateY")
                    places.append((int(ch.group(1)), a, b, c, dd, tx, ty))
                self.sprites[cid] = places

    def bbox(self, cid, depth=0):
        if depth > 40:
            return None
        if cid in self.shapes:
            return self.shapes[cid]
        if cid not in self.sprites:
            return None
        acc = None
        for ch, a, b, c, dd, tx, ty in self.sprites[cid]:
            if ch == cid:      # 自引用（根 sprite 会 place 自己）：跳过，否则 bbox 无限放大
                continue
            cb = self.bbox(ch, depth + 1)
            if cb is None:
                continue
            pts = [(cb[0], cb[1]), (cb[2], cb[1]), (cb[0], cb[3]), (cb[2], cb[3])]
            tp = [(a * x + c * y + tx, b * x + dd * y + ty) for x, y in pts]
            xs = [p[0] for p in tp]; ys = [p[1] for p in tp]
            box = (min(xs), min(ys), max(xs), max(ys))
            acc = box if acc is None else (min(acc[0], box[0]), min(acc[1], box[1]),
                                           max(acc[2], box[2]), max(acc[3], box[3]))
        return acc


def main():
    off = {}
    metrics_p = os.path.join(OUT, "metrics.json")
    metrics = json.load(open(metrics_p, encoding="utf-8")) if os.path.exists(metrics_p) else {}
    for style in (1, 2, 3, 4):
        tmp = tempfile.mkdtemp(prefix="bxswf_%d_" % style)
        spr, xml = prepare(style, tmp)
        sw = Swf(open(xml, encoding="utf-8").read())
        root_id, layers = STYLE_LAYERS[style]
        root_box = sw.bbox(root_id)
        if not root_box:
            print("style %d: root bbox 解析失败" % style)
            continue
        print("== style %d  根 bbox(twips)=%s  即 %.1fx%.1f px" %
              (style, root_box,
               (root_box[2] - root_box[0]) / TWIPS, (root_box[3] - root_box[1]) / TWIPS))
        cur = {}
        for name, chid in layers:
            lb = sw.bbox(chid)
            if not lb:
                print("   %-8s bbox 解析失败" % name)
                continue
            # 层在根坐标中的位置 = 根 place matrix（各层同一 matrix，从 sprite 定义里取）
            px = py = 0.0
            for ch2, a2, b2, c2, d2, tx2, ty2 in sw.sprites[root_id]:
                if ch2 == chid:
                    px, py = tx2, ty2
                    break
            ox = (px + lb[0] - root_box[0]) / TWIPS
            oy = (py + lb[1] - root_box[1]) / TWIPS
            lw = (lb[2] - lb[0]) / TWIPS
            lh = (lb[3] - lb[1]) / TWIPS
            cur[name] = [int(round(ox * ZOOM)), int(round(oy * ZOOM))]
            print("   %-8s bbox=%.1fx%.1f px  根内偏移=(%.2f,%.2f) → 4x=(%d,%d)"
                  % (name, lw, lh, ox, oy, cur[name][0], cur[name][1]))
        off[str(style)] = cur
        if style in (1, 2, 3, 4) and str(style) in metrics:
            m = metrics[str(style)]
            print("   整帧(计算) %.0fx%.0f  vs metrics %dx%d" %
                  ((root_box[2] - root_box[0]) / TWIPS, (root_box[3] - root_box[1]) / TWIPS,
                   m["fullW"], m["fullH"]))
    # 只写侧文件，绝不覆盖 offsets.json：
    # 本脚本按 SWF shapeBounds 推导，忽略了根 place 的 scale 与图层重叠，
    # 对 fireMc 会算出与画面明显不符的位置（2026-10-27 实测 style2/4 差 ~375px）。
    # 运行时用的是 calibrate_fly_offsets.py 的图像标定结果，这里只作交叉参考。
    p = os.path.join(OUT, "offsets_swf.json")
    json.dump(off, open(p, "w", encoding="utf-8"), ensure_ascii=False, indent=1, sort_keys=True)
    print("已写 offsets_swf.json（SWF shapeBounds 推导，仅供对照，不覆盖 offsets.json）")


if __name__ == "__main__":
    main()
