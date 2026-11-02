# NONO 游戏内 UI 1:1 复刻方案

> 代号 20261022-06 · 2026-10-22
> 目标：桌宠里的 NONO 交互与赛尔号游戏内一致——悬浮出现快捷栏、点开是同一个面板、平移过去是同一套界面。
> **核心原则：不重绘。直接复用 SWF 原素材贴图，只补文字与命中区。** 这样才可能"和游戏差不多"。

---

## 0. 结论先行

现有 `panels.go` 是 GDI 自绘（`fill/box/button/bar`），外观与游戏无关，**必须整体替换**为：

```
FFDec 导出的面板 PNG（背景/按钮四态）
  + GDI+ 贴图（AlphaBlend / UpdateLayeredWindow）
  + 按游戏坐标摆放的命中矩形
  + GDI 文本按游戏字号/颜色绘制
```

素材已经全部在本地，无需联网、无需美术重画。

---

## 1. 证据链（代码 + 资源）

### 1.1 源码（`Seer-golang/front-end scripts/NieoCore scripts/com/robot/`）

| 文件 | 作用 |
|---|---|
| `core/mode/NonoModel.as:204-235, 1069-1117` | 本体鼠标事件：自家 NONO `ROLL_OVER` 弹快捷栏；别家 NONO `CLICK` 只弹信息面板 |
| `core/ui/nono/NonoShortcut.as`（348 行） | 快捷栏：8 槽位、图标资源、提示文案、各槽行为 |
| `core/ui/nono/NonoShortcutKeyItem.as` | 单个快捷图标：加载 `resource/nono/shortcutKey/{id}.swf`，ToolTip 显示文案 |
| `core/ui/nono/NonoInfoPanelController.as` | 信息面板入口（模块名 `NewNonoInfoPanel`） |
| `core/info/NonoInfo.as` | 数据模型（16 个字段，见 §4） |

### 1.2 面板 SWF（`森木编译版本/win-unpacked/resources/gameres/root/`）

| 资源 | 模块名 | 说明 |
|---|---|---|
| `module/com/robot/module/app/MachineDogPanel.swf` | `MachineDogPanel` | NONO 主面板 + 储藏空间 + 功能芯片面板（同一个 SWF，含 23 个类） |
| `module/com/robot/module/app/NewNonoInfoPanel.swf` | `NewNonoInfoPanel` | 基本资料面板（8 个类） |
| `resource/taskIcon.swf` → `DefineSprite_488_UI_NonoShortcut` | — | 快捷栏底板 |
| `resource/nono/shortcutKey/{1,2,2_1,3,4,700011,700015,700017}.swf` | — | 快捷栏 8 个图标（SimpleButton 四态） |

### 1.3 已导出外观（本方案附件，`docs/game_ui/`）

| 文件 | 原生尺寸 | 内容 |
|---|---|---|
| `main_panel.png` | 535×395 | NONO 主面板：脸 + 电池/电源/关闭 + 三个蓝箭头 + 页码 |
| `storage_panel.png` | 242×300 | 储藏空间：普通储藏/超能储藏页签 + 3×3 格 + 翻页 |
| `info_panel.png` | 298×349 | 基本资料：昵称/超能等级/AI/主人/生日 + 四条进度条 + 加载芯片 |
| `chip_item.png` | 60×60 | 仓库单格（淡蓝圆角） |
| `shortcut_key_1.png` | 96×96（含透明边） | 快捷栏图标 1（医疗十字） |

> 图片均为 `zoom 2` 导出，运行时按 1:1 缩小到原生尺寸或直接用 2x 图作高清素材。

---

## 2. 游戏内交互全景

```
鼠标移到自家 NONO 身上（头顶 +30px）
└─ 快捷栏 NonoShortcut（8 格 × 图标）
   ├─ 1  精灵治疗        → 超能免费；普通弹确认"恢复体力需要花费50赛尔豆…"
   ├─ 2  跟随主人/回家休息 → 地图不允许时变灰（灰度滤镜 + 禁点）
   ├─ 3  经验分配        → ExpAdmPanel（经验分配器）
   ├─ 4  给NoNo充电       → 超能 NONO 该槽隐藏
   ├─ 5  精灵仓库 700011  → 需拥有该功能 + AI 等级达标
   ├─ 6  精灵追踪 700015  → 同上
   ├─ 7  飞行模式 700017  → 需超能 4 级
   ├─ 8  （保留空槽）
   └─ 点底板 bgmc       → 主面板 MachineDogPanel
鼠标移出 → 快捷栏消失

点击别家 NONO → 基本资料面板（只读，无按钮）

主面板 MachineDogPanel（535×395）
├─ 面板中部表情 faceMc（开机/关机/充电/加载芯片 各有表情）
├─ 🔋 chargBtn  「充电」    → 发充电，充电中禁用
├─ ⏻ closeBtn 「开机」    → 能量>0 或超能才可；否则"你的NoNo能量不够!"
├─ ⏻ closeMc  「关机」    → 关机动画
├─ ✖ closeThisBtn「关闭」   → 关窗
├─ ◀ leftMc   「打开基本资料」→ 基本资料面板
├─ ▶ rightMc  「打开储藏空间」→ 储藏空间面板
├─ ◎ chipBtn  「打开功能」  → 功能芯片面板（3 个/页，主面板右侧 480,280）
├─ ◀▶ leftBtn/rightBtn     → 功能芯片翻页
└─ pageTxt "1/2"           → 页码

基本资料 NewNonoInfoPanel（298×349）
├─ 标题"基本资料" + 右上 ⊙（关闭）
├─ 左侧 NONO 立绘
├─ 昵称（铅笔图标可改，UTF-8 限 16 字节）+ 超能等级 + AI 等级 + 主人 + 生日
├─ 超能值 / 智慧值 / 能量值 / 亲密度 四条进度条
└─ 底部"加载芯片"：小 NONO 头像 + 芯片名 + 左右切换

储藏空间 MachineDogStoragePanel（242×300）
├─ 页签「普通储藏」/「超能储藏」
├─ 3×3 九宫格（每格 60×60，间距 8px，起点 7.5,35.9）
├─ 翻页 ◀ ▶ + pageTxt
└─ 点击芯片 → 校验链（见 §3.4）→ 执行 Chip_7000xx

功能芯片面板 CanChipCollectionPanel（3 个/页）
├─ 芯片集合：700001/2/3/4/5/11/15/16/17/18
├─ 只显示"已拥有"的（NonoInfo.func 位图）
├─ 悬浮显示芯片名
└─ 点击 → App_7000xx 功能面板；700005 若已跟随则提示
```

---

## 3. 逐面板规格

### 3.1 快捷栏 NonoShortcut

- 出现条件：`NonoModel.onOver`，自家 NONO，且不在走路中（`walk.isPlaying` 为 true 时不弹）。
- 位置：NONO 局部坐标 `(0,-30)` 转全局 → 即头顶上方 30px。
- 关闭：`ROLL_OUT` 立刻隐藏；点击底板打开主面板后也隐藏。
- 底板：`UI_NonoShortcut`（228×228 @zoom2），内含 `bgmc` 与 `key1..key8` 容器。
- 图标资源：`resource/nono/shortcutKey/{id}.swf`，每个是 SimpleButton（4 态）。
- 图标排布：8 个 `NonoShortcutKeyItem`，尺寸继承图标（96×96 原 SWF 内实际视觉约 30×30）。
  **待补**：`bgmc` 与 key1..8 的精确坐标需从 SWF 显示列表读取（或实机截图量取）。
- 提示文案（`_tipA`）：
  `精灵治疗` / `跟随主人`（或"回家休息"）/ `经验分配` / `给NoNo充电` / `精灵仓库` / `精灵追踪` / `飞行模式`
- 置灰规则：地图 `getIsLocal` 为 true 时 key2 加灰度滤镜 + 禁点。
- 槽位显示规则：
  - 槽 1、2、3 常驻
  - 槽 4 仅非超能 NONO 显示
  - 槽 5-7 需 `func[id-700001]` 为真；非超能还需 `ai >= ItemXMLInfo.getAiLevel(id)`

### 3.2 主面板 MachineDogPanel

| 元件名 | 外观 | ToolTip | 点击行为 | 前置/失败提示 |
|---|---|---|---|---|
| `chargBtn` | 绿色电池 | 充电 | 发充电指令；`chargeTime != 0` 时禁用 | 充电中不响应 |
| `closeBtn` | 绿色电源 | 开机 | 开机；`state[0]` 为假时显示 | 能量为 0 且非超能 → "你的NoNo能量不够!" |
| `closeMc` | 灰色电源 | 关机 | 播关机表情 + 关机 | 仅开机状态可见 |
| `closeThisBtn` | ✖ | 关闭 | 关面板 | — |
| `leftMc` | 底部左蓝箭头 | 打开基本资料 / 关闭基本资料 | 开/关基本资料面板，箭头切帧 1↔2 | — |
| `rightMc` | 底部右蓝箭头 | 打开储藏空间 / 关闭储藏空间 | 开/关储藏空间，箭头切帧 1↔2 | — |
| `chipBtn` | 中间蓝色圆标 | 打开功能 / 关闭功能 | 开/关功能芯片面板 | 未开机 → "NoNo的功能只有在开机状态下才可以使用。" |
| `leftBtn`/`rightBtn` | 两侧大箭头 | — | 功能芯片翻页，刷新 `pageTxt` | 仅开芯片面板时可见 |
| `pageTxt` | 文本 | — | 显示 `当前页/总页` | — |
| `bgMc` | 芯片面板底板 | — | 仅开芯片面板时可见 | — |
| `faceMc` | NONO 脸 | — | 表情：HIDE_FACE / CLOSE_FACE / CHARGING_FACE / LOADCHIP_FACE | — |

开面板位置：`DisplayUtil.align(MIDDLE_CENTER)` → **屏幕正中**。
三个子面板互斥：开基本资料会关储藏空间与功能面板，反之亦然。

### 3.3 基本资料 NewNonoInfoPanel

- 关闭方式：右上 ⊙ 按钮。
- 昵称修改：点铅笔 → 输入 → 提交（`writeUTFBytes`，最多 16 字节）。
- 数值与 ToolTip：

| 条 | 取值 | ToolTip |
|---|---|---|
| 超能值 `_mc0Mc` | `superEnergy / powerA[superLevel]` | "N级超能NoNo" / "距离升级还需要 X 天超级能量" / 满级"十级超能NoNo" |
| 智慧值 `_mc1Mc` | `iq / total` | "你的AI已经满级了哦" 或 "距离AI升级还需要 X 点智慧值" |
| 能量值 `_mc2Mc` | `power / 100` | 满"能量满满哦！" / 否则"还差 X 点能量" |
| 亲密度 `_mc3Mc` | `mate / 100` | 按等级给文案 |

- 主人显示：自己的 NONO 显示 `MainManager.actorInfo.nick`，别家显示其主人昵称或 userID。
- 超能等级：`_ps0Mc`，未超能显示"曾经 N 级"。
- 底部"加载芯片"：显示当前装备/加载的芯片名。

### 3.4 储藏空间 MachineDogStoragePanel

- 两个页签：`btn1` 普通储藏（`ITEM_LIST 700001..700550 type=2`）、`btn2` 超能储藏；`bgMc` 切帧 1/2。
- 网格：9 格，`x = 7.5 + (60+8) * (i%3)`，`y = 35.9 + (60+8) * (i/3)`。
- 翻页：9 个/页，`pageTxt` 显示 `n/total`；单页时翻页按钮禁用。
- 点击芯片的校验链（顺序不能乱）：
  1. 超能储藏 + 非超能 → "主人，我已经失去了超能晶体的超级能量了，只有超能NoNo才能使用这些超能道具哦！"
  2. 未开机且非 700401-700410 → "该芯片只有在开机状态下才能使用。"
  3. 非超能且 `ai < 芯片要求 AI` → "你目前的AI等级不能加载此芯片。"
  4. 700017 且 `superLevel < 4` → "你的NoNo超级能量不足，无法使用飞行模式哟…到了超能4级就可以开通飞行模式了哟！"
  5. 消耗品 `itemNum--`，归零则格子清空并重排
  6. 按 `Chip_{itemID}` 类执行效果
- 关闭：右上 ⊙ → 收起面板（`rightMc` 箭头恢复帧 1，提示改回"打开储藏空间"）。

### 3.5 功能芯片面板 CanChipCollectionPanel

- 位置：主面板右侧固定 `(480,280)`（相对面板坐标，实际是屏幕坐标 `_panelMc.x/y` 基准）。
- 每页 3 个芯片按钮，按 `_allIdA` 反序首页 `[700003,700002,700001]`。
- 只渲染 `func[id-700001] == true` 的芯片。
- 悬浮显示芯片名（`_tipA`）。
- 点击执行 `com.robot.app.nono.featureApp.App_{id}`，然后关面板。
- 700005 若 `state[1]`（已跟随）→ "你的NoNo现在已经处于跟随状态！"

---

## 4. 数据模型（NonoInfo，16 字段）

| 字段 | 类型 | 说明 |
|---|---|---|
| `userID` | uint | 主人 ID |
| `flag[32]` | bit | 标记位 |
| `state[32]` | bit | `state[0]`=开机、`state[1]`=跟随 |
| `nick` | UTF-8 ≤16B | NONO 昵称 |
| `superNono` | bool | 是否超能 NONO |
| `color` | uint | 颜色壳（变色芯片写入） |
| `power` | ×1000 | 能量，满 100 |
| `mate` | ×1000 | 亲密度，满 100 |
| `iq` | uint | 智慧值 |
| `ai` | ushort | AI 等级 |
| `birth` | ×1000 | 生日时间戳 |
| `chargeTime` | uint | 充电剩余时间 |
| `func[160]` | bit | 功能解锁位图（芯片 id - 700001 索引） |
| `superEnergy` | uint | 超能能量 |
| `superLevel` | uint | 超能等级 |
| `superStage` | uint | 超能形态 1-5 |
| `flyStyle` | uint | 飞行样式 |

桌宠本期只需实现其中：`state[0]/state[1]`、`nick`、`color`、`power`、`mate`、`iq`、`ai`、`birth`、`chargeTime`、`func[]`；超能相关字段保留结构但不启用（首期仅普通形态）。

---

## 5. 渲染与交互实现

### 5.1 面板窗口
- 每个面板一个 `WS_POPUP | WS_EX_LAYERED` 窗口，`UpdateLayeredWindow` 贴 32 位 DIB。
- 背景 = 导出 PNG（运行时按目标尺寸缩放，或预先烘焙成 2x 素材）。
- 文字 = GDI `TextOutW` 画在 DIB 上（字体：微软雅黑，游戏用宋体/黑体，按截图对字号）。
- 按钮多态：导出 SimpleButton 的 up/over/down 三帧 PNG，鼠标移动切换贴图。

### 5.2 命中区
- 每个按钮一个矩形（从 SWF 元件坐标 + 截图量取），`WM_MOUSEMOVE` 判断 hover，`WM_LBUTTONDOWN/UP` 判断点击。
- 快捷栏按图标矩形命中；底板 `bgmc` 单独命中。
- 面板外框不做穿透；面板内部空白区域不穿透（游戏里也是不穿透）。
- 主窗口（NONO 本体）保持现有 alpha 穿透逻辑。

### 5.3 状态机
```
Idle ──悬浮──> Shortcut ──移出──> Idle
                └─点底板─> MainPanel
MainPanel ──左箭头──> InfoPanel ──⊙──> MainPanel
          ──右箭头──> StoragePanel ──⊙──> MainPanel
          ──中键──> ChipPanel ──点芯片/再点中键──> MainPanel
```
同一时刻只允许一个面板窗口；子面板打开时主面板保持显示（游戏里主面板不关）。

### 5.4 与现有代码的关系
- 保留：`main.go`（本体渲染）、`render.go`、`assets.go`、`save.go`、`tray.go`、`win32.go`、`chips.go`（芯片效果）
- 重写：`panels.go`（430 行）→ 拆成 `panel_main.go` / `panel_info.go` / `panel_storage.go` / `panel_chip.go` / `panel_shortcut.go` / `uitheme.go`（贴图与文字工具）
- 新增：`assets/ui/` 目录存放面板与图标 PNG（随 assets 外置）

---

## 6. 分期

| 期 | 内容 | 预估 |
|---|---|---|
| P1 | 素材管线：批量导出面板/按钮/图标 + 坐标标定表 | 0.5 天 |
| P2 | 主面板（贴图 + 8 个按钮 + 表情 + 页码） | 1 天 |
| P3 | 快捷栏（8 图标 + 悬浮/移出 + 置灰 + 点开主面板） | 0.5 天 |
| P4 | 基本资料面板（四条进度条 + 昵称改 + 关闭） | 0.5 天 |
| P5 | 储藏空间（页签 + 3×3 + 翻页 + 校验链 + 装备/卸载） | 1 天 |
| P6 | 功能芯片面板（3/页 + 翻页 + 点击执行） | 0.5 天 |
| P7 | 联调：面板互斥、状态同步、存档、Wine 实跑截图比对 | 0.5 天 |

总计约 4.5 天工作量（按现有代码基础）。

---

## 7. 需要你拍板的三件事

1. **面板位置**：游戏里主面板居中屏幕；桌宠是弹在本体附近（例如本体右侧/上方），还是也居中？我建议弹在本体旁边、限制在屏幕内。
2. **快捷栏位置**：游戏是头顶上方 30px；桌宠是否同样贴头顶，还是避开本体覆盖（比如右上角）？建议头顶，和游戏一致。
3. **数据来源**：仍是本地存档（自己喂数值），还是这期就把"能量/智慧/亲密度随互动增长"也做进去？前者先保证 UI 一致，后者再加。

---

## 8. 附：已确认但待补的细节

- [ ] `UI_NonoShortcut` 内部 key1..8 的精确坐标（需读 SWF 显示列表或实机截图量取）
- [ ] 快捷栏底板 `bgmc` 的尺寸（228×228 @zoom2 是否含 8 格全部）
- [ ] 面板按钮三态图的尺寸与偏移（导出 SimpleButton 各帧）
- [ ] 游戏字体：面板文字偏宋体/黑体，需从截图比对字号（12~14px）
- [ ] 芯片图标：`items.xml` 中 icon 字段与 `resource/item/` 对应关系

---

## 9. 主面板坐标标定（已实测，2026-10-22）

方法：对 `docs/game_ui/main_panel.png`（1070×790，zoom2）做连通域色块检测，原生坐标 = 像素 / 2。

| 元件 | 原生中心 | 视觉尺寸 | 识别特征 |
|---|---|---|---|
| `chargBtn` 电池 | (202, 280) | 32×32 | 绿色环 |
| `closeBtn`/`closeMc` 电源 | (265, 280) | 44×44 | 绿色粗环 |
| `closeThisBtn` ✖ | (327, 282) | 32×34 | 灰色圆 |
| `leftMc` 底部左箭头 | (164 附近, 318) | 22×27 | 亮蓝三角 |
| `rightMc` 底部右箭头 | (367 附近, 318) | 22×27 | 亮蓝三角 |
| `chipBtn` 中键 | (264, 336) | 44×37 | 亮蓝圆 |
| `leftBtn` 左侧大箭头 | 约 (150, 320) | — | 浅蓝大三角 |
| `rightBtn` 右侧大箭头 | 约 (385, 320) | — | 浅蓝大三角 |

> 实现时改用「模板匹配」：以导出的按钮 PNG 在面板图上做归一化相关，自动取精确 bbox，避免人工误差。

按钮资源已导出：`/tmp/np_btn`（76 个 PNG，含 `MC700001/2/3/4/5/11/15/16/17/18` 十个功能芯片按钮）。

| 面板 | 原生尺寸 | 网格参数 |
|---|---|---|
| 主面板 | 535×395 | 屏幕居中 |
| 仓库 | 242×300 | 格 60×60，间距 8，起点 (7.5, 35.9) |
| 基本资料 | 298×349 | — |
| 芯片格 | 60×60 | — |
