# NONO 桌宠功能 1:1 排查表（2026-10-22）

依据全部来自客户端 AS 源码，逐个人工核对。每项标注：游戏行为 → 当前实现 → 差异。

## 一、主面板 MachineDogPanel（按钮 7 个）

源码：`MachineDogPanel.as` L142-300

| # | 按钮 | 游戏行为 | 当前实现 | 差异 |
|---|---|---|---|---|
| 1 | chargBtn 电池（提示"充电"） | `onCloseThisHandler(null)` **先关闭面板**，`chargeTime==0` 时才发 `NONO_CHARGE=1`，面板重新打开后播 `CHARGING_FACE` | 不关面板，直接把能量置 100 + 气泡 | ❌ 未关面板、无充电中状态、充电完成后面板不重开 |
| 2 | closeBtn 开机（提示"开机"） | `superNono==1` 或 `power>0` 才发开机；否则 `Alarm("你的NoNo能量不够!")` | 有能量检查，切换 Open 并气泡 | ⚠️ 文案不一致，缺少"开机中"表情 |
| 3 | closeMc 关机（提示"关机"） | `playFace(CLOSE_FACE)` + 发 `NONO_CLOSE_OPEN=0`，面板保持到协议返回 | 只 `say("NONO 已关机")`，不播表情 | ❌ 缺 CLOSE_FACE |
| 4 | rightMc 右箭头（"打开储藏空间"） | 打开储藏，贴主面板右侧 | ✅ 已实现 | — |
| 5 | leftMc 左箭头（"打开基本资料"） | 打开基本资料，贴主面板左侧 | ✅ 已实现 | — |
| 6 | chipBtn 中键（"打开功能"/"关闭功能"） | 打开时 `bgMc/leftBtn/rightBtn` 变可见；再点关闭并隐藏这三者；`state[0]==0`（未开机）→ `Alarm("NoNo的功能只有在开机状态下才可以使用。")` | 开关环，但底部箭头/背景条不随状态显隐；未做开机检查 | ❌ 显隐与开机检查 |
| 7 | closeThisBtn 关闭（"关闭"） | 关面板并 `closeAll()` 广播 CLOSE 给子面板 | ✅ 已实现 | — |

补充：`pageTxt` 显示 `curPage/totalPage`，在主面板上——当前未显示。

## 二、快捷栏 NonoShortcut（8 槽）

源码：`NonoShortcut.as` L78-130、`NonoShortcutKeyItem.as` L71-98

| key | 图标 id | 提示 | 游戏行为 | 当前实现 | 差异 |
|---|---|---|---|---|---|
| 1 | 1 | 精灵治疗 | `PetManager.cureAll()` | 气泡"已为你的精灵恢复体力" | ⚠️ 无实际面板（桌宠无精灵系统，可保留气泡） |
| 2 | 2 / 2_1 | 跟随主人 / 回家休息 | 切换；已在跟随 → `Alarm("你的NoNo现在已经处于跟随状态！")`；未开通功能 → `Alarm("你的NoNo还没有开通这个功能哦!")` | 切换 Follow + 气泡 | ⚠️ 缺未开通与重复跟随提示 |
| 3 | 3 | 经验分配 | 打开 `ExpAdmPanel` | 气泡"暂无待分配经验" | ❌ 无界面 |
| 4 | 4 | 给NoNo充电 | `onPower` → 打开充电流程 | 直接满能量 | ❌ 无界面 |
| 5 | 700011 | 精灵仓库 | `PetStorageController.show()` | 打开芯片储藏面板 | ⚠️ 语义不同（游戏是精灵仓库，桌宠无精灵） |
| 6 | 700015 | 精灵追踪 | `SpriteTrackController.show()` | 气泡"暂无可追踪目标" | ❌ 无界面 |
| 7 | 700017 | 飞行模式 | 变身状态 → `Alarm("你目前处在变身状态不可以飞行！")`；否则飞 | 气泡"能量不足" | ⚠️ 提示不符 |
| 8 | — | 空槽 | — | 空 | — |

## 三、功能芯片环 CanChipCollectionPanel（10 个）

源码：`CanChipCollectionPanel.as` L110-135，`featureApp/App_7000xx.as`

| 芯片 | 提示 | 游戏行为 | 当前实现 | 差异 |
|---|---|---|---|---|
| 700001 | 精灵恢复 | `PetManager.cureAll()` | 本地效果（加护盾） | ❌ 行为不对 |
| 700002 | 分子转化仪 | 打开 `MoleculePanel`；转化中 → `Alarm("分子转化仪中有正在转化的精元,剩余时间:xx")` | 无 | ❌ |
| 700003 | 经验分配器 | 打开 `ExpAdmPanel` | 无 | ❌ |
| 700004 | 精灵模拟训练 | `PetSimulationTrainingController.start()` | 无 | ❌ |
| 700005 | 跟随模式 | 发 `NONO_FOLLOW_OR_HOOM=1`；已跟随 → `Alarm` | 无 | ❌ |
| 700011 | 精灵仓库 | `PetStorageController.show()` | 无 | ❌ |
| 700015 | 精灵追踪 | `SpriteTrackController.show()` | 无 | ❌ |
| 700016 | 技能排序 | 打开 `PetListPanel` | 无 | ❌ |
| 700017 | 开启飞行模式 | 变身检查 → 飞行 | 无 | ❌ |
| 700018 | 元神赋形 | `SoulTransformController.show()` | 无 | ❌ |

**全部共性差异**：游戏里点任意芯片后 `_closeFun()` → 广播 CLOSE → **整个 NONO 面板与环一起关闭**。当前点芯片只出效果，面板不关。❌

## 四、储藏空间 MachineDogStoragePanel

源码：`MachineDogStoragePanel.as` L110-260

| 项 | 游戏行为 | 当前实现 | 差异 |
|---|---|---|---|
| 页签"普通储藏" | `_curType=_nono`，`bgMc.gotoAndStop(1)` | 只压暗另一个页签，无背景切换 | ⚠️ 只导出一张底图 |
| 页签"超能储藏" | `_curType=_superNono`，`bgMc.gotoAndStop(2)` | 同上 | ❌ 缺第 2 帧底图 |
| 页码 | `pageTxt = curPage/totalPage` | 无 | ❌ |
| 3×3 格子 | 每格一个 `ChipListItem`，含图标 + 数量 | 只贴图标，无数量 | ❌ |
| 点芯片 | 五道校验：超能页非超能 NOON→Alarm；未开机且非 700401-700410→Alarm；AI 不足→Alarm；700017 需 superLevel≥4→Alarm；消耗型数量-1 用完清除 | 只给提示"已使用" | ❌ 全部未实现 |
| 点后面板 | `PlayID!=0` → 关全部；否则显示描述 | 无 | ❌ |

## 五、基本资料 NewNonoInfoPanel

源码：`NewNonoInfoPanel.as` L560-700、`ChipInfoBgPanel.as`

| 项 | 游戏行为 | 当前实现 | 差异 |
|---|---|---|---|
| 文本字段 | 昵称/AI/主人/生日 + 4 条进度数值 | ✅ 已对齐 | — |
| 底部"加载芯片"区 | `_iconMc` 8 个普通芯片位 + `_superIconMc` 超能位，`ChipInfoBgPanel.addIcon(id, isHave)`：已装配彩色、未装配灰暗 | **完全空白** | ❌ 缺 8 个芯片图标 |
| 左右箭头 | `_curPage/_curSuperPage` 翻页 | 有按钮，无行为 | ❌ |
| 铅笔改昵称 | 打开改名输入 | ✅ | — |
| 关闭 | 广播 CLOSE | ✅ | — |

## 六、其他入口

| 项 | 游戏行为 | 当前实现 | 差异 |
|---|---|---|---|
| 点击 NONO 本体 | 游戏内无"点本体开面板"，面板靠快捷栏/机器人菜单 | 单击随机动作 + 双击开面板 | ⚠️ 桌宠自定义，保留 |
| 右键菜单 | 游戏无 | 桌宠自定义（设置/退出） | 保留 |

## 修复优先级

1. **P0 交互流程**：主面板充电先关面板、关机播 CLOSE_FACE、功能环打开时显隐 bgMc/箭头、未开机时点中键报警、点芯片后关全部面板。
2. **P0 缺失界面**：基本资料底部 8 个芯片位（素材 + 装配态配色）。
3. **P1 储藏空间**：页码、超能页背景帧、点芯片五道校验与数量递减。
4. **P1 快捷栏与芯片行为对齐**：未开通/已跟随/变身的提示文案、经验分配器等无精灵系统的功能用"提示 + 面板占位"。
5. **P2**：主面板 pageTxt 页码、充电中状态机（chargeTime）。
