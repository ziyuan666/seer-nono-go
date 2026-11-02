/*
 * swfcore.js —— 赛尔号 SWF 浏览器端解析核心
 *
 * 从 nono_deskpet_vec/tools/importer/{swfread,swfshape}.py 移植，口径逐条对齐：
 *   - 位流读取、MATRIX/CXFORM/FILLSTYLE/LINESTYLE 的版本差异（RGB vs RGBA）
 *   - SHAPERECORD：MoveDelta 是绝对坐标、StateNewStyles 要 align 并重置 pen
 *   - 填充语义：一条边同时属于 fill0 和 fill1（fill1 要反向），否则细长条会碎成两块
 *   - 子路径串链：端点相接才串，已闭合的链不再焊新段（否则同心环挖空失效）
 *   - Shape4 的 UsesFillWindingRule → nonzero
 *
 * 未移植：面遍历（_faces，依赖 Python 的 polyunion/faces 模块）。串链遇到 T 型接头
 * 会补一条近路直线，个别 shape 可能缺块；赛尔号动作 SWF 实测未见明显问题。
 */
(function (global) {
  'use strict';

  // ---------------- 位读取 ----------------
  class BR {
    constructor(buf, bytePos) {
      this.b = buf;
      this.bit = (bytePos || 0) * 8;
    }
    ub(n) {
      let v = 0;
      for (let i = 0; i < n; i++) {
        v = (v << 1) | ((this.b[this.bit >> 3] >> (7 - (this.bit & 7))) & 1);
        this.bit++;
      }
      return v >>> 0;
    }
    sb(n) {
      if (n === 0) return 0;
      const v = this.ub(n);
      return (v & (1 << (n - 1))) ? v - (1 << n) : v;
    }
    align() { this.bit = Math.ceil(this.bit / 8) * 8; }
    get bytepos() { return this.bit >> 3; }
    seek(p) { this.bit = p * 8; }
    u8() { const p = this.bytepos; this.bit += 8; return this.b[p]; }
    u16() {
      const p = this.bytepos; this.bit += 16;
      return this.b[p] | (this.b[p + 1] << 8);
    }
    u32() {
      const p = this.bytepos; this.bit += 32;
      return (this.b[p] | (this.b[p + 1] << 8) | (this.b[p + 2] << 16) | (this.b[p + 3] << 24)) >>> 0;
    }
    rgb() {
      const p = this.bytepos; this.bit += 24;
      return [this.b[p], this.b[p + 1], this.b[p + 2]];
    }
    rgba() {
      const p = this.bytepos; this.bit += 32;
      return [this.b[p], this.b[p + 1], this.b[p + 2], this.b[p + 3]];
    }
  }

  function readRect(br) {
    const n = br.ub(5);
    return [br.sb(n), br.sb(n), br.sb(n), br.sb(n)]; // xmin,xmax,ymin,ymax (twips)
  }

  function readMatrix(br) {
    let sx = 1, sy = 1;
    if (br.ub(1)) {
      const n = br.ub(5);
      sx = n ? br.sb(n) / 65536 : 0;
      sy = n ? br.sb(n) / 65536 : 0;
    }
    let r0 = 0, r1 = 0;
    if (br.ub(1)) {
      const n = br.ub(5);
      r0 = n ? br.sb(n) / 65536 : 0;
      r1 = n ? br.sb(n) / 65536 : 0;
    }
    const n = br.ub(5);
    const tx = n ? br.sb(n) : 0;
    const ty = n ? br.sb(n) : 0;
    br.align();
    return { sx, r0, r1, sy, tx, ty };
  }

  function readCxform(br) {
    const hasAdd = br.ub(1), hasMul = br.ub(1), n = br.ub(4);
    const mul = [1, 1, 1, 1], add = [0, 0, 0, 0];
    if (hasMul) for (let i = 0; i < 4; i++) mul[i] = n ? br.sb(n) / 256 : 1;
    if (hasAdd) for (let i = 0; i < 4; i++) add[i] = n ? br.sb(n) : 0;
    br.align();
    return { mul, add };
  }

  const hexc = (c) => '#' + [c[0], c[1], c[2]].map((v) => v.toString(16).padStart(2, '0')).join('');

  function shapeVersionOf(tag) {
    return { 2: 1, 22: 2, 32: 3, 83: 4, 46: 3, 84: 4 }[tag] || 3;
  }

  function readGradient(br, sv, focal) {
    const spread = br.ub(2), interp = br.ub(2), n = br.ub(4);
    const stops = [];
    for (let i = 0; i < n; i++) {
      const ratio = br.u8();
      const col = sv >= 3 ? br.rgba() : br.rgb().concat([255]);
      stops.push({
        offset: Math.round((ratio / 255) * 1e4) / 1e4,
        color: hexc(col),
        opacity: Math.round((col[3] / 255) * 1e4) / 1e4,
      });
    }
    let focalPoint = null;
    if (focal) focalPoint = br.sb(16) / 256;
    return { spread, interp, stops, focal: focalPoint };
  }

  function readFillStyle(br, tag, morph) {
    const sv = shapeVersionOf(tag);
    const t = br.u8();
    if (t === 0x00) {
      const c1 = sv >= 3 ? br.rgba() : br.rgb().concat([255]);
      if (morph) br.rgba();
      return { kind: 'solid', color: hexc(c1), alpha: Math.round((c1[3] / 255) * 1e4) / 1e4 };
    }
    if (t === 0x10 || t === 0x12 || t === 0x13) {
      const m = readMatrix(br);
      const g = readGradient(br, sv, t === 0x13);
      if (morph) { readMatrix(br); readGradient(br, sv, t === 0x13); }
      g.type = t === 0x10 ? 'linear' : 'radial';
      g.matrix = [m.sx, m.r0, m.r1, m.sy, m.tx / 20, m.ty / 20];
      return { kind: 'gradient', gradient: g };
    }
    if (t >= 0x40 && t <= 0x43) {
      br.u16(); readMatrix(br);
      if (morph) readMatrix(br);
      return { kind: 'bitmap', type: t };
    }
    throw new Error('未知 FILLSTYLE 0x' + t.toString(16) + ' @byte ' + br.bytepos);
  }

  function readFillStyleArray(br, tag, morph) {
    const sv = shapeVersionOf(tag);
    let n = br.u8();
    if (n === 0xFF && sv >= 2) n = br.u16();
    const out = [];
    for (let i = 0; i < n; i++) out.push(readFillStyle(br, tag, morph));
    return out;
  }

  function readLineStyle(br, tag, morph) {
    const sv = shapeVersionOf(tag);
    if (sv >= 4) {
      const w1 = br.u16();
      if (morph) br.u16();
      const startCap = br.ub(2), join = br.ub(2), hasFill = br.ub(1);
      br.ub(1); br.ub(1); br.ub(1); br.ub(5); br.ub(1); br.ub(2);
      br.align();
      if (join === 2) br.u16();
      let col, alpha;
      if (hasFill) {
        const f = readFillStyle(br, tag, false);
        col = f && f.color ? f.color : '#888888';
        alpha = f && f.alpha !== undefined ? f.alpha : 1;
      } else {
        const c1 = br.rgba();
        if (morph) br.rgba();
        col = hexc(c1);
        alpha = Math.round((c1[3] / 255) * 1e4) / 1e4;
      }
      return { color: col, width: Math.round((w1 / 20) * 1e3) / 1e3, alpha, cap: startCap, join };
    }
    const w1 = br.u16();
    if (morph) br.u16();
    const c1 = sv >= 3 ? br.rgba() : br.rgb().concat([255]);
    if (morph) { if (sv >= 2) br.rgba(); else br.rgb(); }
    return {
      color: hexc(c1), width: Math.round((w1 / 20) * 1e3) / 1e3,
      alpha: Math.round((c1[3] / 255) * 1e4) / 1e4, cap: 0, join: 0,
    };
  }

  function readLineStyleArray(br, tag, morph) {
    const sv = shapeVersionOf(tag);
    let n = br.u8();
    if (n === 0xFF && sv >= 2) n = br.u16();
    const out = [];
    for (let i = 0; i < n; i++) out.push(readLineStyle(br, tag, morph));
    return out;
  }

  // ---------------- SHAPERECORD ----------------
  // sub = { f0, f1, ln, segs: [[cmd,x,y,cx,cy], ...] }
  function parseShapeRecords(br, tag, curFills, curLines, warn) {
    let nfill = br.ub(4), nline = br.ub(4);
    let pen = [0, 0], fill0 = 0, fill1 = 0, line = 0;
    let sub = null;
    const subs = [];
    const curFill = (i) => (i >= 1 && i <= curFills.length ? curFills[i - 1] : null);
    const curLine = (i) => (i >= 1 && i <= curLines.length ? curLines[i - 1] : null);
    const closeSub = () => {
      if (sub && sub.segs.length >= 2) subs.push(sub);
      sub = null;
    };

    for (;;) {
      if (br.ub(1)) {
        const straight = br.ub(1);
        const nb = br.ub(4) + 2;
        const sx = pen[0], sy = pen[1];
        let cmd, cx = null, cy = null;
        if (straight) {
          let dx, dy;
          if (br.ub(1)) { dx = br.sb(nb); dy = br.sb(nb); }
          else if (br.ub(1)) { dx = 0; dy = br.sb(nb); }
          else { dx = br.sb(nb); dy = 0; }
          pen = [pen[0] + dx, pen[1] + dy];
          cmd = 'L';
        } else {
          const cdx = br.sb(nb), cdy = br.sb(nb);
          const adx = br.sb(nb), ady = br.sb(nb);
          cx = pen[0] + cdx; cy = pen[1] + cdy;
          pen = [pen[0] + cdx + adx, pen[1] + cdy + ady];
          cmd = 'Q';
        }
        if (sub === null) {
          sub = { f0: curFill(fill0), f1: curFill(fill1), ln: curLine(line), segs: [['M', sx, sy, null, null]] };
        }
        sub.segs.push([cmd, pen[0], pen[1], cx, cy]);
        continue;
      }

      const stateNew = br.ub(1), stateLine = br.ub(1), stateFill1 = br.ub(1);
      const stateFill0 = br.ub(1), stateMove = br.ub(1);
      if (!(stateNew || stateLine || stateFill1 || stateFill0 || stateMove)) break;

      if (stateNew) { closeSub(); pen = [0, 0]; }
      if (stateMove) {
        const mb = br.ub(5);
        pen = mb ? [br.sb(mb), br.sb(mb)] : [0, 0];  // MoveDelta 是绝对坐标，不是累加
        closeSub();
      }
      const newF0 = stateFill0 ? br.ub(nfill) : fill0;
      const newF1 = stateFill1 ? br.ub(nfill) : fill1;
      const newLn = stateLine ? br.ub(nline) : line;
      if (newF0 !== fill0 || newF1 !== fill1 || newLn !== line) closeSub();
      fill0 = newF0; fill1 = newF1; line = newLn;

      if (stateNew) {
        br.align();                                  // 样式表是字节结构
        curFills = readFillStyleArray(br, tag);      // 必须用原始 tag（版本决定 RGB/RGBA）
        curLines = readLineStyleArray(br, tag);
        br.align();
        nfill = br.ub(4);
        nline = br.ub(4);
      }
    }
    closeSub();
    return subs;
  }

  // ---------------- 组装 path ----------------
  // 与 Python 版 _fmt 一致：'%.2f'，只把完整的 '.00' 去掉（0.30 保留、6.00 → 6）
  const fmt = (v) => {
    const s = (v / 20).toFixed(2);
    return s.endsWith('.00') ? s.slice(0, -3) : s;
  };

  function reverseSegs(segs) {
    if (segs.length < 2) return segs;
    const start0 = [segs[0][1], segs[0][2]];
    const pts = segs.slice(1);
    const out = [['M', pts[pts.length - 1][1], pts[pts.length - 1][2], null, null]];
    for (let i = pts.length - 1; i >= 0; i--) {
      const s = pts[i];
      const e = i > 0 ? [pts[i - 1][1], pts[i - 1][2]] : start0;
      if (s[0] === 'Q') out.push(['Q', e[0], e[1], s[3], s[4]]);
      else out.push(['L', e[0], e[1], null, null]);
    }
    return out;
  }

  function chainSubpaths(parts, tol) {
    tol = tol === undefined ? 0.01 : tol;
    const chains = [];
    const pt = (s) => [s[1], s[2]];
    const closed = (ch) => {
      if (ch.length <= 1) return false;
      const a = pt(ch[0]), b = pt(ch[ch.length - 1]);
      return Math.abs(a[0] - b[0]) <= tol && Math.abs(a[1] - b[1]) <= tol;
    };
    for (const part of parts) {
      if (part.length < 2) continue;
      const head = pt(part[0]), tail = pt(part[part.length - 1]);
      let joined = false;
      for (const ch of chains) {
        if (closed(ch)) continue;  // 已闭合的环不能再焊，否则同心环挖空失效
        const last = pt(ch[ch.length - 1]), first = pt(ch[0]);
        if (Math.abs(last[0] - head[0]) <= tol && Math.abs(last[1] - head[1]) <= tol) {
          ch.push(...part); joined = true; break;
        }
        if (Math.abs(first[0] - tail[0]) <= tol && Math.abs(first[1] - tail[1]) <= tol) {
          ch.unshift(...part); joined = true; break;
        }
      }
      if (!joined) chains.push(part.slice());
    }
    let merged = true;
    while (merged && chains.length > 1) {
      merged = false;
      outer:
      for (let i = 0; i < chains.length; i++) {
        if (closed(chains[i])) continue;
        for (let j = 0; j < chains.length; j++) {
          if (i === j || closed(chains[j])) continue;
          const a = chains[i], b = chains[j];
          const last = pt(a[a.length - 1]), first = pt(b[0]);
          if (Math.abs(last[0] - first[0]) <= tol && Math.abs(last[1] - first[1]) <= tol) {
            a.push(...b); chains.splice(j, 1); merged = true; break outer;
          }
        }
      }
    }
    return chains;
  }

  function segsToD(segs, close) {
    if (close === undefined) close = true;
    // Python 版用 tuple/list 区分「单条 seg」和「链列表」，JS 里两者都是 Array，
    // 只能看第一项是不是命令字符串（'M'/'L'/'Q'）。
    if (segs.length && Array.isArray(segs[0]) && typeof segs[0][0] !== 'string') {
      return segs.map((ch) => segsToD(ch, close)).filter(Boolean).join(' ');
    }
    if (!segs.length) return '';
    const out = [];
    let start = null, cur = null, first = true;
    for (const [cmd, x, y, cx, cy] of segs) {
      if (cmd === 'M') {
        if (first) { out.push('M' + fmt(x) + ' ' + fmt(y)); start = [x, y]; first = false; }
        else if (!cur || x !== cur[0] || y !== cur[1]) out.push('L' + fmt(x) + ' ' + fmt(y));
        cur = [x, y];
      } else if (cmd === 'L') {
        out.push('L' + fmt(x) + ' ' + fmt(y)); cur = [x, y];
      } else {
        out.push('Q' + fmt(cx) + ' ' + fmt(cy) + ' ' + fmt(x) + ' ' + fmt(y)); cur = [x, y];
      }
    }
    if (close && start && cur && (cur[0] !== start[0] || cur[1] !== start[1])) {
      out.push('L' + fmt(start[0]) + ' ' + fmt(start[1]));
    }
    return out.join(' ');
  }

  function assemble(cid, tag, bounds, fills, lines, subs, warn, fillRule) {
    const xmin = bounds[0] / 20, xmax = bounds[1] / 20;
    const ymin = bounds[2] / 20, ymax = bounds[3] / 20;
    const groups = new Map();   // fill 对象 -> parts
    const strokes = new Map();  // line 对象 -> parts

    for (const s of subs) {
      const { f0, f1, ln, segs } = s;
      if (f0 && f0 === f1) {
        if (!groups.has(f0)) groups.set(f0, []);
        groups.get(f0).push(segs);
        continue;
      }
      if (f0) { if (!groups.has(f0)) groups.set(f0, []); groups.get(f0).push(segs); }
      // fill1 是边的右侧，必须反向才能和 fill0 首尾相接围成同一个环
      if (f1) { if (!groups.has(f1)) groups.set(f1, []); groups.get(f1).push(reverseSegs(segs)); }
      if (ln) { if (!strokes.has(ln)) strokes.set(ln, []); strokes.get(ln).push(segs); }
    }

    const paths = [];
    for (const [f, parts] of groups) {
      const d = segsToD(chainSubpaths(parts));
      if (!d) continue;
      const item = { d, rule: fillRule };
      if (f.kind === 'solid') {
        item.fill = f.color;
        if (f.alpha < 1) item.fa = f.alpha;
      } else if (f.kind === 'gradient') {
        const g = f.gradient;
        item.grad = {
          type: g.type, stops: g.stops, matrix: g.matrix, focal: g.focal,
          spread: g.spread,
        };
        // 同时给一个中间色兜底（前端不实现渐变时也能显示）
        item.fill = g.stops.length ? g.stops[g.stops.length >> 1].color : '#888888';
      } else {
        if (warn) warn.push('shape ' + cid + ': 跳过 bitmap 填充');
        continue;
      }
      paths.push(item);
    }

    for (const [ln, parts] of strokes) {
      // 描边不串链：串接会在断开处补 SWF 里不存在的连线
      const d = parts.filter((p) => p.length >= 2).map((p) => segsToD(p, false)).filter(Boolean).join(' ');
      if (!d) continue;
      const item = { d, stroke: ln.color, sw: ln.width > 0 ? ln.width : 1 };
      if (ln.alpha < 1) item.sa = ln.alpha;
      paths.push(item);
    }

    return {
      id: cid, tag,
      bounds: [xmin, xmax, ymin, ymax],
      width: xmax - xmin, height: ymax - ymin,
      paths,
    };
  }

  function parseDefineShape(payload, tag) {
    const cid = payload[0] | (payload[1] << 8);
    const br = new BR(payload, 2);
    const bounds = readRect(br);
    let fillRule = 'evenodd';
    if (tag === 83) {
      br.align(); readRect(br); br.align();   // EdgeBounds
      const flags = br.u8();
      if ((flags >> 2) & 1) fillRule = 'nonzero';
    }
    br.align();
    const fills = readFillStyleArray(br, tag);
    const lines = readLineStyleArray(br, tag);
    br.align();
    const warn = [];
    const subs = parseShapeRecords(br, tag, fills, lines, warn);
    return assemble(cid, tag, bounds, fills, lines, subs, warn, fillRule);
  }

  function parseDefineMorph(payload, tag) {
    const cid = payload[0] | (payload[1] << 8);
    const br = new BR(payload, 2);
    const bounds = readRect(br);
    br.align(); readRect(br); br.align();     // EndBounds
    if (tag === 84) {
      readRect(br); br.align(); readRect(br); br.align();
      br.u8();
    }
    br.u32(); br.align();
    const fills = readFillStyleArray(br, tag, true);
    const lines = readLineStyleArray(br, tag, true);
    br.align();
    const warn = [];
    const subs = parseShapeRecords(br, tag, fills, lines, warn);
    return assemble(cid, tag, bounds, fills, lines, subs, warn, 'evenodd');
  }

  function parseShapeDef(payload, tag) {
    return (tag === 46 || tag === 84) ? parseDefineMorph(payload, tag) : parseDefineShape(payload, tag);
  }

  // ---------------- SWF 容器 ----------------
  function iterTags(data, pos) {
    const out = [];
    while (pos + 2 <= data.length) {
      const cl = data[pos] | (data[pos + 1] << 8);
      const code = cl >> 6;
      let len = cl & 0x3F;
      pos += 2;
      if (len === 0x3F) {
        len = (data[pos] | (data[pos + 1] << 8) | (data[pos + 2] << 16) | (data[pos + 3] << 24)) >>> 0;
        pos += 4;
      }
      out.push([code, data.subarray(pos, pos + len)]);
      pos += len;
      if (code === 0) break;
    }
    return out;
  }

  function parsePlace2(p) {
    const flags = p[0];
    const o = {
      depth: p[1] | (p[2] << 8), cid: null, matrix: null, cxform: null,
      remove: !!(flags >> 7),
    };
    if (o.remove) return o;
    let i = 3;
    if ((flags >> 1) & 1) { o.cid = p[i] | (p[i + 1] << 8); i += 2; }
    if ((flags >> 2) & 1) { const m = readMatrix(new BR(p, i)); o.matrix = m; i = Math.ceil(new BR(p, i).bit / 8); }
    return o;
  }

  // PlaceObject2/3 的矩阵/色彩变换要按位流推进，这里用一个统一的位读取器
  function parsePlaceObject(payload, isV3) {
    const p = payload;
    const flags = p[0];
    const flags2 = isV3 ? p[1] : 0;
    const base = isV3 ? 4 : 3;
    const o = {
      depth: (p[isV3 ? 2 : 1] | (p[isV3 ? 3 : 2] << 8)),
      cid: null, matrix: null, cxform: null, clip: null,
      remove: isV3 ? (!!(flags >> 7) && !((flags >> 1) & 1)) : !!(flags >> 7),
    };
    if (o.remove && !isV3) return o;
    let i = base;
    if (isV3 && ((flags2 >> 4) & 1)) { while (p[i] !== 0) i++; i++; }  // ClassName
    if ((flags >> 1) & 1) { o.cid = p[i] | (p[i + 1] << 8); i += 2; }
    if ((flags >> 2) & 1) {
      const br = new BR(p, i);
      o.matrix = readMatrix(br);
      i = Math.ceil(br.bit / 8);
    }
    if ((flags >> 3) & 1) {
      const br = new BR(p, i);
      o.cxform = readCxform(br);
      i = Math.ceil(br.bit / 8);
    }
    return o;
  }

  function newTimeline(nframes) {
    return { nframes, frames: [], stateAt(f) { return stateFrom(this, f); }, entryFrame(d, f) { return entryFrom(this, d, f); } };
  }

  function stateFrom(tl, frameIdx) {
    const st = {};
    const lim = Math.min(frameIdx + 1, tl.frames.length);
    for (let f = 0; f < lim; f++) {
      for (const op of tl.frames[f]) {
        if (op.op === 'remove') { delete st[op.depth]; continue; }
        const d = op.depth;
        if (st[d]) {
          const m = Object.assign({}, st[d]);
          for (const k of ['cid', 'matrix', 'cxform', 'clip']) {
            if (op[k] !== null && op[k] !== undefined) m[k] = op[k];
          }
          st[d] = m;
        } else {
          st[d] = Object.assign({}, op);
        }
      }
    }
    return st;
  }

  function entryFrom(tl, depth, atFrame) {
    let last = 0;
    const lim = Math.min(atFrame + 1, tl.frames.length);
    for (let f = 0; f < lim; f++) {
      for (const op of tl.frames[f]) {
        if (op.op === 'remove') { if (op.depth === depth) last = null; continue; }
        if (op.depth === depth && op.cid !== null) last = f;
      }
    }
    return last === null ? atFrame : last;
  }

  function parseSWF(bytes) {
    const d = bytes;
    const sig = String.fromCharCode(d[0], d[1], d[2]);
    if (sig === 'ZWS') throw new Error('ZWS（LZMA 压缩）暂不支持，请用 FFDec 另存为未压缩或 zlib 版');
    if (sig !== 'FWS' && sig !== 'CWS') throw new Error('不是 SWF 文件（签名 ' + sig + '）');

    const nb = d[8] >> 3;
    const bodyStart = 8 + Math.ceil((5 + nb * 4) / 8) + 4;
    const hr = new BR(d, 8);
    const rect = readRect(hr);
    const fps = (d[bodyStart - 4] | (d[bodyStart - 3] << 8)) / 256;
    const rootCount = d[bodyStart - 2] | (d[bodyStart - 1] << 8);

    const swf = {
      width: (rect[1] - rect[0]) / 20,
      height: (rect[3] - rect[2]) / 20,
      fps, rootCount,
      shapes: new Map(),   // cid -> {tag, payload}
      sprites: new Map(),  // sid -> timeline
      root: newTimeline(rootCount),
    };

    const rootCur = [];
    for (const [code, payload] of iterTags(d, bodyStart)) {
      if (code === 0) break;
      switch (code) {
        case 2: case 22: case 32: case 83: case 46: case 84: {
          const cid = payload[0] | (payload[1] << 8);
          swf.shapes.set(cid, { tag: code, payload });
          break;
        }
        case 39: {
          const sid = payload[0] | (payload[1] << 8);
          const n = payload[2] | (payload[3] << 8);
          const tl = newTimeline(n);
          let cur = [];
          for (const [c2, p2] of iterTags(payload, 4)) {
            if (c2 === 43) continue;                       // FrameLabel
            else if (c2 === 26 || c2 === 70) {
              const po = parsePlaceObject(p2, c2 === 70);
              if (po.remove && po.cid === null) cur.push({ op: 'remove', depth: po.depth });
              else cur.push({ op: 'place', depth: po.depth, cid: po.cid, matrix: po.matrix, cxform: po.cxform, clip: po.clip });
            } else if (c2 === 5 && p2.length >= 4) {
              cur.push({ op: 'remove', depth: p2[2] | (p2[3] << 8) });
            } else if (c2 === 28 && p2.length >= 2) {
              cur.push({ op: 'remove', depth: p2[0] | (p2[1] << 8) });
            } else if (c2 === 1) {
              tl.frames.push(cur); cur = [];
            } else if (c2 === 0) break;
          }
          if (tl.frames.length < n) tl.frames.push(cur);
          swf.sprites.set(sid, tl);
          break;
        }
        // 根时间轴的显示列表
        case 26: case 70: {
          const po = parsePlaceObject(payload, code === 70);
          if (po.remove && po.cid === null) rootCur.push({ op: 'remove', depth: po.depth });
          else rootCur.push({ op: 'place', depth: po.depth, cid: po.cid, matrix: po.matrix, cxform: po.cxform, clip: po.clip });
          break;
        }
        case 5: if (payload.length >= 4) rootCur.push({ op: 'remove', depth: payload[2] | (payload[3] << 8) }); break;
        case 28: if (payload.length >= 2) rootCur.push({ op: 'remove', depth: payload[0] | (payload[1] << 8) }); break;
        case 1: swf.root.frames.push(rootCur.splice(0)); break;
        default: break;
      }
    }
    if (swf.root.frames.length < rootCount) swf.root.frames.push(rootCur);
    return swf;
  }

  // ---------------- tick 引擎 ----------------
  function mulMatrix(a, b) {
    return [
      a[0] * b[0] + a[2] * b[1],
      a[1] * b[0] + a[3] * b[1],
      a[0] * b[2] + a[2] * b[3],
      a[1] * b[2] + a[3] * b[3],
      a[0] * b[4] + a[2] * b[5] + a[4],
      a[1] * b[4] + a[3] * b[5] + a[5],
    ];
  }

  function matrixToSvg(m) {
    if (!m) return [1, 0, 0, 1, 0, 0];
    return [m.sx, m.r0, m.r1, m.sy, m.tx / 20, m.ty / 20];
  }

  function cxAlpha(cx) {
    if (!cx) return 1;
    return Math.max(0, Math.min(1, cx.mul[3] + cx.add[3] / 255));
  }

  // 把一个时间轴实例在第 frame 帧的状态递归展开成叶子 shape
  function flatten(swf, tl, frame, m, alpha, out, budget) {
    if (budget === undefined) budget = [96];
    if (budget[0] <= 0) return;
    budget[0]--;

    const state = tl.stateAt(frame);
    const depths = Object.keys(state).map(Number).sort((a, b) => a - b);
    for (const d of depths) {
      const place = state[d];
      const cid = place.cid;
      if (cid === null || cid === undefined) continue;
      const pm = mulMatrix(m, matrixToSvg(place.matrix));
      const pa = alpha * cxAlpha(place.cxform);
      if (swf.shapes.has(cid)) {
        out.push({ s: cid, m: pm.map((v) => Math.round(v * 1e4) / 1e4), a: Math.round(pa * 1e4) / 1e4 });
      } else if (swf.sprites.has(cid)) {
        const child = swf.sprites.get(cid);
        const entry = tl.entryFrame(d, frame);
        const cf = child.nframes ? (frame - entry) % child.nframes : 0;
        flatten(swf, child, cf < 0 ? cf + child.nframes : cf, pm, pa, out, budget);
      }
    }
  }

  /**
   * 选出「动画主体」：
   *   根时间轴本身有多帧 → 用根（最真实，能看到全部元素）
   *   根只有 1 帧且只挂了一个 sprite → 跟随它（赛尔号动作 SWF 的常态）
   */
  function pickPlayable(swf) {
    const root = swf.root;
    if (root.nframes > 1) return { tl: root, name: 'root', nframes: root.nframes };
    const state = root.stateAt(0);
    const placed = Object.values(state).filter((o) => o.cid !== null && swf.sprites.has(o.cid));
    if (placed.length === 1) {
      const tl = swf.sprites.get(placed[0].cid);
      return { tl, name: 'sprite ' + placed[0].cid, nframes: tl.nframes };
    }
    if (placed.length > 1) return { tl: root, name: 'root', nframes: 1 };
    return { tl: root, name: 'root', nframes: Math.max(1, root.nframes) };
  }

  global.SwfCore = {
    BR, readRect, readMatrix, readCxform,
    parseShapeDef, parseSWF, flatten, pickPlayable,
    mulMatrix, matrixToSvg, iterTags,
  };
})(typeof window !== 'undefined' ? window : globalThis);
