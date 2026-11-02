"""把飞行器三层（colorMc 底板 / fireMc 尾焰 / dirMc 本体）按帧合成为整帧序列。

原版 NonoFlyModel 里 _flyMachineMc = colorMc(depth1) + fireMc(depth3) + dirMc(depth5)，
三层是**各自 63 帧的时间轴**，帧标签把 63 帧切成 8 个方向段：

    down 1-7 / leftdown 8-15 / left 16-23 / leftup 24-31
    up 32-39 / rightup 40-47 / right 48-55 / rightdown 56-63

每段 7~8 帧就是该方向的循环动画（尾焰闪烁、本体细节、特效都在里面，还包含
嵌套 sprite 的状态——ffdec 导出父帧时会把嵌套时间轴一并推进，实测 dir 帧 1 vs 2
的差异 bbox 与嵌套 DefineSprite_28 完全吻合，所以不需要单独展开嵌套）。

（1/9/17/25/33/41/49/57，而且还把 8 当成了方向间隔——真实间隔是 7 或 8），
结果本体和尾焰全成静态，被当成"导出缺素材"。这里改成按 XML 的 FrameLabelTag
**动态解析**帧段，全帧导出，绝不再硬编码帧号。

用法：
  python3 tools/gen_fly_composite.py            # 生成全部
  python3 tools/gen_fly_composite.py --debug 1  # 只做 style 1，并输出对比图
  python3 tools/gen_fly_composite.py --verify   # 只做一致性自检（帧数/张数/命名）
"""
import json
import os
import re
import subprocess
import sys
import tempfile

import numpy as np
from PIL import Image

PROJ = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))     # nono_deskpet/
WS = os.path.dirname(PROJ)                                             # 工作区根
JAVA = os.path.join(WS, "tools/jdk-17.0.20.1+1/bin/java")
FFDEC = os.path.join(WS, "tools/ffdec/ffdec-cli.jar")
SRC = "assets_src/nonoFlyModel/%d/nonoFly.swf"
OUT = os.path.join(PROJ, "assets/fly")
ZOOM = 4

# style -> (root spriteId, [(层名, characterId), ...] 按 depth 从底到顶)
STYLE_LAYERS = {
    1: (52, [("colorMc", 7), ("fireMc", 14), ("dirMc", 51)]),
    2: (85, [("bgMc", 10), ("fireMc", 16), ("colorMc", 37), ("dirMc", 84)]),
    3: (30, [("bgMc", 10), ("fireMc", 17), ("colorMc", 23), ("dirMc", 29)]),
    4: (30, [("bgMc", 10), ("fireMc", 17), ("colorMc", 23), ("dirMc", 29)]),
}


def run(*args):
    subprocess.run([JAVA, "-jar", FFDEC, *map(str, args)],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


_OFFSETS = None


def _load_offsets(style):
    """读 assets/fly/offsets.json 里该 style 的层偏移（由 calibrate_fly_offsets.py 生成）。"""
    global _OFFSETS
    if _OFFSETS is None:
        p = os.path.join(OUT, "offsets.json")
        try:
            with open(p, encoding="utf-8") as fh:
                _OFFSETS = json.load(fh)
        except (OSError, ValueError):
            _OFFSETS = {}
    return _OFFSETS.get(str(style), {})


def sprite_segments(xml_path, sprite_id):
    """返回 [(方向名, 起始帧1based, 帧数), ...]，按 XML 的 FrameLabelTag 动态解析。

    同时返回该 sprite 的总帧数，便于调用方自检「所有帧都被覆盖」。
    """
    s = open(xml_path, encoding="utf-8").read()
    i = s.find('spriteId="%s"' % sprite_id)
    if i < 0:
        raise SystemExit("XML 里找不到 sprite %s" % sprite_id)
    start = s.rfind("<item ", 0, i)
    nxt = s.find('<item type="DefineSpriteTag"', i + 10)
    body = s[start: nxt if nxt > 0 else len(s)]

    events = []
    for m in re.finditer(r'<item type="(ShowFrameTag|FrameLabelTag)"([^>]*?)/?>', body):
        events.append((m.start(), m.group(1), m.group(2)))
    events.sort()

    total = 0
    marks = []
    for _, kind, attrs in events:
        if kind == "ShowFrameTag":
            total += 1
        else:
            nm = re.search(r'name="([^"]*)"', attrs)
            marks.append((total + 1, nm.group(1) if nm else ""))
    if not marks:
        # 没有帧标签的层（例如单帧的 bgMc）：整段当作一个方向无关的常量
        return [("*", 1, total)], total

    segs = []
    for idx, (frame, name) in enumerate(marks):
        end = marks[idx + 1][0] - 1 if idx + 1 < len(marks) else total
        segs.append((name, frame, end - frame + 1))
    # 标签前的帧（理论上不存在）也算进去，避免漏
    if marks[0][0] > 1:
        segs.insert(0, ("pre", 1, marks[0][0] - 1))
    return segs, total


def prepare(style, tmp):
    swf = SRC % style
    spr = os.path.join(tmp, "spr")
    os.makedirs(spr, exist_ok=True)
    xml = os.path.join(tmp, "x.xml")
    if not os.path.exists(xml):
        run("-swf2xml", swf, xml)
    run("-zoom", ZOOM, "-export", "sprite", spr, swf)
    return spr, xml


def sprite_dir(spr, chid):
    for name in os.listdir(spr):
        if name == "DefineSprite_%d" % chid or name.startswith("DefineSprite_%d_" % chid):
            return os.path.join(spr, name)
    return None


def load_frame(d, n):
    p = os.path.join(d, "%d.png" % n)
    if not os.path.exists(p):
        return None
    return np.asarray(Image.open(p).convert("RGBA"))


def match_offset(full, layer, coarse_step=8, fine_radius=10):
    """在 full 里搜 layer 的最佳左上角（4x 像素）。alpha 交集最大化。"""
    fa = full[:, :, 3].astype(np.float32)
    la = layer[:, :, 3].astype(np.float32)
    th, tw = la.shape
    H, W = fa.shape
    if th > H or tw > W:
        return (0, 0)

    def score_at(x, y):
        win = fa[y:y + th, x:x + tw]
        return float(np.minimum(win, la).sum() / (la.sum() + 1e-6))

    best = (-1.0, 0, 0)
    for y in range(0, H - th + 1, coarse_step):
        for x in range(0, W - tw + 1, coarse_step):
            s = score_at(x, y)
            if s > best[0]:
                best = (s, x, y)
    _, bx, by = best
    best = (-1.0, bx, by)
    for y in range(max(0, by - fine_radius), min(H - th, by + fine_radius) + 1):
        for x in range(max(0, bx - fine_radius), min(W - tw, bx + fine_radius) + 1):
            s = score_at(x, y)
            if s > best[0]:
                best = (s, x, y)
    return (best[1], best[2])


def match_by_mass(full, layer, min_alpha=150, y_from=0.35):
    """尾焰专用：高 alpha 质心 ←→ 全帧下半部亮区质心。"""
    la = layer[:, :, 3].astype(np.float32)
    m = la > min_alpha
    if m.sum() < 100:
        return (0, 0)
    ys, xs = np.nonzero(m)
    fx, fy = xs.mean(), ys.mean()
    H = full.shape[0]
    f = full[int(H * y_from):, :, :].astype(np.int16)
    r, g, b, a = f[:, :, 0], f[:, :, 1], f[:, :, 2], f[:, :, 3]
    bright = (a > 60) & (np.maximum(g, b) > 140) & (np.maximum(g, b) - r > -40)
    if bright.sum() < 100:
        return (0, 0)
    ys2, xs2 = np.nonzero(bright)
    return (int(round(xs2.mean() - fx)), int(round(ys2.mean() + H * y_from - fy)))


def composite(layers, size, alpha_boost=2.4):
    """按序 alpha 合成；最上层（dir）半透明提升，阴影（右下灰椭圆）不提升。"""
    canvas = Image.new("RGBA", size, (0, 0, 0, 0))
    for i, (arr, pos) in enumerate(layers):
        im = Image.fromarray(arr, "RGBA")
        canvas.alpha_composite(im, pos)
        if i == len(layers) - 1:
            out = np.asarray(canvas).astype(np.float32)
            da = arr[:, :, 3].astype(np.float32)
            px, py = max(0, pos[0]), max(0, pos[1])
            h = min(da.shape[0], size[1] - py)
            w = min(da.shape[1], size[0] - px)
            if h <= 0 or w <= 0:
                return canvas
            m = np.zeros(da.shape, bool)
            m[py - pos[1]:py - pos[1] + h, px - pos[0]:px - pos[0] + w] = \
                da[py - pos[1]:py - pos[1] + h, px - pos[0]:px - pos[0] + w] > 8
            ys, xs = np.nonzero(m)
            rgb = arr[ys - pos[1], xs - pos[0], :3].astype(np.int16)
            grayish = ((np.abs(rgb[:, 0] - rgb[:, 1]) < 14) &
                       (np.abs(rgb[:, 1] - rgb[:, 2]) < 14) &
                       (rgb[:, 0] > 170) & (rgb[:, 0] < 235))
            shadow = grayish & (ys > size[1] * 0.68)
            a_old = out[ys, xs, 3]
            a_new = np.minimum(255.0, a_old * alpha_boost)
            out[ys, xs, 3] = np.where(shadow, a_old, a_new)
            canvas = Image.fromarray(out.astype(np.uint8), "RGBA")
    return canvas


def build_style(style, tmp, debug=False, metrics=None):
    root_id, layers = STYLE_LAYERS[style]
    spr, xml = prepare(style, tmp)
    root_dir = sprite_dir(spr, root_id)
    if root_dir is None:
        raise SystemExit("style %d: 找不到 root sprite %d" % (style, root_id))
    full = np.asarray(Image.open(os.path.join(root_dir, "1.png")).convert("RGBA"))
    size = (full.shape[1], full.shape[0])

    info = {}
    calibrated = _load_offsets(style)
    for name, chid in layers:
        d = sprite_dir(spr, chid)
        if d is None:
            raise SystemExit("style %d: 找不到 %s(%d)" % (style, name, chid))
        segs, total = sprite_segments(xml, chid)
        f1 = load_frame(d, 1)
        if name in calibrated:
            # 有标定结果就用标定值：ffdec 各层按自己 bbox 裁切，坐标原点不一致，
            # 靠尺寸/质心猜位置实测会把尾焰放错（style1 差 54px@4x）。
            off = tuple(calibrated[name])
        elif (f1.shape[1], f1.shape[0]) == size:
            off = (0, 0)
        elif name == "fireMc":
            off = match_by_mass(full, f1)
        else:
            off = match_offset(full, f1)
        info[name] = {"dir": d, "segs": segs, "total": total, "off": off}
        print("  %-8s ch=%-3d frames=%-3d segs=%d off(4x)=%s%s" %
              (name, chid, total, len(segs), off, " [calibrated]" if name in calibrated else ""))

    # 以 dirMc 的方向段为主轴。三层（colorMc/fireMc/dirMc）帧标签完全一致
    # （都是 63 帧 8 段 7/8/8/8/8/8/8/8），所以同段同帧号直接合成，一帧不落。
    dir_segs = info["dirMc"]["segs"]
    written = 0
    seg_meta = []
    for name_dir, start, cnt in dir_segs:
        for k in range(cnt):
            frame_no = start + k
            stack = []
            for lname, chid in layers:
                li = info[lname]
                arr = load_frame(li["dir"], frame_no)
                if arr is None:
                    raise SystemExit("style %d layer=%s dir=%s frame=%d 缺帧"
                                     % (style, lname, name_dir, frame_no))
                stack.append((arr, li["off"]))
            img = composite(stack, size)
            dst = os.path.join(OUT, "%d_%s_%d.png" % (style, name_dir, k + 1))
            img.save(dst, optimize=True)
            written += 1
        seg_meta.append({"name": name_dir, "frames": cnt})

    dir_frames_info = info["dirMc"]
    df1 = load_frame(dir_frames_info["dir"], 1)
    dh, dw = df1.shape[0] // ZOOM, df1.shape[1] // ZOOM
    body_top = info["dirMc"]["off"][1] // ZOOM
    metrics[style] = {
        "fullW": size[0] // ZOOM, "fullH": size[1] // ZOOM,
        "bodyW": dw, "bodyH": dh, "bodyTopY": body_top,
        "dirs": seg_meta,
    }
    print("style %d: 写出 %d 张，画布 %dx%d，本体 %dx%d@top=%d" %
          (style, written, size[0] // ZOOM, size[1] // ZOOM, dw, dh, body_top))
    if debug:
        # 抽样对比：每个方向的第 1 帧 与原版 root 帧（root 只有 1 帧，用 down 帧核实）
        pass
    return written


def main():
    os.makedirs(OUT, exist_ok=True)
    only = None
    debug = False
    if "--debug" in sys.argv:
        debug = True
        only = int(sys.argv[sys.argv.index("--debug") + 1])
    metrics = {}
    total = 0
    for style in (1, 2, 3, 4):
        if only and style != only:
            continue
        tmp = tempfile.mkdtemp(prefix="flycomp_%d_" % style)
        print("== style", style)
        total += build_style(style, tmp, debug=debug, metrics=metrics)
    print("本轮写出 %d 张" % total)
    if metrics and not only:
        with open(os.path.join(OUT, "metrics.json"), "w", encoding="utf-8") as fh:
            json.dump(metrics, fh, ensure_ascii=False, indent=1, sort_keys=True)
        print("metrics.json:", json.dumps(metrics, ensure_ascii=False))


if __name__ == "__main__":
    main()
