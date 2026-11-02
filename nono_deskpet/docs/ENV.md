# NONO 桌宠 · 环境变量总表

> 生成于 nono-20261102-02（D-19）。共 55 个 `NONO_*` 开关（含 5 个 `NONO_VEC_*` 与 `NONO_TEST_*` 系列）。
> 全部为**调试/排查/自测用**，产品路径不依赖任何环境变量。
> 未设置 = 功能关闭 / 走默认行为。
> 维护约定：新增调试开关必须在调用点注释写明取值与用途，并在本表补行。

## 通用

| 变量 | 取值 | 作用 |
|---|---|---|
| `NONO_LOG` | `1` | 写 `nono_debug.log`。日志超 8MB 自动截断（`.1` 保留一份） |
| `NONO_TRACE` | 任意 | 打印逐帧 trace（绘制/时钟细节） |
| `NONO_PROF` | 任意 | 打印性能剖析（tick/绘制耗时） |
| `NONO_SAY` | 文本 | 启动后把该文本当气泡台词显示一遍 |
| `NONO_NO_PASSTHROUGH` | `1` | 关闭点击穿透（`WM_NCHITTEST` 恒返回 `HTCLIENT`） |
| `NONO_DISPCACHE_CHECK` | 任意 | 校验待机帧显示缓存命中 |

## 内存 / 性能（2026-10-30 起有默认值，一般不设）

| 变量 | 取值 | 作用 |
|---|---|---|
| `NONO_GCPERCENT` | 数字 | 覆盖 `GOGC` 百分比（默认 150） |
| `NONO_MEMLIMIT` | MB 数字 | 覆盖软内存上限（默认 200MB；`0` = 关闭） |
| `NONO_FREEMEM` | 秒 | 到点做一次 GC + `FreeOSMemory` 归还空闲页（默认 20） |
| `NONO_MEMSTATS` | 任意 | 每 4 秒打一条内存统计（8 条后停） |
| `NONO_HEAPPROFILE` | 秒 | 延迟 N 秒写 `heap.prof`（默认 15） |
| `NONO_CPUPROFILE` | 任意 | 启动 CPU profile 20 秒，写 `cpu.prof` |
| `NONO_PREWARM_FREEMEM` | 任意 | 预热完成后提前归还空闲页 |

## 渲染 / 素材

| 变量 | 取值 | 作用 |
|---|---|---|
| `NONO_VEC` | `0` | 总开关：关闭矢量后端（UI 与精灵都回退 PNG） |
| `NONO_VEC_SPRITE` | `0` | 关闭精灵矢量后端（回退 PNG 帧） |
| `NONO_VEC_FLY` | `0` | 关闭飞行器矢量合成（回退 PNG 整帧图） |
| `NONO_VEC_UI` | `0` | 关闭 UI 面板矢量底图（回退 PNG） |
| `NONO_VEC_BASE` | — | （保留，未使用） |
| `NONO_DEBUG_CANVAS` | `1` | 把合成帧落盘成 PNG（排查绘制问题） |
| `NONO_DEBUG_CENTER` | 任意 | 把快捷栏环心画成红十字 |
| `NONO_DEBUG_DIB` | 任意 | 把 DIB/画布落盘（排查 DIB 路径） |
| `NONO_DEBUG_LAYERS` | 任意 | 逐层渲染并落盘（排查分层） |
| `NONO_DUMP_PANEL` | PNG 路径 | 把面板画布落盘（Wine 下分层窗口截图不可用时的取证） |
| `NONO_IDLE_DUMP` | 任意 | 待机帧落盘 |
| `NONO_FLAME_TEX` | 任意 | 覆盖尾焰贴图路径（程序化尾焰用） |
| `NONO_GHOST` | `0` | 关闭超能残影特效 |

## 飞行

| 变量 | 取值 | 作用 |
|---|---|---|
| `NONO_FLY_STYLE` | `1`~`4` | 启动即固定飞行载具风格并直接起飞 |
| `NONO_FLY_RIDE` | 任意 | 强制飞行挂载在某方向（调试挂载几何） |
| `NONO_FLY_ANIM` | 任意 | 固定飞行动画段 |
| `NONO_FLY_DIR` | 方向 | 固定飞行朝向 |
| `NONO_FLY_NO_MACHINE` | 任意 | 关闭飞行载具整体（只留本体） |
| `NONO_DUMP_FLY` / `NONO_DUMP_FLY_ALL` | 任意 | 落盘飞行合成帧（单个 / 全部） |
| `NONO_FLY_DUMP_ALL` / `NONO_FLY_DUMP_DIRS` / `NONO_FLY_DUMP_N` | 任意 | 飞行序列帧落盘（全部 / 按方向 / 指定数量） |

## 行为调试

| 变量 | 取值 | 作用 |
|---|---|---|
| `NONO_AUTOFLY` | 任意 | 启动后 ~3s 自动起飞（无鼠标环境验证用） |
| `NONO_AUTOPLAY` | 动作 ID / `exp<ID>` | 启动后 1.5s 自动播一次动作/表情 |
| `NONO_SHOW_SHORTCUT` | 任意 | 启动后自动弹出快捷栏 |
| `NONO_SELFTEST` | `1` | 跑自测序列（面板/芯片/截图），见 `main.go` 的 `WM_APP+*` case |
| `NONO_UI` | `main`/`shortcut`/`info`/`storage`/`chip`/`lab` | 启动后自动打开对应面板并截图取证 |

## 自测（NONO_TEST_*）

| 变量 | 取值 | 作用 |
|---|---|---|
| `NONO_TEST_ACTION` / `NONO_TEST_ACTIONS` | 动作 ID | 自动播动作 |
| `NONO_TEST_EXP` | 表情 ID | 自动播表情 |
| `NONO_TEST_FORMS` | 任意 | 形态切换自测 |
| `NONO_TEST_FORM`（未列） | — | （并入 FORMS） |
| `NONO_TEST_POWER` | 任意 | 开关机自测 |
| `NONO_TEST_TURNON` | 任意 | 模拟托盘「开机 NONO」 |
| `NONO_TEST_USE` | 芯片 ID | 直接调用 `useChip` 验证数值效果 |
| `NONO_TEST_MAINACTION` | 动作名 | 主面板按钮动作自测 |
| `NONO_TEST_PANEL` | 面板名 | 面板打开/绘制自测 |
| `NONO_TEST_CHIPFLIP` | 任意 | 芯片翻页自测 |
| `NONO_TEST_AUTOSTART` | `on`/`off` | 设置/取消开机自启并打印结果 |

## 已用但未在代码中 Getenv 的（历史遗留说明）

- `NONO_VEC_BASE`：见上表（保留未使用）。
- 55 个开关中缺失的、但 grep 时出现过的 `NONO_{AUTOFLY,AUTOPLAY,SAY,...}` 均已在上面列出。