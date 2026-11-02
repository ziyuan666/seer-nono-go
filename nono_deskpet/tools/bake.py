#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""把 NONO SWF 烘焙成桌宠素材（PNG 序列 + meta.json）。

核心难点：FFDec 导出的每个 sprite 层是独立裁切画布，位置信息丢失。
本脚本不猜坐标，而是用第 1 帧做模板匹配，自动求出 body / color_2 层
在完整 pet 画面中的像素偏移（实测普通形态误差 <3/255）。

用法：
    python3 bake.py <form_key> <body_swf> <actions...>

例：
    python3 bake.py plain \
        gameres/root/resource/nono/nono.swf \
        gameres/root/resource/nono/action/1.swf \
        gameres/root/resource/nono/action/2.swf

倍率：默认 4x（NONO_ZOOM 可改）。SWF 是矢量，1x 栅格化后放大必糊，
4x 导出 + 运行时面积平均下采样才有干净边缘。

输出：
    assets/<form_key>/idle/<方向>/<n>.png
    assets/<form_key>/action/<id>/<n>.png
    assets/<form_key>/meta.json
"""
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET
from pathlib import Path

import numpy as np
from PIL import Image

ROOT = Path(__file__).resolve().parents[2]          # 工作区根
FFDEC = ROOT / "tools/ffdec/ffdec-cli.jar"
JAVA = ROOT / "tools/jdk-17.0.20.1+1/bin/java"
OUT = Path(__file__).resolve().parents[1] / "assets"
# 导出倍率：SWF 是矢量，1x 栅格化后再放大必糊。
# 4x 导出 + 运行时面积平均下采样 = 边缘干净。
ZOOM = int(os.environ.get("NONO_ZOOM", "4"))

DIRS = ["down", "leftdown", "left", "leftup", "up", "rightup", "right", "rightdown"]


def run_ffdec(*args):
    subprocess.run([str(JAVA), "-jar", str(FFDEC), *map(str, args)],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def swf_to_xml(swf: Path, xml: Path):
    run_ffdec("-swf2xml", swf, xml)


def parse_sprites(xml: Path):
    """spriteId -> {frames, labels:[(frame,name)], places:[{name,chid,depth,tx,ty}]}"""
    root = ET.parse(xml).getroot()
    out = {}
    for it in root.iter("item"):
        if it.get("type") != "DefineSpriteTag":
            continue
        sid = it.get("spriteId")
        labels, places = [], []
        frame = 0
        subs = it.find("subTags")
        if subs is None:
            continue
        for s in subs:
            t = s.get("type")
            if t == "ShowFrameTag":
                frame += 1
            elif t == "FrameLabelTag":
                labels.append((frame + 1, s.get("name")))
            elif t in ("PlaceObject2Tag", "PlaceObject3Tag"):
                m = s.find("matrix")
                tx = float(m.get("translateX", 0)) if m is not None else 0.0
                ty = float(m.get("translateY", 0)) if m is not None else 0.0
                places.append({
                    "name": s.get("name") or "",
                    "chid": s.get("characterId"),
                    "depth": int(s.get("depth", 0)),
                    "tx": tx / 20.0,      # twips -> px
                    "ty": ty / 20.0,
                })
        out[sid] = {"frames": int(it.get("frameCount")),
                    "labels": labels, "places": places}
    return out


def find_sprite_dir(export_root: Path, chid: str):
    for p in export_root.iterdir():
        if p.is_dir() and p.name.startswith(f"DefineSprite_{chid}"):
            return p
    return None


def load(path: Path):
    return np.array(Image.open(path).convert("RGBA")).astype(np.int32)


def downsample(arr: np.ndarray, z: int) -> np.ndarray:
    """只用于模板匹配，把 4x 图缩回 1x，避免 match_offset 在 4x 上跑 16 倍慢。"""
    if z <= 1:
        return arr
    im = Image.fromarray(arr.astype(np.uint8), "RGBA")
    w, h = im.size
    im = im.resize((max(1, w // z), max(1, h // z)), Image.LANCZOS)
    return np.array(im).astype(np.int32)


def match_offset(reference: np.ndarray, layer: np.ndarray, need=30):
    """在 reference 中滑动 layer，返回最小色差的 (dx,dy)；找不到返回 None。"""
    H, W = reference.shape[:2]
    h, w = layer.shape[:2]
    mask = layer[:, :, 3] > 128
    if mask.sum() < need:
        return None
    best = None
    m_ys, m_xs = np.where(mask)
    for dy in range(0, H - h + 1):
        for dx in range(0, W - w + 1):
            win = reference[dy:dy + h, dx:dx + w]
            ok = win[:, :, 3] > 128
            m = ok[m_ys, m_xs]
            if m.sum() < need:
                continue
            err = np.abs(win[:, :, :3][m_ys[m], m_xs[m]] -
                         layer[:, :, :3][m_ys[m], m_xs[m]]).mean()
            if best is None or err < best[0]:
                best = (err, dx, dy)
    return None if best is None else (best[1], best[2], round(float(best[0]), 2))


def slice_by_labels(labels, total):
    if not labels:
        return [(0, total)]
    out = []
    for i, (start, _name) in enumerate(labels):
        end = labels[i + 1][0] - 1 if i + 1 < len(labels) else total
        out.append((start, end))
    return out


def bake_form(key: str, swf: Path):
    tmp = Path(tempfile.mkdtemp(prefix="nono_bake_"))
    xml = tmp / "x.xml"
    export = tmp / "sprites"
    swf_to_xml(swf, xml)
    run_ffdec("-zoom", ZOOM, "-export", "sprite", export, swf)
    sprites = parse_sprites(xml)

    # pet = symbolClass 里名为 pet 的 sprite（XML 中无名字，用最大帧数+places 特征找）
    # 普通形态：pet sprite 只有 1 帧，但含 body/color_2 子层
    pet_id = None
    for sid, sp in sprites.items():
        names = {p["name"] for p in sp["places"]}
        if "body" in names:
            pet_id = sid
            break
    if pet_id is None:
        raise SystemExit("找不到含 body 子层的 pet sprite")
    pet = sprites[pet_id]
    pet_dir = find_sprite_dir(export, pet_id)
    pet_frame = pet_dir / "1.png"
    reference = load(pet_frame)
    H, W = reference.shape[:2]
    ref_small = downsample(reference, ZOOM)

    # 按 depth 排序的层（body / color_1 / color_2）
    layers = []
    for pl in sorted(pet["places"], key=lambda x: x["depth"]):
        if not pl["name"] or pl["name"] not in ("body", "color_1", "color_2"):
            continue
        d = find_sprite_dir(export, pl["chid"])
        if d is None:
            continue
        layer = load(d / "1.png")
        off = match_offset(ref_small, downsample(layer, ZOOM))
        if off is None:
            continue
        dx, dy, err = off[0] * ZOOM, off[1] * ZOOM, off[2]
        # 用 XML 的 translate 校正符号（SWF 的 y 轴向上）
        layers.append({"name": pl["name"], "dir": d, "chid": pl["chid"],
                       "dx": dx, "dy": dy, "err": err,
                       "sprite": sprites[pl["chid"]]})

    out_dir = OUT / key
    if (out_dir / "idle").exists():
        shutil.rmtree(out_dir / "idle")
    (out_dir / "idle").mkdir(parents=True, exist_ok=True)

    meta = {"key": key, "zoom": ZOOM, "canvas": {"w": W, "h": H},
            "dirs": {}, "layers": []}
    # 以 body 层的帧标签为主（各层帧数一致）
    body = next(l for l in layers if l["name"] == "body")
    total = body["sprite"]["frames"]
    labels = body["sprite"]["labels"]
    windows = slice_by_labels(labels, total)
    if len(windows) == 1:
        # 没有方向标签：只做一个 down
        windows = [(0, total)]
        names = ["down"]
    else:
        names = [n for (_s, n) in labels]

    for name, (start, end) in zip(names, windows):
        if name not in DIRS:
            continue
        ddir = out_dir / "idle" / name
        ddir.mkdir(parents=True, exist_ok=True)
        n = 0
        for fi in range(start, end + 1):
            canvas = Image.new("RGBA", (W, H), (0, 0, 0, 0))
            for l in layers:
                p = l["dir"] / f"{fi}.png"
                if not p.exists():
                    continue
                canvas.alpha_composite(Image.open(p).convert("RGBA"),
                                       (max(0, l["dx"]), max(0, l["dy"])))
            n += 1
            canvas.save(ddir / f"{n:02d}.png")
        meta["dirs"][name] = n

    # 动作与表情由调用方另行烘焙（pet sprite 即完整画面）
    meta["layers"] = [{"name": l["name"], "dx": l["dx"], "dy": l["dy"],
                       "err": l["err"]} for l in layers]
    (out_dir / "meta.json").write_text(
        json.dumps(meta, ensure_ascii=False, indent=2), encoding="utf-8")
    shutil.rmtree(tmp, ignore_errors=True)
    print(f"[idle] {key}: {meta['dirs']}  layers={meta['layers']}  zoom={ZOOM}")


def find_pet_id(swf: Path, tmp: Path):
    """从 SymbolClass 导出里精确取名为 pet 的 characterId。"""
    sym = tmp / "sym"
    if not sym.exists():
        run_ffdec("-export", "symbolClass", sym, swf)
    for f in sym.glob("*.csv"):
        for line in f.read_text(encoding="utf-8", errors="ignore").splitlines():
            m = re.match(r'\s*(\d+)\s*;\s*"?pet"?\s*$', line)
            if m:
                return m.group(1)
    return None


def bake_sequence(out_sub: str, swf: Path):
    """动作/表情 SWF：pet sprite 的帧序列就是完整画面。"""
    parts = out_sub.split("/")
    key, tag = parts[0], parts[-1]
    tmp = Path(tempfile.mkdtemp(prefix="nono_seq_"))
    xml = tmp / "x.xml"
    export = tmp / "sprites"
    swf_to_xml(swf, xml)
    run_ffdec("-zoom", ZOOM, "-export", "sprite", export, swf)
    sprites = parse_sprites(xml)

    pet_id = find_pet_id(swf, tmp)
    if pet_id is None or pet_id not in sprites:
        # 兜底：帧数最多且不含具名子层
        candidates = []
        for sid, sp in sprites.items():
            if sp["frames"] > 1 and not any(p["name"] for p in sp["places"]):
                candidates.append((sp["frames"], sid))
        if not candidates:
            raise SystemExit(f"{swf} 找不到 pet sprite")
        frames, sid = max(candidates)
    else:
        sid = pet_id
        frames = sprites[pet_id]["frames"]
    d = find_sprite_dir(export, sid)
    dst = OUT / out_sub
    if dst.exists():
        shutil.rmtree(dst)
    dst.mkdir(parents=True, exist_ok=True)
    n = 0
    for i in range(1, frames + 1):
        p = d / f"{i}.png"
        if p.exists():
            shutil.copy2(p, dst / f"{n:03d}.png")
            n += 1
    shutil.rmtree(tmp, ignore_errors=True)
    print(f"[{out_sub}] {n} frames -> {dst}")
    return n


if __name__ == "__main__":
    if len(sys.argv) < 3:
        raise SystemExit(__doc__)
    form = sys.argv[1]
    body_swf = Path(sys.argv[2])
    if not body_swf.is_absolute():
        body_swf = ROOT / body_swf
    bake_form(form, body_swf)
    for extra in sys.argv[3:]:
        p = Path(extra)
        if not p.is_absolute():
            p = ROOT / p
        # 普通形态是 action/1.swf；超能形态是 action/1_1.swf（动作ID_形态号），
        # 年费是 annual/action/1.swf。统一取动作号，形态号由 form key 体现。
        m = re.search(r"/(action|exp)/(\d+)(?:_\d+)?\.swf$", str(p).replace("\\", "/"))
        if m:
            bake_sequence(f"{form}/{m.group(1)}/{m.group(2)}", p)
