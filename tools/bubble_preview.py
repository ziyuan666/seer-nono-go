#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""气泡样式预览：三种候选 × 短/长台词。
渲染 4 倍超采样，输出 2 倍尺寸（真机 16px 字 → 预览 32px），圆角/尾巴/描边都看得清。
只用于选样式，不参与打包。"""
from PIL import Image, ImageDraw, ImageFont, ImageFilter

R = 4            # 渲染超采样倍数
DISP = 2         # 输出相对真机的倍数
FS = 16 * R      # 真机字号 16
PADX, PADY = 14 * R, 8 * R
RAD = 10 * R     # 圆角 10px
TAIL_W, TAIL_H = 9 * R, 7 * R
FONT = "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc"

SHORT = "太好了"
LONG = "主人，让我陪你一块参加精灵战斗吧！"

STYLES = [
    ("A 扁平白 · 对齐赛尔号 UI",
     dict(fill=(255, 255, 255), grad=None, border=(196, 208, 226),
          text=(43, 58, 85), shadow=None)),
    ("B 浮起 · 白底 + 柔和投影",
     dict(fill=(255, 255, 255), grad=None, border=(210, 220, 234),
          text=(43, 58, 85), shadow=(24, 52, 100, 80))),
    ("C 淡蓝渐变 · 赛尔号主色",
     dict(fill=(243, 248, 255), grad=((246, 250, 255), (214, 232, 255)),
          border=(160, 194, 238), text=(24, 58, 104), shadow=None)),
]


def make_mask(w, h, r, tw, th):
    m = Image.new("L", (w, h + th), 0)
    d = ImageDraw.Draw(m)
    d.rounded_rectangle([0, 0, w - 1, h - 1], radius=r, fill=255)
    cx = w // 2
    d.polygon([(cx - tw // 2, h - 3), (cx + tw // 2, h - 3),
               (cx, h + th - 1)], fill=255)
    return m


def draw_bubble(text, st, font):
    tw = int(font.getlength(text))
    w, h = tw + PADX * 2, FS + PADY * 2
    mask = make_mask(w, h, RAD, TAIL_W, TAIL_H)
    out = Image.new("RGBA", mask.size, (0, 0, 0, 0))

    if st["shadow"]:
        sh = mask.filter(ImageFilter.GaussianBlur(4 * R))
        sh = sh.transform(sh.size, Image.AFFINE, (1, 0, 0, 0, 1, -2 * R))
        out = Image.composite(Image.new("RGBA", mask.size, st["shadow"]), out, sh)

    body = Image.new("RGBA", mask.size, st["fill"] + (255,))
    if st["grad"]:
        top, bot = st["grad"]
        g = Image.new("RGBA", (1, mask.size[1]))
        for y in range(mask.size[1]):
            t = y / max(1, mask.size[1] - 1)
            g.putpixel((0, y), tuple(int(top[i] + (bot[i] - top[i]) * t)
                                     for i in range(3)) + (255,))
        body = g.resize(mask.size)
    out = Image.composite(body, out, mask)

    # 描边：外扩 1px 的环，颜色统一
    grown = mask.filter(ImageFilter.MaxFilter(2 * (R // 2) + 1))
    ring = Image.composite(grown, Image.new("L", mask.size, 0),
                           mask.point(lambda v: 255 - v))
    out = Image.composite(Image.new("RGBA", mask.size, st["border"] + (255,)),
                          out, ring)

    d = ImageDraw.Draw(out)
    d.text((PADX, PADY), text, font=font, fill=st["text"] + (255,))
    return out


def pet(d, cx, cy, s):
    d.ellipse([cx - s, cy - s, cx + s, cy + s], fill=(238, 238, 240),
              outline=(118, 118, 128), width=max(2, R // 2))
    d.ellipse([cx - s * .62, cy - s * .55, cx + s * .62, cy + s * .45],
              fill=(104, 99, 99))


def main():
    font = ImageFont.truetype(FONT, FS)
    tf = ImageFont.truetype(FONT, 20)
    col = 640
    gap = 40
    canvas = Image.new("RGBA", (col * 3 + gap * 4, 760), (233, 241, 251, 255))
    d = ImageDraw.Draw(canvas)

    for i, (name, st) in enumerate(STYLES):
        x0 = gap + i * (col + gap)
        d.text((x0, 18), name, font=tf, fill=(28, 48, 78))
        y = 58
        for label, txt in (("短台词（3 字）", SHORT), ("长台词（16 字）", LONG)):
            d.text((x0, y), label, font=tf, fill=(96, 116, 146))
            y += 30
            b = draw_bubble(txt, st, font)
            b = b.resize((b.width * DISP // R, b.height * DISP // R), Image.LANCZOS)
            canvas.alpha_composite(b, (x0 + (col - b.width) // 2, y))
            y += b.height + 6
            pet(d, x0 + col // 2, y + 44, 36)
            y += 96

    out = "/tmp/bubble_styles.png"
    canvas.convert("RGB").save(out)
    print("saved", out, canvas.size)


if __name__ == "__main__":
    main()
