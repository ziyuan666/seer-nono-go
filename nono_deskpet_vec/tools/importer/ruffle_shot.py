#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""用 Ruffle（按 Flash 规范实现的开源播放器）渲染 SWF 并截帧。

目的：给「我们的光栅化」和「FFDec 的 PNG 导出」之间的分歧提供第三方判据。
不依赖 FFDec 的导出逻辑，只依赖 SWF 规范。

用法：python3 ruffle_shot.py <输出目录> <等待秒数列表,逗号分隔>
"""
import asyncio
import os
import sys

from playwright.async_api import async_playwright

URL = 'http://127.0.0.1:%s/index.html?swf=%s' % (
    os.environ.get('RSHOT_PORT', '8765'), os.environ.get('RSHOT_SWF', 'a2.swf'))


async def main():
    out_dir = sys.argv[1]
    waits = [float(x) for x in (sys.argv[2] if len(sys.argv) > 2 else '1.0').split(',')]
    os.makedirs(out_dir, exist_ok=True)
    async with async_playwright() as pw:
        # 容器里预装的 chromium 版本号与当前 playwright 期望的不一致（缺 1248），
        # 直接用已有的 chromium-1234 二进制，避免再下一份 150MB。
        exe = os.environ.get('CHROME_BIN') or None
        browser = await pw.chromium.launch(
            executable_path=exe,
            args=['--use-gl=swiftshader', '--enable-unsafe-swiftshader'])
        page = await browser.new_page(viewport={'width': 1000, 'height': 600})
        page.on('console', lambda m: print('  [console]', m.text[:160]))
        page.on('pageerror', lambda e: print('  [pageerror]', str(e)[:200]))
        await page.goto(URL, wait_until='load', timeout=60000)
        try:
            await page.wait_for_function('window.__ruffleReady === true', timeout=60000)
            print('ruffle 就绪')
        except Exception as e:
            print('等待就绪超时:', str(e)[:120])
        prev = 0.0
        for w in waits:
            await asyncio.sleep(max(0.0, w - prev))
            prev = w
            p = os.path.join(out_dir, 't%.2f.png' % w)
            await page.screenshot(path=p, clip={'x': 0, 'y': 0, 'width': 960, 'height': 560})
            print('截图 t=%.2fs -> %s' % (w, p))
        await browser.close()


if __name__ == '__main__':
    asyncio.run(main())
