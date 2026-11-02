# 主面板矢量化 · 管线现状（2026-10-23 更新）

## 一句话

主面板已能用「SWF display list + FFDec shape SVG」合成出**逐像素对齐**的矢量图，与 FFDec 官方渲染比对 `mean abs diff = 2.24`（0–255 尺度，早期 68 → 7.89 → 2.24）。剩余差异全部是描边抗锯齿算法差异，无位置/形状/颜色偏差。

## 根因（本轮修复的关键）

FFDec 导出的每个 shape SVG 坐标是 **相对自己 ShapeBounds 左上角**的：

```
SWF 坐标 = FFDec 坐标 + ShapeBounds(minX, minY)
```

因此合成时必须对每个 shape 再乘一个 `translate(minX, minY)`。

漏掉这一步时：
- 大多数 shape 的 `minX/minY = 0` → 看起来完全正确（外框、按钮、箭头全对）；
- 少数 `minX/minY ≠ 0` 的 shape 会整体偏移 → 表现为**眼睛偏移 24/20px、左上多出一个小圆**。

三个"疑难差异"实际是同一个 bug。修复后 diff 7.89 → 2.24，眼睛位置、多余小圆同时消失。

## 现在的管线

```
MachineDogPanel.swf
  ├─ swf-go 解析 ──> chars(shape/sprite) + buttons(手写 DefineButton2) + SymbolClass + ShapeBounds
  │                   ↓
  │              display list 遍历（PlaceObject2/3 矩阵 × ColorTransform alpha，depth 256 视为 hitTest 跳过）
  │                   ↓
  │              (shapeId, 累积矩阵 × translate(bounds.min), alpha) 列表
  └─ FFDec -export shape ──> 109 个 shape SVG（几何真值：path d + 渐变 defs）
                              ↓
                        合成总 SVG（viewBox = 内容 bounds，`FIT=1` 自动计算）
```

工具源码（已持久化）：`nono_deskpet/tools/shapegen/`（main.go / render2.go / button.go / svgsrc.go / symclass.go / model.go）

## 关键数据（实测）

| 项 | 值 |
|---|---|
| 主面板入口 | SymbolClass `MachineDogMainInfo_MC` → charId 102 |
| 储藏面板入口 | `MachineDogStorage_MC` → charId 177 |
| 芯片图标入口 | `ChipItem_MC` → charId 182 |
| 面板脸动画 | `_fla.Timeline_13/16/22`、`_fla.sdasdg_39`、faceMc → 101 |
| stage | 500×375 px（header rect 10000×7500 twips） |
| 主面板内容 bounds | 0,0 .. 280,280 |
| 储藏内容 bounds | −0.75,−0.75 .. 211.5,260.75 |
| 已放置元素 | 主面板 22 个（shape 级）、储藏 10 个 |
| ClipDepth 遮罩 | 0 个（该 SWF 不用遮罩） |
| DefineButton2 | 19 个（swf-go 未实现 code 34，已手写解析） |
| ButtonStateFlags | bit0 up / bit1 over / bit2 down / bit3 hitTest |

## 已排除的假设

1. **不是缺遮罩**：ClipDepth 全 SWF 为 0。
2. **不是位图填充**：109 shape 里 0 个 bitmap fill。
3. **不是坐标单位错**：shape 坐标 twips/20 = px。
4. **不是矩阵层级错**：22 个元素累积矩阵与 FFDec 一致。
5. **swf-go 的 shape record 解析有缺陷**：shape 1 累积到 1515px，FFDec 只有 281px → 几何一律用 FFDec SVG。
6. **不是 sprite 多帧干扰**：sprite 101（faceMc）共 279 帧，但第 1 帧只有 depth 1 = shape 58，取首帧正确。

## 视觉证据

| 文件 | 说明 |
|---|---|
| `main_vec.png` | 我的矢量合成（FIT bounds，400px 输出） |
| `storage_vec.png` | 储藏面板合成（页签中文为矢量路径，缩放不糊） |
| `chipitem_vec.png` | 芯片图标合成 |
| `main_vec_ffdec_cmp.png` | 与 FFDec 官方渲染 50% 叠加 |

## 未处理

1. morph shape、滤镜（DropShadow/Glow）、sprite 多帧状态（目前固定第 1 帧）。
2. 中间蓝圆内的 NONO 图标：该帧本身没有，属于运行时 `addChild`。
3. 位图填充（本 SWF 无，其他面板 SWF 需单独确认）。

## 复现命令

```bash
# 1) FFDec 导出全部 shape SVG
java -jar tools/ffdec/ffdec.jar -export shape /tmp/ffout MachineDogPanel.swf

# 2) 构建并合成（swf / 输出 svg / 入口 charId / shape SVG 目录）
cd nono_deskpet/tools/shapegen && go build -o sg .
FIT=1 ./sg MachineDogPanel.swf out.svg 102 /tmp/ffout

# 3) 转 PNG
python3 -c "import cairosvg; cairosvg.svg2png(url='out.svg', write_to='out.png', output_width=400)"
```

## 落盘

- 代码工具：`nono_deskpet/tools/shapegen/`
- 产物与对比图：`nono_deskpet/docs/vec/`
- 变更记录：`变更工程/当前改动/20261023-02/说明.md`
