#!/usr/bin/env python3
"""从 NewNonoInfoPanel.swf 生成「基本资料」面板素材：干净底图 + 全部动态件贴图。

  之前的做法是「渲染整帧当底图 → 再用放大位图 + 白底去盖掉不该有的东西」，
  属性上就是错的：遮盖不是状态切换，文字被盖掉、放大贴图发糊。
  正确做法与原版 display list 一致——底图只放静态件，动态件全部按状态画：

  NewNonoBaseInfoMc (DefineSprite_137) 的子实例（depth/cid/面板坐标 twips÷20）：
    1   bgMc        14    (0,0)          2 帧：1=普通灰、2=超能青
    3   (内嵌白框)  17    (6.45,37)      静态
    7   mc1         26    (15.55,177)    智慧值进度条 → 代码画
    10  mc2         28    (15.55,196.45) 能量值进度条 → 代码画
    13  mc3         30    (15.55,214.7)  同步率进度条 → 代码画
    16  mc0         32    (15.6,158)     超能值进度条 → 代码画
    19  faceMc      84    (1.55,27.65)   头像 → 代码画（77+82 / 66+70）
    44  (加载芯片)  86    (17.55,235.7)  静态分隔线
    51  (昵 称)     90    (99.05,43.25)  静态标签（文字转形状 shape）
    52  sp1         94    (98.05,84.4)   只有「AI 等级:」标签，值由代码画
    55  sp2         97    (99.05,102.4)  只有「主　人:」标签
    58  sp3         100   (99.05,120.9)  只有「生　日:」标签
    61  sp0         118   (99.05,65.75)  10 帧「超能等级: + 球N翼」整帧
    77  changeMc    120   (249.6,42.1)   扳手
    79  sucMc       122   (246.4,43.25)  √
    81  superIconMc 126   (66.6,258.2)   超能芯片行容器 → 代码画
    84  iconMc      127   (36.6,296.6)   普通芯片行容器 → 代码画
    86  dragMc      129   (0,0)          拖拽区（遮罩，无图形）
    88  closeBtn    133   (262.35,7.25)  关闭
    93  nickBgMc    135   (159,39)       昵称高亮底 → 代码画 alpha
    95  nickTxt     136   (160.8,43.6)   昵称值 → 代码画

  所以底图 = 14(帧1或2) + 17 + 86 + 90，其余一律贴图/代码。
  导出全部用 -zoom 4（矢量按 4 倍原生栅格化），代码按实际显示尺寸缩放，不糊。

产物（assets/ui/）：
  info_base_plain.png  info_base_super.png
  info_lbl_ai.png      info_lbl_owner.png   info_lbl_birth.png
  info_lv_1..10.png
  info_wrench.png      info_ok.png
  info_close.png       info_close_over.png  info_close_down.png
  info_nor_body.png    info_nor_eye_1..4.png
  info_super_body_1..4.png  info_super_eye_1..3.png

用法：python3 tools/mk_info_assets.py [--swf 路径]
"""
import argparse
import glob
import os
import re
import shutil
import subprocess
import sys
import tempfile

from PIL import Image

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
UI = os.path.join(ROOT, 'assets', 'ui')
FFDEC = os.path.join(os.path.dirname(ROOT), 'tools', 'ffdec', 'ffdec-cli.jar')
DEFAULT_SWF = os.path.join(os.path.dirname(ROOT), 'gameres_deploy', 'gameres', 'root',
                           'module', 'com', 'robot', 'module', 'app', 'NewNonoInfoPanel.swf')

# 137 里的放置坐标（面板像素 = twips/20）
#
# 注意：FFDec 导出的 sprite PNG 左上取的是该 sprite 的 bounds 左上，不等于
# sprite 原点。所以「贴图左上」= DL 坐标 + bounds.min（= tools/calibrate_info_anchor.py
# 在 137 整帧图里实测出来的偏移 ÷4）。下面这些是实测修正后的贴图坐标，不是 DL 坐标。
POS_BG = (0.0, 0.0)
POS_INNER = (6.45 + 5.25, 37.0 + 5.0)        # cid 17  DL(6.45,37)    + (5.25,5.0)
POS_CHIPLINE = (17.55 + 4.0, 235.7 + 5.25)   # cid 86  DL(17.55,235.7) + (4.0,5.25)
POS_NICKLBL = (99.05, 43.25)  # cid 90 是 shape 导出（cairosvg 渲染，无 padding）
PANEL_W, PANEL_H = 298.0, 350.0

# 四条进度条左侧的静态标签（mc0..mc3 里 depth1 的 shape，mc 内局部 (0,0)）。
# 值 = 该 mc 在 137 里的 DL 坐标；shape 自身 bounds 由 svg 的 matrix 反解（见 shape_png）。
BAR_LABELS = [
    (31, (15.60, 158.00)),   # mc0 超能值:
    (18, (15.55, 177.00)),   # mc1 智慧值:
    (27, (15.55, 196.45)),   # mc2 能量值:
    (29, (15.55, 214.70)),   # mc3 亲密度:
]
# 超能芯片行的半透明青底条（superIconMc(126) 的 depth1 = shape 123，fill #00ffff a=.5）
CHIPBAR_CID = 123
CHIPBAR_DL = (66.60, 258.20)  # superIconMc 在 137 里的位置

# 芯片图标：原版 ChipInfoBgPanel.addIcon 加载 resource/nono/icon/{id}.swf，
# 图标美术自带 27x27 青白圆角框。旧 chips/*.png 是整帧导出的错误裁剪（84x52 等，
# 带空边且无框），uiFit 压到 25x25 后框和比例一起没了。
ICON_SRC = os.path.join(os.path.dirname(ROOT), 'docs', 'NONO形态与动作_20261021',
                        '资源', 'nono', 'icon')


def ffdec_export(swf, ids, outdir, zoom=4):
    os.makedirs(outdir, exist_ok=True)
    cmd = ['java', '-jar', FFDEC, '-zoom', str(zoom), '-selectid', ','.join(str(i) for i in ids),
           '-export', 'sprite', outdir, swf]
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        print(r.stdout[-2000:], r.stderr[-2000:])
        raise SystemExit('ffdec sprite 导出失败 ids=%s' % ids)


def ffdec_export_button(swf, bid, outdir, zoom=4):
    """Button 类型（closeBtn=DefineButton2_133）用 button 导出，产物是 1_up/2_over/3_down 状态图。"""
    os.makedirs(outdir, exist_ok=True)
    cmd = ['java', '-jar', FFDEC, '-zoom', str(zoom), '-selectid', str(bid),
           '-export', 'button', outdir, swf]
    subprocess.run(cmd, capture_output=True, text=True)
    d = os.path.join(outdir, 'DefineButton2_%d' % bid)
    out = {}
    if os.path.isdir(d):
        for f in sorted(os.listdir(d)):
            if f.endswith('.png') and f.split('_')[-1][:-4] in ('up', 'over', 'down'):
                out[f.split('_')[-1][:-4]] = Image.open(os.path.join(d, f)).convert('RGBA')
    if not out:
        raise SystemExit('button %d 导出失败' % bid)
    return out


def shade(im, k):
    """按系数整体调明暗，保持 alpha 不变（给按钮派生 over/down，原版没有这两态）。"""
    out = im.copy()
    px = out.load()
    for y in range(out.height):
        for x in range(out.width):
            r, g, b, a = px[x, y]
            if a == 0:
                continue
            px[x, y] = (min(255, int(r * k)), min(255, int(g * k)), min(255, int(b * k)), a)
    return out


def ffdec_shape_svg(swf, sid, outdir):
    os.makedirs(outdir, exist_ok=True)
    cmd = ['java', '-jar', FFDEC, '-selectid', str(sid), '-export', 'shape', outdir, swf]
    subprocess.run(cmd, capture_output=True, text=True)
    p = os.path.join(outdir, '%d.svg' % sid)
    if not os.path.exists(p):
        raise SystemExit('ffdec shape 导出失败 id=%d' % sid)
    return p


def shape_png(swf, sid, outdir):
    """shape → 4x PNG，并反解它在局部坐标系里的原点。

    FFDec 的 shape SVG 会把 bounds 平移进画布：matrix(4,0,0,4,tx,ty)，
    tx = (-bounds.x + 1) * 4 ⇒ png 左上对应局部 (bounds.x-1, bounds.y-1)
    ⇒ 局部原点落在 png 内的 (1-bx)*4 处，即返回 (img, bx, by) 后按
    「贴图左上 = DL + (bx-1, by-1)」放置。
    """
    import cairosvg
    outdir = os.path.join(outdir, 's%d' % sid)
    os.makedirs(outdir, exist_ok=True)
    cmd = ['java', '-jar', FFDEC, '-zoom', '4', '-selectid', str(sid),
           '-export', 'shape', outdir, swf]
    subprocess.run(cmd, capture_output=True, text=True)
    p = os.path.join(outdir, '%d.svg' % sid)
    if not os.path.exists(p):
        raise SystemExit('ffdec shape 导出失败 id=%d' % sid)
    txt = open(p, encoding='utf-8').read()
    m = re.search(r'matrix\(\s*([-\d.]+),\s*[-\d.]+,\s*[-\d.]+,\s*[-\d.]+,\s*([-\d.]+),\s*([-\d.]+)\)', txt)
    if not m:
        raise SystemExit('shape %d 的 svg 里没找到 matrix' % sid)
    tx, ty = float(m.group(2)), float(m.group(3))
    out = os.path.join(outdir, '%d.png' % sid)
    cairosvg.svg2png(url=p, write_to=out)
    return Image.open(out).convert('RGBA'), 1.0 - tx / 4.0, 1.0 - ty / 4.0


def frames(outdir, sid):
    """取某个 sprite 的各帧 PNG，按帧号排序。

    目录名有两种形态：DefineSprite_118 和 DefineSprite_118__fla.xxx（带符号名），
    也可能是 DefineSprite_137_NewNonoBaseInfoMc_NewNonoBaseInfoMc，所以按
    「DefineSprite_<id>」后跟非数字边界来匹配。
    """
    best, hit = None, []
    for name in os.listdir(outdir):
        d = os.path.join(outdir, name)
        if not os.path.isdir(d):
            continue
        if name == 'DefineSprite_%d' % sid or re.match(r'^DefineSprite_%d(_|$)' % sid, name):
            got = sorted([f for f in os.listdir(d) if f.endswith('.png')],
                         key=lambda f: int(os.path.splitext(f)[0]))
            if got:
                best, hit = d, got
    if not best:
        raise SystemExit('没找到 sprite %d 的帧' % sid)
    return [Image.open(os.path.join(best, f)).convert('RGBA') for f in hit]


def save(im, name):
    p = os.path.join(UI, name)
    im.save(p)
    print('  %-28s %dx%d' % (name, im.width, im.height))


def export_chips(tmp):
    """重导芯片图标：resource/nono/icon/{id}.swf 的 shape → 4x PNG（27x27 → 108x108）。

    旧 chips/*.png 是整帧导出（84x52 等，带空边、不透明），uiFit 压到 25x25 后
    比例失真、并把图标美术自带的青白圆角框一起裁没了。
    """
    import cairosvg
    ids = sorted(int(os.path.splitext(f)[0]) for f in os.listdir(ICON_SRC)
                 if f.endswith('.swf'))
    for cid in ids:
        swf = os.path.join(ICON_SRC, '%d.swf' % cid)
        d = os.path.join(tmp, 'chip%d' % cid)
        os.makedirs(d, exist_ok=True)
        subprocess.run(['java', '-jar', FFDEC, '-export', 'shape', d, swf],
                       capture_output=True, text=True)
        svgs = sorted(glob.glob(os.path.join(d, '*.svg')))
        if not svgs:
            print('  跳过 %d：swf 里没有 shape' % cid)
            continue
        png = os.path.join(d, '%d.png' % cid)
        cairosvg.svg2png(url=svgs[0], write_to=png, scale=4)
        save(Image.open(png).convert('RGBA'), os.path.join('chips', '%d.png' % cid))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--swf', default=DEFAULT_SWF)
    args = ap.parse_args()
    if not os.path.exists(args.swf):
        raise SystemExit('SWF 不存在: %s' % args.swf)

    tmp = tempfile.mkdtemp(prefix='infoassets_')
    try:
        ffdec_export(args.swf, [14, 17, 86, 89, 20, 22, 135, 94, 97, 100, 118, 120, 122, 77, 82, 66, 70, 125], tmp)
        svg90 = ffdec_shape_svg(args.swf, 90, os.path.join(tmp, 's90'))

        # 四条进度条标签（「超能值:/智慧值:/能量值:/亲密度:」）与超能芯片行青底条：
        # 都是静态件，直接烘进底图，代码只画条和芯片图标。
        bar_lbl = {sid: shape_png(args.swf, sid, os.path.join(tmp, 'bl'))
                   for sid, _ in BAR_LABELS}
        chipbar = shape_png(args.swf, CHIPBAR_CID, os.path.join(tmp, 'cb'))

        bg1, bg2 = frames(tmp, 14)[:2]
        inner = frames(tmp, 17)[0]
        chipline = frames(tmp, 86)[0]

        import cairosvg
        png90 = os.path.join(tmp, 's90.png')
        cairosvg.svg2png(url=svg90, write_to=png90, scale=4)
        lbl_nick = Image.open(png90).convert('RGBA')

        # 左右翻页箭头（leftMc/rightMc）在 137 里带青色发光滤镜，sprite 89 单独导出
        # 没有滤镜、尺寸也不一样。这两个控件外观不随状态变化，直接从 137 整帧裁下
        # （含光晕）合成进底图，避免"贴图缺光晕 + 位置偏 5px"。
        ffdec_export(args.swf, [137], os.path.join(tmp, 'f137'))
        f137 = frames(os.path.join(tmp, 'f137'), 137)[0]
        k2 = f137.width / PANEL_W

        def crop_panel(x0, y0, x1, y1):
            return f137.crop((int(x0 * k2), int(y0 * k2), int(x1 * k2), int(y1 * k2)))

        arrowL = crop_panel(13, 285, 34, 310)
        arrowR = crop_panel(264, 285, 285, 310)
        save(arrowL, 'info_arrow_left.png')
        save(arrowR, 'info_arrow.png')

        # superIconMc 里的「超能芯片」徽章 cid125（name="nono"）。旧做法是从 137
        # 整帧裁，会把半透明青底一起裁进来、叠在底图青条上变双层；改为单独导出
        # 125（透明背景），贴图左上由模板匹配实测 = (31.25, 256.75)。
        chipNono = frames(tmp, 125)[0]
        POS_CHIPNONO = (31.25, 256.75)
        save(chipNono, 'info_chipnono.png')

        # 底图坐标比例：FFDec 导出的 bg 像素宽 / 面板宽（≈4）
        kx = bg1.width / PANEL_W
        ky = bg1.height / PANEL_H

        def compose(bg):
            c = Image.new('RGBA', bg.size, (0, 0, 0, 0))
            c.alpha_composite(bg, (0, 0))
            c.alpha_composite(inner, (int(POS_INNER[0] * kx), int(POS_INNER[1] * ky)))
            c.alpha_composite(chipline, (int(POS_CHIPLINE[0] * kx), int(POS_CHIPLINE[1] * ky)))
            # 「昵称:」不再烘进底图：它与其它三行标签同属静态文字，但烘焙后
            # 想与 AI/主人/生日 对齐只能整体重导底图。改为运行时贴图
            # （info_lbl_nick.png），三行用同一个 X，视觉才是一条竖线。
            # c.alpha_composite(lbl_nick, (int(POS_NICKLBL[0] * kx), int(POS_NICKLBL[1] * ky)))
            c.alpha_composite(arrowL, (int(13 * kx), int(285 * ky)))
            c.alpha_composite(arrowR, (int(264 * kx), int(285 * ky)))
            # 层级照原版：superIconMc 里 depth1=cid123 青底条，depth2=cid125 NONO 图标
            # ⇒ 先贴青条，再贴 NONO，否则半透明青会把图标染青。
            im, bx, by = chipbar
            c.alpha_composite(im, (int((CHIPBAR_DL[0] + bx - 1) * kx),
                                   int((CHIPBAR_DL[1] + by - 1) * ky)))
            c.alpha_composite(chipNono, (int(POS_CHIPNONO[0] * kx), int(POS_CHIPNONO[1] * ky)))
            for sid, (dx, dy) in BAR_LABELS:
                im, bx, by = bar_lbl[sid]
                c.alpha_composite(im, (int((dx + bx - 1) * kx), int((dy + by - 1) * ky)))
            return c

        print('底图:')
        save(compose(bg1), 'info_base_plain.png')
        save(compose(bg2), 'info_base_super.png')

        print('标签（只含文字，值由代码画）:')
        save(lbl_nick, 'info_lbl_nick.png')
        save(frames(tmp, 94)[0], 'info_lbl_ai.png')
        save(frames(tmp, 97)[0], 'info_lbl_owner.png')
        save(frames(tmp, 100)[0], 'info_lbl_birth.png')

        print('超能等级整帧（含「超能等级:」标签）:')
        for i, im in enumerate(frames(tmp, 118), 1):
            save(im, 'info_lv_%d.png' % i)

        print('改名按钮（over/down 由 4x up 派生：原版只有单一状态）:')
        wrench = frames(tmp, 120)[0]
        save(wrench, 'info_wrench.png')
        save(shade(wrench, 1.12), 'info_wrench_over.png')
        save(shade(wrench, 0.86), 'info_wrench_down.png')
        ok = frames(tmp, 122)[0]
        save(ok, 'info_ok.png')
        save(shade(ok, 1.12), 'info_ok_over.png')
        save(shade(ok, 0.86), 'info_ok_down.png')

        print('关闭按钮（DefineButton2_133 三态）:')
        btns = ffdec_export_button(args.swf, 133, os.path.join(tmp, 'btn133'))
        save(btns['up'], 'info_close.png')
        save(btns['over'], 'info_close_over.png')
        save(btns['down'], 'info_close_down.png')

        print('进度条（mc 内：底框 cid22 / 填充 cid20 可 scaleX）:')
        save(frames(tmp, 22)[0], 'info_bar_bg.png')
        save(frames(tmp, 20)[0], 'info_bar_fill.png')

        print('芯片图标（resource/nono/icon/*.swf，自带青白圆角框）:')
        export_chips(tmp)

        print('昵称编辑高亮底（nickBgMc，编辑时 alpha 0.3）:')
        save(frames(tmp, 135)[0], 'info_nickbg.png')

        print('普通头像 norMc = 77 + eyeMc(82 四帧):')
        save(frames(tmp, 77)[0], 'info_nor_body.png')
        for i, im in enumerate(frames(tmp, 82), 1):
            save(im, 'info_nor_eye_%d.png' % i)

        print('超能头像 superMc = bgMc(66 四帧) + eyeMc(70 三帧):')
        for i, im in enumerate(frames(tmp, 66), 1):
            save(im, 'info_super_body_%d.png' % i)
        for i, im in enumerate(frames(tmp, 70), 1):
            save(im, 'info_super_eye_%d.png' % i)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    print('完成。SWF=%s' % args.swf)


if __name__ == '__main__':
    sys.exit(main())
