# NONO 等级 → 形态 → 动作资源对照

> 路径说明：文中服务端源码统一写绝对路径 /app/working/workspaces/a2ZDuB/kaiyuanhao/（外部参考源码，未随交接复制到本地）。
> 生成日期：2026-10-21
> 依据：客户端反编译源码 `com/robot/core/mode/NonoModel.as` + 配置表 `MachXMLInfo` 嵌入式 XML + 实际资源目录 `gameres/root/resource/nono/`
> 生效版本：`gameres_deploy` 与 `森木编译版本/win-unpacked/resources/gameres` 的 `resource/nono/` **逐文件 diff 完全一致**（157 个文件 / 3.2 MB）

---

## 1. 一句话结论

NONO 有三套资源树：**普通 NONO → `nono/nono.swf`**、**超能 NONO → `nono/super/nono_<形态>.swf`**、**至尊年费 NONO → `nono/annual/nono.swf`**。
超能等级 1–12 只换算成形态 1–5；动作/表情文件按 `动作ID_形态.swf` / `表情ID_形态.swf` 取，**资源里本身有缺口，缺失的动作不会播放也不会报错**。

---

## 2. 等级 → 形态 → 文件映射

### 2.1 映射规则（服务端）

`internal/core/userdb/userdb.go:1273 calculateSuperNonoTypeByLevel()` 与 `internal/handlers/handlers.go:27217`（同逻辑）、`internal/core/nonoformcache/cache.go:98 SuperLevelToForm()`：

| 超能等级 SuperLevel | 形态 SuperStage | 形态名称（按资源序） | 文件 |
|---|---|---|---|
| 0（未开通超能） | — | 普通 NONO | `nono/nono.swf` |
| 1–3 | 1 | 超能 NONO 形态 1 | `nono/super/nono_1.swf` |
| 4–6 | 2 | 超能 NONO 形态 2 | `nono/super/nono_2.swf` |
| 7–8 | 3 | 超能 NONO 形态 3 | `nono/super/nono_3.swf` |
| 9–11 | 4 | 超能 NONO 形态 4 | `nono/super/nono_4.swf` |
| 12 | 5 | 超能 NONO 形态 5 | `nono/super/nono_5.swf` |
| SuperNono = 6（至尊年费） | 忽略形态 | 至尊年费 NONO | `nono/annual/nono.swf` |

> 注意：`SuperNono >= 6` 时客户端走 `annual/` 分支并**完全忽略 superStage**（`NonoModel.as` 构造函数）。
> 因此服务端下发给客户端的 `superStage` 必须是 1–5，绝不要下发 6（客户端会去请求不存在的 `super/nono_6.swf`，表现为 NONO 画不出来）。

### 2.2 判定代码（客户端）

```as
// core/mode/NonoModel.as 构造函数
if (this._info.isExtremeNono) {        // SuperNono >= 6
    this._resPath = "annual/";
    this._useStageSuffix = false;
} else if (this._info.superNono) {     // SuperNono 1..5
    this._resPath = "super/";
    this._useStageSuffix = true;
}
```

`BasePeoleModel.showNono()` 里还有兜底：`if (param1.superStage == 0) param1.superStage = 1;`

---

## 3. 资源路径规则

| 资源类型 | 普通 NONO | 超能 NONO（形态 1–5） | 至尊年费 |
|---|---|---|---|
| 本体 | `nono/nono.swf` | `nono/super/nono_<stage>.swf` | `nono/annual/nono.swf` |
| 动作 | `nono/action/<actionID>.swf` | `nono/super/action/<actionID>_<stage>.swf` | `nono/annual/action/<actionID>.swf` |
| 表情 | `nono/exp/<expID>.swf` | `nono/super/exp/<expID>_<stage>.swf` | `nono/annual/exp/<expID>.swf` |

- 路径由 `ClientConfig.getNonoPath()` 拼接：`<resURL>nono/<参数>.swf`
- 取动作：`NonoModel.getActionRes()` → `_resPath + "action/" + _actionID + ("_" + superStage)`
- 取表情：`NonoModel.getExpRes()` → `_resPath + "exp/" + _expID + ("_" + superStage)`
- 动作/表情都通过 `ResourceManager.getResourceList(..., ["pet","sou"])` 加载：第 1 个是动画 `pet`，第 2 个是音效 `sou`（可选）

---

## 4. 动作表（共 7 个，来自 MachXMLInfo 配置表）

| ID | 名称 | autoEnd | souLoops | 说明 |
|---|---|---|---|---|
| 1 | 充电 | 0（不自动结束） | 999999 | 充电中持续循环，靠取消充电事件收尾 |
| 2 | 开机 | 1（播完自动回本体） | 1 | 打开 NONO |
| 3 | 关机 | 0（不自动结束） | 1 | 关闭 NONO |
| 4 | 玩球 | 1 | 1 | 道具动作 |
| 5 | 吃电池 | 1 | 1 | 道具动作 |
| 6 | 召唤 | 1 | 1 | 跟随状态下被召唤 |
| 7 | 玩魔方 | 1 | 1 | 道具动作 |

配置表原文（`MachXMLInfo` 嵌入式 XML，文件 `配置表/NoNo配置表_MachXMLInfo.xml`）：

```xml
<action>
    <item id="1" name="充电" autoEnd="0" souLoops="999999"/>
    <item id="2" name="开机" autoEnd="1" souLoops="1"/>
    <item id="3" name="关机" autoEnd="0" souLoops="1"/>
    <item id="4" name="玩球" autoEnd="1" souLoops="1"/>
    <item id="5" name="吃电池" autoEnd="1" souLoops="1"/>
    <item id="6" name="召唤" autoEnd="1" souLoops="1"/>
    <item id="7" name="玩魔方" autoEnd="1" souLoops="1"/>
</action>
```

---

## 5. 各形态实际可播放的动作（资源清单，含缺口）

> 以下为资源目录实际存在情况。**表里没有组合 = 文件不存在 = 播放时静默无反应**，这是原版资源本身的缺口，不是 bug。

### 5.1 普通 NONO（`nono.action`）

| 动作 ID | 1 充电 | 2 开机 | 3 关机 | 4 玩球 | 5 吃电池 | 6 召唤 | 7 玩魔方 |
|---|---|---|---|---|---|---|---|
| 有文件 | ✅ | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |

目录实况：`action/1.swf`、`action/2.swf`（仅 2 个）。

### 5.2 超能 NONO（`nono/super/action/<ID>_<形态>.swf`）

| 动作 \ 形态 | 1 | 2 | 3 | 4 | 5 |
|---|---|---|---|---|---|
| 1 充电 | ✅ 1_1 | ✅ 1_2 | ❌ | ❌ | ❌ |
| 2 开机 | ✅ 2_1 | ✅ 2_2 | ✅ 2_3 | ✅ 2_4 | ✅ 2_5 |
| 3 关机 | ✅ 3_1 | ✅ 3_2 | ✅ 3_3 | ✅ 3_4 | ✅ 3_5 |
| 4 玩球 | ✅ 4_1 | ✅ 4_2 | ✅ 4_3 | ✅ 4_4 | ✅ 4_5 |
| 5 吃电池 | ✅ 5_1 | ✅ 5_2 | ✅ 5_3 | ✅ 5_4 | ❌ |
| 6 召唤 | ✅ 6_1 | ✅ 6_2 | ✅ 6_3 | ✅ 6_4 | ✅ 6_5 |
| 7 玩魔方 | ✅ 7_1 | ✅ 7_2 | ✅ 7_3 | ✅ 7_4 | ✅ 7_5 |

缺口汇总：**形态 3/4/5 没有充电动作（1_x 缺失）**；**形态 5 没有吃电池动作（5_5 缺失）**。
形态 1/2 的充电各有专属文件（1_1、1_2）。

### 5.3 至尊年费（`nono/annual/action/`）

| 动作 ID | 1 充电 | 2 开机 | 3 关机 | 4 玩球 | 5 吃电池 | 6 召唤 | 7 玩魔方 |
|---|---|---|---|---|---|---|---|
| 有文件 | ❌ | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ |

目录实况：`2.swf`、`3.swf`、`4.swf`、`6.swf`、`7.swf`（没有 1 和 5）。

---

## 6. 表情（exp）与台词

### 6.1 表情 ID 表

| ID | 名称 | 普通 NONO 台词池 | 超能/年费台词池 |
|---|---|---|---|
| 1 | 开心 | lines 1,2,4,5,7,13,15,16,17,18,19,21,24,25,26,27 | superLines 1,5,6,7,8,11~30 |
| 2 | 生气 | lines 6,26,27 | superLines 9 |
| 3 | 惊讶 | lines 3,10,25,26,27 | superLines 2,10 |
| 4 | 悲哀 | lines 9,11,26,27 | superLines 3,4 |

- 普通 NONO 用 `<exp>` + `<lines>`（27 句）
- 超能 NONO 用 `<superExp>` + `<superLines>`（30 句）
- 年费 NONO 走 `annual/` 资源，表情文件 `annual/exp/1~4.swf`（4 个全齐）

### 6.2 表情资源清单

| 类型 | 资源 |
|---|---|
| 普通 | `exp/1.swf` ~ `exp/4.swf`（4 个全齐） |
| 超能 | `super/exp/<expID>_<stage>.swf`：expID 1–4 × stage 1–5 = **20 个全齐** |
| 年费 | `annual/exp/1.swf` ~ `4.swf`（4 个全齐） |

### 6.3 台词筛选

`MachXMLInfo.getLinesIDForExp(expID, powerLevel, mateLevel)` 按能量/亲密度等级过滤：

- `getPowerLevel()`：power ≤30 → 1；31–69 → 2；≥70 → 3
- `getMateLevel()`：mate ≤30 → 1；31–69 → 2；≥70 → 3
- 命中池内仍随机取一句。

---

## 7. 动作触发链路（命令号 → 动作 ID）

| 触发场景 | 命令号 | 客户端监听 | 结果 |
|---|---|---|---|
| NONO 开/关机 | 9014 `NONO_CLOSE_OPEN` | `OnOffCmdListener` → `CLOSE_OPEN` | 开 → 动作 2；关 → 动作 3 |
| NONO 充电 | 9016 `NONO_CHARGE` | `ChargeCmdListener` → `CHARGEING` | 开始 → 动作 1（循环）；结束 → 回本体/关机 |
| 道具/播放动作 | 9013 `NONO_PLAY` | `NonoPlayCmdListener` | `ItemXMLInfo.getPlayID(itemId)` 得到动作 ID（4/5/7 等） |
| 使用道具（含芯片） | 9010 `NONO_IMPLEMENT_TOOL` | `ToolCmdListener` | 同样按 itemId 查 playID 播动作 |
| 召唤（跟随中） | 2112 `ON_OR_OFF_FLYING` | `NonoManager.onFlyHandler` | `isBeckon=true` → 行走结束时播动作 6 |
| 上线/进入地图首次显示 | 9003 `NONO_INFO` | `NonoModel.initCheck()` | chargeTime≠0 → 动作 1；未开机或无电量 → 动作 3；否则待机 |
| 待机随机表情 | 无（本地定时器） | `startAutoTime()` / `onExpTime()` | 10–20 秒随机（`randomHalve(20000)` = 20s − rand×10s）播一个 exp |
| 改色 | 9012 `NONO_CHANGE_COLOR` | `ChangeColorCmdListener` | 不播动作，只重绘 `color_1`/`color_2` |

### 7.1 播放结束行为

- `autoEnd=1`：播完最后一帧自动卸载动作 MC、恢复本体、再做一次 `initCheck()`（可能接着关机/充电）。
- `autoEnd=0`（动作 1 充电、动作 3 关机）：播完停在最后一帧，必须靠后续事件（充电结束 / 开机 / 关机）切换。

### 7.2 音效

动作第 2 个资源是 `sou` 类音效；仅当 `souLoops > 0` 时播放，`souLoops` 为循环次数（动作 1 = 999999，等价无限循环）。

---

## 8. 其他 NONO 相关资源（非形态/动作，易混淆）

| 路径 | 用途 |
|---|---|
| `nono/nonoFlyModel/1~4/nonoFly.swf` | 飞行模式载具，索引来自 `NonoInfo.flyStyle`（由 2112 请求参数传入），与等级无直接映射 |
| `nono/nonoFlyModel/choicePanel.swf` | 飞行载具选择面板 |
| `nono/nonoFlyingMachine1.swf` | 飞行器通用资源 |
| `nono/chip/*.swf` | 芯片外观/效果（700001–700502） |
| `nono/chipprev/*.swf` | 芯片预览图 |
| `nono/icon/*.swf` | 芯片图标 |
| `nono/shortcutKey/*.swf` | NONO 快捷功能按钮图标（治疗/跟随/经验分配/充电等），**不是动作**，不受形态影响 |
| `module/com/robot/app/NonoSpriteTrack.swf` | NONO 精灵追踪功能面板 |

---

## 9. 源文件索引

| 内容 | 路径（相对外部参考源根 `/app/working/workspaces/a2ZDuB/kaiyuanhao/`） |
|---|---|
| 形态/动作/表情装载逻辑 | `core_original/scripts/com/robot/core/mode/NonoModel.as`（`getActionRes` / `getExpRes` / `initCheck` / `onNonoEvent`） |
| 形态分支判定 | `core_original/scripts/com/robot/core/mode/NonoModel.as` 构造函数 |
| 超能/年费标志解析 | `core_original/scripts/com/robot/core/info/NonoInfo.as`（`superNono=false` / `isExtremeNono = 值>=6`） |
| 显示入口 | `core_original/scripts/com/robot/core/mode/BasePeoleModel.as` `showNono()` |
| 动作/表情/台词配置表 | `core_original/_assets/22_com.robot.core.config.xml.MachXMLInfo_xmlClass.bin`（纯文本 XML） |
| 解析类 | `core_original/scripts/com/robot/core/config/xml/MachXMLInfo.as` |
| 等级→形态服务端换算 | `internal/core/userdb/userdb.go:1273`、`internal/handlers/handlers.go:27217`、`internal/core/nonoformcache/cache.go:98` |
| 实际资源根 | `gameres_deploy/gameres/root/resource/nono/` |

---

## 10. 已核实的事实边界

1. `gameres_deploy` 与 `森木编译版本` 的 `resource/nono` 目录 **diff 无差异**（157 文件）。
2. 客户端反编译源码取自 `core_original`（原版 Core.swf 解包），NONO 装载逻辑未改动。
3. 动作/表情缺口的结论来自**目录实际文件枚举**，不是推测。
4. 形态 5 的 `5_5.swf`（吃电池）与形态 3/4/5 的 `1_x.swf`（充电）确实不存在于发布资源中。
5. 未验证项：SWF 内部帧标记（`pet`/`sou` 类名）未逐个打开核对，仅按文件存在性判断可播放性。
