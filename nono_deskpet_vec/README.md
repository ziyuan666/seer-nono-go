# nono_deskpet_vec

NONO 桌宠的**分层矢量精灵**实现。目标：替掉「SWF → 169MB PNG 整帧烘焙」链路，
让素材变成几 MB 的 JSON，新增形态/表情不碰 Flash。

状态：**已接入桌宠**（P4/P5 完成；素材目录已并到 `../nono_deskpet/assets/sprites/`，本目录不再留拷贝）。
旧 PNG 烘焙链的退役由 20261026-07 处理，不归本文件。

---

## 目录

```
nono_deskpet_vec/
├── assets/sprites/<form>/
│   ├── sprite.json          # 形态骨架：canvas / colors / layers / directions
│   └── parts/shape_*.json   # 几何：path d + fill/gradient + stroke + matrix
├── cmd/vecgen/              # 工具：validate / render / sheet / diff
├── pkg/
│   ├── pathd/               # SVG path d 解析 + 贝塞尔细分
│   ├── raster/              # 光栅化（fill / stroke / gradient mask / 超采样）
│   ├── spec/                # JSON 格式定义 + 加载 + 校验
│   └── sprite/              # 合成器（层排序 + 帧缓存）
├── tools/importer/          # SWF → JSON 导入器（Python）
│   ├── swfread.py           # 最小 SWF 读取器（tag / place / sprite 树）
│   └── build_sprite.py      # 展平显示列表 + 解析 FFDec 导出的 SVG
└── docs/拼接方案.md          # 方案与决策记录
```

## 格式（format: 1）

```json
{
  "format": 1,
  "name": "plain",
  "canvas": { "w": 67.2, "h": 71.5, "anchor": [30.26, 32.58] },
  "colors": { "shell": "#ffffff" },
  "layers": [ {"name":"color_2","z":0,"tintable":true}, ... ],
  "directions": {
    "down": { "frames": [ { "parts": [
        {"layer":"color_2","part":"shape_1","matrix":[a,b,c,d,e,f],"alpha":1}
    ] } ] }
  }
}
```

- `canvas.anchor` = **球心在画布里的位置**，6 个形态统一按它对齐，切换形态球心不动。
- `matrix` 是 SVG 语义 `x' = a*x + c*y + e`，已含 FFDec 的 bounds 补偿与画布原点偏移。
- `parts/shape_*.json` 每条 path：`d`（SVG path）、`fill`、`gradient`、`stroke`。

## 用法

```bash
go build -o vecgen ./cmd/vecgen

./vecgen validate assets/sprites/plain/sprite.json
./vecgen render   -dir down -frame 0 -scale 4 -ss 4 -out out.png assets/sprites/plain/sprite.json
./vecgen render   -dir up -frame 0 -layer body -out body.png assets/sprites/plain/sprite.json
./vecgen sheet    -frames 17 -scale 2 -only down,up -bg "#e8e8e8" -out sheet.png assets/sprites/plain/sprite.json
./vecgen diff     a.png b.png -out diff.png
```

> 注意：flag 必须写在位置参数**前面**（Go `flag` 遇到第一个非 flag 参数就停止解析）。

## 导入（一次性）

```bash
# 1) 导出 shape 几何（FFDec 只做这一件事，之后完全脱离 Flash）
java -jar tools/ffdec-cli.jar -zoom 1 -export shape /tmp/ffvec <swf>

# 2) 展平成 JSON
cd tools/importer
python3 build_sprite.py --swf <swf> --root <spriteId> --name <form> \
    --svg-dir /tmp/ffvec --out ../../assets/sprites --auto-frames
```

`--root` 是该 SWF 的根 sprite（未被其它 sprite 引用的那个）。
`--auto-frames` 自动取「非 1 帧、非 63 帧」的众数作为待机帧数。

## 已导入的形态

| 形态 | root | 待机帧 | 层数 | 画布 | 锚点(球心) |
|---|---|---|---|---|---|
| plain  | 127 | 17 | 3 | 67.2×71.5   | (30.26, 32.58) |
| super1 | 150 | 13 | 3 | 69.7×99.6   | (34.86, 49.89) |
| super2 | 189 | 19 | 5 | 79.6×86.9   | (39.10, 43.31) |
| super3 | 124 | 16 | 5 | 80.2×83.4   | (41.00, 38.36) |
| super4 | 152 | 21 | 5 | 192.5×107.7 | (94.38, 48.57) |
| super5 | 106 | 20 | 3 | 59.6×70.2   | (29.26, 18.07) |

每形态 8 方向，帧数各不相同——按 SWF 真实时间轴，不再强行统一成 17 帧。

### 表情 / 动作（`assets/anim/`）

| 名称 | 源 | 帧数 | 说明 |
|---|---|---|---|
| exp1 | exp/1.swf | 69 | 表情动画（含本体） |
| exp2 | exp/2.swf | 62 | 同上 |
| exp3 | exp/3.swf | 70 | 同上 |
| exp4 | exp/4.swf | 60 | 同上 |
| action1 | action/1.swf | 67 | 动作（心电图/扫描） |
| action2 | action/2.swf | 111 | 动作（变形） |

单方向格式：`directions.default.frames[]`，播放时整体替换主体。

## 关键实现决定

1. **方向段容器冻结**：带方向 label 的 63 帧 sprite 一律取该方向段首帧，不参与推进；
   其余嵌套 sprite 按输出帧号 `i % nframes` 循环。这样结构上不可能跨段串层。
2. **不解析运行时 SWF**：SWF 只在导入时用一次，运行时只读 JSON。
3. **渐变支持**：51 条 linear + 41 条 radial（最多 10 个 stop），按 SVG 规范算 t 后查 stop。
4. **超采样**：默认 4x 渲染后 box 下采样，对标 Flash 的抗锯齿。
5. **描边**：折线扩成四边形 + 圆关节，用同一 rasterizer 填充；线宽随矩阵缩放。
6. **滤镜**（2026-10-26 补）：PlaceObject3 + tag 70 里的 blur/glow/shadow 进
   `frame.filters[fid]`，渲染走 `pkg/sprite/filter.go` → `pkg/raster/glow.go`
   （三次盒滤波，`r = max(1, round(blur*0.25))`；strength / compositeSource /
   knockout / inner 按 Flash 语义）。7143 帧里 4189 帧带滤镜。
   帧 bbox 必须加 `filter_pad()`，否则按 bbox 开窗会把光晕切出直边。

## 验证记录（2026-10-24）

- 6 形态 × 8 方向 × 全帧共 **848 帧**渲染：0 空帧，面积波动全部 <35%（无跨段变形）。
- plain down 与原版烘焙帧对齐后：bbox 尺寸差 1.3%，黑球宽度 97px vs 95px（2%），
  差异集中在抗锯齿边缘——原版烘焙链自身有 unify 缩放，矢量为未经二次缩放的原始几何。
- 6 形态球心按 anchor 对齐后，红叉落在球体同一位置（±5px，来自 ballc 烘焙测量误差）。

## 待做

- [x] P4 接入桌宠：`FrameSet` 换成矢量渲染器，跑通交互/面板/存档
- [x] P5 表情与动作（exp/action）导入（57 条动画 / 7143 帧）
- [ ] P6 退役烘焙链、删 169MB PNG（20261026-07 在做）
- [x] 颜色槽位染色（对应原版 FillColor）
- [ ] 开发期热重载（改 JSON 立即生效）
