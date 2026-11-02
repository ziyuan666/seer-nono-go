#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""用 Chromium 按 SVG 规范渲染 FFDec 导出的 shape SVG，作为渐变坐标语义的判据。

为什么需要它：FFDec 的 PNG 导出和 Ruffle 都显示「小星芒」，而我们按 SWF 规范
（渐变空间 → shape 坐标空间）算出来是「大光斑」。SVG 里 path 在 <g transform> 内、
gradientUnits="userSpaceOnUse" 规定渐变坐标是「引用元素的用户坐标系」，用标准
SVG 渲染器跑一遍就能确定 FFDec 的表达意图到底是哪一个。
"""
import asyncio
import os
import sys

from playwright.async_api import async_playwright


async def shot(svg_path, out_png, zoom=8):
    html = f"""<!DOCTYPE html><html><body style="margin:0;background:#999">
<img src="file://{svg_path}" style="width:{47.6*zoom}px;height:{47.65*zoom}px;
     image-rendering:pixelated;display:block">
</body></html>"""
    tmp = '/tmp/svgview.html'
    with open(tmp, 'w', encoding='utf-8') as f:
        f.write(html)
    async with async_playwright() as pw:
        exe = os.environ.get('CHROME_BIN') or None
        b = await pw.chromium.launch(executable_path=exe,
                                     args=['--allow-file-access-from-files'])
        p = await b.new_page(viewport={'width': int(47.6 * zoom) + 8,
                                       'height': int(47.65 * zoom) + 8})
        await p.goto('file://' + tmp, wait_until='load')
        await asyncio.sleep(1.0)
        await p.screenshot(path=out_png)
        await b.close()
    print('已渲染 ->', out_png)


if __name__ == '__main__':
    asyncio.run(shot(sys.argv[1], sys.argv[2], int(sys.argv[3]) if len(sys.argv) > 3 else 8))
