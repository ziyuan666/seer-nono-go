#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""regress_all.py —— 全部 SWF 的 shape 渲染回归（自研解析 vs FFDec SVG）。

参考 SVG 由 export_ref_all.py 现场导出到 /tmp/ffall，目录名 = SWF 相对路径把
'/' 换成 '__'（1:1 对应，不猜命名规则）。

用法：
    python3 regress_all.py --tag after                # 面遍历开启（默认）
    SWF_FACES=0 python3 regress_all.py --tag before   # 旧串链基线
    python3 regress_all.py --diff before after        # 逐 SWF 比较
"""
import argparse
import json
import os
import re
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
SWF_ROOT = os.path.join(ROOT, 'source_swf', '资源', 'nono')
REF_ROOT = '/tmp/ffall'


def safe(rel):
    return rel.replace('/', '__').replace('.swf', '')


def list_swfs():
    out = []
    for dirpath, _d, files in os.walk(SWF_ROOT):
        for f in files:
            if f.endswith('.swf'):
                out.append(os.path.relpath(os.path.join(dirpath, f), SWF_ROOT))
    return sorted(out)


def run_one(rel):
    ref = os.path.join(REF_ROOT, safe(rel))
    if not os.path.isdir(ref):
        return rel, None, '参考目录缺失'
    swf = os.path.join(SWF_ROOT, rel)
    jf = '/tmp/regress_one_%s.json' % safe(rel)
    p = subprocess.run([sys.executable, os.path.join(HERE, 'verify_render.py'),
                        '--swf', swf, '--svg-dir', ref, '--verbose',
                        '--json-out', jf],
                       capture_output=True, text=True, timeout=1800)
    if not os.path.exists(jf):
        return rel, None, '输出异常 %s' % (p.stdout[-100:] or p.stderr[-100:])
    d = json.load(open(jf))
    os.unlink(jf)
    d['fails'] = {int(k): v for k, v in d['fails'].items()}
    return rel, d, None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--tag', default='after')
    ap.add_argument('--jobs', type=int, default=6)
    ap.add_argument('--diff', nargs=2, metavar=('BEFORE', 'AFTER'))
    args = ap.parse_args()

    if args.diff:
        a = json.load(open('/tmp/regress_%s.json' % args.diff[0], encoding='utf-8'))
        b = json.load(open('/tmp/regress_%s.json' % args.diff[1], encoding='utf-8'))
        worse, better, newfail = [], [], []
        for k in sorted(set(a) & set(b)):
            va, vb = a[k], b[k]
            if not va or not vb:
                continue
            fa, fb = set(va['fails']), set(vb['fails'])
            if fb - fa:
                newfail.append((k, sorted(fb - fa), va, vb))
            if vb['ok'] < va['ok']:
                worse.append((k, va['ok'], vb['ok']))
            elif vb['ok'] > va['ok']:
                better.append((k, va['ok'], vb['ok']))
        print('新增失败 SWF %d 个；通过数变差 %d 个，变好 %d 个'
              % (len(newfail), len(worse), len(better)))
        for k, cids, va, vb in newfail[:15]:
            print('  ! %-34s 新失败 cid %s' % (k, cids))
            for c in cids[:4]:
                print('      cid %-4d %.3f -> %.3f' % (c, va['fails'].get(c, 0), vb['fails'].get(c, 0)))
        for k, x, y in better[:15]:
            print('  ↑ %-34s %d -> %d' % (k, x, y))
        ta = sum(v['ok'] for v in a.values() if v)
        tb = sum(v['ok'] for v in b.values() if v)
        ba = sum(v['bad'] for v in a.values() if v)
        bb = sum(v['bad'] for v in b.values() if v)
        print('合计通过 %d -> %d，失败 %d -> %d' % (ta, tb, ba, bb))
        return

    results = {}
    swfs = list_swfs()

    def work(rel):
        return run_one(rel)

    with ThreadPoolExecutor(max_workers=args.jobs) as ex:
        for rel, data, err in ex.map(work, swfs):
            results[rel] = data
            if err:
                print('%-40s %s' % (rel, err))
            else:
                print('%-40s 通过 %3d 失败 %2d 无SVG %3d'
                      % (rel, data['ok'], data['bad'], data['nosvg']))
            sys.stdout.flush()
    out = '/tmp/regress_%s.json' % args.tag
    json.dump(results, open(out, 'w'), indent=1)
    tok = sum(v['ok'] for v in results.values() if v)
    tbad = sum(v['bad'] for v in results.values() if v)
    tnosvg = sum(v['nosvg'] for v in results.values() if v)
    print('\n合计: 通过 %d / 失败 %d / 无SVG %d -> %s' % (tok, tbad, tnosvg, out))


if __name__ == '__main__':
    main()
