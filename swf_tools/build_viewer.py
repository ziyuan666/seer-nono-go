#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""build_viewer.py —— 把 swfcore.js 内联进 HTML 模板，产出一个自包含的播放器。

产出：nono_swf_viewer.html（单文件，双击即可用，不需要 Python / FFDec / 网络）
"""

import os

HERE = os.path.dirname(os.path.abspath(__file__))
CORE = os.path.join(HERE, 'swfcore.js')
TPL = os.path.join(HERE, 'viewer_template.html')
OUT = os.path.join(HERE, 'nono_swf_viewer.html')


def main():
    core = open(CORE, encoding='utf-8').read()
    tpl = open(TPL, encoding='utf-8').read()
    html = tpl.replace('/*__SWFCORE__*/', core)
    open(OUT, 'w', encoding='utf-8').write(html)
    print(f'{OUT}  {len(html) / 1024:.1f} KB')


if __name__ == '__main__':
    main()
