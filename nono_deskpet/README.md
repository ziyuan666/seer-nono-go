# NONO 桌宠（Go + Win32 分层窗口）

赛尔号 NONO 桌面宠物，独立程序，不依赖游戏服务端。支持 **7 套形态**：普通（`nono/nono.swf`）、超能 1–5（`super/nono_1..5.swf`）、至尊（`annual/nono.swf`，右键菜单或设置面板「至尊形态」）。形态资源覆盖情况见 `docs/NONO形态资源缺失文档.md`。

## 运行

```
nono_deskpet.exe
assets/            ← 必须和 exe 放在同一目录
```

- 存档：`%APPDATA%\NonoDeskpet\save.json`（首次运行自动生成）
- 窗口：透明分层窗口，按当前帧 alpha 判断点击穿透；右键打开菜单
- 单击角色：随机播放表情 / 动作
- 拖动角色：移动位置（松手不触发动作）

## 功能

| 操作 | 说明 |
|---|---|
| 待机 | 播放 `assets/plain/idle_anim/` 的 89 帧悬浮序列（按 GIF 原始时长，一轮约 2.96 秒）；窗口与气泡不位移；随机走动、碰屏幕边缘反向 |
| 单击 | 55% 表情（1–4），45% 动作（普通形态只有动作 1、2） |
| 拖动 | 跟随鼠标，松手落位并写入存档 |
| 右键 | 信息面板 / 使用芯片 / 恢复默认外观 / 设置 / 退出 |
| 托盘 | 图标常驻，右键打开菜单，资源管理器重启后自动重挂 |
| 信息面板 | 等级、能量、亲密度、智能、经验、当前颜色、已装芯片、状态 |
| 设置面板 | 缩放、置顶、自动走动、气泡、跟随、悬浮、护盾、炫光、音效、移动帧率（静止/慢/中/快）、开机自启 |
| 高 DPI | 声明 Per-Monitor V2，125%/150% 缩放下不会被系统拉伸模糊 |
| 多显示器 | 走动边界取虚拟屏幕联合区域，坐标越界会自动夹回 |

## 芯片（40 项，全部本地实现）

数据来自 `data/items.xml` 的 `700xxx` 项，导出为 `assets/chips.json`。

- **变色（12）**：把 `color_1`/`color_2` 白色蒙版实时染色，不预烘焙；换芯片即换色
- **能量（3）**：能量 / 亲密度数值
- **智慧（2）**：智能数值
- **玩具（2）**：亲密度 + 播放动作（本形态缺该动作时用表情代替并提示）
- **功能（21）**：本地效果映射（`items.xml` 里 `ItemKind=1` 共 21 项：700001–700020 + 700061；其中 700020 原版无 chip 图标与客户端类，桌宠仍实现了炫光）

| 芯片 | 本地效果 |
|---|---|
| 精灵恢复 | 能量回满 |
| 分子转化 | 能量 → 智能 |
| 经验分配 / 经验加成 | 经验 +10 / 加成开关 |
| 模拟训练 | 智能 +1 |
| 跟随模式 / 精灵追踪 | 跟随鼠标 |
| 机械臂 | 玩球动作（缺资源时用表情） |
| 星球测绘 | 气泡报坐标 |
| 雷达 | 气泡提示附近无精灵 |
| 精灵翻译 / 初级语言 | 随机台词 |
| 精灵仓库 | 打开信息面板 |
| 超能屏障 | 护盾外观（可恢复） |
| 反物质 | 能量 -10、智能 +1 |
| 超能精炼 | 能量 +20 |
| 技能排序 | 气泡提示 |
| 飞行模式 | 悬浮外观（可恢复） |
| 元神赋形 | 随机颜色（可恢复） |
| 时空穿梭 | 随机瞬移 |
| 超能炫光 | 呼吸闪烁（可恢复） |

**所有改变外观的芯片（变色/悬浮/护盾/炫光/缩放）都能恢复**：右键「恢复默认外观」、信息面板与设置面板里的「恢复默认外观」按钮，或再次点击同一芯片取消开关。

## 界面（1:1 复刻游戏内）

面板全部**直接贴 SWF 导出的原图**（`assets/ui/`），不再用 GDI 自绘，GDI 只负责画文字。

| 入口 | 行为 |
|---|---|
| 鼠标移到 NONO 身上 | 8 个快捷图标环绕球面板中心出现（尺寸随本体缩放等比，1 倍时图标 24px） |
| 点本体 | 打开主面板（屏幕居中，可拖动，拖后位置当次会话保留） |
| 主面板中键 | 功能芯片环（10 个芯片分两页，翻页用主面板左右箭头） |
| 主面板左/右箭头 | 基本资料 / 储藏空间 |
| 主面板底部 8 个芯片位 | 已装配的芯片（未装配为灰暗） |
| 黑屏区域 | faceMc 表情动画：待机眨眼、开机/关机、装载芯片、充电各段 |

快捷栏会跟随 NONO 移动；光标没动（NONO 自己走开）时保持显示，主动移开超过 30px 才收起。

## 构建

需要 Go 1.21+：

```bash
# Linux 交叉编译（-H windowsgui：GUI 子系统，启动不弹控制台黑框）
GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui" -o nono_deskpet.exe .

# Windows 本机
go build -ldflags "-H windowsgui" -o nono_deskpet.exe .
```

`build.sh` / `build.bat` 已提供。

## 素材管线

### 矢量后端（默认）

精灵本体默认走**自研矢量材质**：`tools/importer/swfshape.py` 直接解析 SWF 的
SHAPERECORD 位流（含 DefineShape1-4 与 DefineMorphShape2 的起始形状），
生成分层矢量 JSON（`assets/sprites/<key>/sprite.json` + `parts/shape_*.json`），
运行时用纯 Go 光栅化（1x + 3x 超采样），不依赖 Flash 工具链。

```bash
# 逐个动画重导（--native = 自研解析器，不给则走 FFDec SVG）
python3 tools/importer/build_anims.py --native
# 单形态 idle：7 形态 × 8 方向（plain/super 17 帧，annual 7 帧；--out 给父目录，脚本自己拼 name）
# 至尊（annual）：根 sprite 138，产物目录名仍是 annual（formKey 特判，不叫 super6）
python3 tools/importer/build_sprite.py --swf <nonon.swf 路径> --root 127 --name plain \
    --out ../assets/sprites --native
```

- 开关：默认开，`NONO_VEC_SPRITE=0` 回退到 PNG 烘焙帧
- 面板底图、飞行器仍走 PNG：它们的矢量路径另有缺陷，由 `NONO_VEC=1` 单独控制
- 质量口径：shape 级与 FFDec SVG 比对，宽高必须 100% 一致、几何 IoU ≥ 0.95
  （当前全量 2542/2792 通过）；sprite 级逐帧渲染与 PNG 素材比，可见内容尺寸
  与位置一致（plain 38×61 vs 38.8×62.8 逻辑像素）
- morph（DefineMorphShape2）的 StartBounds 只覆盖起始轮廓，贝塞尔控制点会伸出画布，
  所以 morph 的 bounds 取「声明 bounds ∪ 实际几何范围」，尖刺不会被裁

### PNG 烘焙（回退路径）

`tools/bake.py` 用 FFDec 分层导出 PNG，再按自动对齐参数叠加成整帧。

**SWF 是矢量图**，1x 栅格化后再放大必然发糊。管线默认按 **4x（`NONO_ZOOM=4`）** 导出，
运行时用面积平均下采样到显示尺寸，边缘比双线性放大干净得多：

```bash
python3 tools/bake.py plain <nono.swf> [action/1.swf ...] [exp/1.swf ...]
```

- 分层：`body` + `color_2`（纯白蒙版），按 sprite 名判断层级，不硬编码
- 对齐：逐层搜索 dx/dy 使叠加误差最小（body dx=11 dy=8，err≈4.9）
- 输出：`assets/plain/idle/<方向>/NNN.png`、`action/<id>/`、`exp/<id>/`
- 素材尺寸：普通形态 253×270（4x），`meta.json` 记录 `zoom: 4`
- 显示缩放：`scale=2` 时下采样到 126×135；`scale=4` 时 1:1 原生像素（最清晰）
- 悬浮：站定播放 `idle_anim` 逐帧序列（89 帧、一轮约 2.96 秒），上下浮动来自帧内容本身；窗口、气泡、点击区域都**不**位移（`bobAmp=0`）
- 帧率：动作/表情 12.5fps；待机按帧自带时长（原 GIF 节奏）；移动帧率由设置项"移动帧率"控制（0 静止 / 4 慢 / 3 中 / 2 快）
- 气泡宽度跟着文字走：文字长时窗口临时加宽、角色保持居中且屏幕位置不动，超长自动缩到 13px

## 已知限制

- 普通形态只有动作 1、2，缺 3/4/5/6/7；调用缺失动作时降级为表情并提示
- 音效只有表情 1–4 与开机动作（从 SWF 提取，`assets/sfx/`），其余动作无音效
- 超能 1–5 形态已接；至尊（`annual`）形态已接（超能 13 级）。41 项「皮肤」物品（700800–700840）官方无素材且客户端不请求，不做复刻，详见 `docs/NONO形态资源缺失文档.md`
- 至尊形态待机每方向 **7 帧、4–5 种姿态**；导出必须带 `--dir-seg --anim-frames 7`，缺省参数会把方向容器连同子层冻结成单帧（曾因此误判为「素材没有动画」）

## 目录

```
nono_deskpet/
├── main.go      窗口、消息循环、状态机、菜单
├── win32.go     Win32 API 绑定
├── render.go    染色、缩放、DIB 合成
├── assets.go    帧清单与懒加载
├── chips.go     芯片表与本地效果
├── panels.go    旧 GDI 面板的调度骨架（已被下面的贴图面板取代）
├── panel_ui.go  通用分层窗口（贴图 + 按钮命中 + 拖动 + 透明穿透）
├── panel_main.go / panel_shortcut.go / panel_info.go / panel_storage.go / panel_chip.go
│                主面板 / 快捷栏 / 基本资料 / 储藏空间 / 功能芯片
├── panel_shell.go  7 个精灵类功能面板（贴游戏模块 SWF 原图）
├── panel_input.go  昵称输入窗
├── uitheme.go   字体与配色
├── face.go      主面板黑屏 faceMc 动画（279 帧）
├── tray.go      托盘图标与音效
├── autostart.go 开机自启（注册表）
├── save.go      存档
├── tools/bake.py 素材烘焙
├── tools/check_assets.py 素材完整性校验
└── assets/      帧 + chips.json + sfx/
```

## 内存

4x 单帧 253×270×4 ≈ 273KB，502 帧全驻留要 137MB（实测峰值 283MB）。
因此帧按需解码：待机只留当前+上次方向（8 帧 ≈ 2.2MB），动作/表情只留正在播的那一套，
播完即释放。常驻实测 **44–49MB**。

## 自测

```
set NONO_SELFTEST=1   # 自动跑：面板→粉色→黑色→恢复→护盾→悬浮，并输出 bmp 取证
set NONO_LOG=1        # 写 nono_debug.log
set NONO_NO_PASSTHROUGH=1  # 关闭透明区穿透（Wine 下调试用）
```
