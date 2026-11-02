# NONO 形态资源缺失文档

> 路径说明：文中服务端源码统一写绝对路径 /app/working/workspaces/a2ZDuB/kaiyuanhao/（外部参考源码，未随交接复制到本地）。
> 2026-10-27 · 代号 20261027-08
> 目的：把「哪些形态/皮肤在官方资源里根本不存在」「哪些存在但客户端不消费」「唯一能做的是哪一套」一次写清楚，以后不必重复考古。
> 所有结论都来自实物：本地 `source_swf/资源/nono/`、官方资源站探测、客户端 AS 源码、`/app/working/workspaces/a2ZDuB/kaiyuanhao/` 服务端源码。

---

## 一、结论速览

| 分类 | 数量 | 素材状态 | 客户端是否消费 | 桌宠处理 |
|---|---|---|---|---|
| 普通 `plain` | 1 | 完整（`nono/nono.swf`） | 是 | 已接入 |
| 超能 `super1`–`super5` | 5 | 完整（`super/nono_1..5.swf` + `super/action|exp`） | 是 | 已接入 |
| 至尊 `annual` | 1 | **完整**（`annual/nono.swf` + `annual/action|exp`，每方向 7 帧 4–5 姿态） | 是（`superStage>=6` 分支） | **本次已接入（形态 6）** |
| 皮肤 `700800`–`700840` | 41 | **本地无、官方资源站也无** | **否**（客户端没有对应的资源请求点） | 不做模型复刻 |

一句话：**能换模型的形态只有 plain / super1-5 / annual 共 7 套，全都做完了；41 项「皮肤」不是模型，官方也没给素材。**

---

## 二、皮肤 700800–700840（41 项）：没有素材，且客户端根本不请求

### 2.1 数据侧

`/app/working/workspaces/a2ZDuB/kaiyuanhao/data/items.xml` 里这 41 项 `ItemKind=5`（皮肤），ID 连续 `700800`–`700840`。
它们只是**物品记录**（名称/价格/描述），没有任何 `model`、`skin`、`res` 字段指向资源文件。

### 2.2 客户端侧：拿不到资源路径

`NonoModel.as` 的 `getNonoPath` 是唯一决定换模型的函数，**全部 8 个调用点**只拼两种路径：

```
""        → 普通 nono/
"super/"  → superStage 1..5 拼 super/nono_<n>
"annual/" → superStage>=6 拼 annual/nono
```

补充证据：

- `NonoInfo` 结构体**没有 skin 字段**，`_resPath` 的取值只有 `""`、`"super/"`、`"annual/"` 三种；
- `NonoModel.as:112-135` 的分支里没有第 4 种前缀；
- 换句话说，物品 ID 700800–700840 在任何路径上都不会变成 URL。

### 2.3 资源侧：确实没有

对 `700800` 试了 6 种命名变体（`700800.swf`、`skin_700800.swf`、`nono_700800.swf`、`700800/nono.swf`、`skin/700800.swf`、`nono/skin/700800.swf`），**全部 404**。
对照组同目录下 `annual/nono.swf`、`super/nono_1.swf`、芯片图 `chip/*.png` 均 200，说明不是网络/权限问题。

`resource/nono/` 目录列表返回 403（禁止列目录），所以**不能断言服务器上一个文件都没有**；能断言的是「已知 6 种命名全 404 + 客户端不消费」。

### 2.4 结论

41 项皮肤**不做视觉复刻**。理由不是偷懒，是**没有素材可用，且原版客户端也不会去加载**——强行自造一套等于脱离原作。

---

## 三、至尊 `annual`：第 7 套形态（本次已接入）

### 3.1 为什么它和皮肤不同

客户端 `NonoModel.as:112-135` 明确有 `annual/` 分支，触发条件是 `superStage >= 6`。
也就是说：**这是官方代码里真实存在、会被请求、有完整素材的第 7 套模型**，只是正常游戏流程里到不了（见 3.4）。

### 3.2 资源清单（本地实测）

| 路径 | 说明 |
|---|---|
| `nono/annual/nono.swf` | 12 486 B，根 `DefineSprite_138`，8 方向 |
| `nono/annual/action/{2,3,4,6,7}.swf` | 动作 5 段（无 1、5） |
| `nono/annual/exp/{1,2,3,4}.swf` | 表情 4 段 |

SWF 结构（自研 `swfshape` 解析器读出）：

- shapes 68 / sprites 70 / 方向 sprite 22；
- 方向容器 5 个（22/43/76/91/137）各 63 帧，帧标签 down=0/leftdown=7/left=15/leftup=23/up=31/rightup=39/right=47/rightdown=55；导出后每方向 7 帧，每帧 7–20 个 part；
- 层：`foot_2`、`foot`、`body`、`color_1`、`shape_137`（共 5 层）；
- 画布 `87.027 × 87.426`（矢量单位），球心 `ballc = (48.077, 50.181)`（`measure_vec_ballc.py` 7 帧中位数实测）。

导出产物：

```
assets/sprites/annual/sprite.json        （8 方向 × 7 帧骨架）
assets/sprites/annual/parts/             （27 个 shape 几何）
assets/sprites/annual/action_{2,3,4,6,7}/
assets/sprites/annual/exp_{1..4}/
assets/annual/meta.json                  （key/zoom/ball/idle_frames，矢量后端会覆盖 canvas/ballc/dirs）
```

### 3.3 桌宠接入方式

| 入口 | 行为 |
|---|---|
| 右键菜单 → 超能形态 → 「至尊 NONO（形态 6）」 | `setSuperLevel(13)` |
| 设置面板 → 「至尊形态」开关 | 开 = 13 级，关 = 0 级（回到普通） |

实现要点（`20261027-08`）：

- `superLevelToForm(13) = 6`，`formKey()` 特判形态 6 → 目录 `annual`（不是 `super6`）；
- 菜单命令号 `menuSuperBase+13`，点击范围放宽到 `+13`；
- `IdleMs` / `MoveMs` 由 `[6]int` 扩到 `[7]int`，下标 6 = 至尊（旧存档 6 元素数组自动补 0，兼容）；
- 设置面板「动画速度」表新增「至尊」一行，面板高度 460×700、窗口 y 由 240 改 60（Wine 800 高屏下原本会被截底）；
- `prewarmForms` 预读列表加入 `annual`。

### 3.4 服务端为什么正常流程拿不到

`外部参考服务端` 的 `nonoFormForClient` 在换算 `superStage` 时**封顶 5**，客户端 `superStage>=6` 的 `annual` 分支永远进不去。
所以游戏里没人见过至尊形态；桌宠把 13 级开放给玩家，属于**还原客户端能力**，不是二创。

### 3.5 待机动画：有 4–5 种姿态（2026-10-27 修正）

先说结论：**annual 有真正的待机动画，每方向 7 帧里含 4 种（rightdown 5 种）不同姿态**。

之前一版写成"annual 没有待机动画、17 帧都是复制"是**错的**，原因与纠正过程记录如下，避免以后再踩：

1. `annual/nono.swf` 的根 sprite 138 确实只有 **1 帧**（原始字节 `8a 00 01 00`：spriteId=138, frameCount=1），主时间轴也只有 1 帧——这一点没变。用户看到的 “138” 是 **chid（角色 ID）**，不是帧数。
2. 真正的动画在方向容器里：`22/43/76/91/137` 各 **63 帧**，帧标签 `down=0, leftdown=7, left=15, leftup=23, up=31, rightup=39, right=47, rightdown=55`（每方向 7–8 帧）。
3. 客户端 `NonoModel.changedir()` 对各部件做 `gotoAndStop(_direction)`，`_direction` 是**字符串标签**，所以父容器停在段首帧。
4. 但父容器段内 7 帧的显示列表完全相同，**变化来自它下面的子 MC 自播**（Flash 语义：父 MC stop 不影响子 MC 运行）。
5. 导出器 `build_sprite.py` 在 `--dir-seg` 缺省时，把方向容器和它的子层**一起冻结在段首**，于是 17 帧导出结果全同（像素级 SHA-256 `uniq=1`）——这是导出器的近似，不是素材事实。
6. 改成 `--dir-seg --anim-frames 7` 重新导出后，逐帧像素哈希：`down/left/leftdown/leftup/up/rightup/right` 各 **uniq=4**，`rightdown` **uniq=5**；桌宠 Wine 实测连续截图 0.4 s 间隔像素变化 115–2330 px（修正前为 0）。

导出参数（已固化）：

```bash
python3 build_sprite.py --swf .../annual/nono.swf --root 138 --name annual \
  --out .../assets/sprites --native --dir-seg --anim-frames 7
```

段长取 7 而不是 8：down 段正好 7 帧；其余方向段长 8，取 7 帧会丢掉最后一帧，但该帧与前一帧姿态相同（序列形如 `A A B B C C D D`），不损失任何姿态。

#### 3.5.1 二次修正（同日，回答「每段 7 帧完全相同」是否成立）

分两层看，**一句话：容器层成立，画面层不成立**。

| 层次 | 结论 | 验证方式 |
|---|---|---|
| 方向容器（22/43/76/91/137）自身 | **段内 7 帧确实完全相同**，8 个段各 `uniq=1`，全 63 帧只有 6 种 | 逐帧序列化完整 op（cid/matrix/cxform/filters/name 全字段）后 MD5 |
| 最终画面 | **不相同**：颜色/触角/装饰在变 | 像素级 SHA-256 逐帧比对 |

原因：容器段首帧 place 的是一个 **21 帧子 MC**（如 22→sprite 3、137→sprite 106），Flash 语义下父 MC `gotoAndStop` 不冻结子 MC，子层会自己往前播。

进一步实测（新增 `build_sprite.py --free-child`：父容器钉在段首、子层按输出帧推进，这才是游戏里的真实情况）：

| 导出窗口 | 每方向不同姿态数 | 体积 | 桌宠表现 |
|---|---|---|---|
| 7 帧（`--dir-seg`） | 4（rightdown 5） | 216 K | 正常，小幅动作 |
| 21 帧（`--free-child`） | 10–14 | 384 K | **异常**：装饰层跑到本体外面 |

21 帧版异常的原因：装饰容器 137 下面挂 `sprite 106 → 103(150 帧) / 105(100 帧)`，这些长序列不是待机呼吸，而是**光环飞出**一类的特技帧；全量播会让光环飘到距本体 150 px 处。

所以**当前采用 7 帧窗口**：既能动、又停在"光环未离家"的安全区间。若以后要完整还原长序列，需要按子层分别裁剪（只取 137 的前 N 帧），不能一刀切。

---

## 四、素材缺失项汇总（后续不要再找）

| 想找的东西 | 状态 |
|---|---|
| 41 项皮肤（700800–700840）的模型/贴图 | 没有；客户端不请求 |
| annual 的待机动画 | 有：每方向 7 帧里 4–5 种姿态（导出必须带 `--dir-seg`，否则被冻成 1 帧） |
| `700020` 芯片图标 | 原版即缺（`items.xml` 有条目、客户端无图标类） |
| annual 的动作 1 / 动作 5 | 资源包里不存在（只有 2,3,4,6,7） |
| `annual/action_2` 等单帧动作 | 存在但只有 1 帧（开关机类，属正常） |

---

## 五、复现命令

```bash
# 1. 导出 annual 待机骨架（8 方向 × 7 帧，段内循环）
cd nono_deskpet_vec/tools/importer
python3 build_sprite.py \
  --swf ../../source_swf/资源/nono/annual/nono.swf --root 138 --name annual \
  --out ../../nono_deskpet/assets/sprites --native --dir-seg --anim-frames 7

# 2. 导出动作/表情（--no-export 跳过 ffdec，纯自研解析器）
python3 build_anims.py --native --no-export --form annual

# 3. 量球心写回 sprite.json 的 canvas.ballc
cd ../../   # nono_deskpet_vec
python3 tools/importer/measure_vec_ballc.py --sprites ../nono_deskpet/assets/sprites

# 4. 冒烟：存档 superLevel=13 启动
cd ../nono_deskpet
python3 -c "import json;p='save.json';d=json.load(open(p));d.update(superLevel=13,super=True);json.dump(d,open(p,'w'))"
WINEPREFIX=/tmp/nono-wine DISPLAY=:1 NONO_LOG=1 NONO_TEST_PANEL=set timeout 20 wine nono_deskpet.exe
grep -E "form loaded|vec annual" nono_debug.log
# 期望：form loaded: annual zoom=4 superLevel=13 ... anims=[2 3 4 6 7] exps=[1 2 3 4]
```

---

## 六、未验证 / 未知项

1. `resource/nono/` 服务器目录 403，无法确认是否还存在其他命名规则的皮肤资源（客户端不消费，实际不影响）；
2. annual 形态下的**飞行态**未测（`fly` 与形态叠加的组合）；
3. annual 的护盾外观未单独实测（护盾尺寸按 `meta.Ball=99` 统一口径，与其他形态一致）；
4. 41 项皮肤在**其他年份版本**的客户端里是否曾被消费，未考证（当前版本明确没有）。
