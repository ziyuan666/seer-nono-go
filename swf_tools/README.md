# swf_tools · 赛尔号 SWF 逐帧播放器

两个东西：

| 文件 | 怎么用 | 适合 |
|---|---|---|
| **`nono_swf_viewer.html`** | 双击打开 → 拖入/选择一个赛尔号 SWF | **日常用这个**：选哪个看哪个，不用装任何东西 |
| `sel_player.py` | 命令行把某个 SWF 转成 HTML | 批量出成品、或要发给不懂操作的人 |

只针对赛尔号这类「纯 shape + 时间轴」的动作 SWF，不追求通用 SWF 兼容。

---

## 一、nono_swf_viewer.html（通用播放器）

双击 → 打开后把 SWF 拖进去（或点一下选文件）→ 直接播。

- 解析、渲染全部在浏览器里完成，**不需要 Python、不需要 FFDec、不需要联网**
- 支持 FWS（未压缩）和 CWS（zlib）；ZWS（LZMA）会提示先另存
- 顶部会列出这个 SWF 里所有帧数 > 1 的时间轴，可以随时切（比如 3_1 有 9 个：
  sprite 99 是整体动作 165 帧，sprite 20/23 是 550 帧的深零件）

| 操作 | 键 |
|---|---|
| 播放 / 暂停 | 空格 |
| 上/下一帧 | ← → |
| 首 / 末帧 | Home / End |
| 定位 | 进度条 |
| 缩放 | 滑杆 / 滚轮（10%–800%，矢量重绘不糊） |
| 导出当前帧 | 「导出 PNG」（带 alpha） |
| 循环 | 勾选框 |

改了 `swfcore.js` 或模板后重新打包：

```bash
python3 build_viewer.py        # 生成 nono_swf_viewer.html
```

调试时可以直接带参数加载，省得手动选文件：

```
nono_swf_viewer.html?url=xxx.swf      # 需与 SWF 同源（http 服务下可用）
```

---

## 二、sel_player.py（命令行转成品）

```bash
python3 sel_player.py 3_1.swf -o 3_1.html        # 自动挑动画主体
python3 sel_player.py 3_1.swf --sprite 98 -o x.html
python3 sel_player.py 3_1.swf --list             # 只打印时间轴树
```

生成的是「帧数据已经烘进 HTML」的自包含文件，比通用版小（3_1 是 148 KB），
适合发给别人——对方不用选文件，打开就是那一个动作。

---

## 为什么不用现成工具

| 方案 | 卡在哪 |
|---|---|
| FFDec 帧导出 | 按根时间轴重建，子 MC 最多推进一层；3_1 的 550 帧深层动画导出来是空白 |
| Ruffle 官方二进制 | 菜单只有 Pause/Rewind，**没有 Step Forward**，也没有导出帧 |
| 自己编 Ruffle | 需要 Vulkan + 交叉编译工具链，当前容器两样都没有 |
| 纯解析库（swf-go / yaswfp 等） | 没有运行时，`DoABC` 里的 `stop()` 不执行也拿不到合成画面 |

赛尔号动作 SWF 的画面 100% 由 shape 几何 + 显示列表 + 时间轴构成，AS3 脚本只做 `stop()`。
所以只要自己实现「显示列表 + tick」这一层就够了，不需要 AVM2。

---

## 原理

```
SWF ──容器解析──▶ 每个 DefineSprite 的 place/remove/ShowFrame 帧序列
              ──tick 模拟──▶ 每 tick 递归展开整棵树，累积 matrix + cxform
              ──shape 解析──▶ SVG path（懒加载，解析一次后缓存 Path2D）
              ──▶ canvas 渲染
```

两套实现共用同一套口径：

- `swfcore.js` —— 浏览器版（`nono_swf_viewer.html` 内联的就是它）
- `../nono_deskpet_vec/tools/importer/{swfread,swfshape}.py` —— Python 版（`sel_player.py` 用）

**已验证逐字符等价**：3_1 的 63 个 shape，两边生成的 `d` 字符串 63/63 完全相同。

### tick 语义（与 Flash 一致的部分）

- 时间轴是树，每个 `DefineSprite` 实例有独立播放头；
- 每 tick，所有 playing 的实例各前进 1 帧，到末帧回 0；
- **子 MC 从它被 place 的那一帧开始从自己的第 0 帧播**（`entryFrame`），
  不是跟父帧号同步取模 —— 这条错了会出现「本该在右侧的元素画到左侧」；
- 父的帧数 / stop 不影响子 MC。

### 选哪个时间轴播

- 根时间轴本身有多帧 → 播根（能看到全部元素，最真实）；
- 根只有 1 帧且只挂了一个 sprite → 跟随它（赛尔号动作 SWF 的常态）；
- 其余帧数 > 1 的 sprite 全部列进顶部下拉，随时切换。

---

## 已知限制

1. **`stop()` 没实现**：末帧后循环播放，而官方会在末帧定格（`3_1` 的 165 帧、
   `9`/`98` 的 95 帧都有 stop）。需要解析 AVM2 的 addFrameScript 表，暂未做；
   观察动画时循环其实更有用。
2. **cxform 只取 alpha**：RGB 变换（变色）不生效。3_1 第 11 帧那两根触须 FFDec 是
   半透明青色、这里是 alpha 0.8 白色，就是这条导致的（其余帧一致）。
3. **渐变降级为中间色**：3_1 实测 73 条 path 里 0 条渐变，没影响；带渐变的 SWF 会偏色。
4. **bitmap fill / 遮罩（ClipDepth）未实现**：动作 SWF 基本没有；有则缺失或多画。
5. **面遍历（_faces）未移植**：Python 版遇到 T 型接头会改用平面图面遍历重建填充边界，
   JS 版只有串链。个别 shape 可能补一条近路直线、缺块。
6. 画布按「全部帧的并集」定尺寸以避免播放抖动，所以单帧内容可能只占画布一角。

## 与 FFDec 的实测对照（3_1，sprite 99）

1 / 11 / 41 / 81 / 91 / 165 帧的形状、位置、层级与 FFDec 导出图一致。
