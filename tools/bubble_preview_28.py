#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""28 字最长台词的气泡折行对比预览（样式 C）。

对比两种折行：
  方案一  2 行对半（14+14）—— 气泡带 60px 可容纳
  方案二  3 行、每行 11 字（11+11+6）—— 需要气泡带 80px

上排按 2 倍放大看边缘细节，下排 1 倍真实尺寸并配本体示意看比例。
只用于选型，不参与打包。
"""
from PIL import Image, ImageDraw, ImageFont, ImageFilter

R = 4                      # 渲染超采样
FS = 16 * R                # 真机字号 16
LH = 20 * R                # 行高 20
PADX, PADY = 14 * R, 6 * R
RAD = 10 * R
TAIL_W, TAIL_H = 9 * R, 7 * R
FONT = "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc"

TEXT = "克洛斯地心、地心游记，带我去，带我去，我也要玩水晶敲敲！"

STYLE = dict(fill=(243, 248, 255), grad=((246, 250, 255), (214, 232, 255)),
             border=(160, 194, 238), text=(24, 58, 104))


def split_balanced(s, lines):
    """按目标行数尽量均分，返回等长切分。"""
    n = len(s)
    per = -(-n // lines)
    return [s[i:i + per] for i in range(0, n, per)]


def split_per_char(s, per):
    return [s[i:i + per] for i in range(0, len(s), per)]


PUNCT = "、，。！？；：,.!?;:）)」』】…"


def split_at_punct(s, target):
    """两行：断点取离 target 最近的「标点之后」，避免标点顶到行首。"""
    cands = [i + 1 for i in range(len(s) - 1) if s[i] in PUNCT]
    if not cands:
        return split_balanced(s, 2)
    cut = min(cands, key=lambda i: abs(i - target))
    return [s[:cut], s[cut:]]


def make_mask(w, h, r, tw, th):
    m = Image.new("L", (w, h + th), 0)
    d = ImageDraw.Draw(m)
    d.rounded_rectangle([0, 0, w - 1, h - 1], radius=r, fill=255)
    cx = w // 2
    d.polygon([(cx - tw // 2, h - 3), (cx + tw // 2, h - 3),
               (cx, h + th - 1)], fill=255)
    return m


def draw_bubble(lines, font):
    tw = max(int(font.getlength(x)) for x in lines)
    w = tw + PADX * 2
    h = LH * len(lines) + PADY * 2
    mask = make_mask(w, h, RAD, TAIL_W, TAIL_H)
    out = Image.new("RGBA", mask.size, (0, 0, 0, 0))

    top, bot = STYLE["grad"]
    g = Image.new("RGBA", (1, mask.size[1]))
    for y in range(mask.size[1]):
        t = y / max(1, mask.size[1] - 1)
        g.putpixel((0, y), tuple(int(top[i] + (bot[i] - top[i]) * t)
                                 for i in range(3)) + (255,))
    out = Image.composite(g.resize(mask.size), out, mask)

    grown = mask.filter(ImageFilter.MaxFilter(2 * (R // 2) + 1))
    ring = Image.composite(grown, Image.new("L", mask.size, 0),
                           mask.point(lambda v: 255 - v))
    out = Image.composite(Image.new("RGBA", mask.size, STYLE["border"] + (255,)),
                          out, ring)

    d = ImageDraw.Draw(out)
    for i, ln in enumerate(lines):
        lw = int(font.getlength(ln))
        d.text(((w - lw) // 2, PADY + i * LH), ln, font=font,
               fill=STYLE["text"] + (255,))
    return out


def pet(d, cx, cy, s):
    d.ellipse([cx - s, cy - s * 1.15, cx + s, cy + s * 1.15], fill=(238, 238, 240),
              outline=(118, 118, 128), width=max(2, R // 2))
    d.ellipse([cx - s * .62, cy - s * .55, cx + s * .62, cy + s * .45],
              fill=(104, 99, 99))


def main():
    font = ImageFont.truetype(FONT, FS)
    tf = ImageFont.truetype(FONT, 20)
    variants = [
        ("方案一：2 行 · 断在标点（15 + 13）", split_at_punct(TEXT, 14)),
        ("方案二：3 行，每行 11 字（11 + 11 + 6）", split_per_char(TEXT, 11)),
    ]

    col, gap = 660, 40
    canvas = Image.new("RGBA", (col * 2 + gap * 3, 720), (233, 241, 251, 255))
    d = ImageDraw.Draw(canvas)

    for i, (name, lines) in enumerate(variants):
        x0 = gap + i * (col + gap)
        d.text((x0, 16), name, font=tf, fill=(28, 48, 78))
        d.text((x0, 44), " / ".join(f"{len(l)}字" for l in lines),
               font=tf, fill=(96, 116, 146))

        # 上排：2 倍放大看边缘
        b = draw_bubble(lines, font)
        big = b.resize((b.width * 2 // R, b.height * 2 // R), Image.LANCZOS)
        d.text((x0, 80), "2 倍放大（看圆角/描边/尾巴）", font=tf, fill=(96, 116, 146))
        y = 110
        if big.width > col - 20:
            big = big.resize((col - 20, big.height * (col - 20) // big.width),
                             Image.LANCZOS)
        canvas.alpha_composite(big, (x0 + (col - big.width) // 2, y))

        # 下排：1 倍真实尺寸 + 本体比例
        small = b.resize((b.width // R, b.height // R), Image.LANCZOS)
        y = 110 + big.height + 34
        d.text((x0, y), "1 倍真实尺寸（下面灰球=本体约 200px 宽）",
               font=tf, fill=(96, 116, 146))
        y += 30
        canvas.alpha_composite(small, (x0 + (col - small.width) // 2, y))
        pet(d, x0 + col // 2, y + small.height + 52, 52)
        d.text((x0, y + small.height + 118),
               f"气泡 {small.width}×{small.height}px ｜ 需要气泡带高 "
               f"{small.height + 1}px", font=tf, fill=(28, 48, 78))

    out = "/tmp/bubble_28.png"
    canvas.convert("RGB").save(out)
    print("saved", out, canvas.size)


if __name__ == "__main__":
    main()
