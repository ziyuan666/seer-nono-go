# -*- coding: utf-8 -*-
"""sprite.json 的 02 量化：帧数据数组化。

01（可读）：
  {"directions":{"default":{"frames":[{"parts":[
      {"part":"shape_46","layer":"L","matrix":[a,b,c,d,e,f],"alpha":1.0}]}]}}}

02（紧凑）：
  {"pt":["shape_46",...], "ly":[[name,z,tintable],...],
   "d":{"default":[[ [partIdx,layerIdx,ma,mb,mc,md,me,mf,alpha255], ... ], ...]}}

矩阵乘 65536（16.16 定点，原值本就是定点数），alpha 乘 255。
canvas / colors / name 原样保留，避免改动跨形态对齐标定。
"""
import json
import os
import sys


def qmat(m):
    return [int(round(v * 65536)) for v in m]


def quantize_sprite(obj, parts_index):
    layers = obj.get('layers', [])
    ly = [{'n': l.get('name', ''), 'z': int(l.get('z', 0)), 't': 1 if l.get('tintable') else 0}
          for l in layers]
    layer_idx = {}
    for i, l in enumerate(layers):
        layer_idx[l.get('name', '')] = i

    dirs = {}
    for dname, d in obj.get('directions', {}).items():
        frames = []
        for fr in d.get('frames', []):
            row = []
            for p in fr.get('parts', []):
                pname = p.get('part', '')
                if pname not in parts_index:
                    parts_index[pname] = len(parts_index)
                row.append([
                    parts_index[pname],
                    layer_idx.get(p.get('layer', ''), 0),
                    *qmat(p.get('matrix', [1, 0, 0, 1, 0, 0])),
                    int(round(float(p.get('alpha', 1.0)) * 255)),
                ])
            frames.append(row)
        dirs[dname] = frames

    out = {
        'f': obj.get('format', 1),
        'n': obj.get('name', ''),
        'c': obj.get('canvas', {}),
    }
    if obj.get('colors'):
        out['col'] = obj['colors']
    out['ly'] = ly
    out['d'] = dirs
    return out


def main(base_in, base_out):
    # 第一遍：收集所有 sprite 用到的 part 名（每个动画目录各自一张表）
    total_src = total_dst = 0
    n = 0
    for root, _dirs, files in os.walk(base_in):
        if 'sprite.json' not in files:
            continue
        p = os.path.join(root, 'sprite.json')
        obj = json.load(open(p, encoding='utf-8'))
        parts_index = {}
        q = quantize_sprite(obj, parts_index)
        q['pt'] = [None] * len(parts_index)
        for name, i in parts_index.items():
            q['pt'][i] = name
        rel = os.path.relpath(p, base_in)
        dst = os.path.join(base_out, rel)
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        txt = json.dumps(q, ensure_ascii=False, separators=(',', ':'))
        with open(dst, 'w', encoding='utf-8') as f:
            f.write(txt)
        total_src += os.path.getsize(p)
        total_dst += len(txt.encode('utf-8'))
        n += 1
    print('sprite 量化 %d 个：%.2fMB → %.2fMB（%.1f%%）' %
          (n, total_src / 1e6, total_dst / 1e6, total_dst * 100.0 / total_src if total_src else 0))


if __name__ == '__main__':
    main(sys.argv[1], sys.argv[2])
