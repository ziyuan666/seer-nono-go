//go:build windows

package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	appTick      = WM_APP + 1
	menuInfo     = 1000
	menuReset    = 1001
	menuSetting  = 1002
	menuOpenNono = 1003
	menuQuit     = 1099
	menuLab      = 1004 // 芯片制造机（发明室）

	// 超能形态子菜单：2001+lv 对应「超能 lv 级」，2000 表示变回普通形态
	menuSuperOff  = 2000
	menuSuperBase = 2001

	// 20261027-11：「飞行风格」（2101+style）与「芯片工坊」（3000+chipID）两个
	// 菜单 ID 区间随子菜单一起删除，编号留空不复用。

	// 动作演示：4001 + actionID（1-7）
	menuActBase = 4001

	// 表情演示：4101 + expID（1-4）。子渊 2026-10-26：「再新增一个表情播放，
	// 可以播放当前形态支持的表情」——和动作演示并列，列出 animIDs(2)。
	menuExpBase = 4101
)

const (
	// 气泡带高度：样式 C 的气泡固定在窗口顶部这条带子里，本体紧贴其下。
	// 折行后最长 2 行（assets/lines.json 54 条台词全量实测），
	// 2 行 = 2*20 行高 + 6*2 内边距 + 7 尾巴 = 59，取 60。
	// 单行时气泡贴带底而不是居中，否则短台词的气泡会离头顶越来越远。
	bubbleH     = 60
	tickMs      = 40            // 主定时器间隔（UI 刷新节拍）
	animFrameMs = 1000.0 / 24.0 // 原版主舞台 Core.swf 24fps → 41.67ms/帧

	// idleFrameMs 待机/移动的每帧时长默认值。
	//
	// 2026-10-26 子渊：「待机/移动的播放速度和表情对齐，表情的速度最合适」。
	// 实测两边帧率本来就一样（都是 24fps，1 秒推进 24 帧），差别在内容密度：
	// 表情 94 帧里只有 4 种姿态（大量保持帧，平均约 1 秒才换一次），
	// 待机 20 帧是 20 种姿态连续变（41.67ms 换一次），感官上快得多。
	// 所以这里把待机/移动放慢到 100ms/帧（10fps 观感，20 帧转一圈 2 秒）。
	//
	// 2026-10-26 后续：各形态帧数不同（plain 17、super1 13、super5 20），
	// 同帧时长下循环周期 1.3~2.1 秒不等，普通形态显得偏快。
	// 子渊要求「可以自己改每个形态待机和移动的帧率」，于是数值开放到
	// Save.IdleMs / Save.MoveMs，在设置面板「动画速度」里调；
	// 这里只是「未设置时」的回落值。
	// 120 而不是 100/130：帧推进是「累加到 idleMs 就跳一帧」，而 tick 固定 40ms。
	// 100ms = 2.5 tick、130ms = 3.25 tick，都会变成 80/120、120/160 交替 →
	// 播放节奏本身在抖（子渊 R3「抖动」的另一半，位置抖已单独修掉）。
	// 取 3 tick = 120ms 整除，每帧等距。frameMsFor 还会把用户自定义值也对齐到
	// 最近的 tick 倍数，避免面板调成 190 之后又变成 160/200 交替。
	idleFrameMs = 120.0
	// plainDefaultMs 普通形态的出厂帧时长。
	// 40ms = 1 tick，17 帧 = 680ms 一圈，与官方待机 GIF（89 帧 / 2960ms，
	// 球体浮动周期实测 20.4 帧 = 680ms）完全对齐。
	// 早期是 120ms（3 tick），一圈 2.04s，比客户端慢 2.9 倍；再叠加素材里
	// 8 个「2 帧 A/B 开关容器」污染帧，肉眼就成了 240ms 一次的位置抖动。
	plainDefaultMs = 40.0
	bobAmp         = 0.0  // 原版 NonoModel/NonoFlyModel 都没有整体浮动
	bobPeriod      = 60.0 // 保留常量仅为兼容旧存档/调试开关，不再参与默认渲染
)

type Anim struct {
	Kind int // 0 idle, 1 action, 2 exp
	ID   int
	// Frames 仅 PNG 后端使用（整段解码后的切片）。
	// 矢量后端不再缓存整段位图：帧由 FrameSet.animFrame 现渲染，NFrames 给帧数。
	Frames []*image.RGBA
	// NFrames 是这段动画的总帧数（矢量后端用；PNG 后端为 0 时回退 len(Frames)）。
	NFrames int
	// FrameOff 是当前帧图相对 clip 原点的偏移（4x 像素），绘制时补回去。
	FrameOff [2]float64
	// Last 是最近一张已渲染成功的帧。矢量后端后台异步出帧，播放到尚未
	// 渲染完的帧号时沿用上一帧，避免整段动作卡在首帧。
	Last  *image.RGBA
	Idx   int
	Ticks int
	// Acc 是本帧已经累计的毫秒数。原版动画跑在 24fps 主舞台上（Core.swf
	// frameRate=24），不是固定每 tick 一帧，tick 抖动会直接改变播放速度。
	Acc float64
	// BodyC 是这段动作第 0 帧里本体（可染色球壳）的中心，精灵画布坐标。
	// 各段动作的本体在各自 sprite 里位置完全不同（super1 action_1 在 y≈18、
	// action_4 在 y≈250），按画布贴底放置会让开播瞬间本体掉下几十像素，
	// 所以改成把 BodyC 对齐到待机球心锚点。零值表示该段没有标注，退回旧行为。
	BodyC [2]float64
	// AnimPlaceTick 记录这段动画第一帧出现的 tick 号，用于核对实际播放节奏。
	AnimPlaceTick int
	// BBox 是本帧的非透明内容范围（图内坐标）。动作画布常被远处的小特效
	// 撑得很大，窗口按画布走会暴涨；按 BBox 走，窗口只贴合看得见的内容，
	// 贴图仍用整画布坐标，所以体心对齐不受影响。
	BBox image.Rectangle
	// Loop 为 true 时播到末帧回到第 0 帧继续播，不调用 idle()。
	// 循环段必须配合 FrameSet.animSetPinAll(true) —— 否则矢量后端每圈回绕
	// 都会把整个片段的帧缓存作废重渲。当前没有调用点（关机已改 Hold）。
	Loop bool
	// Loops 是已回绕到首帧的圈数，只用于日志（每 20 圈打一条，确认在真循环）。
	Loops int
	// Hold 为 true 时播到最后一帧就定格（Frozen），既不回退也不回待机。
	// 关机保持用（20261029-03）：官方每个动作 sprite 的末帧都有 stop()，
	// super1-4 只是因为 NonoModel 清了根末帧脚本才意外变成循环；子渊要求
	// 「保持最后一帧」，所以按设计意图定格，不模拟那个副作用。
	Hold bool
	// Frozen 是「已经停在末帧」的事实位：tick 看到它直接 return，不再重绘。
	Frozen bool
}

// total 返回这段动画的总帧数：矢量后端走 NFrames，PNG 后端走 len(Frames)。
func (a *Anim) total() int {
	if a == nil {
		return 0
	}
	if a.NFrames > 0 {
		return a.NFrames
	}
	return len(a.Frames)
}

type Game struct {
	Hwnd  HWND
	R     *Renderer
	FS    *FrameSet
	Save  *Save
	Anim  *Anim
	Dir   string
	Frame int
	TickN int
	// LastTickAt 是上一次 tick 的真实时间，用来把「定时器实际间隔」而不是
	// 常数 tickMs 累加进动画时钟。原因：SetTimer 的精度取决于系统计时器分辨率，
	// Windows 上 timeBeginPeriod(1) 后 ≈40ms，但 Wine 实测 49.7ms；
	// 用固定 40ms 累加会让动画比真实时间快 24%（24fps 的内容跑成 30fps 观感）。
	LastTickAt time.Time

	CurW, CurH int
	// BodyX/BodyY/BodyW/BodyH：角色本体（不含气泡留白、不含残影留边）在屏幕上的矩形。
	// 快捷栏、跟随、走动边界一律按它算，用 CurW/CurH 会随留边/气泡一起抖：
	// 残影留边一出现窗口就宽 2*ghostPad，跟随目标会整体偏 ghostPad 并来回跳。
	BodyX, BodyY int
	BodyW, BodyH int
	// FollowSide 跟随换边记忆：1=站光标右侧、-1=站光标左侧、0=还没选过。
	// 光标跨过屏幕中线后先记着，40px 死区内不换边，避免中线抖动导致左右横跳。
	FollowSide int
	// AlignNote 帧间隔对齐日志计数（只打前几条，避免刷屏）。
	AlignNote int
	// IdleSkipN 累计「本 tick 本来要推进多帧、被限速截断」的帧数。
	// 只用于回归核对：长期跑下来应保持很小，说明没有成片丢帧。
	IdleSkipN int
	CurFrame  *image.RGBA // 最近一次渲染的合成图（用于点击穿透判断）
	// BallSX/BallSY：本体球形中心的屏幕坐标（非飞行态：球心锚点；飞行态：
	// 飞行器可见像素的中心）。由「跨形态统一锚点 + 当前形态 meta.ballc」算出，
	// 同一位置切换形态时数值不变；快捷栏环绕、跟随都以它为圆心（子渊 2026-10-24）。
	// 飞行器没有球心锚点，改用实测 alpha 轮廓中心——素材声明的 body bbox
	// 与实际渲染出来的可见范围在部分方向差得多（子渊 2026-10-27 两次反馈
	// 「环心不在飞行器中心」）。
	BallSX, BallSY int
	// FlyVisW/FlyVisH：飞行器当前帧可见内容尺寸（像素），快捷栏半径按它算。
	// 用可见轮廓而不是整帧：整帧 105×149 里有大量透明边，按整帧算图标会离得很远。
	FlyVisW, FlyVisH int

	Down          bool
	Moved         bool
	PendingAction string // 按下时命中快捷栏按钮，等抬起且未拖动再执行
	// 拖动用「增量式」：按下时记住光标屏幕坐标和当时的 Base，移动时按位移量
	// 平移 Base。旧实现用「光标 - 客户区偏移」绝对定位，落点会随窗口内布局
	// （offX、bubbleH、飞行高度、动作 clip 尺寸）变化，关机定格这类大 clip
	// 窗口下拖动就偏（子渊 2026-10-29 反馈「拖动漂移、不跟手」）。
	DragScrX, DragScrY   int
	DragBaseX, DragBaseY int

	VX, VY     float64
	WalkTicks  int
	PauseTicks int
	// FlyMs 是飞行模式真实经过的毫秒，专门给飞行器子时间轴（眼睛 15 帧 /
	// 光圈 18 帧）当时钟。不能用 TickN×40：定时器抖动会累积偏差，而这两条
	// 子时间轴是各自循环的，累积误差会表现为眨眼节奏越来越歪。
	FlyMs int64
	// flySeqDumped 保证 NONO_FLY_DUMP_ALL 的序列落盘只做一次。
	flySeqDumped bool
	// flySeerLogged 飞行挂载几何只记一次日志（reg/机器口/抬升量），排错用。
	flySeerLogged bool
	// 自由游走活动区（对应原版 _autoRect）：首次 tick 锁定为初始位置
	WalkHomeX, WalkHomeY int
	WalkHomeSet          bool

	// LastSavePosAt 自动走动/拖动结束的位置落盘节流（2026-11-02 E-02 修复）：
	// 位置不能在走动过程中每 tick 写，太频繁；也不能只在退出时写，被强杀/崩溃
	// 会丢。节流规则：距上次保存 ≥2s 且位移 >8px 才写一次。
	LastSavePosAt time.Time
	LastSavePosX, LastSavePosY int
	// openPanelAtTick 最近一次「单击打开主面板」的时刻，双击彩蛋用它收回面板
	// （2026-11-02 E-05 修复，见 onClickEgg 注释）。
	openPanelAtTick int

	// 快捷栏显示时的光标位置，用于区分"NONO 走开"和"用户主动移开"
	SCMouseX, SCMouseY int

	Bubble      string
	BubbleTicks int
	BubbleW     int
	// BubbleLines 是已折好的行（每行 15~18 字，见 wrapBubble）；BubbleShape 是
	// 样式 C 圆角气泡的形状位图（渐变 + 描边，不含文字）。形状只在文本变化时
	// 重建一次，之后每帧只做合成 + GDI 画字，避免 40ms 节拍里反复算 SDF。
	BubbleLines []string
	BubbleShape *image.RGBA
	FrameTick   int

	// 待机自发表情/台词的计时（tick 数），BodyHidden 记录本体窗口是否因关机隐藏。
	// prevX/prevY 用实际坐标差判断"这一帧本体有没有动过"：跟随模式下窗口
	// 一直在微调，不能拿「跟随开关开着」当移动依据，否则永远不冒表情。
	ExpTimer      int
	BodyHidden    bool
	PowerOffTicks int
	prevX, prevY  int

	// PowerAnim 记录当前正在播的电源动作 id（2 开机 / 3 关机 / 0 无）。
	// 只在电源动作播放期间非 0，用于让点击/菜单知道「本体此刻在播电源动画」。
	// 关机保持态另有 Save.Open=false 这个权威状态，PowerAnim 只做辅助。
	PowerAnim int
	// FlyOff 是会话级的飞行抑制位（20261029-01）：关机要播本体关机动画，
	// 而飞行态只画飞行器，两者冲突。关机时置 true、开机时清回 false，
	// 不改 Save.Fly —— 用户原本的飞行设置开机后自动恢复。
	FlyOff bool

	// 待机浮动序列的播放状态（只在站定时推进，移动时不动）
	IdleAnimIdx int
	IdleAnimAcc float64

	BaseX, BaseY int
	// 上一帧的窗口坐标，用于算移动方向（残影朝移动反方向拖）
	prevBaseX, prevBaseY int
	// WinX/WinY 是本帧分层窗口在屏幕上的真实左上角。飞行模式下窗口要比
	// BaseY 上移「飞行器高度」，所以凡是要把屏幕坐标换算成图像坐标
	// （命中检测、拖动、快捷栏定位）的地方都必须用 WinX/WinY，不能用 BaseY。
	WinX, WinY int
	// WinY0 同上但不含 ±6px 呼吸浮动，给快捷栏定位和跟随目标用，避免抖动
	WinY0 int
	// AnimPadX/AnimPadY 是动作/表情帧为了「完整容纳内容」而把窗口向左/上
	// 外扩的量。窗口原点左/上移 pad，内容在窗口内右/下移 pad，两者抵消，
	// 球心屏幕坐标数学上保持不变（因此 BallSX/BallSY 公式不用改）。
	// 不走裁切：子渊明确要求画布不设限、画面内容一点不裁。
	AnimPadX int
	AnimPadY int
	// AnimLastIdx 是 currentFrame 最近一次真正交给绘制的那一帧的序号。
	// 帧图按单帧 bbox 渲染，绘制要按该帧自己的偏移还原位置，不能用
	// g.Anim.Idx（异步出帧时会退回 Last/邻居帧，两者可能差好几帧）。
	AnimLastIdx int
	// 快捷栏点击轮询状态（Wine 下窗口消息会丢，见 tick 里的说明）
	SCPollDown bool
	SCPollX    int
	SCPollY    int
	// SCPollHit 记录「按下那一刻命中的快捷栏按钮」，抬起时必须命中同一个才算点击。
	// 只看抬起位置会把「点别处→划过快捷栏→松手」判成点击（鼠标滑过就开功能）。
	SCPollHit int

	TransparentHits bool // 是否让透明像素穿透（调试期间关闭）

	// dispCache 缓存「缩放完成、可直接贴画布」的待机帧。
	//
	// 为什么要缓存：超能形态的 4x 素材帧约 1093×936，每 tick 走一遍 areaScaleMulti
	// 要 17~31ms（实测 super4），把整条 40ms 的 tick 预算吃光，表现为「切到高级
	// 形态就一直卡」。待机是 51 帧循环播放，同一帧每 2 秒就会复用一次，缓存后
	// 第二圈起 resize 归零（306KB/帧，单方向 51 帧 ≈ 15.6MB）。
	// 只在待机帧上启用：动作/表情每帧只播一次，缓存纯浪费内存。
	// key 含形态/方向/帧号/目标尺寸/染色；条目数到上限就整表丢弃（见 dispFrame），
	// 不做逐条 LRU —— 待机方向来回切换时按方向清空会命中率归零，实测没必要。
	dispCache map[dispCacheKey]*image.RGBA
	// IdleShown 是 currentFrame 这一次实际交出去的待机帧号。补帧没到位时它会
	// 回退到邻居帧（见 currentFrame 的说明），与 g.Frame 并不总是相等；
	// 缓存键必须用它，否则会把「邻居帧的像素」记在「本该是这一帧」的键上，
	// 补帧一到位画面就停在错的帧上。
	IdleShown int
}

// dispCacheKey 是显示帧缓存的键：任何会改变输出像素的输入都必须进来，
// 否则换染色/换缩放会命中旧图，画面就停在上一个外观上。
type dispCacheKey struct {
	form  string
	dir   string
	frame int
	w, h  int
	color uint32
}

var g *Game

// autoFlyOnce / autoScOnce 保证两个调试钩子各自只触发一次
var autoPanelOnce int
var chipFlipOnce int
var autoFlyOnce int
var autoScOnce int

// autoActOnce 动作测试钩子（NONO_TEST_ACTION=<id>），只在冒烟时用一次
var autoActOnce int

// prewarmDone 形态预热是否已启动（只跑一次）。
var prewarmDone bool

// testUseOnce 让 NONO_TEST_USE 只触发一次。
var testUseOnce int

// formBenchDone formbench 只跑一次。
var formBenchDone bool

type selftestStep struct {
	msg  uintptr
	wait int
}

var (
	selftestSteps  []selftestStep
	selftestIdx    int
	selftestStatic int
)

func (g *Game) setBubble(s string) {
	g.Bubble = s
	g.BubbleLines = wrapBubble(s, bubbleLineMax, bubbleLineWin)
	g.BubbleW = bubbleTextW(g.BubbleLines)
	g.BubbleShape = nil // 文本变了，形状（宽度）必须重建
	g.BubbleTicks = 90  // 约 3.6 秒
}

// estimateTextW 粗估文字宽度（中文 16px，ASCII 8px），只用于撑窗口宽度。
func estimateTextW(s string) int {
	w := 0
	for _, r := range s {
		if r > 127 {
			w += 16
		} else {
			w += 8
		}
	}
	return w
}

// ---- 气泡折行（子渊 2026-10-31 定稿的规则）----
//
// 每行 15 字；切点再看第 16~18 字，有标点就延到该标点之后（行变成 16~18 字），
// 没有就硬切在 15；第 15 字本身是标点则正好断在它后面。
// 颜文字/ASCII 串整体不切（切点落在串内就退到串首），否则
// 「~\(≧▽≦)/~啦啦~\(≧▽≦)/~啦啦啦」会被撕成「…≦)」+「/~啦啦啦」。
// 末行不足 3 字时整句对半重分，避免 15+1 这种孤字（「…立刻搞」+「定」）。
const (
	bubbleLineMax = 15 // 每行目标字数
	bubbleLineWin = 3  // 允许为吃标点多带几个字
	bubbleLineCap = bubbleLineMax + bubbleLineWin
	bubbleWidow   = 3 // 末行少于这么多字就重分
)

var bubblePunct = map[rune]bool{
	'、': true, '，': true, '。': true, '！': true, '？': true,
	'；': true, '：': true, '…': true,
	',': true, '.': true, '!': true, '?': true, ';': true, ':': true,
	'）': true, ')': true, '」': true, '』': true, '】': true, '〉': true, '》': true,
}

// bubbleNonBreak 判断该字是否属于「不可切断的串」（颜文字 / ASCII 单词）。
// 颜文字里的 ≧▽≦ 是 U+2266/U+25BD/U+2267，落在 0x2000~0x2E80 且不是标点，
// 所以不能只判 ASCII，否则 ~\(≧▽≦)/~ 还是会被撕开。
func bubbleNonBreak(r rune) bool {
	if r == ' ' || r == '\t' {
		return false
	}
	if r >= 0x20 && r <= 0x7E {
		return true
	}
	if r >= 0x2000 && r < 0x2E80 && !bubblePunct[r] {
		return true
	}
	return false
}

// bubbleCut 定 s[i:] 这一行的切点：15 字基线 → 吃 3 字窗口内的标点 →
// 避免切在颜文字/ASCII 串内部。返回值恒 > i（除非 i 已到末尾）。
func bubbleCut(rs []rune, i, per, win int) int {
	n := len(rs)
	capPer := per + win
	cut := i + per
	if cut > n {
		cut = n
	}
	for k := cut; k < cut+win && k < n; k++ {
		if bubblePunct[rs[k]] {
			cut = k + 1
			break
		}
	}
	if cut < n && bubbleNonBreak(rs[cut-1]) && bubbleNonBreak(rs[cut]) {
		b := cut
		for b > i && bubbleNonBreak(rs[b-1]) {
			b--
		}
		if b > i {
			cut = b // 退到串首，整串留给下一行
		} else {
			// 整串比一行还长：只能往后切到串尾，且不超过硬上限
			f := cut
			for f < n && bubbleNonBreak(rs[f]) {
				f++
			}
			if f-i <= capPer {
				cut = f
			}
		}
	}
	if cut <= i {
		cut = i + 1
	}
	return cut
}

// wrapBubble 把一句话折成若干行（每行 per~per+win 字）。
// per<=0 时不折行，原样返回一行。
func wrapBubble(s string, per, win int) []string {
	rs := []rune(s)
	n := len(rs)
	if n == 0 {
		return nil
	}
	if per <= 0 {
		return []string{s}
	}
	var out []string
	i := 0
	for n-i > per {
		cut := bubbleCut(rs, i, per, win)
		if n-cut < bubbleWidow {
			// 末行孤字：从本行起点对半重分，再走一遍标点/串规则
			cut = bubbleCut(rs, i, (n-i+1)/2, win)
		}
		line := strings.TrimRight(string(rs[i:cut]), " ")
		i = cut
		for i < n && rs[i] == ' ' {
			i++
		}
		if line != "" {
			out = append(out, line)
		}
	}
	if i < n {
		if t := strings.TrimSpace(string(rs[i:])); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// bubbleTextW 气泡内容宽（像素）：取最宽一行 + 左右内边距。窗口宽度按它撑，
// 少加这 2 个内边距文字就会贴到圆角上被吃掉。
func bubbleTextW(lines []string) int {
	w := 0
	for _, l := range lines {
		if x := textW(l); x > w {
			w = x
		}
	}
	if w == 0 {
		return 0
	}
	return w + bubblePadX*2
}

// negHeight 把字号换算成 CreateFontW 需要的「负 cHeight」。
// 正数 cHeight 是字符单元高（含内部行距），雅黑下 em 只有 ~12px，字明显偏小；
// 负数表示 EM 高度，16 就是 16px，也正好等于 estimateTextW 里「中文 16px」的口径。
func negHeight(n int) uintptr {
	return uintptr(int32(-n)) // Win32 的 int 参数只取低 32 位，高 32 位的符号扩展无影响
}

// textW 用真实字体量文字宽度（像素）。气泡宽度靠它定：只按 16px/字粗估时，
// ASCII 多的台词（「(*^__^*)」「~\(≧▽≦)/~」）会把气泡撑宽一大截。
// 量不出时退回 estimateTextW，宁可宽一点也不裁字。
func textW(s string) int {
	if measDC == 0 {
		hdc, _, _ := pCreateCompatibleDC.Call(0)
		f, _, _ := pCreateFontW.Call(negHeight(bubbleFont), 0, 0, 0, FW_NORMAL, 0, 0, 0,
			DEFAULT_CHARSET, 0, 0, 0, 0, uintptr(unsafe.Pointer(utf16ptr("Microsoft YaHei"))))
		if hdc == 0 || f == 0 {
			return estimateTextW(s)
		}
		measDC = HDC(hdc)
		measFont = f
		measOld, _, _ = pSelectObject.Call(hdc, f)
	}
	if measDC == 0 {
		return estimateTextW(s)
	}
	return measureText(measDC, s)
}

func dirFromVec(dx, dy float64) string {
	if dx == 0 && dy == 0 {
		return "down"
	}
	ang := math.Atan2(dy, dx) * 180 / math.Pi // 屏幕坐标，y 向下
	switch {
	case ang >= -22.5 && ang < 22.5:
		return "right"
	case ang >= 22.5 && ang < 67.5:
		return "rightdown"
	case ang >= 67.5 && ang < 112.5:
		return "down"
	case ang >= 112.5 && ang < 157.5:
		return "leftdown"
	case ang >= 157.5 || ang < -157.5:
		return "left"
	case ang >= -157.5 && ang < -112.5:
		return "leftup"
	case ang >= -112.5 && ang < -67.5:
		return "up"
	default:
		return "rightup"
	}
}

func (g *Game) playAction(id int) {
	if !g.Save.Open {
		// 关机保持态只有「开机」是合法动作（20261029-01）。电源动画本身走
		// playPowerAnim，不经过这里，避免被自己的关机校验拦住。
		g.say("NONO 已关机，请先开机")
		return
	}
	if g.Anim != nil {
		// 换新动作前先收尾旧段：idle() 会停掉上一段的后台渲染 goroutine 并释放
		// 帧缓存。直接覆盖 g.Anim 会让旧 worker 永久空转（关机循环切开机时必然
		// 走到这条路）。代价是 g.Frame 归零，与正常播完行为一致。
		g.idle()
	}
	// 矢量后端的帧是按需现渲染的，没有可返回的整段切片，帧数走 animCount；
	// PNG 后端仍返回整段切片。两者都要能播。
	if n := g.FS.animCount(1, id); n > 0 {
		logf("playAction %d: NFrames=%d tick=%d", id, n, g.TickN)
		g.Anim = &Anim{Kind: 1, ID: id, NFrames: n, BodyC: g.FS.animBodyC(1, id)}
		if id == 2 { // 开机动作带音效
			g.playSFX("boot.wav")
		}
		return
	}
	if fr := g.FS.actionFrames(id); len(fr) > 0 {
		g.Anim = &Anim{Kind: 1, ID: id, Frames: fr, BodyC: g.FS.animBodyC(1, id)}
		if id == 2 { // 开机动作带音效
			g.playSFX("boot.wav")
		}
		return
	}
	// 该形态没有这个动作资源：用表情代替，不静默
	g.playExp(g.randomExpID())
}

// playPowerAnim 播电源动作（2 开机 / 3 关机）。和 playAction 的区别：
//   - 不受「关机态禁止播动作」的拦截（它自己就是状态切换的执行者）；
//   - hold=true 时播到末帧定格（关机保持，20261029-03）；
//   - loop=true 时循环播放，需配合 animSetPinAll 整段驻留（当前无调用点）。
//
// 返回 false 表示当前形态没有这段资源（普通形态没有 action_3，官方资源缺口）。
func (g *Game) playPowerAnim(id int, loop, hold bool) bool {
	if g.Anim != nil {
		g.idle()
	}
	if n := g.FS.animCount(1, id); n > 0 {
		if loop {
			// 循环段整段驻留：否则矢量后端每圈回绕都会清空并重渲整个片段。
			g.FS.animSetPinAll(1, id, true)
		}
		g.PowerAnim = id
		g.Anim = &Anim{Kind: 1, ID: id, NFrames: n, BodyC: g.FS.animBodyC(1, id), Loop: loop, Hold: hold}
		logf("playPowerAnim %d: NFrames=%d loop=%v hold=%v tick=%d", id, n, loop, hold, g.TickN)
		if id == 2 {
			g.playSFX("boot.wav")
		}
		return true
	}
	if fr := g.FS.actionFrames(id); len(fr) > 0 {
		g.PowerAnim = id
		g.Anim = &Anim{Kind: 1, ID: id, Frames: fr, BodyC: g.FS.animBodyC(1, id), Loop: loop, Hold: hold}
		logf("playPowerAnim %d: PNG frames=%d loop=%v hold=%v tick=%d", id, len(fr), loop, hold, g.TickN)
		if id == 2 {
			g.playSFX("boot.wav")
		}
		return true
	}
	g.PowerAnim = 0
	logf("playPowerAnim %d: 本形态无此动作资源", id)
	return false
}

// powerOff 关机：置关机态、播关机动作并定格在末帧。
// 依据 MachXMLInfo action 表：id=3 关机 autoEnd=0（停住等外部指令）；
// 官方每个动作 sprite 的末帧都有 stop()（action/3_1 的 pet.as 第 165 帧），
// super1-4 之所以会循环，是 NonoModel.onActionScript 清空根末帧脚本的副作用。
// 子渊 2026-10-29：「不是循环某几帧，就按我说的保持最后一帧」。
func (g *Game) powerOff() {
	if !g.Save.Open {
		return
	}
	g.Save.Open = false
	g.Save.write()
	// 关机要播本体动画，先抑制飞行器渲染（Save.Fly 不动，开机自动恢复）。
	g.FlyOff = true
	// 关机后快捷栏必须收起：动画期间 tick 在播动作分支直接 return，
	// updateShortcutPos 不会跑，留着就是一个不跟随的悬空窗口。
	hideShortcut()
	if !g.playPowerAnim(3, false, true) {
		// 普通形态（plain）官方只有 action/1 充电、action/2 开机，没有关机动作。
		// 子渊 2026-10-29：「没有那就没有」——不借其他形态素材、不伪造，
		// 停在当前待机帧（本体仍然可见，保持关机态），等开机动画恢复。
		g.say("NONO 关机")
	}
	g.draw()
	logf("power off: open=false anim=%v tick=%d", g.Anim != nil, g.TickN)
}

// powerOn 开机：置开机态、播开机动作（action 2，带 boot.wav），播完回待机。
// 依据 MachXMLInfo action 表：id=2 开机 autoEnd=1（播完回待机）。
func (g *Game) powerOn() {
	if g.Save.Power <= 0 {
		g.say("你的NoNo能量不够!")
		return
	}
	wasOff := !g.Save.Open
	g.Save.Open = true
	// 开机动画期间抑制飞行器（否则画面上只有载具、看不到开机动作）；
	// 播完由 idle() 的 PowerAnim==2 分支恢复。没有开机动作时下面立即恢复。
	g.FlyOff = g.Save.Fly
	g.Save.write()
	g.syncBodyVisible()
	if !g.playPowerAnim(2, false, false) {
		// 没有开机动作的形态：仍然正常开机，只是少了这段动画。
		g.PowerAnim = 0
		g.FlyOff = false
	}
	if wasOff {
		g.say("NONO 开机！")
	}
	g.draw()
	logf("power on: open=true anim=%v tick=%d", g.Anim != nil, g.TickN)
}

func (g *Game) playExp(id int) {
	if !g.Save.Open {
		// 关机保持态不播表情（20261029-01）：否则关机循环会被一个表情顶掉，
		// 本体停在待机帧，看起来像"自己开机了"。
		return
	}
	if n := g.FS.animCount(2, id); n > 0 {
		g.Dir = "down"
		g.Anim = &Anim{Kind: 2, ID: id, NFrames: n, BodyC: g.FS.animBodyC(2, id)}
		g.playSFX("exp" + itoa(id) + ".wav")
		return
	}
	if fr := g.FS.expFrames(id); len(fr) > 0 {
		// 原版 onResExp / onResFollowExp 都先 `this.direction = Direction.DOWN`
		// 再 gotoAndPlay，表情一律正面朝下播；播完不自动回原方向，等下一次
		// changedir（走路/跟随）才切。
		g.Dir = "down"
		g.Anim = &Anim{Kind: 2, ID: id, Frames: fr, BodyC: g.FS.animBodyC(2, id)}
		g.playSFX("exp" + itoa(id) + ".wav")
	}
}

func (g *Game) idle() {
	if g.Anim != nil {
		logf("idle() 清动作 用了 %d tick (%d→%d)", g.TickN-g.Anim.AnimPlaceTick, g.Anim.AnimPlaceTick, g.TickN)
		logf("idle() 清动作 kind=%d id=%d idx=%d/%d (NFrames=%d lenFrames=%d)",
			g.Anim.Kind, g.Anim.ID, g.Anim.Idx, g.Anim.total(), g.Anim.NFrames, len(g.Anim.Frames))
	}
	// 先停后台渲染 goroutine，再清 Anim（顺序反了就丢掉了 kind/id 无从停）
	if g.Anim != nil {
		if m := g.FS.animMiss(g.Anim.Kind, g.Anim.ID); m > 0 {
			fz, fzMax := g.FS.animFreeze(g.Anim.Kind, g.Anim.ID)
			logf("anim miss kind=%d id=%d 共 %d 次；冻结 %d 帧，最长连续 %d 帧；visChanges=%d",
				g.Anim.Kind, g.Anim.ID, m, fz, fzMax,
				g.FS.animVisChanges(g.Anim.Kind, g.Anim.ID))
		}
		g.FS.animSetPinAll(g.Anim.Kind, g.Anim.ID, false) // 解除整段驻留（关机循环用）
		g.FS.animStopWorker(g.Anim.Kind, g.Anim.ID)
	}
	g.Anim = nil
	// 开机动作播完 → 恢复飞行渲染（20261029-01）。
	// 必须再判 Save.Open（20261029-04）：powerOff 先置 FlyOff=true 再调
	// playPowerAnim(3)，而 playPowerAnim 内部会先 idle() 清掉上一段动画，
	// 那时 PowerAnim 还停在启动时 action 2 的 2 —— 只看 PowerAnim 会把
	// FlyOff 复位成 false，关机定格就按飞行器口径算窗口（y0 被夹到 0），
	// 拖动随之漂移、不跟手（子渊 2026-10-29 反馈）。
	if g.PowerAnim == 2 && g.Save.Open {
		g.FlyOff = false
	}
	g.PowerAnim = 0
	g.FS.releaseAnim()
	// 回到待机时归零，和原版 gotoAndStop(方向标签) 停在段首帧的行为一致
	g.Frame = 0
	g.FrameTick = 0
}

// canExpress 判断此刻是否允许自发表情：开机、没有正在播的动作/表情、本体没在移动。
// 原版 NonoModel 在 WALK_START 时 cancel 掉表情计时、WALK_END 时重排，
// 所以「正在走」不播表情；这里用实际坐标差判断，跟随模式也适用。
func (g *Game) canExpress() bool {
	if !g.Save.Open || g.Anim != nil || g.BodyHidden {
		return false
	}
	if g.prevX == 0 && g.prevY == 0 {
		// 首帧对齐：避免把尚未初始化的 (0,0) 误判成"刚移动过"
		g.prevX, g.prevY = g.BaseX, g.BaseY
		return false
	}
	return g.BaseX == g.prevX && g.BaseY == g.prevY && g.WalkTicks == 0
}

// idleExpress 播一次表情；独立态配一句台词（原版跟随态只播表情、不 showBox）。
// 播完重排 10~20 秒（40ms/tick → 250~500 tick），对齐 randomHalve(20000)。
func (g *Game) idleExpress() {
	id := g.randomExpID()
	g.playExp(id)
	line := ""
	if !g.Save.Follow {
		line = lineForExp(id)
		g.sayFor(line, 12)
	}
	g.ExpTimer = 250 + rand.Intn(250)
	logf("idleExpress exp=%d line=%q next=%d", id, line, g.ExpTimer)
}

// syncBodyVisible 让窗口显隐跟随 Save.Open，对应原版 state[0]=false 时模型不显示。
//
// 20261029-01 起关机不再一律隐藏（要播关机动画）；20261029-02 按子渊要求细分：
//   - 该形态有 action 3（super1-5、annual）→ 循环播动画，窗口保持可见；
//   - 该形态没有 action 3（plain 官方只有 1、2）→ 1 秒后 SW_HIDE，
//     与本改动之前的行为完全一致（子渊「这部分先不做，恢复之前的」）。
//
// BodyHidden 是"窗口被隐藏"的事实位，只有 plain 关机路径会置 true，
// 开机分支的 SW_SHOW 负责还原。
func (g *Game) syncBodyVisible() {
	if g.Hwnd == 0 {
		return
	}
	if !g.Save.Open {
		// 分两种（20261029-02 按子渊要求区分）：
		// ① 有循环关机动画（super1-5 / annual 的 action 3）→ 窗口留着播，绝不隐藏；
		// ② 没有资源的形态（plain 官方只有 action/1、2）→ 回到改动前的行为：
		//    延迟 25 tick（1 秒，让主面板黑屏 close 播完）后 SW_HIDE，本体消失。
		//    子渊 2026-10-29：「普通默认形态不要定格帧，这部分先不做，恢复之前的」。
		if g.Anim != nil {
			g.PowerOffTicks = 0
			return
		}
		if g.BodyHidden {
			g.PowerOffTicks = 0
			return
		}
		g.PowerOffTicks++
		if g.PowerOffTicks%10 == 0 {
			logf("power off ticks=%d anim=%v hwnd=%d", g.PowerOffTicks, g.Anim != nil, g.Hwnd)
		}
		if g.PowerOffTicks < 25 {
			return
		}
		hideShortcut()
		pShowWindow.Call(uintptr(g.Hwnd), SW_HIDE)
		g.BodyHidden = true
		g.PowerOffTicks = 0
		logf("body hidden (power off, 该形态无关机动画)")
		return
	}
	g.PowerOffTicks = 0
	if !g.BodyHidden {
		return
	}
	pShowWindow.Call(uintptr(g.Hwnd), SW_SHOW)
	g.BodyHidden = false
	logf("body shown")
}

// turnOn 托盘「开机 NONO」入口，统一走 powerOn（播 action 2 + boot.wav）。
func (g *Game) turnOn() {
	g.powerOn()
}

var autoExpOnce int8
var powerTestOnce int8
var anchorCheckedOnce int8

// flyForceDir 是调试钩子：NONO_FLY_DIR=down|left|up|... 时强制锁定飞行器方向，
// 用于 8 方向截图核对（每 tick 重设，避免被走动/跟随覆盖）。
var flyForceDir = os.Getenv("NONO_FLY_DIR")

// debugSay 是调试钩子：NONO_SAY=<文本> 时气泡常显（消失后自动补回），
// 供 Wine 下确定性截图核对折行与样式；不设环境变量时零影响。
var debugSay = os.Getenv("NONO_SAY")

func (g *Game) tick() {
	if debugSay != "" && g.Bubble == "" {
		g.setBubble(debugSay)
	}
	if flyForceDir != "" && g.Save.Fly {
		g.Dir = flyForceDir
	}
	tTick0 := time.Now()
	defer func() {
		if profEnabled && g.TickN%12 == 0 {
			logf("PROF ticktotal=%.1fms", float64(time.Since(tTick0))/1e6)
		}
	}()
	g.TickN++
	// 真实间隔：定时器抖动/系统分辨率差异都要如实反映到动画时钟里。
	// 取不到（首次 tick）或异常值（窗口拖动阻塞导致 >200ms）时退回常数 tickMs，
	// 避免一次掉帧就把动画整体快进几十帧。
	dtMs := tickMs
	if !g.LastTickAt.IsZero() {
		if d := int(time.Since(g.LastTickAt) / time.Millisecond); d > 0 && d <= 200 {
			dtMs = d
		}
	}
	g.LastTickAt = time.Now()
	if g.Save.Fly {
		g.FlyMs += int64(dtMs)
	}
	if g.TickN%100 == 0 {
		logf("tick %d", g.TickN)
	}
	// 调试钩子：NONO_TEST_MAINACTION=power|cycle —— 走主面板电源键的真实分支
	// （mainPanelAction("power")，含 facePlay(close/open) 与 900ms 收面板）。
	// cycle = 120 tick 关机、400 tick 先打开主面板再点开机，验证 Q4「关机态
	// 允许点本体开面板 → 面板上电源键开机」这条路径。
	if mode := os.Getenv("NONO_TEST_MAINACTION"); mode != "" {
		if g.TickN == 120 {
			logf("dbg mainaction power @tick=%d open=%v", g.TickN, g.Save.Open)
			mainPanelAction("power")
		}
		if mode == "cycle" && g.TickN == 400 {
			logf("dbg mainaction power @tick=%d open=%v (panel first)", g.TickN, g.Save.Open)
			g.showMainPanel()
			mainPanelAction("power")
		}
	}
	// 调试钩子：NONO_TEST_POWER=off|on|cycle —— 电源动画自动走一遍。
	// 必须放在本函数最前面：关机态与动作播放都会在下方提前 return，
	// 钩子放后面会永远等不到触发。
	//   off   = 3.2s 时走关机路径（powerOff，循环播 action 3）
	//   on    = 3.2s 时走开机路径（powerOn，验证从关机态恢复）
	//   cycle = 3.2s 关机、12s 开机（验证循环 → 开机的缓存释放）
	// 形态用 NONO_SUPER 或改存档切换，plain 的 action 3 官方缺失属预期。
	if mode := os.Getenv("NONO_TEST_POWER"); mode != "" {
		if powerTestOnce == 0 && g.TickN == 80 {
			powerTestOnce = 1
			logf("dbg power %s @tick=%d open=%v", mode, g.TickN, g.Save.Open)
			if mode == "on" {
				g.powerOn()
			} else {
				g.powerOff()
			}
		}
		if mode == "cycle" && g.TickN == 300 {
			logf("dbg power cycle -> on @tick=%d", g.TickN)
			g.powerOn()
		}
	}
	// 调试钩子：NONO_TEST_ACTION=1..7 在启动 60 tick 后播指定动作，
	// 用于验证动作帧渲染质量/播放是否掉帧（菜单点击在 Wine 下点不准坐标）。
	// 形态预热：启动 4 秒后把没加载过的形态依次读进 formCache。
	//
	// 子渊 2026-10-26 打回「切换时间大概要 2 秒」：真机 loadFrameSet 比 Wine 慢
	// 得多（Wine 实测 400~500ms，真机 2s），异步只能保证不卡，没法保证「立刻变」。
	// 启动后在用户多半还没切形态的空档把 6 个形态全读进内存，之后切形态就是纯
	// 指针替换。与 applyForm 共用 loadingKey 串行，避免两边同时解析抢占 CPU/磁盘。
	// 形态切换耗时基准（R2）：热身后按 NONO_TEST_FORMS 给的等级序列逐个切换。
	if !formBenchDone && g.TickN == 300 && os.Getenv("NONO_TEST_FORMS") != "" {
		formBenchDone = true
		for _, x := range strings.Split(os.Getenv("NONO_TEST_FORMS"), ",") {
			lv := 0
			fmt.Sscanf(x, "%d", &lv)
			t0 := time.Now()
			g.setSuperLevel(lv)
			logf("dbg formswitch lv=%d totalMs=%d", lv, time.Since(t0)/1e6)
		}
	}
	// 开机动画播完再预热（20261029-01）：启动要播 action 2（最长 super1 170 帧
	// ≈7 秒），预热会同步抢 CPU 和渲染 worker，实测会让开机动作前段掉帧。
	if !prewarmDone && g.TickN >= 100 && g.Anim == nil {
		prewarmDone = true
		go prewarmForms(g.Save.formKey())
	}
	if autoActOnce == 0 && g.TickN == 60 && os.Getenv("NONO_TEST_ACTION") != "" {
		autoActOnce = 1
		id := 4
		fmt.Sscanf(os.Getenv("NONO_TEST_ACTION"), "%d", &id)
		logf("test action %d", id)
		g.playAction(id)
	}
	// 调试钩子：NONO_TEST_EXP=1..4 播指定表情（表情由计时随机触发，
	// 冒烟无法确定命中时机，4x 改版后需要逐个核对落位）。
	if autoExpOnce == 0 && g.TickN == 60 && os.Getenv("NONO_TEST_EXP") != "" {
		autoExpOnce = 1
		id := 1
		fmt.Sscanf(os.Getenv("NONO_TEST_EXP"), "%d", &id)
		logf("test exp %d", id)
		g.playExp(id)
	}
	// 调试钩子：NONO_TEST_PANEL=main|info|storage 自动开对应面板，
	// 用于 Wine 下验证面板底图/按钮（点击本体在 Wine 里收不到按键）。
	if autoPanelOnce == 0 && g.TickN == 40 && os.Getenv("NONO_TEST_PANEL") != "" {
		autoPanelOnce = 1
		switch os.Getenv("NONO_TEST_PANEL") {
		case "main":
			g.showMainPanel()
		case "info":
			showInfoPanel()
		case "nick":
			// 改名模式冒烟：验证扳手已被 √ 取代、输入框覆盖在昵称框上
			showInfoPanel()
			setInfoRenameEditing(true)
			showNickInput()
		case "tip":
			// tooltip 冒烟：等级行悬停提示
			showInfoPanel()
			showInfoTip(itoa(g.Save.SuperLevel) + "级超能NoNo")
		case "storage":
			showStoragePanel()
		case "chip":
			showChipPanel()
		case "flychoice":
			// 飞行模式选择面板冒烟：核对 250×134 底图（圆角外透明）、4 格命中位置、
			// 选中格是否常驻官方 over 态。配合 NONO_DUMP_PANEL 离屏落盘。
			showFlyChoice()
		case "setsnap":
			// 设置面板不显示、只离屏渲染成 BMP：Wine 下截不到分层窗口，
			// 用快照核对布局（动画速度 6 行是否被截）。
			snapshotSettingsPanel("panel_set.bmp")
		case "formbench":
			// 实际切换放在 TickN==300 的独立钩子里（见下方 formBenchDone），
			// 因为形态预热要到 TickN==100 才开始、约 2 秒跑完，TickN==60 切测不到缓存。
		case "set":
			// 真正弹出设置面板，供 xdotool 点击「动画速度」按钮做端到端验证。
			g.showSettings()
			// NONO_TEST_ACTIONS=idle-0,idle-0 直接走与鼠标点击完全相同的
			// doPanelAction 路径，用于验证按钮逻辑与存档落盘（Wine 下点不准坐标）。
			if s := os.Getenv("NONO_TEST_ACTIONS"); s != "" {
				for _, a := range strings.Split(s, ",") {
					doPanelAction(a)
				}
				logf("dbg actions %s -> idle0=%d move0=%d", s, g.Save.IdleMs[0], g.Save.MoveMs[0])
			}
		}
		logf("dbg panel %s shown", os.Getenv("NONO_TEST_PANEL"))
	}
	// 调试钩子：NONO_TEST_USE=700451 直接调用 useChip，验证芯片数值效果
	// （Wine 下点不准储藏格坐标，走与点击完全相同的入口）。
	if testUseOnce == 0 && g.TickN == 60 && os.Getenv("NONO_TEST_USE") != "" {
		testUseOnce = 1
		id := atoiSafe(os.Getenv("NONO_TEST_USE"))
		before := g.Save.AI
		g.useChip(id)
		logf("dbg use chip %d ai %d -> %d", id, before, g.Save.AI)
	}
	// 调试钩子：NONO_TEST_CHIPFLIP=N 打开「基本资料」后右翻 N 页。
	// Wine 里点不准面板箭头坐标，翻页/末页留空只能靠这条钩子验证。
	if chipFlipOnce == 0 && g.TickN == 80 && os.Getenv("NONO_TEST_CHIPFLIP") != "" {
		chipFlipOnce = 1
		n := 1
		fmt.Sscanf(os.Getenv("NONO_TEST_CHIPFLIP"), "%d", &n)
		for i := 0; i < n; i++ {
			chipFlip(1)
		}
		logf("dbg chipflip -> super=%d normal=%d", infoChipPage[0], infoChipPage[1])
	}
	if autoFlyOnce == 0 && g.TickN == 75 && os.Getenv("NONO_AUTOFLY") != "" {
		autoFlyOnce = 1
		g.Save.Fly = !g.Save.Fly
		logf("autofly toggle fly=%v", g.Save.Fly)
	}
	// 调试钩子：NONO_SHOW_SHORTCUT=1 时自动弹一次快捷栏，用于验证「环心 =
	// 球心」的定位（Wine 下点击本体不收按键，没法手动触发）。
	if autoScOnce == 0 && g.TickN == 40 && os.Getenv("NONO_SHOW_SHORTCUT") != "" {
		autoScOnce = 1
		g.showShortcut()
		logf("dbg shortcut ball=%d,%d win=%dx%d at=%d,%d",
			g.BallSX, g.BallSY, shortcutWin.W, shortcutWin.H, shortcutWin.X, shortcutWin.Y)
	}

	// 快捷栏跟随本体走动：放到 draw() 末尾做，用当前帧的 WinY0/CurW，
	// 否则跟随移动时会拿上一帧的位置，快捷栏滞后一个身位（实测偏 50-60px）。

	// 任何面板可见时，每帧把它重新提到最前并压住本体窗口。
	// Wine 下 UpdateLayeredWindow 会让本体浮到同组顶部，只在 showAt 里压一次不够
	// （实测合成后播动作时本体又盖到面板标题上，2026-10-23）。
	keepPanelsOnTop()

	// 主面板黑屏表情动画（faceMc）：24fps 近似 40ms 一帧
	if mainPanelWin != nil && mainPanelWin.Visible && faceTick() {
		mainPanelWin.paint()
	}

	if g.BubbleTicks > 0 {
		g.BubbleTicks--
		if g.BubbleTicks == 0 {
			g.Bubble = ""
			// 行与形状一起清：形状位图按文本缓存，留着只会占内存，
			// 下次 setBubble 也会自己置空。
			g.BubbleLines = nil
			g.BubbleShape = nil
		}
	}

	// 快捷栏点击轮询兜底：Wine 下本体/快捷栏两个分层窗口经常收不到
	// WM_LBUTTONDOWN（悬停正常、按下静默），而面板窗口能收。这里只针对
	// 「快捷栏可见 + 光标在快捷栏内」轮询左键沿，位移 <8px 才算点击，
	// 不影响面板按钮与拖动（它们走各自的窗口消息）。
	if shortcutWin != nil && shortcutWin.Visible {
		pt := POINT{}
		pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		st, _, _ := pGetAsyncKeyState.Call(VK_LBUTTON)
		down := int16(uint16(st)) < 0
		if down && !g.SCPollDown {
			g.SCPollDown = true
			g.SCPollX, g.SCPollY = int(pt.X), int(pt.Y)
			// 按下时就定住命中项：抬起只看这里记的按钮，避免"点别的→划过→松手"
			// 被判成点击（子渊 2026-10-24 反馈鼠标滑过就打开功能）。
			g.SCPollHit = shortcutWin.hitAt(int(pt.X)-shortcutWin.X, int(pt.Y)-shortcutWin.Y)
		} else if !down && g.SCPollDown {
			g.SCPollDown = false
			dx := int(pt.X) - g.SCPollX
			dy := int(pt.Y) - g.SCPollY
			hit := g.SCPollHit
			g.SCPollHit = -1
			if dx*dx+dy*dy <= 64 && hit >= 0 {
				cx, cy := int(pt.X)-shortcutWin.X, int(pt.Y)-shortcutWin.Y
				if idx := shortcutWin.hitAt(cx, cy); idx == hit && idx < len(shortcutWin.Btns) {
					logf("sc poll click idx=%d action=%s", idx, shortcutWin.Btns[idx].Action)
					shortcutAction(shortcutWin.Btns[idx].Action)
					return
				}
			}
		}
	} else {
		g.SCPollDown = false
		g.SCPollHit = -1
	}

	// 游戏内 ROLL_OUT：光标离开「本体 + 快捷栏 + 两者之间连接带」后收起。
	// 连接带必须算进去，否则从头顶移向快捷栏时穿过空隙会误收起。
	if shortcutWin != nil && shortcutWin.Visible && os.Getenv("NONO_UI") == "" {
		pt := POINT{}
		pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		x, y := int(pt.X), int(pt.Y)
		left := g.BaseX
		right := g.BaseX + g.BodyW
		if shortcutWin.X < left {
			left = shortcutWin.X
		}
		if shortcutWin.X+shortcutWin.W > right {
			right = shortcutWin.X + shortcutWin.W
		}
		top := shortcutWin.Y
		bottom := g.BaseY + g.BodyH
		inArea := x >= left && x < right && y >= top && y < bottom
		// 环形图标本身就画在本体窗口之外，只按本体矩形判定会让鼠标一移向
		// 图标就"出圈"→ 快捷栏立刻收起 → 图标永远点不中（子渊 2026-10-23
		// 反馈「跟随二次点击不取消」的真实原因）。把快捷栏窗口也计入判定区。
		if !inArea && shortcutWin != nil && shortcutWin.Visible {
			inArea = x >= shortcutWin.X && x < shortcutWin.X+shortcutWin.W &&
				y >= shortcutWin.Y && y < shortcutWin.Y+shortcutWin.H
		}
		if !inArea {
			// 光标基本没动、是 NONO 自己走开的情况：保持显示并跟随移动；
			// 只有光标主动移开（超过阈值）才收起，否则一走路圈就消失。
			// 原版 ROLL_OUT 是「光标真离开就收起」；桌宠的区域会随本体移动，
			// 所以只能近似：光标自身位移超过 10px 视为主动移开。
			moved := absInt(x-g.SCMouseX) > 10 || absInt(y-g.SCMouseY) > 10
			if moved {
				hideShortcut()
			}
		}
	}

	// 关机/开机：对应游戏 state[0]，关机后本体不显示（窗口隐藏，托盘可开机）
	g.syncBodyVisible()

	// 待机自发表情：原版 onFollowTime / startAutoTime 的 randomHalve(20000)，
	// 即 10~20 秒随机播一次表情；独立态同时冒一句台词（跟随态无台词，对齐 §3.1）。
	// 放在动作播放分支之前，否则动作播放期间的 return 会让计时器永远不递减。
	if g.canExpress() {
		if g.ExpTimer > 0 {
			g.ExpTimer--
		}
		if g.ExpTimer == 0 {
			g.idleExpress()
		}
	}

	// 动作/表情播放
	if g.Anim != nil {
		g.Anim.Ticks++
		if g.Anim.Frozen {
			// 定格在末帧（20261029-03 关机保持）：画面已经上屏，不再推进也不再
			// 重绘。窗口内容由 UpdateLayeredWindow 保留，CPU 占用归零。
			//
			// 但 installPendingForm 在本分支之后，必须在这里补调一次：关机定格
			// 是长期状态，右键切超能形态是异步加载，直接 return 会让新形态永远
			// 落不了地（关机态一点切形态就没反应）。
			g.installPendingForm()
			// 拖动中必须继续重绘（20261029-04）：Windows 在 SetCapture 拖动时
			// 会合并 WM_MOUSEMOVE，待机态靠 tick 每 40ms 重绘兜底所以平滑；
			// 定格态若只靠 mousemove 重绘，窗口就明显落后光标（「拖动漂移、
			// 不跟手」）。静止时仍然不重绘，CPU 保持 0。
			if g.Down && g.Moved {
				g.draw()
			}
			return
		}
		// 按真实时间推进：原版主舞台 Core.swf frameRate=24，MC 以 41.67ms/帧
		// 播放（nono.swf 自带 25fps 不生效——外部加载的子 SWF 跟随舞台帧率）。
		// 旧实现「一 tick 一帧」在 40ms 定时器下等效 25fps，且定时器抖动会
		// 直接变成播放速度抖动，这是「桌面比游戏快/不稳」的来源之一。
		g.Anim.Acc += float64(dtMs)
		for g.Anim.Acc >= animFrameMs {
			g.Anim.Acc -= animFrameMs
			last := g.Anim.total() - 1
			if g.Anim.Idx >= last {
				if g.Anim.Hold {
					// 关机保持：停在最后一帧。官方每个动作 sprite 的末帧都有
					// stop()（action/3_1 的 pet.as: addFrameScript(164)->stop()），
					// super1-4 循环播放只是 NonoModel 清根末帧脚本的副作用；
					// 子渊 2026-10-29：「不是循环某几帧，就保持最后一帧」。
					g.Anim.Idx = last
					g.Anim.Acc = 0
					g.Anim.Frozen = true
					logf("anim hold kind=%d id=%d 停在末帧 %d tick=%d", g.Anim.Kind, g.Anim.ID, last, g.TickN)
					break
				}
				if g.Anim.Loop {
					// 循环段（暂无调用点）：回绕到首帧继续播。帧缓存由
					// FrameSet.animSetPinAll(true) 整段驻留，回绕不再触发重渲；
					// 这里 continue 让剩余 Acc 继续推进（dtMs 抖动时一次 tick
					// 可能连推多帧，语义与动作段一致）。
					g.Anim.Idx = 0
					g.Anim.Loops++
					if g.Anim.Loops == 1 || g.Anim.Loops%20 == 0 {
						logf("anim loop kind=%d id=%d 第 %d 圈 tick=%d", g.Anim.Kind, g.Anim.ID, g.Anim.Loops, g.TickN)
					}
					continue
				}
				g.idle()
				break
			}
			g.Anim.Idx++
		}
		g.draw()
		return
	}

	// 关机保持（20261029-01）：没有循环关机动画的形态（plain 官方无 action 3）
	// 停在这一帧不动 —— 不推进待机、不走动、不跟随、不冒表情。
	// 有循环动画时上面已经 return，这里只兜住「关机且无动画」的情况。
	if !g.Save.Open {
		return
	}

	// 待机/移动帧：原版 NonoModel.onResLoad / changedir 只对最外层 MC 做
	// gotoAndStop(方向标签)，停在该方向段的第 1 帧；但段首帧里嵌套的内层 MC
	// （sprite 52→49、68→52→51、94→71/79/84/89 等）是独立时间轴，仍按主舞台
	// 24fps 继续播放。「游戏内完全静止」的旧结论已作废（只对外层停帧）。
	// assets/plain/idle/<方向>/ 里逐层烘出的 17 帧就是这段内层动画，
	// 这里按 24fps（≈41.67ms/帧）循环推进；不足 17 帧的旧素材也按实际长度跑。
	g.IdleAnimAcc += float64(dtMs)
	// 每形态的待机/移动帧时长可在设置面板单独调（Save.IdleMs / MoveMs）。
	// 「移动中」用上一帧的实际位移判断：跟随/自动走动都会改 BaseX/BaseY，
	// 而 prevBase 由 draw() 末尾更新，正好是「上一帧的位置」。
	idleN := g.FS.idleFrameCount(g.Dir)
	idleMs := g.frameMsFor(g.BaseX != g.prevBaseX || g.BaseY != g.prevBaseY)
	// 每个 tick 最多推进 1 帧：拖拽窗口/切形态/建 renderer 时单 tick 可能耗掉
	// 数百毫秒，累加式推进会把中间几帧一口气跳过去——待机 17 帧里有 5 帧是
	// 眨眼（横条眼），跳帧就直接表现为「眨眼被吞掉」（子渊 2026-10-26 反馈）。
	// 宁可整体慢半拍，也不让任何一帧没上过屏。
	if want := int(g.IdleAnimAcc / idleMs); want > 0 {
		if want > 1 {
			// 只补 1 帧，多出来的额度丢弃（宁可慢，也不跳帧）。保留一个 idleMs
			// 而不是清零，避免把「刚好到点」的余量也吃掉、导致整体越来越慢。
			g.IdleAnimAcc = idleMs
			g.IdleSkipN += want - 1
			logf("idle throttle want=%d frame=%d idleMs=%.1f skipTotal=%d", want, g.Frame, idleMs, g.IdleSkipN)
		} else {
			g.IdleAnimAcc -= idleMs
		}
		g.Frame++
	}
	if idleN > 0 {
		g.Frame %= idleN
	} else {
		g.Frame = 0
	}

	if g.TickN%25 == 0 {
		logf("idle dir=%s frame=%d base=%d,%d body=%dx%d@%d,%d win=%dx%d fly=%v follow=%v walk=%v super=%d", g.Dir, g.Frame, g.BaseX, g.BaseY, g.BodyW, g.BodyH, g.BodyX, g.BodyY, g.CurW, g.CurH, g.Save.Fly, g.Save.Follow, g.Save.AutoWalk, g.Save.SuperLevel)
	}

	// 走动
	// 拖动期间必须停下：WM_MOUSEMOVE 每帧把 BaseX 设到光标，tick 又按活动区
	// clamp 回去，两边抢同一个坐标，表现就是「拖不走 / 拖到哪都弹回原处」
	// （子渊 2026-10-27）。
	if g.Save.AutoWalk && !g.Save.Follow && !g.Down {
		if g.PauseTicks > 0 {
			g.PauseTicks--
		} else if g.WalkTicks > 0 {
			g.WalkTicks--
			// 原版独立态在 _autoRect 给定的矩形内游走，不是整个场景。
			vx, vy, vw, vh := g.walkBounds()
			x0, x1 := vx, vx+vw-g.BodyW
			y0, y1 := vy, vy+vh-g.BodyH
			nx := g.BaseX + int(g.VX)
			ny := g.BaseY + int(g.VY)
			cx, cy := g.BaseX, g.BaseY
			bounced := false
			// 分轴判定：只反转撞到的那一个轴，另一个轴继续走（沿边滑）。
			//
			// 2026-10-27 子渊打回「原地抽搐，两个相反方向来回旋转」：旧实现
			// 只要有一个轴越界就 VX、VY 一起反转，斜向走时撞一侧边界会整体
			// 180° 原路退回（实测日志：rightup 撞 y=0 后变成 leftdown）。
			// 活动区被缩放/形态撑到接近本体尺寸时，这会退化成每帧反转——
			// 位置原地抖几像素、方向帧 right/left 左右闪，就是抽搐。
			if x1 > x0 {
				cx = clamp(nx, x0, x1)
				if cx != nx {
					g.VX = -g.VX
					bounced = true
				}
			}
			// x1 <= x0 表示屏幕比本体还窄，没有可走空间：位置钉死、不反转，
			// 否则又会退化成每帧反弹。
			if y1 > y0 {
				cy = clamp(ny, y0, y1)
				if cy != ny {
					g.VY = -g.VY
					bounced = true
				}
			}
			g.BaseX, g.BaseY = cx, cy
			// 2026-11-02 E-02：自动走动中间也节流落盘，进程被杀不丢位置。
			g.savePosThrottled()
			// 朝向每 tick 跟随实际速度：走动此前完全不更新朝向，一直是启动时
			// 那一帧的方向（「移动没有应用对应方向动画」的真正原因）。
			if g.VX != 0 || g.VY != 0 {
				g.Dir = dirFromVec(g.VX, g.VY)
			}
			if bounced {
				logf("walk bounce vx=%.0f vy=%.0f dir=%s base=%d,%d bounds=%d..%d,%d..%d", g.VX, g.VY, g.Dir, g.BaseX, g.BaseY, x0, x1, y0, y1)
			}
		} else {
			// 停下后再决定下一步
			if rand.Intn(90) == 0 {
				g.startWalk()
			}
		}
	}

	// 跟随鼠标。快捷栏弹出时暂停跟随（原版 NonoFlyModel.as:130 里走一步就
	// NonoShortcut.hide()，等价于「操作快捷栏时不追」）：否则鼠标移向环形
	// 图标的过程中 NONO 一路跟着，图标永远点不中（子渊 2026-10-23 反馈
	// 「跟随点击后生效，二次点击不会取消」的直接原因）。
	// 赛尔环绕栏的「召唤 NONO」也能拉起跟随：与本地开关取或，
	// 不改本地 Save.Follow（召唤结束自动回落到本地设置）。
	// 飞行态「赛尔坐进飞行器」的抬升量：本 tick 先按 0 算，只有下面「飞行 + 跟赛尔」
	// 分支会改写；tick 末尾一次性写进 atomic（回包由 follow_seer.go 的心跳 goroutine 发）。
	//
	// 不能「先 store 0 再在分支里 store 86」：心跳 goroutine 正好在两次写之间采样
	// 就会发 `nono fly 0`，赛尔一落一起地抖（2026-11-01 实测 0→86→0→86）。
	// 抬升量只取决于「飞行风格 + 赛尔缩放」，与跟随是否暂停**无关**：
	// 鼠标悬停弹出快捷栏会暂停跟随，如果抬升跟着归零，人就从车里掉出来
	// （2026-11-01 实测：悬停 NONO → 快捷栏弹出 → 回包 `nono fly 0` → 赛尔下坠 101px）。
	liftWant := g.flyRideLiftPx()
	if (g.Save.Follow || seerSummonOn()) && (shortcutWin == nil || !shortcutWin.Visible) {
		pt := POINT{}
		pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		mx, my := int(pt.X), int(pt.Y)
		// 赛尔桌宠在跑 → 跟赛尔本体；否则跟鼠标（同一套偏移/停步/换边逻辑）。
		// 子渊 2026-10-31：「跟随现在就做」——NONO 的跟随开关不变，语义扩一层。
		sx, sy, sw, sh, sdir, seerOn := seerPos()
		if seerOn {
			mx, my = sx, sy
			if !seerFollowLogged {
				seerFollowLogged = true
				logf("跟随目标 → 赛尔桌宠 %d,%d 宽%d 高%d 朝向%d（UDP 心跳）", sx, sy, sw, sh, sdir)
			}
		} else if seerFollowLogged {
			seerFollowLogged = false
			logf("跟随目标 → 鼠标（赛尔桌宠心跳断了）")
		}
		vx, vy, vw, vh := virtualScreen()
		vecScreenW, vecScreenH = vw, vh
		// 原版 NONO 挂在赛尔侧边，偏移 MAX=30，按朝向换边且绝不压在主人身上（§4.1）。
		// 桌宠把鼠标当主人：站到鼠标靠屏幕中心那一侧，留 30px 间隙。
		//
		// 2026-10-26：尺寸一律用 BodyW/BodyH（本体可见区），不用 CurW/CurH。
		// 残影留边会让窗口比本体宽 2*ghostPad，用窗口尺寸算「站在哪一侧」会：
		//   ① 目标位置整体偏 ghostPad；
		//   ② 留边随「这一帧有没有在动」切换，目标位置每帧跳 16px，跟随就抖。
		// 换边加 40px 死区（g.FollowSide 记忆上一次选边）：光标停在中线附近
		// 轻微抖动时，NONO 不会左右来回横跳。
		mid := vx + vw/2
		side := 1 // 1 = 站在光标右侧（靠屏幕左半边的光标），-1 = 站光标左侧
		if mx > mid {
			side = -1
		}
		if g.FollowSide != 0 && side != g.FollowSide && absInt(mx-mid) < 40 {
			side = g.FollowSide
		}
		g.FollowSide = side
		tx := mx + 30
		if side < 0 {
			tx = mx - 30 - g.BodyW
		}
		if g.flyOn() {
			// 飞行态没有左右站位概念（官方飞行器就挂在主人原点），整帧水平居中
			// 到目标上；不然从「跟赛尔」切回「跟鼠标」会横跳 30px。
			tx = mx - (g.BallSX - g.BaseX)
		}
		if tx < vx {
			tx = vx
		}
		if tx > vx+vw-g.BodyW {
			tx = vx + vw - g.BodyW
		}
		// 跟随目标以「窗口顶」为准，飞行时窗口顶比 BaseY 高一整个飞行器，
		// 不补偿的话 NONO 会浮在鼠标上方很远（子渊 2026-10-23 反馈）。
		ty := my - g.BodyH + 20 + g.flyOffset()
		if ty < vy {
			ty = vy
		}
		if ty > vy+vh-g.BodyH {
			ty = vy + vh - g.BodyH
		}
		// 跟着赛尔桌宠时换成「并排站」的目标：按赛尔本体宽度让开、留 20px 间隙，
		// 竖直方向中心对齐。上面那套 30px + 140px 停步是按「光标」调的：
		// 光标是个点，而赛尔本体宽 74，直接套会把两只叠在一起（2026-10-31 实测重叠 94px）。
		stopDist := 140 // 跟光标：贴近本体就不再挪
		weldSeer := false // 飞行+跟赛尔：位置/朝向整体焊死到赛尔身上（官方子节点口径）
		if seerOn {
			// 官方 NonoModel.moveForPeople（NonoModel.as:790-842）：
			//   _dirPos = 主人 pos + (x, y)，x/y 取自 MAX = 30（NonoModel.as:42），
			//   主人朝左系（left/leftup/leftdown/down）→ x = +30（NONO 站右），
			//   朝右系（right/rightup/rightdown/up）→ x = -30（站左）；
			//   朝下系 → y = -30（NONO 在上），朝上系 → y = +10，左右 → y = 0。
			// 主人 pos 是**脚底**，NONO 的 pos 也是它自己的脚下。
			//
			// 以前这里用「赛尔本体宽 + 20px 间隙 + NONO 本体宽」算并排位置，
			// 而 NONO 的 BodyW 实测 289（里面大半是透明留白，机器人只有 ~90 宽），
			// 于是目标点被推到两百多像素外 —— 子渊 2026-10-31「跟随很远」。
			// 现在照官方用固定 30px；锚点用 NONO 球心（BallSX/BallSY，跨形态恒定，
			// 不随装扮/飞行变），再减去球心到脚下的 nonoBallRadius 换算成官方口径。
			dxo, dyo := officialFollowOffset(sdir)
			seerFeetY := my + sh/2 // 心跳发的是本体中心 → 换回脚底
			offBX := g.BallSX - g.BaseX
			offBY := g.BallSY - g.BaseY
			if g.flyOn() {
				// 飞行态走官方口径：NonoFlyModel.as:145-152 先 keepUp(skeleton,
				// -80/-50/-90/-70) 把**主人**抬起来，再
				// people.sprite.addChildAt(flyMachineMc, 1) 把飞行器挂到主人原点
				// (0,0)、第 1 层。全文没有 moveForPeople —— 没有 ±30 站位、没有方向
				// 偏移，飞行器 mc 原点就焊在主人脚下。
				//
				// 落到我们两个独立窗口上（2026-11-01 第二轮，子渊实拍四张核对）：
				//   ① 飞行器 **mc 原点**（flyRegPoint，不是整帧底边）对准赛尔脚底；
				//   ② 赛尔自己抬高 lift（由 follow_seer 回包 `nono fly <lift>` 通知），
				//      头/肩露在机器口外 —— 官方那张「人坐在车里」就是这两条。
				// 上一版只做了「整帧底边压脚底 + 不画本体」，漏了 ②，
				// 结果机器整个飘在赛尔头顶上方，看着是「车飘在头上」而不是坐进去。
				// 飞行器显示尺寸 = 飞行器整帧原尺寸 × **赛尔的缩放**（C1 严格官方口径）。
				// 官方 NonoFlyModel 和主人共用同一个 0.8 比例（NonoModel.SCALE），
				// 所以人机相对大小是固定的；以前用 NONO 自己的 Save.Scale，
				// 两边设置不一样时机器就会偏大/偏小。心跳末位带了赛尔缩放×1000，
				// 拿不到（旧版赛尔）才回退到自己的 flyScale。
				rideScale := g.flyScale()
				if sc := atomic.LoadInt32(&seerFollowScale); sc > 100 {
					rideScale = float64(sc) / 1000
				}
				fh := int(float64(flyOriginalFullH(g.Save.FlyStyle)) * rideScale * flySizeAdj[g.Save.FlyStyle])
				if fh < 24 {
					fh = 24
				}
				tx = mx - offBX + int(flyRideDX[g.Save.FlyStyle])
				if regDy, ok := flyRegDyFromBaseY(g.Save.FlyStyle, fh); ok {
					ty = seerFeetY - regDy
				} else {
					ty = seerFeetY
				}
				// 官方飞行器 NonoFlyModel 的原点 (0,0) 就挂在主人 sprite 原点 = 赛尔脚底，
				// 所以机器的**接地阴影中心**必须落在赛尔脚底。
				// 我方导出的 mc 注册点偏低：实测 style1 阴影中心 y403、赛尔脚底 y383，
				// 差 20px（1280×800 实屏，缩放 1.5）。差这一截就会出现「赛尔浮在机器上方、
				// 中间露一条缝」——子渊 2026-11-01「位置和大小不一样」就是这个缝。
				ty -= flyGroundFix(g.Save.FlyStyle)
				// 赛尔该抬多少：**直接用官方的固定值**，不再按美术反推。
				//
				// 官方 PeculiarAction.keepUp()（PeculiarAction.as:143）就是一句
				// this._people.getBodyMC().y = param2，调用处按 flyStyle 传
				// -80 / -50 / -90 / -70；飞行器 NonoFlyModel 挂在主人 sprite 原点
				// (0,0)（= 赛尔脚底），不额外缩放。所以官方的「嵌入位置」只有这两个数：
				//   ① 飞行器原点 = 主人脚底
				//   ② 主人身体抬高 80/50/90/70（玩家本地单位）
				// 本地单位 × 赛尔显示缩放 = 屏幕像素，和官方同一口径。
				// 之前按「机器顶盖 / 腰环下沿」逐张量取，四次测量四个结果、每次都不一样，
				// 就是子渊说的「怎么还会位置和大小不一样」—— 现在废掉那套标定。
				if _, ok := flyRideLift[g.Save.FlyStyle]; ok {
					// 单一来源：抬升只由 flyRideLiftPx() 算（有灰座位按座位中心对齐，
					// 没有则官方 keepUp 兜底）。这里以前自己又算一遍，
					// 结果两条公式打架、画面上用的是旧值（2026-11-01 踩过）。
					liftWant = g.flyRideLiftPx()
					if !g.flySeerLogged {
						g.flySeerLogged = true
						logf("飞行挂载：style=%d 抬升=%d 缩放=%.2f fh=%d 座位=%v",
							g.Save.FlyStyle, liftWant, rideScale, fh, flySeatArtY[g.Save.FlyStyle])
					}
				} else if !g.flySeerLogged {
					g.flySeerLogged = true
					logf("飞行挂载：flyStyle=%d 无官方 keepUp 值", g.Save.FlyStyle)
				}
				// 官方飞行器是主人的**子节点**：位置随主人、朝向也随主人转，
				// 自己不做任何位移/转向。落到我们两个进程上必须显式做两件事，
				// 否则就是子渊 2026-11-01「赛尔的位置和方向都不一致」：
				//   ① 位置：目标点直接贴过去，不再按 4px/tick 追 ——
				//      赛尔心跳 40ms 一跳，追帧永远落后，边走边分离；
				//   ② 朝向：用**赛尔当前朝向**，不再用 dirFromVec(朝赛尔的方向) ——
				//      后者会让机器侧着身子对着赛尔（机器朝左、赛尔朝下）。
				weldSeer = true
				// Z 序：**不要**把自己提到最前。官方 NonoFlyModel.as:170 是
				// addChildAt(_flyMachineMc, 1)，而主人骨架是后追加的 addChild，
				// 飞行器实际画在主人身体**下面** —— 赛尔压在机器上才对。
				// 2026-11-01 第三轮之前这里每 6 tick 自提最前，方向是反的，已删；
				// 现在由 seer_player 侧每 6 tick 把自己提回最顶（见其 tick）。
			} else {
				tx = mx + dxo - offBX
				ty = seerFeetY + dyo - nonoBallRadius - offBY
			}
			if tx < vx {
				tx = vx
			}
			if tx > vx+vw-g.BodyW {
				tx = vx + vw - g.BodyW
			}
			if ty < vy {
				ty = vy
			}
			if ty > vy+vh-g.BodyH {
				ty = vy + vh - g.BodyH
			}
			stopDist = 8 // 跟赛尔：4px/步，留 8px 死区避免原地抖
		}
		dx := tx - g.BaseX
		dy := ty - g.BaseY
		// 鼠标已经贴近本体时不再挪：否则 NONO 会一直追着光标微调，快捷栏
		// 跟着动，环形图标永远点不中（跟随开关点了也取消不了的直接原因）。
		dist := absInt(mx-(g.BaseX+g.BodyW/2)) + absInt(my-(g.BaseY+g.BodyH/2))
		if seerOn {
			dist = absInt(dx) + absInt(dy)
		}
		if dist <= stopDist {
			dx, dy = 0, 0
		}
		// 移动门 = stopDist，不再用固定的 20px（400 = 20²）。
		//
		// 以前这里写死 `dx*dx+dy*dy > 400`，而跟赛尔时 stopDist=8：
		// 距离落在 8~20px 之间既不判到点、也不移动 —— 目标变了也不追，
		// NONO 就卡在那里（子渊 2026-10-31「跟随赛尔这个功能没用」，
		// 实测停在 base=232 而目标 tx=249，差 17px 正好在死区里）。
		if weldSeer {
			// 焊死：位置直接等于目标，朝向等于赛尔朝向（官方子节点语义）。
			// 心跳 40ms 一包，40ms tick 对齐后视觉上就是「一起动」。
			// 机器中线对准赛尔中线（素材注册点 x 不等于美术中线，实测偏左 5~12px）
			tx += int(flyDxFix[g.Save.FlyStyle] + 0.5)
			g.BaseX, g.BaseY = tx, ty
			if sdir >= 0 && sdir < len(seerDirNames) {
				g.Dir = seerDirNames[sdir]
			}
		} else if dist > stopDist {
			// 原版 NonoModel.moveForPeople/onMoveDir：_dirSpeed 由 angleSpeed
			// 算出来后 *4，每个 ENTER_FRAME 走 4px（24fps ≈ 96px/s）。
			// tick=40ms 下 4px/tick ≈ 100px/s，与旧值 3 相比更贴原版追随手感。
			g.BaseX += sign(dx) * minInt(4, absInt(dx))
			g.BaseY += sign(dy) * minInt(4, absInt(dy))
			g.Dir = dirFromVec(float64(dx), float64(dy))
		}
	}

	atomic.StoreInt32(&seerLift, liftWant)
	g.installPendingForm()
	g.prevX, g.prevY = g.BaseX, g.BaseY
	g.draw()
}

func sign(v int) int {
	if v > 0 {
		return 1
	}
	if v < 0 {
		return -1
	}
	return 0
}

// formIndex 形态在 Save.IdleMs / MoveMs 里的下标：0=plain、1..5 对应 super1..super5、6=annual。
func (g *Game) formIndex() int {
	if g.FS == nil {
		return 0
	}
	if g.FS.Key == "annual" {
		return 6
	}
	if len(g.FS.Key) != 6 || g.FS.Key[:5] != "super" {
		return 0
	}
	n := int(g.FS.Key[5] - '0')
	if n < 1 || n > 5 {
		return 0
	}
	return n
}

// frameMsFor 返回当前形态「待机 / 移动」的每帧时长（毫秒）。
// 下标未配置（0）时回落到默认值。值越小播放越快。
//
// plain 的默认值比超能慢一档（130 vs 100）：它 17 帧是内层 MC 连续变化的
// 17 种姿态，同样帧率下循环周期只有 1.7 秒，视觉上明显比 super5（20 帧 2 秒）
// 更"赶"。130ms 让 plain 一圈约 2.2 秒，接近用户认可的 super5 观感。
// 觉得不对可在设置面板「动画速度」里逐形态调，这里只是出厂值。
func (g *Game) frameMsFor(moving bool) float64 {
	tbl := &g.Save.IdleMs
	if moving {
		tbl = &g.Save.MoveMs
	}
	idx := g.formIndex()
	v := 0.0
	if idx == 0 {
		v = plainDefaultMs
	} else {
		v = idleFrameMs
	}
	if t := tbl[idx]; t > 0 {
		v = float64(t)
	}
	// 对齐到 tickMs 的整数倍：累加器按 tick 走，非整除的间隔一定会在
	// 「这一帧 2 tick、下一帧 3 tick」之间交替，肉眼就是节奏一快一慢。
	n := math.Round(v / float64(tickMs))
	if n < 1 {
		n = 1
	}
	if ms := n * float64(tickMs); ms != v {
		g.AlignNote += 1
		if g.AlignNote <= 3 {
			logf("frameMsFor align %v -> %v (idx=%d moving=%v)", v, ms, idx, moving)
		}
		return ms
	}
	return v
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (g *Game) startWalk() {
	d := rand.Intn(8)
	vx := []float64{1, 1, 0, -1, -1, -1, 0, 1}[d]
	vy := []float64{0, 1, 1, 1, 0, -1, -1, -1}[d]
	// 原版 NonoModel 构造里 _speed = 3，WalkAction.loop 每个 ENTER_FRAME
	// 按 _speed 像素推进（主舞台 24fps ≈ 72px/s）。桌宠 tick=40ms，3px/tick
	// ≈ 75px/s，取 3 对齐；旧值 1.6 只有原版一半速度。
	g.VX, g.VY = vx*3, vy*3
	// 原版没有「固定行走时长」：onAutoWalk 每 3~4.5 秒（starAutoWalk(3000)
	// → randomHalfAdd）挑一个随机点，AStar 走到点就停。桌宠用矩形活动区近似，
	// 走 1.2~2.0 秒（30~50 tick ≈ 90~150px）后回到等待，总周期对齐 3~4.5 秒。
	g.WalkTicks = 30 + rand.Intn(20)
	g.PauseTicks = 0
	g.Dir = dirFromVec(g.VX, g.VY)
}

// ---- 渲染 ----

func (g *Game) currentFrame() *image.RGBA {
	if g.Anim != nil && g.Anim.total() > 0 {
		i := g.Anim.Idx
		if i >= g.Anim.total() {
			i = g.Anim.total() - 1
		}
		// 矢量后端：按帧现渲染（实测平均 9.5ms、最慢 32.5ms，24fps 预算 41ms）。
		// 不再缓存整段位图——那是 super5/action_7 吃 658MB 的原因。
		img, off := g.FS.animFrame(g.Anim.Kind, g.Anim.ID, i)
		if img == nil && len(g.Anim.Frames) > 0 {
			// PNG 后端：animFrame 已返回切片里的帧；这里只兜住越界。
			if i < len(g.Anim.Frames) {
				img = g.Anim.Frames[i]
			}
		}
		if img == nil {
			return nil
		}
		g.AnimLastIdx = i
		g.Anim.FrameOff = off
		g.Anim.BBox = g.FS.animFrameBBox(g.Anim.Kind, g.Anim.ID, i)
		return img
	}
	// 待机/移动取当前方向的第 g.Frame 帧：外层 MC 停在方向段首，内层 17 帧
	// 时间轴在 main.go 的待机分支里按 24fps 推进，所以这里直接用帧序号。
	fr := g.FS.idleFrames(g.Dir)
	if len(fr) == 0 {
		if v := g.FS.idleFrames("down"); len(v) > 0 {
			g.IdleShown = 0
			return v[0]
		}
		return nil
	}
	if g.Frame >= len(fr) {
		g.Frame = 0
	}
	// 方向帧现在是「同步首帧 + 后台补齐」，补帧还没到位时槽位是 nil。
	// 回退到最近的有效帧，避免闪空/闪烁（宁可停一格，也不能黑一下）。
	if img := fr[g.Frame]; img != nil {
		g.IdleShown = g.Frame
		return img
	}
	for k := 1; k < len(fr); k++ {
		j := (g.Frame - k + len(fr)) % len(fr)
		if fr[j] != nil {
			g.IdleShown = j
			return fr[j]
		}
	}
	return nil
}

var logEnabled = os.Getenv("NONO_LOG") != ""

// ---- 日志（2026-11-02 对照 seer-20261102-05 D-11/D-12 修复）----
// 旧实现每次 logf 都 OpenFile + Close，鼠标在面板上移动时每帧一次文件 IO；
// 也没有轮转，长期开 NONO_LOG 让 nono_debug.log 无限增长。
// 现在：进程内保持单个 *os.File 句柄（Mutex 串行），超过 8MB 截断重开。

const logMaxBytes = 8 << 20 // 8MB

var (
	logOnce   sync.Once
	logMu     sync.Mutex
	logFile   *os.File
	logOpened bool
)

func openLog() {
	if logOpened {
		return
	}
	fl, err := os.OpenFile("nono_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	logFile = fl
	logOpened = true
}

func rotateLogIfNeeded() {
	if logFile == nil {
		return
	}
	st, err := logFile.Stat()
	if err != nil || st.Size() < logMaxBytes {
		return
	}
	logFile.Close()
	logFile = nil
	logOpened = false
	// 旧的挪成 .1（保留一份，避免把历史直接抹掉）
	os.Rename("nono_debug.log", "nono_debug.log.1")
	openLog()
}

func logf(f string, a ...interface{}) {
	if !logEnabled {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	logOnce.Do(openLog)
	if logFile == nil {
		return
	}
	fmt.Fprintf(logFile, time.Now().Format("15:04:05.000")+" "+f+"\n", a...)
	rotateLogIfNeeded()
}

// fatalBox 致命错误弹窗（2026-11-02 对照 seer-20261102-05 D-17 修复）。
// 旧实现里 pMsgBox 定义了但从不调用——loadChips/loadFrameSet 失败直接
// panic，用户只看到「双击没反应/闪退」，没有任何 GUI 提示。
// 现在：启动的每个致命错误先弹 MessageBox 再 panic（panic 仍会崩，
// 但用户至少知道原因，而不是以为桌宠没装好）。
func fatalBox(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	logf("FATAL: %s", msg)
	title := utf16ptr("NONO 桌宠")
	text := utf16ptr(msg)
	pMsgBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10 /* MB_ICONERROR */)
}

// debugCenterMark：把快捷栏环心画成红十字（NONO_DEBUG_CENTER=1）。只在排错时开。
var debugCenterMark = os.Getenv("NONO_DEBUG_CENTER") != ""
var profEnabled = os.Getenv("NONO_PROF") != ""
var profTick0 time.Time
var profTickN int
var profResize, profCopy, profBlit time.Duration
var profClip, profVis, profGhost time.Duration
var profSegHead time.Duration
var profNewRenderer time.Duration
var flyBodyClipH int

// flyShowBody：飞行态是否额外叠一层 NONO 本体。
//
// 2026-11-01 改回 false（官方口径）：NonoFlyModel.as:151-158 是「模型整体替换」
// —— NonoModel 被 destroy，画面上只剩飞行器；NONO 的脸本来就烘在飞行器正面
// 黑面板里（style1/2/3/4 逐帧 dump 已核，四种都有脸）。子渊 2026-11-01 给了
// 官方实拍四张，飞行时看不到独立本体，只有飞行器。
// 之前 2026-10-31 因为「座舱空着」把本体叠回去，但本体帧 227x182（≈180px）
// 比飞行器可见主体（77x90）大两倍，叠上去像「NONO 浮在自己车顶上」，
// 反而离官方更远。要恢复旧观感，把这个常量改回 true 即可（其余逻辑不动）。
const flyShowBody = false

var flyBodyDumped bool
var flyBodyLogged bool
var flyMachineDumped bool
var canvasBuf *image.RGBA
var canvasBufW, canvasBufH int

func (g *Game) draw() {
	tDraw0 := time.Now()
	// 玩家色同步给矢量渲染器（只染 tintable 层）。必须放在取帧之前，
	// 否则这一帧拿到的还是旧颜色的缓存。
	if g.FS != nil && g.FS.vec != nil {
		g.FS.vec.setTint(g.Save.Color)
		// 700314 自定义渐变芯片：除了单色底（GradA）还要把渐变下发，
		// 否则选了渐变也只显示起始色。其余芯片走单色，明确关掉渐变。
		if g.Save.ColorChip == 700314 {
			g.FS.vec.setGradient(g.Save.GradA, g.Save.GradB, g.Save.GradMode)
		} else {
			g.FS.vec.clearGradient()
		}
		// 待机帧的预缩放口径必须跟当前 Scale 一致，且要在取帧之前同步：
		// 缩放变了还继续交旧尺寸的帧，贴图那一步会再缩一次（缩两次 = 变糊）。
		// 无变化时只是一次浮点比较，不影响热路径。
		if sc := g.Save.Scale; sc >= 1 {
			g.FS.vec.syncDispScale(sc, float64(g.FS.Meta.Zoom))
		}
	}
	tFrame0 := time.Now()
	frame := g.currentFrame()
	if frame == nil {
		return
	}
	tFrame1 := time.Now()
	sc := g.Save.Scale
	if sc < 1 {
		sc = 1
	}
	zoom := float64(g.FS.Meta.Zoom)
	if zoom < 1 {
		zoom = 1
	}
	// 帧自身显示尺寸（按素材原始像素等比缩到界面）
	frameW := int(float64(frame.Bounds().Dx()) * sc / zoom)
	frameH := int(float64(frame.Bounds().Dy()) * sc / zoom)
	// 矢量待机帧已在渲染后按同一公式预缩（vec.shrinkTo），缓存里存的就是
	// 「能直接贴画布」的显示图，再乘一次 sc/zoom 会缩第二次。动作/表情帧
	// 仍是每次现渲染的 4x 图，走上面的原公式。
	if g.Anim == nil && g.FS.vec != nil && g.FS.vec.prescale() {
		frameW = frame.Bounds().Dx()
		frameH = frame.Bounds().Dy()
		if vecPrescaleLogKey != g.FS.Key {
			vecPrescaleLogKey = g.FS.Key
			logf("vec prescale %s: frame=%dx%d (4x≈%.0fx%.0f)",
				g.FS.Key, frameW, frameH, float64(frameW)*zoom/sc, float64(frameH)*zoom/sc)
		}
	}
	if frameW < 1 {
		frameW = 1
	}
	if frameH < 1 {
		frameH = 1
	}
	// 统一画布（子渊 2026-10-24）：表情/动作素材按各自 sprite 的 bbox 裁切，
	// 尺寸互不相同（idle 253x270、exp/2 156x242、action/1 172x186、action/2 270x308）。
	// 用当前帧尺寸当窗口尺寸并贴底，播动作时窗口高度一帧一变，角色看着会跳到
	// NONO 上方或掉到下面。改为固定画布：帧按自己尺寸缩放、底部居中放进画布，
	// 窗口尺寸只跟形态画布走，位置就稳了。
	imgW, imgH := frameW, frameH
	// 球心统一画布：非飞行、非动作帧时用「跨形态固定」的画布尺寸，并让
	// 本形态的球心落在固定锚点 (anchorX, anchorY) 上。这样切换形态只换帧
	// 内容，窗口尺寸与球心屏幕坐标都不动（子渊 2026-10-24：
	// 「其他形态的切换也要保持切换那一瞬间的球形本体都是在一个位置」）。
	var (
		ballMode       bool
		ballAX, ballAY float64 // 球心锚点（窗口内，显示像素）
		ballCX, ballCY float64 // 当前帧球心（帧内，显示像素）
	)
	if cfg := ballAnchor(); cfg != nil && len(g.FS.Meta.BallC) == 2 {
		z1 := float64(cfg.Zoom)
		ballAX = cfg.AnchorX * sc / z1
		ballAY = cfg.AnchorY * sc / z1
		ballCX = g.FS.Meta.BallC[0] * sc / zoom
		ballCY = g.FS.Meta.BallC[1] * sc / zoom
		imgW = int(math.Round(cfg.WinW * sc / z1))
		imgH = int(math.Round(cfg.WinH * sc / z1))
		// 待机帧：画布小于帧则扩到帧（防止把本体裁掉）。
		// 动作/表情帧：窗口改由内容 bbox 决定（见下方 animW/animH），这里
		// 若按整画布抬高，远处一颗小特效就能把窗口撑到 500+ 像素。
		if g.Anim == nil {
			if imgW < frameW {
				imgW = frameW
			}
			if imgH < frameH {
				imgH = frameH
			}
		}
		ballMode = true
	}
	if !ballMode && g.FS.Meta.Canvas.W > 0 && g.FS.Meta.Canvas.H > 0 {
		if cw := int(float64(g.FS.Meta.Canvas.W) * sc / zoom); cw > imgW {
			imgW = cw
		}
		if chh := int(float64(g.FS.Meta.Canvas.H) * sc / zoom); chh > imgH {
			imgH = chh
		}
	}

	img := frame
	// 矢量路径的染色在渲染器里按层做（只乘 tintable 层，眼睛/面具保持原色），
	// 这里不能再用 tintedCopy 整帧染一遍，否则双重染色、眼睛也跟着变色。
	// PNG 路径没有层信息，只能退化成整帧染。
	// 白色也照染：tintedCopy 是按亮度加权替换，白色目标会把 225~254 的亮部
	// 轻微提亮——这个提亮是刻意保留的（子渊 2026-10-27：该提亮的保留，
	// 是为了维持观感）。默认外观走矢量后端，不经过这里，不会有额外开销。
	if g.FS.vec == nil {
		r, gg, b := rgb(g.Save.Color)
		img = tintedCopy(img, color.RGBA{r, gg, b, 255})
	}
	var flySrc *image.RGBA
	flyW, flyH := 0, 0
	// 飞行模式只把飞行器贴进画布（见下方 flySrc 分支），本体帧既不参与合成，
	// 它的全帧 Lanczos 重采样与发光脉冲就是纯白做（Wine 实测 resize 8.8~9.4ms/tick）。
	// 非飞行路径行为逐位不变：resize/glow 只在 !Fly 时执行。
	if !g.flyOn() {
		tRs0 := time.Now()
		img = g.dispFrame(img, frameW, frameH)
		profResize = time.Since(tRs0)

		if g.Save.Glow {
			img = glowPulse(img, g.TickN)
		}
	} else {
		profResize = 0
	}

	// 飞行模式（原版 NonoFlyModel.as:152 把飞行器 addChildAt 到主人 sprite，
	// 整个 NonoModel 本体不创建）：桌宠没有赛尔主人，所以飞行时只画飞行器，
	// 不叠本体。尺寸用飞行器原始像素，不做二次缩放，避免发虚。
	// flySrc / flyW / flyH 已在上方声明（本体 resize 需要提前判断 Fly）。
	if g.flyOn() {
		// 严格原版尺寸（子渊 2026-10-24 定，取代此前的 gem 归一）：
		// 四个 style 一律按各自 SWF 的原始整帧尺寸 × Save.Scale 显示，
		// 保留原版差异（1x：120×146 / 177×165 / 105×149 / 167×170）。
		// 依据：游戏里飞行器由 NonoFlyModel 整体 addChild，尺寸就是 SWF 原始
		// 整帧 × SCALE(0.8)，代码里没有任何四形态对齐逻辑。
		fh := int(float64(flyOriginalFullH(g.Save.FlyStyle)) * sc)
		if fh < 24 {
			fh = 24
		}
		// 分层合成：colorMc 按玩家色染色 + fireMc 独立 8 帧火苗 + dirMc 本体，
		// 各层按自己的段内帧数循环，尾焰不再被方向段的 7 帧拽住。
		// 矢量后端优先：FrameAt 按真实毫秒让每条子时间轴（眼睛 15 帧、
		// 光圈 18 帧）自己走，不再被 12 相位的尾焰表带偏。
		if vf := vecFly(); vf != nil {
			flySrc = vf.FrameAt(g.Save.FlyStyle, dirIndex(g.Dir), fh, g.Save.Color, g.FlyMs)
			// 调试：NONO_FLY_DUMP_ALL=<前缀> 一次性落盘前 N 个相位
			// （NONO_FLY_DUMP_N，默认 12），用于逐帧核对子时间轴。
			if !g.flySeqDumped {
				g.flySeqDumped = true
				if pre := os.Getenv("NONO_FLY_DUMP_ALL"); pre != "" {
					n := 12
					if s := os.Getenv("NONO_FLY_DUMP_N"); s != "" {
						if k, err := strconv.Atoi(s); err == nil && k > 0 {
							n = k
						}
					}
					// NONO_FLY_DUMP_DIRS=1 时 8 个方向各落一套，用于换向核对。
					if os.Getenv("NONO_FLY_DUMP_DIRS") != "" {
						for d := 1; d <= 8; d++ {
							dumpFlySeq(g.Save.FlyStyle, d, fh, g.Save.Color, pre+itoa(d)+"_", n)
						}
					} else {
						dumpFlySeq(g.Save.FlyStyle, dirIndex(g.Dir), fh, g.Save.Color, pre, n)
					}
				}
			}
		}
		if flySrc == nil {
			frameList := flyCompositeFrames(g.Save.FlyStyle, dirIndex(g.Dir), fh, g.Save.Color)
			if len(frameList) > 0 {
				flySrc = frameList[g.TickN%len(frameList)]
			}
		}
		if flySrc != nil {
			flyW, flyH = flySrc.Bounds().Dx(), flySrc.Bounds().Dy()
		}
	}
	// 气泡比角色宽时，窗口跟着变宽，角色居中，避免文字被裁
	w := imgW
	if flySrc != nil {
		w = flyW
	}
	if g.Bubble != "" && g.BubbleW+20 > w {
		w = g.BubbleW + 20
	}
	if w > 420 && !ballMode {
		// 球心模式下宽度由统一锚点决定（super4 长鳍本来就宽），不能再截断，
		// 否则鳍被裁 + 球心右侧内容缺失。
		w = 420
	}
	h := imgH + bubbleH
	if flySrc != nil {
		h = flyH + bubbleH
		g.AnimPadX, g.AnimPadY = 0, 0
	}
	// 飞行模式下本体也要画：官方 NonoFlyModel.as:151-152 先 keepUp 把本体容器
	// 抬到 y=-50（各风格不同），再把飞行器 addChildAt 进 people.sprite 第 1 层，
	// 本体始终存在、飞行器垫在它背后。这里把本体球心落到
	// 「mc 原点 - keepUp」，即官方那个挂点往上的抬升量。
	// 原点是所有方向共用的一个点（整帧裁剪框与方向无关），所以换方向时本体
	// 不会跟着飞行器轮廓滑动。
	flyX, flyY := 0, 0
	flyBodyX, flyBodyY := 0, 0
	flyHasBody := false
	if flySrc != nil {
		flyX, flyY = (w-flyW)/2, bubbleH
		// 官方飞行态只画飞行器（见 flyShowBody 常量注释）：本体整套布局一并跳过，
		// 否则窗口会按本体尺寸撑大，留出一大块永远透明的空白。
		if flyShowBody {
			if rx, ry, ok := flyRegPoint(g.Save.FlyStyle, flyH); ok {
				lift := float64(flyLiftFor(g.Save.FlyStyle)) * sc
				bx := flyX + int(math.Round(rx-ballCX))
				by := flyY + int(math.Round(ry-lift-ballCY))
				// 本体比飞行器框还靠外时把窗口撑开（不裁内容），整体移回正。
				if pad := -bx; pad > 0 {
					w += pad
					flyX += pad
					bx = 0
				}
				if pad := -by; pad > 0 {
					h += pad
					flyY += pad
					by = 0
				}
				if bx+frameW > w {
					w = bx + frameW
				}
				if by+frameH > h {
					h = by + frameH
				}
				flyBodyX, flyBodyY, flyHasBody = bx, by, true
				// 飞行时切掉本体自带的地面阴影（官方 keepUp 会把 bodyMC 里非
				// cloth/color/waist/head/decorator 的子层 alpha 清零，阴影就在其中）。
				// 按像素找孤岛，不按 Meta.Ball 半径算 —— 后者比球体实际半径小，
				// 会把球底切平（2026-10-31 子渊实拍指出）。
				flyBodyClipH = bodyShadowClipH(img)
				if logEnabled && !flyBodyLogged {
					flyBodyLogged = true
					logf("fly body style=%d reg=%.1f,%.1f lift=%.1f frame=%dx%d ballc=%.1f,%.1f fly=%d,%d %dx%d body=%d,%d w,h=%d,%d",
						g.Save.FlyStyle, rx, ry, lift, frameW, frameH, ballCX, ballCY,
						flyX, flyY, flyW, flyH, bx, by, w, h)
				}
			}
		}
	}
	// 画布内角色实际落位（帧可能比画布小，底部居中）
	offX := (w - frameW) / 2
	offY := bubbleH + (imgH - frameH)
	profSegHead = time.Since(tFrame1)
	if ballMode && flySrc == nil {
		if g.Anim == nil {
			// 待机没有外扩，必须清零：动作结束后残留的 pad 会让窗口永久偏移。
			g.AnimPadX, g.AnimPadY = 0, 0
			// 球心对齐：帧里的球心落到锚点，不再按画布底部居中。
			// 气泡把窗口撑宽时锚点不变，球心不会跟着窗口横向跑。
			offX = int(math.Round(ballAX - ballCX))
			offY = bubbleH + int(math.Round(ballAY-ballCY))
		} else if g.Anim.BodyC[0] != 0 || g.Anim.BodyC[1] != 0 {
			// 动作/表情：把本体中心对齐到待机球心锚点。各段动作的本体在
			// 自己 sprite 里位置差异很大，贴底放置会在开播瞬间掉几十像素
			// （实测 plain action_1 从 y=246 掉到 y=292）。
			// 帧图是整画布（坐标与 bodyc 同源）；窗口尺寸改由内容 bbox 决定，
			// 否则远处一颗小特效碎片就能把窗口撑到 500+ 像素宽。
			// 贴整画布，不裁到 bbox：窗口是容器、内容是组件。用 bbox 决定
			// 窗口尺寸等于让容器迁就组件，会逼出裁切（子渊明确：画布不设限、
			// 画面内容一点不裁）。透明像素本来就 WM_NCHITTEST 穿透，窗口
			// 大不挡桌面点击，所以没必要为了「视觉不暴涨」去裁。
			bx := g.Anim.BodyC[0] * sc / zoom
			by := g.Anim.BodyC[1] * sc / zoom
			// 窗口尺寸按 clip（跨帧并集）算，不按当前帧图算：帧图是单帧 bbox
			// 裁剪出来的，逐帧大小不同（实测 618→1360），用它决定窗口会让窗口
			// 每帧 resize，canvas 重建 + renderer 重建，单帧耗时飙到 367ms。
			animW := frameW
			animH := frameH
			tClip0 := time.Now()
			if cw := g.FS.animClipWH(g.Anim.Kind, g.Anim.ID); cw[0] > 0 && cw[1] > 0 {
				animW = int(math.Round(cw[0] * sc / zoom))
				animH = int(math.Round(cw[1] * sc / zoom))
			}
			profClip = time.Since(tClip0)
			offX = int(math.Round(ballAX - bx))
			offY = bubbleH + int(math.Round(ballAY-by))
			// 2026-10-25：不再用 pad 把负偏移「转成扩窗口」。
			// 窗口既然要裁到屏幕内，扩出去的部分照样会被裁掉，反而让内容落到
			// 窗口外（实测 currentFrame 直接返回 nil，画面卡在旧帧）。
			// 现在允许 offX/offY 为负，由 copyTo / 渲染范围各自做边界裁剪。
			g.AnimPadX, g.AnimPadY = 0, 0
			// 动作帧落位排查用（bbox 每帧变，日志按需开）：画大特效时本体是否
			// 被挤出窗口，靠这几个数直接判断，不用猜。
			if logEnabled && g.TickN%10 == 0 {
				if g.Anim != nil && g.Anim.AnimPlaceTick == 0 {
					g.Anim.AnimPlaceTick = g.TickN
				}
				logf("anim place kind=%d id=%d bb=%v bodyc=%v off=%d,%d anim=%dx%d img=%dx%d bubbleH=%d pad=%d,%d",
					g.Anim.Kind, g.Anim.ID, g.Anim.BBox, g.Anim.BodyC, offX, offY, animW, animH, imgW, imgH, bubbleH,
					g.AnimPadX, g.AnimPadY)
			}
			// 内容允许超出固定画布（大范围特效）：把窗口朝右/下扩到包住内容。
			if offX+animW > imgW {
				imgW = offX + animW
			}
			if need := offY + animH; need > imgH+bubbleH {
				imgH = need - bubbleH
			}
		} else {
			g.AnimPadX, g.AnimPadY = 0, 0
			// 没有本体中心标注（旧素材/PNG 后端）：固定画布内水平居中、底部对齐。
			// 画布尺寸不变是关键——窗口不会因为播一次动作就缩放、让角色跳一下。
			offX = (w - frameW) / 2
			offY = bubbleH + (imgH - frameH)
		}
	}
	// 动作/表情可能把内容扩到固定画布之外（大范围特效）。w/h 在出这些
	// 数值之前就已赋值，这里必须重新取一次，否则窗口不跟着扩，特效被裁。
	if ballMode && flySrc == nil {
		if imgW+g.AnimPadX > w {
			w = imgW + g.AnimPadX
		}
		if imgH+bubbleH+g.AnimPadY > h {
			h = imgH + bubbleH + g.AnimPadY
		}
	}
	// y0 是不含呼吸浮动的稳定窗口顶：快捷栏定位、跟随目标都基于它，
	// 否则每帧 ±6px 的浮动会让快捷栏图标抖动、跟随目标抖动（点击点不中）。
	// 减 bubbleH 是必须的：气泡占用窗口顶部空间，不减的话气泡一出现本体就
	// 整体下沉一个气泡高度，视觉上"说话时往下掉"。
	y0 := g.BaseY - bubbleH
	if flySrc != nil {
		// 整帧（本体+尾焰+阴影）底边对齐 BaseY 后，本体比原来高出一个
		// 「整帧底→本体底」的空隙，补回来才和改版前的落点一致。
		// 用实测本体 bbox 算（flyBodyInsetV2）：旧值取 dirMc 导出画布高度，
		// bodyBottom 恒超 fullH，函数永远返回 0，补偿其实从未生效。
		y0 -= flyH
		y0 += flyBodyInsetV2(g.Save.FlyStyle, flyH)
		// 4x 口径下 flyH 是 416 而不是 146（矢量按显示尺寸原生渲染）。BaseY 默认
		// 200 时 y0 = -216，窗口整个跑到屏幕上方，实机只剩尾焰圆点可见
		// （子渊 2026-10-25 飞行冒烟截图）。这里只做可见性兜底：贴住屏幕顶，
		// 不改 BaseY（改 BaseY 会让存档里的落点跟着漂）。
		if y0 < 0 {
			y0 = 0
		}
	}
	y := y0
	if bobAmp != 0 {
		y += int(math.Round(bobAmp * math.Sin(2*math.Pi*float64(g.TickN)/bobPeriod)))
	}
	// 原版 NonoFlyModel 只有 config() 里一次 keepUp(相对主人固定 y 偏移)，
	// 没有任何周期性上下浮动（NonoFlyModel.as:145-176）。旧实现这里加了
	// 6px 正弦，属于我方发明，已按严格原版删除。
	g.WinY0 = y0
	// 动作/表情窗口锁定在屏幕可见范围内（2026-10-25）。
	//
	// clip 是跨帧并集，super5/action_7 达 937x912（4x = 3746x3648）：窗口照抄
	// 会吃 49MB canvas、单帧 367ms，而其中绝大部分落在屏幕外、物理上看不见。
	// 这里把窗口裁到屏幕内，同时把「窗口屏幕原点」和「内容偏移」平移同一个量，
	// 屏幕上的像素一个都不变（裁掉的只是本来就不在屏幕上的部分）。
	// winX/winY 是本帧窗口的屏幕原点。裁剪只改它们，**绝不能回写 g.BaseX**：
	// BaseX 是「内容坐标」的基准，一旦被裁剪量反复修改就会逐帧漂移
	// （实测 offX 每帧变 → 可见区每帧变 → 渲染缓冲每帧被清空 → 每帧都 miss，
	// worker 永远只能渲出前 10 帧，画面看着在播其实一直是同一张）。
	winX, winY := g.BaseX, y
	if ballMode && flySrc == nil {
		vx, vy, vw, vh := virtualScreen()
		if vw > 0 && vh > 0 {
			// 2026-10-26：以前窗口左上角被钉死在 (BaseX, y)，屏幕左上那块
			// （BaseX=300 / y=172 以内）永远用不上。动作的 offX/offY 一旦为负
			// ——本体中心在画布靠右下、内容从窗口左/上边界外开始——那部分直接丢。
			// 实测 super5/action_7：offX=-228、offY=-1170，窗口只有 489 高，
			// 可见比例 19%（子渊反馈「动作被裁剪 90% 不显示」）。
			// 这里改为向左/上扩到屏幕边界：扩出来的量同时加到 w/h、并从
			// offX/offY 补回同样的量，屏幕上的像素位置一个都不变。
			// BaseX 绝不能回写——它是存档落点与「切形态球心不动」的基准。
			if offX < 0 && winX > vx {
				ext := min(-offX, winX-vx)
				winX -= ext
				offX += ext
				w += ext
			}
			if offY < 0 && winY > vy {
				ext := min(-offY, winY-vy)
				winY -= ext
				offY += ext
				h += ext
			}
			// 右下照旧裁到屏幕内（内容可能比屏幕大，物理上装不下）。
			if nx1 := min(winX+w, vx+vw); nx1 > winX {
				w = nx1 - winX
			}
			if ny1 := min(winY+h, vy+vh); ny1 > winY {
				h = ny1 - winY
			}
			// 残影（方案 A：画进主窗口）需要窗口留边，否则移动拖影会被窗口
			// 边界切掉一截。这里**不用**残影实际包围盒逐帧扩边——残影位置每帧
			// 都变，窗口就会每帧 resize、renderer 跟着重建（实测待机走路时窗口
			// 高度 190→328 连续增长，每帧一次 newRenderer，25fps 直接被打穿）。
			// 改成固定留 ghostPad：残影与本体的偏差有硬上限（单帧位移 ≤12px），
			// 窗口尺寸在「有残影 / 没残影」两种状态之间切换，稳定不抖。
			// 向左/上扩时同步平移 offX/offY，屏幕像素位置一个都不变。
			if ghostActiveOrPending(g) {
				if ext := min(ghostPad, winX-vx); ext > 0 {
					winX -= ext
					offX += ext
					w += ext
				}
				if ext := min(ghostPad, winY-vy); ext > 0 {
					winY -= ext
					offY += ext
					h += ext
				}
				if nx1 := min(winX+w+ghostPad, vx+vw); nx1 > winX+w {
					w = nx1 - winX
				}
				if ny1 := min(winY+h+ghostPad, vy+vh); ny1 > winY+h {
					h = ny1 - winY
				}
			}
		}
	}
	// 把窗口可见区（clip 坐标）告诉矢量后端：帧内容常常远超屏幕，渲染屏外
	// 部分是纯浪费（super5/action_7 有帧 2620x2620，只有 1036x1024 可见）。
	// offX/offY 是 clip 原点在窗口内的位置，取反就是窗口覆盖的 clip 范围。
	if ballMode && flySrc == nil && g.Anim != nil {
		tV0 := time.Now()
		g.FS.animSetVisible(g.Anim.Kind, g.Anim.ID, -offX, -offY, w, h, sc)
		profVis = time.Since(tV0)
	}
	if g.R == nil || g.CurW != w || g.CurH != h {
		// 旧逻辑：窗口变宽/变窄时左移或右移半个差值，让画面中心不动。
		// 球心模式不这么做——窗口锚点固定，右/下扩边，BaseX 保持原值
		// 才能保证「切换形态球心不动」。
		if !ballMode && g.Anim == nil && !ghostActiveOrPending(g) && g.CurW != 0 && g.CurW != w {
			g.BaseX -= (w - g.CurW) / 2
		}
		// roleScreenX 是「本体在窗口里的水平中心」：飞行时窗口宽 = 飞行器宽，
		// 用待机画布宽 imgW 算会得到负数（实测 -29），误导排查。
		roleW := imgW
		if flySrc != nil {
			roleW = flyW
		}
		logf("resize w=%d winX=%d roleScreenX=%d", w, winX, winX+(w-roleW)/2)
		if g.R != nil {
			g.R.release()
		}
		tNR0 := time.Now()
		g.R = newRenderer(w, h)
		profNewRenderer = time.Since(tNR0)
		g.CurW, g.CurH = w, h
		// 飞行 ↔ 非飞行切换时 canvas 高度差一个载具（imgW*4/5）。分层窗口的尺寸
		// 虽由 UpdateLayeredWindow 的 psize 决定，但窗口变矮时旧尺寸下方那块像素
		// 不会被擦掉，表现成 NONO 下面挂着一张飞行载具残图（子渊 2026-10-23 反馈）。
		// 先显式把窗口缩到新尺寸，再让下面的 updateLayered 填新内容。
		if g.Hwnd != 0 {
			pSetWindowPos.Call(uintptr(g.Hwnd), 0, 0, 0, uintptr(w), uintptr(h),
				SWP_NOMOVE|SWP_NOZORDER|SWP_NOACTIVATE)
		}
	}

	// Body* 是「本体内容」的屏幕矩形：窗口可能比内容大（残影留边、动作扩窗），
	// 跟随、快捷栏定位、走动边界都用这个矩形，用窗口尺寸会随留边一起抖。
	// winX+offX 不受留边影响：向左扩时 winX 减 padL、offX 同时加 padL，和不变。
	g.BodyX, g.BodyY = winX+offX, winY+offY
	g.BodyW, g.BodyH = imgW, imgH
	if flySrc != nil {
		g.BodyW, g.BodyH = flyW, flyH
	}

	tCanvas0 := time.Now()
	// canvas 复用：每帧 image.NewRGBA(1102x793) 要分配 3.5MB，
	// 25fps 就是 87MB/s，Go GC 在动作期间触发频繁、单次停顿十几毫秒，
	// 这才是「各种动作切换很卡顿」的主因（实测 total 61~80ms 里 resize 只占 15ms，
	// 其余是不可归因的 GC 停顿）。尺寸不变时复用同一块内存，只清零。
	if canvasBuf == nil || canvasBufW != w || canvasBufH != h {
		canvasBuf = image.NewRGBA(image.Rect(0, 0, w, h))
		canvasBufW, canvasBufH = w, h
	}
	canvas := canvasBuf
	clear(canvas.Pix)
	tAlloc := time.Since(tCanvas0)
	// 飞行：先画飞行器，再把本体叠在它之上。官方 NonoFlyModel.as:151-152
	// 是 keepUp 抬本体 + addChildAt(flyMachineMc, 1) 把飞行器插进本体 sprite，
	// 本体始终可见、飞行器垫背后；只画飞行器会让座舱空着（子渊 2026-10-31 指出）。
	// 尾焰/阴影已在 tools/gen_fly_composite.py 合成进整帧，这里不再单独叠
	//（旧实现把尾焰贴在画布底部，结果糊在 NONO 脸上——子渊 2026-10-24 反馈）。
	if flySrc != nil {
		if os.Getenv("NONO_FLY_NO_MACHINE") == "" {
			copyTo(canvas, flySrc, flyX, flyY)
			if os.Getenv("NONO_DEBUG_CANVAS") != "" && !flyMachineDumped {
				flyMachineDumped = true
				debugCanvas(canvas, "dbg_machine.png")
			}
		}
		if flyHasBody {
			// 必须用 blendTo（copyTo 是覆盖，会把飞行器连同透明像素一起抹掉）
			blendOver(canvas, img, flyBodyX, flyBodyY, flyBodyClipH)
			if os.Getenv("NONO_DEBUG_CANVAS") != "" && !flyBodyDumped {
				flyBodyDumped = true
				debugCanvas(img, "dbg_body.png")
				debugCanvas(canvas, "dbg_canvas.png")
				logf("fly body bounds=%v ox,oy=%d,%d", img.Bounds(), flyBodyX, flyBodyY)
			}
		}
		if debugCenterMark {
			// 调试：把快捷栏环心画成红十字，用来肉眼核对「环心是否落在飞行器中心」
			cxp, cyp := g.BallSX-winX+g.AnimPadX, g.BallSY-winY+g.AnimPadY
			for d := -9; d <= 9; d++ {
				for _, p := range [][2]int{{cxp + d, cyp}, {cxp, cyp + d}} {
					if p[0] >= 0 && p[1] >= 0 && p[0] < w && p[1] < h {
						o := canvas.PixOffset(p[0], p[1])
						canvas.Pix[o], canvas.Pix[o+1], canvas.Pix[o+2], canvas.Pix[o+3] = 255, 0, 0, 255
					}
				}
			}
		}
	} else {
		// 帧贴底（气泡在上方），水平居中。
		// 统一画布后 imgH 可能大于帧本身高度，帧要在画布内底部居中对齐，
		// 不能直接按 offX 贴——否则小尺寸动作帧会粘在画布顶上。
		// 帧图是「单帧 bbox」裁剪出来的，原点不是 clip 原点；偏移必须补回去，
		// 否则每一帧都会贴到窗口左上角（本体看起来在乱窜）。
		// 帧图是按「该帧 bbox」裁剪渲染的，原点是 bbox 左上角而不是 clip 原点；
		// 必须把偏移补回去，否则每帧都会贴到窗口左上角（本体看起来在乱窜）。
		if g.Anim != nil {
			offX += int(math.Round(g.Anim.FrameOff[0] * sc / zoom))
			offY += int(math.Round(g.Anim.FrameOff[1] * sc / zoom))
		}
		// 帧图是「素材原始倍率」渲染出来的（vec 后端恒按 scale=4 出图），
		// 而显示尺寸 frameW/frameH = 图宽 × Save.Scale / Meta.Zoom。
		// Scale 与 Zoom 不相等时（面板 scale +/- 调到 1~3.5）必须重采样，
		// 否则 4x 原图被直接拷进小画布：Scale=2 实测 235 宽的图塞进 385 宽、
		// 245 高的画布，球体只剩左上角一截露出来（2026-10-26 复现）。
		// 走 resizeFrame（预乘 Lanczos3）而不是最近邻，缩小时边缘不发抖。
		if img != nil && (frameW != img.Bounds().Dx() || frameH != img.Bounds().Dy()) {
			tR0 := time.Now()
			img = resizeFrame(img, frameW, frameH)
			profResize = time.Since(tR0)
		}
		tC0 := time.Now()
		copyTo(canvas, img, offX, offY)
		profCopy = time.Since(tC0)
	}

	if g.Save.Shield {
		// 优先贴游戏原资源 pk_nono_shield 首帧（shotEffect.swf 符号 284）；
		// 素材缺失才回退程绘椭圆，保证功能不因缺图消失。
		// 护盾按本体球径归一（素材单位与本体不同口径，直接按 zoom 缩放会小一号）。
		ballPx := float64(g.FS.Meta.Ball) * sc / zoom
		if !drawShieldSprite(canvas, float64(offX+frameW/2), float64(offY+frameH/2), ballPx, 0.5) {
			drawShield(canvas, offX, offY, frameW, frameH, g.TickN)
		}
	}

	// 残影：先把「本体帧」快照进池，再把池里的残影混进画布。
	// 顺序不能反——先混残影再快照的话，残影会被拍进下一帧的残影里，越叠越浓。
	// 画布原点是窗口真实屏幕原点（含裁剪平移），不是 BaseX：动作期间窗口会被
	// 裁到屏幕内整体位移，用 BaseX 会让残影整体错位一个裁剪量。
	g.tickGhost(canvas, winX-g.AnimPadX, winY-g.AnimPadY)
	tGhost0 := time.Now()
	g.drawGhosts(canvas, winX-g.AnimPadX, winY-g.AnimPadY)
	profGhost = time.Since(tGhost0)
	g.prevBaseX, g.prevBaseY = g.BaseX, g.BaseY

	g.CurFrame = canvas
	debugCanvas(canvas, "canvas_debug.png")
	tB0 := time.Now()
	g.R.blit(canvas)
	profBlit = time.Since(tB0)
	if g.Bubble != "" {
		g.drawBubble(g.R)
	}
	// 球心屏幕坐标（快捷栏环绕圆心、跟随都以它为准）。非球心模式（飞行、
	// 动作帧、旧素材包）清零，调用方回退旧估算。
	if ballMode && flySrc == nil {
		// 自检：ball_anchor.json 是老 PNG 口径时会明显小于素材画布（旧值
		// win_w=571 / anchor_x=271，而矢量画布 269、anchor 要 376）。用户只
		// 覆盖 exe 不覆盖 assets 时就会踩到，表现是快捷栏飞散、球心偏出画布。
		// 这里只告警不自动修（自动修需要 6 形态全量 union，代价太大）。
		// 自检：ball_anchor.json 的 schema 版本。1x 时代的文件没有 schema 字段
		// （锚点 271/窗口 571 是按 PNG 帧测的），换到 4x 矢量素材后 super4 的
		// 球心 (376) 会落到画布外——用户只覆盖 exe 不覆盖 assets 时就会踩到。
		// 只告警不自动修：正确锚点需要 6 形态全量 union，运行时算代价太大。
		if ac := ballAnchor(); anchorCheckedOnce == 0 && ac != nil && ac.Schema < 2 {
			anchorCheckedOnce = 1
			logf("⚠️ ball_anchor.json 是旧口径（schema=%d < 2，锚点 %.0f/窗口 %.0fx%.0f），4x 矢量下 super4 球心会偏出画布。请重跑 tools/gen_ball_anchor_vec.py assets --scale 4 --write",
				ac.Schema, ac.AnchorX, ac.WinW, ac.WinH)
		}
		// 必须用窗口实际的 y（动作期间会被裁到屏幕内而整体位移），
		// 用 y0 会让快捷栏环心留在原来位置——实测动作时芯片环整个跑出屏幕。
		sx := g.BaseX + int(math.Round(ballAX))
		sy := y + bubbleH + int(math.Round(ballAY))
		if sx != g.BallSX || sy != g.BallSY {
			logf("ball center form=%s screen=%d,%d base=%d,%d anchor=%.1f,%.1f win=%dx%d",
				g.FS.Key, sx, sy, g.BaseX, g.BaseY, ballAX, ballAY, w, h)
		}
		g.BallSX, g.BallSY = sx, sy
	} else if flySrc != nil {
		// 飞行没有球心锚点（ball_anchor.json 只覆盖本体 6 形态），快捷栏改用
		// 飞行器「实测 alpha 轮廓」的中心当环心：4 个 style × 8 方向的可见范围
		// 各不相同，素材声明的 body bbox 只是其中一个方向的测量值，拿它当环心
		// 会出现「这个方向还行、换个方向就偏了」（子渊 2026-10-27 三次反馈）。
		// 偏移必须带上飞行器在画布里的落位 (w-flyW)/2 —— 气泡把窗口撑宽时它不是 0。
		if box, ctr, ok := flyBodyBox(g.Save.FlyStyle, dirIndex(g.Dir), flyH, flySrc); ok {
			dw, dh := box.Dx(), box.Dy()
			g.FlyVisW, g.FlyVisH = dw, dh
			g.BallSX = winX + (w-flyW)/2 + ctr.X
			g.BallSY = winY + bubbleH + ctr.Y
			// 半径按「跨方向最大可见尺寸」走，转身时不重建窗口
			noteFlyVis(g.Save.FlyStyle, flyScreenH(g.Save.FlyStyle, sc), dirIndex(g.Dir), dw, dh)
			if lx, ly := g.BallSX, g.BallSY; lx != flyLogX || ly != flyLogY {
				flyLogX, flyLogY = lx, ly
				logf("fly center style=%d dir=%s body=%dx%d@%d,%d src=%dx%d win=%d,%d+%d,%d screen=%d,%d",
					g.Save.FlyStyle, g.Dir, dw, dh, box.Min.X, box.Min.Y, flyW, flyH, winX, winY, (w-flyW)/2, bubbleH, lx, ly)
			}
		} else {
			g.FlyVisW, g.FlyVisH = flyW, flyH
			g.BallSX = winX + w/2
			g.BallSY = winY + bubbleH + flyH/2
			if lx, ly := g.BallSX, g.BallSY; lx != flyLogX || ly != flyLogY {
				flyLogX, flyLogY = lx, ly
				logf("fly center FALLBACK src=%dx%d win=%d,%d screen=%d,%d", flyW, flyH, winX, winY, lx, ly)
			}
		}
	} else {
		g.BallSX, g.BallSY = 0, 0
	}
	// 记下窗口真实屏幕原点，供命中检测/拖动/快捷栏定位使用
	// 命中判定用的窗口原点必须与真正上传的位置一致（winX/winY 已含裁剪平移），
	// 用 g.BaseX/y 会让动作期间点击判定整体偏移裁剪量。
	g.WinX, g.WinY = winX-g.AnimPadX, winY-g.AnimPadY
	tUL0 := time.Now()
	// NONO_TRACE=1：逐帧记录窗口/内容矩形，用来定位「画面抖动」（R3）。
	// 正常不打印，避免日志爆量。
	if os.Getenv("NONO_TRACE") != "" {
		logf("trace win=%d,%d wh=%dx%d off=%d,%d fw=%dx%d body=%dx%d@%d,%d frame=%d dir=%s",
			winX, winY, w, h, offX, offY, frameW, frameH, g.BodyW, g.BodyH, g.BodyX, g.BodyY, g.Frame, g.Dir)
	}
	updateLayered(g.Hwnd, g.R.Hdc, winX-g.AnimPadX, winY-g.AnimPadY, w, h)
	if profEnabled && g.TickN%12 == 0 {
		logf("PROF updateLayered=%.1fms blit+draw=%.1fms alloc=%.1f resize=%.1f copy=%.1f blit=%.1f",
			float64(time.Since(tUL0))/1e6, float64(tUL0.Sub(tCanvas0))/1e6,
			float64(tAlloc)/1e6, float64(profResize)/1e6, float64(profCopy)/1e6, float64(profBlit)/1e6)
		logf("PROF2 clip=%.1f vis=%.1f head=%.1f ghost=%.1f", float64(profClip)/1e6, float64(profVis)/1e6, float64(profSegHead)/1e6, float64(profGhost)/1e6)
		logf("PROF3 newRenderer=%.1fms", float64(profNewRenderer)/1e6)
	}
	if profEnabled && profTick0.IsZero() {
		profTick0 = tDraw0
		profTickN = g.TickN
	}
	if profEnabled && !profTick0.IsZero() && g.TickN-profTickN >= 48 {
		dt := time.Since(profTick0).Seconds()
		logf("PROF tick: %d 个 tick 用了 %.3fs → 实际 %.1f fps（interval %.1fms）",
			g.TickN-profTickN, dt, float64(g.TickN-profTickN)/dt, dt*1000/float64(g.TickN-profTickN))
		profTick0 = time.Now()
		profTickN = g.TickN
	}
	if profEnabled && g.TickN%12 == 0 {
		logf("PROF draw=%.1fms frame=%.1fms canvas=%.1fms total=%.1fms w=%d h=%d anim=%v",
			float64(tFrame0.Sub(tDraw0))/1e6, float64(tFrame1.Sub(tFrame0))/1e6,
			float64(tCanvas0.Sub(tFrame1))/1e6, float64(time.Since(tDraw0))/1e6, w, h, g.Anim != nil)
	}
	// 快捷栏跟随：放在这里才能用当前帧的 WinY0/CurW（见 tick 里的说明）
	updateShortcutPos()
}

// flyOffset 返回窗口顶相对 BaseY 的上移量（0 表示非飞行）。
// 直接读上一帧 draw 记下的真实原点，避免各处重复推导载具/抬升量。
// flyOn 是「这一帧是否按飞行器渲染」的唯一判据。
// 除了存档开关，还要看会话级抑制位 FlyOff：关机要播本体关机动画（20261029-01），
// 飞行器画面会把它整个盖掉。不改 Save.Fly，开机后自动恢复用户原来的飞行设置。
func (g *Game) flyOn() bool {
	return g != nil && g.Save != nil && g.Save.Fly && !g.FlyOff
}

// flyScale 返回飞行器显示缩放，与 draw() 里的 sc 同口径（Save.Scale，下限 1）。
// 飞行器几何（reg / 机器口高度 / 整帧高）全部要按这个缩放换算成屏幕像素。
func (g *Game) flyScale() float64 {
	if g == nil || g.Save == nil || g.Save.Scale < 1 {
		return 1
	}
	return g.Save.Scale
}

func (g *Game) flyOffset() int {
	if g == nil {
		return 0
	}
	return g.BaseY - g.WinY0
}

// copyTo 把 src 贴到 dst 的 (ox,oy)，超出目标边界的部分直接裁掉。
//
// 为什么不用 SetRGBA 逐像素（2026-10-25 改）：一帧 1036x1024 就是 100 万次
// 方法调用，实测吃掉 20~50ms，直接把 24fps 预算耗尽。这里是纯覆盖语义
// （不混合 alpha），所以可以按行 copy，走 memmove。
// blendOver 把 src 按非预乘 alpha 的 Over 真正叠到 dst 上（越界自动裁）。
//
// 不能用 ghost.go 的 blendTo：那个是给残影用的，里面有一条「只往完全透明的
// 空白处画，本体占了的像素一律不动」的保护，叠飞行器时会把本体的白球整块
// 跳过（2026-10-31 实测：本体只剩两只耳朵露在飞行器轮廓外）。
// 公式与 nonovec/pkg/raster.OverNonPremul 一致：画布全程非预乘，blit 时才预乘。
// maxH > 0 时只叠前 maxH 行（飞行时用来切掉本体自带的地面阴影）。
func blendOver(dst, src *image.RGBA, ox, oy int, maxH int) {
	sb, db := src.Bounds(), dst.Bounds()
	yEnd := sb.Max.Y
	if maxH > 0 && sb.Min.Y+maxH < yEnd {
		yEnd = sb.Min.Y + maxH
	}
	for y := sb.Min.Y; y < yEnd; y++ {
		dy := oy + (y - sb.Min.Y)
		if dy < db.Min.Y || dy >= db.Max.Y {
			continue
		}
		for x := sb.Min.X; x < sb.Max.X; x++ {
			dx := ox + (x - sb.Min.X)
			if dx < db.Min.X || dx >= db.Max.X {
				continue
			}
			si := src.PixOffset(x, y)
			sa := uint32(src.Pix[si+3])
			if sa == 0 {
				continue
			}
			di := dst.PixOffset(dx, dy)
			da := uint32(dst.Pix[di+3])
			if da == 0 {
				dst.Pix[di], dst.Pix[di+1], dst.Pix[di+2], dst.Pix[di+3] =
					src.Pix[si], src.Pix[si+1], src.Pix[si+2], src.Pix[si+3]
				continue
			}
			outA := sa + da*(255-sa)/255
			if outA == 0 {
				continue
			}
			for k := 0; k < 3; k++ {
				s := uint32(src.Pix[si+k])
				d := uint32(dst.Pix[di+k])
				v := (s*sa*255 + d*da*(255-sa)) / (outA * 255)
				if v > 255 {
					v = 255
				}
				dst.Pix[di+k] = uint8(v)
			}
			dst.Pix[di+3] = uint8(outA)
		}
	}
}

// bodyShadowClipH 返回本体帧在飞行时要叠到第几行（返回 0 = 不裁）。
//
// 官方 keepUp 会把 bodyMC 里非 cloth/color/waist/head/decorator 的子层
// alpha 清零，本体自带的地面阴影属于这一类。矢量 sprite 只有
// body/color_1/color_2 三层，没法按层名过滤，所以按像素判：
// 帧最底下那块「与主体之间有透明空隙、纯黑、半透明、高度不超过整帧 1/4」
// 的孤岛就是阴影，在它上面截断。
//
// 判据宁严勿宽：任何一条不满足就返回 0（不裁）。因为裁错会切进球体本身
// （2026-10-31 用 Meta.Ball 当半径，球底被切平，子渊实拍指出）。
func bodyShadowClipH(img *image.RGBA) int {
	sb := img.Bounds()
	h := sb.Dy()
	if h <= 0 {
		return 0
	}
	has := make([]bool, h)
	for y := 0; y < h; y++ {
		row := sb.Min.Y + y
		for x := sb.Min.X; x < sb.Max.X; x++ {
			if img.Pix[img.PixOffset(x, row)+3] > 8 {
				has[y] = true
				break
			}
		}
	}
	end := -1
	for y := h - 1; y >= 0; y-- {
		if has[y] {
			end = y
			break
		}
	}
	if end < 0 {
		return 0
	}
	start := end
	for start > 0 && has[start-1] {
		start--
	}
	// 孤岛上方必须还有主体
	mainEnd := start - 1
	for mainEnd >= 0 && !has[mainEnd] {
		mainEnd--
	}
	if mainEnd < 0 {
		return 0
	}
	if end-start+1 > h/4 {
		return 0
	}
	// 孤岛必须是纯黑且带半透明
	dark, semi := true, false
	for y := start; y <= end && dark; y++ {
		row := sb.Min.Y + y
		for x := sb.Min.X; x < sb.Max.X; x++ {
			i := img.PixOffset(x, row)
			a := img.Pix[i+3]
			if a <= 8 {
				continue
			}
			if img.Pix[i] > 24 || img.Pix[i+1] > 24 || img.Pix[i+2] > 24 {
				dark = false
				break
			}
			if a < 255 {
				semi = true
			}
		}
	}
	if !dark || !semi {
		return 0
	}
	return start
}

func copyTo(dst, src *image.RGBA, ox, oy int) {
	db, sb := dst.Bounds(), src.Bounds()
	x0, x1 := max(ox, db.Min.X), min(ox+sb.Dx(), db.Max.X)
	y0, y1 := max(oy, db.Min.Y), min(oy+sb.Dy(), db.Max.Y)
	if x1 <= x0 || y1 <= y0 {
		return
	}
	n := (x1 - x0) * 4
	for y := y0; y < y1; y++ {
		si := src.PixOffset(sb.Min.X+(x0-ox), sb.Min.Y+(y-oy))
		di := dst.PixOffset(x0, y)
		copy(dst.Pix[di:di+n], src.Pix[si:si+n])
	}
}

// dispFrame 返回目标尺寸的待机帧，命中缓存时省掉整次重采样。
//
// 背景见 Game.dispCache：超能形态 4x 帧（≈1093×936）一次 areaScaleMulti 要
// 17~31ms，而待机是 51 帧循环，每帧每 2 秒都会再出现一次 —— 缓存后只有第一圈
// 付出重采样成本。动作/表情帧不复用（每帧只播一次，缓存等于纯占内存），
// 尺寸已经相等时直接返回（nothing to do）。
//
// 返回值可能是缓存里的图，调用方**只读**，不得原地修改（后续 glowPulse 会
// 另分配新图，不会写回这里）。
func (g *Game) dispFrame(src *image.RGBA, w, h int) *image.RGBA {
	if src == nil {
		return src
	}
	if b := src.Bounds(); b.Dx() == w && b.Dy() == h {
		return src
	}
	if g.Anim != nil || g.FS == nil {
		return resizeFrame(src, w, h)
	}
	if g.dispCache == nil {
		g.dispCache = make(map[dispCacheKey]*image.RGBA, 64)
	}
	key := dispCacheKey{form: g.FS.Key, dir: g.Dir, frame: g.IdleShown, w: w, h: h, color: g.Save.Color}
	if im, ok := g.dispCache[key]; ok {
		// 自校验（NONO_DISPCACHE_CHECK=1）：重算一遍与缓存逐字节比。
		// resizeFrame 是纯函数，所以相等就证明「缓存键 → 像素」的映射没错；
		// 不等说明键少囊括了某个影响描画的输入（例如补帧回退换过帧）。
		if dispCacheCheck {
			fresh := resizeFrame(src, w, h)
			if !rgbaSame(fresh, im) {
				logf("dispcache MISMATCH %+v", key)
				g.dispCache[key] = fresh
				return fresh
			}
		}
		return im
	}
	im := resizeFrame(src, w, h)
	// 上限 192 条（super4 每帧 ≈204KB → 最坏 39MB）：走动会来回换方向，
	// 8 方向全留要 83MB，超限整表丢弃比逐条 LRU 简单且够用。
	if len(g.dispCache) >= 192 {
		g.dispCache = make(map[dispCacheKey]*image.RGBA, 64)
	}
	g.dispCache[key] = im
	return im
}

// dispCacheCheck 打开显示帧缓存的自校验（NONO_DISPCACHE_CHECK=1），只在排错时开。
var dispCacheCheck = os.Getenv("NONO_DISPCACHE_CHECK") != ""

// rgbaSame 判断两张同尺寸图是否逐字节相同。
func rgbaSame(a, b *image.RGBA) bool {
	if a == nil || b == nil || a.Rect != b.Rect {
		return false
	}
	ab, bb := a.Bounds(), b.Bounds()
	for y := 0; y < ab.Dy(); y++ {
		ar := a.Pix[y*a.Stride : y*a.Stride+ab.Dx()*4]
		br := b.Pix[y*b.Stride : y*b.Stride+bb.Dx()*4]
		for i := range ar {
			if ar[i] != br[i] {
				return false
			}
		}
	}
	return true
}

// glowPulse 让整体亮度随 tick 呼吸，用于超能炫光芯片。
func glowPulse(src *image.RGBA, tick int) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	// 用 0..256 整数查表代替逐像素浮点乘法（旧版每帧 100 万次 float 转换）。
	k := 0.75 + 0.25*math.Sin(float64(tick)/5)
	ki := int(k * 256)
	if ki < 0 {
		ki = 0
	}
	if ki > 256 {
		ki = 256
	}
	var lut [256]uint8
	for i := 0; i < 256; i++ {
		v := i * ki / 256
		if v > 255 {
			v = 255
		}
		lut[i] = uint8(v)
	}
	copy(dst.Pix, src.Pix)
	for i := 0; i < len(dst.Pix); i += 4 {
		if dst.Pix[i+3] == 0 {
			continue
		}
		dst.Pix[i] = lut[dst.Pix[i]]
		dst.Pix[i+1] = lut[dst.Pix[i+1]]
		dst.Pix[i+2] = lut[dst.Pix[i+2]]
	}
	return dst
}

// drawShield 是护盾的**回退**画法：assets/sprites/shield 缺失时才走这里，
// 画一个青色椭圆环带，保证功能不因缺图消失。正常路径是 shield.go 的
// drawShieldSprite —— 贴游戏原资源 pk_nono_shield 的首帧。
//
// 2026-10-25 重写：旧版每 tick 遍历整个窗口（1036x1024 约 100 万像素）做浮点
// 除法 + SetRGBA，实测吃掉 20~40ms，是掉到 20fps 的主因。现在只扫环带的外接
// 矩形、用整数比较代替除法（(dx/ry)^2+(dy/rx)^2 与阈值同乘 rx^2*ry^2）、
// 直接写 Pix，工作量降到原来的几十分之一。
func drawShield(canvas *image.RGBA, offX, top, w, h, tick int) {
	b := canvas.Bounds()
	cx := offX + w/2
	cy := top + h/2
	rx := float64(w) * 0.62
	ry := float64(h) * 0.55
	if rx < 1 || ry < 1 {
		return
	}
	a := 90 + int(40*math.Sin(float64(tick)/6))
	if a > 160 {
		a = 160
	}
	rx2 := rx * rx
	ry2 := ry * ry
	// 外接矩形（环带不会超出 ±(rx,ry)）
	x0 := max(b.Min.X, int(float64(cx)-rx)-1)
	x1 := min(b.Max.X, int(float64(cx)+rx)+2)
	y0 := max(b.Min.Y, int(float64(cy)-ry)-1)
	y1 := min(b.Max.Y, int(float64(cy)+ry)+2)
	if x1 <= x0 || y1 <= y0 {
		return
	}
	// 阈值统一乘 rx2*ry2，全部走整数比较：d = dx^2*ry2 + dy^2*rx2
	const loNum, hiNum = 82, 100 // 0.82 / 1.0 的百分比
	for y := y0; y < y1; y++ {
		dy := int64(y - cy)
		dy2 := dy * dy * int64(rx2)
		row := canvas.Pix[y*canvas.Stride:]
		for x := x0; x < x1; x++ {
			dx := int64(x - cx)
			d := dx*dx*int64(ry2) + dy2
			lim := int64(rx2 * ry2)
			if d > lim*loNum/100 && d < lim*hiNum/100 {
				i := (x - b.Min.X) * 4
				row[i] = 80
				row[i+1] = 220
				row[i+2] = 255
				row[i+3] = uint8(a)
			}
		}
	}
}

func measureText(hdc HDC, text string) int {
	wt, _ := syscall.UTF16FromString(text)
	var sz SIZE
	pGetTextExtentPoint32W.Call(uintptr(hdc), uintptr(unsafe.Pointer(&wt[0])),
		uintptr(len(wt)-1), uintptr(unsafe.Pointer(&sz)))
	return int(sz.CX)
}

// ---- 气泡样式 C：圆角 + 底部尾巴 + 竖直渐变 + 1px 描边 ----
//
// 子渊 2026-10-31 选定（按 2 倍放大的预览图为准）。几何与配色全部集中在这里，
// 以后调样式只动这几个数，不要散到绘制代码里。
const (
	bubblePadX  = 14 // 文字与气泡左右边距
	bubblePadY  = 6  // 文字与气泡上下边距
	bubbleLineH = 20 // 行高
	bubbleFont  = 16 // 字号（雅黑）
	bubbleRad   = 10 // 圆角半径
	bubbleTailW = 9  // 尾巴底宽
	bubbleTailH = 7  // 尾巴高
	bubbleMinW  = 84 // 最短气泡宽度，避免「太好了」缩成一颗小球
	bubbleSS    = 4  // 形状超采样倍数（4x4=16 级边缘灰度）
	// bubbleTextCol 是 COLORREF（0x00BBGGRR），对应 RGB(24,58,104)
	bubbleTextCol = 0x00683A18
)

var (
	bubbleFillTop = [3]float64{246, 250, 255}
	bubbleFillBot = [3]float64{214, 232, 255}
	bubbleBorder  = [3]float64{160, 194, 238}

	// 量文字宽用的内存 DC 与字体，首次 textW 时懒建（主线程）。
	measDC   HDC
	measFont uintptr
	measOld  uintptr
)

// bubbleBodyTop 圆角主体的顶边（1x 像素，相对窗口顶部）。
// 单行/双行都贴 band 底部：尾巴永远紧接本体头顶，气泡不会随行数上浮。
func bubbleBodyTop(lines int) int {
	bodyH := lines*bubbleLineH + bubblePadY*2
	top := bubbleH - bubbleTailH - bodyH
	if top < 0 {
		top = 0
	}
	return top
}

// bubbleGrad 气泡竖直渐变取色（尾巴区域沿用底色）。
func bubbleGrad(y, top, bodyH float64) [3]float64 {
	t := (y - top) / bodyH
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return [3]float64{
		bubbleFillTop[0] + (bubbleFillBot[0]-bubbleFillTop[0])*t,
		bubbleFillTop[1] + (bubbleFillBot[1]-bubbleFillTop[1])*t,
		bubbleFillTop[2] + (bubbleFillBot[2]-bubbleFillTop[2])*t,
	}
}

// distSeg 点到线段的距离。
func distSeg(px, py, ax, ay, bx, by float64) float64 {
	vx, vy := bx-ax, by-ay
	wx, wy := px-ax, py-ay
	l2 := vx*vx + vy*vy
	t := 0.0
	if l2 > 0 {
		t = (wx*vx + wy*vy) / l2
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
	}
	return math.Hypot(wx-t*vx, wy-t*vy)
}

// triSDF 点相对三角形的有符号距离（内部为负）。三角形顶点顺序任意，
// 三条边叉积同号即内部。
func triSDF(px, py, ax, ay, bx, by, cx, cy float64) float64 {
	d := math.Min(distSeg(px, py, ax, ay, bx, by),
		math.Min(distSeg(px, py, bx, by, cx, cy), distSeg(px, py, cx, cy, ax, ay)))
	c1 := (bx-ax)*(py-ay) - (by-ay)*(px-ax)
	c2 := (cx-bx)*(py-by) - (cy-by)*(px-bx)
	c3 := (ax-cx)*(py-cy) - (ay-cy)*(px-cx)
	if (c1 >= 0 && c2 >= 0 && c3 >= 0) || (c1 <= 0 && c2 <= 0 && c3 <= 0) {
		return -d
	}
	return d
}

// bubbleSDF 有符号距离（<0 在形状内）：圆角矩形 ∪ 底部等腰三角尾巴。
func bubbleSDF(x, y, bw, top, bodyH float64) float64 {
	// 圆角矩形占满 [0, bw-1] × [top, top+bodyH-1] 的整数像素范围：最外一圈
	// 像素中心因此落在 d∈(-1,0]，1px 描边才是实心一圈。若把边界放在半像素上，
	// 描边会被抗锯齿摊成半透明，看着像没有边框。
	cx := (bw - 1) / 2
	cy := top + (bodyH-1)/2
	hw := (bw - 1) / 2
	hh := (bodyH - 1) / 2
	dx := math.Abs(x-cx) - (hw - bubbleRad)
	dy := math.Abs(y-cy) - (hh - bubbleRad)
	dRect := math.Hypot(math.Max(dx, 0), math.Max(dy, 0)) +
		math.Min(math.Max(dx, dy), 0) - bubbleRad
	// 尾巴：底边压在 body 底边上，顶点朝下
	by := top + bodyH - 1
	dTri := triSDF(x, y, cx-bubbleTailW/2, by, cx+bubbleTailW/2, by, cx, by+bubbleTailH)
	return math.Min(dRect, dTri)
}

// bubbleShape 生成气泡形状位图（渐变填充 + 1px 描边，不含文字）。
// bubbleSS 倍超采样后按覆盖率降采样得到抗锯齿边缘；结果缓存在 g.BubbleShape，
// setBubble 时置空重建——40ms 的 tick 里不能每帧重算 SDF。
func (g *Game) bubbleShape() *image.RGBA {
	if g.BubbleShape != nil {
		return g.BubbleShape
	}
	lines := g.BubbleLines
	if len(lines) == 0 {
		return nil
	}
	bw := bubbleTextW(lines)
	if bw < bubbleMinW {
		bw = bubbleMinW
	}
	top := float64(bubbleBodyTop(len(lines)))
	bodyH := float64(len(lines)*bubbleLineH + bubblePadY*2)
	if top+bodyH > float64(bubbleH-bubbleTailH) {
		bodyH = float64(bubbleH-bubbleTailH) - top
	}
	img := image.NewRGBA(image.Rect(0, 0, bw, bubbleH))
	ns := float64(bubbleSS * bubbleSS)
	fbw := float64(bw)
	for py := 0; py < bubbleH; py++ {
		for px := 0; px < bw; px++ {
			cov := 0
			var rs, gs, bs float64
			for sy := 0; sy < bubbleSS; sy++ {
				fy := float64(py) + (float64(sy)+0.5)/bubbleSS
				for sx := 0; sx < bubbleSS; sx++ {
					fx := float64(px) + (float64(sx)+0.5)/bubbleSS
					d := bubbleSDF(fx, fy, fbw, top, bodyH)
					if d > 0 {
						continue
					}
					c := bubbleGrad(fy, top, bodyH)
					if d > -1 {
						c = bubbleBorder // 距边缘 1px 以内算描边
					}
					rs += c[0]
					gs += c[1]
					bs += c[2]
					cov++
				}
			}
			if cov == 0 {
				continue
			}
			o := img.PixOffset(px, py)
			img.Pix[o] = uint8(rs/float64(cov) + 0.5)
			img.Pix[o+1] = uint8(gs/float64(cov) + 0.5)
			img.Pix[o+2] = uint8(bs/float64(cov) + 0.5)
			img.Pix[o+3] = uint8(float64(cov)*255/ns + 0.5)
		}
	}
	g.BubbleShape = img
	return img
}

// drawBubble 把气泡形状合成到 DIB 顶部，再用 GDI 画折好的各行文字。
// DIB 是 BGRA + 预乘 alpha，形状位图是普通 RGBA，合成按 over 预乘写入。
func (g *Game) drawBubble(r *Renderer) {
	shape := g.bubbleShape()
	if shape == nil || r.Bits == nil || r.Hdc == 0 {
		return
	}
	bw, bh := shape.Bounds().Dx(), shape.Bounds().Dy()
	x0 := (r.W - bw) / 2
	if x0 < 0 {
		x0 = 0
	}
	y0 := bubbleH - bh // 形状高度就是 band 高度，正常为 0
	if y0 < 0 {
		y0 = 0
	}
	dst := unsafe.Slice((*byte)(r.Bits), r.W*r.H*4)
	for y := 0; y < bh; y++ {
		dy := y0 + y
		if dy >= r.H {
			break
		}
		for x := 0; x < bw; x++ {
			dx := x0 + x
			if dx >= r.W {
				break
			}
			sp := shape.Pix[shape.PixOffset(x, y):]
			a := uint32(sp[3])
			if a == 0 {
				continue
			}
			di := (dy*r.W + dx) * 4
			if a == 255 {
				dst[di], dst[di+1], dst[di+2], dst[di+3] = sp[2], sp[1], sp[0], 255
				continue
			}
			inv := 255 - a
			dst[di] = uint8(uint32(sp[2])*a/255 + uint32(dst[di])*inv/255)
			dst[di+1] = uint8(uint32(sp[1])*a/255 + uint32(dst[di+1])*inv/255)
			dst[di+2] = uint8(uint32(sp[0])*a/255 + uint32(dst[di+2])*inv/255)
			dst[di+3] = uint8(a + uint32(dst[di+3])*inv/255)
		}
	}

	font, _, _ := pCreateFontW.Call(negHeight(bubbleFont), 0, 0, 0, FW_NORMAL, 0, 0, 0,
		DEFAULT_CHARSET, 0, 0, 0, 0, uintptr(unsafe.Pointer(utf16ptr("Microsoft YaHei"))))
	old, _, _ := pSelectObject.Call(uintptr(r.Hdc), font)
	pSetBkMode.Call(uintptr(r.Hdc), TRANSPARENT)
	pSetTextColor.Call(uintptr(r.Hdc), bubbleTextCol)
	top := bubbleBodyTop(len(g.BubbleLines))
	for i, line := range g.BubbleLines {
		tw := measureText(r.Hdc, line)
		tx := (r.W - tw) / 2
		if tx < 4 {
			tx = 4
		}
		ty := y0 + top + bubblePadY + i*bubbleLineH + (bubbleLineH-bubbleFont)/2
		wtext, _ := syscall.UTF16FromString(line)
		pTextOutW.Call(uintptr(r.Hdc), uintptr(tx), uintptr(ty),
			uintptr(unsafe.Pointer(&wtext[0])), uintptr(len(wtext)-1))
	}
	pSelectObject.Call(uintptr(r.Hdc), old)
	pDeleteObject.Call(font)

	// GDI 不写 alpha：把气泡主体内部（避开圆角与尾巴）的 alpha 补成 255，
	// 否则文字和底色在 UpdateLayeredWindow 里会被当成全透明。
	// 只补 [x0+rad+1, x0+bw-rad-1]：圆角外侧的像素本就不该不透明。
	// 文字左右有 14px 内边距 > 半径 10，所以字形一定落在这个内框里。
	bodyBottom := y0 + top + len(g.BubbleLines)*bubbleLineH + bubblePadY*2
	for y := y0 + top + 2; y < bodyBottom-1 && y < r.H; y++ {
		for x := x0 + bubbleRad + 1; x < x0+bw-bubbleRad-1 && x < r.W; x++ {
			dst[(y*r.W+x)*4+3] = 255
		}
	}
}

// ---- 窗口 ----

var mmLogged int

func wndProc(hwnd HWND, msg uint32, wp, lp uintptr) uintptr {
	// 资源管理器重启后通知区域会清空，需要重新挂图标
	if trayCreatedMsg != 0 && uintptr(msg) == trayCreatedMsg {
		if g != nil {
			g.trayAdd()
		}
		return 0
	}
	switch msg {
	case WM_NCHITTEST:
		sx := GET_X_LPARAM(lp)
		sy := GET_Y_LPARAM(lp)
		// 快捷栏画在本体窗口之上的透明区，本体若返回 HTTRANSPARENT 想"穿透"
		// 给快捷栏，Wine 不会做链式穿透，点击直接掉到桌面 —— 表现为快捷栏
		// 看得见、悬停正常、就是点不动（子渊 2026-10-23）。
		// 这里本体自己接住这些点，再转发给快捷栏窗口处理。
		if g != nil && shortcutWin != nil && shortcutWin.Visible && shortcutWin.Contains(int(sx), int(sy)) {
			return HTCLIENT
		}
		if g != nil && g.TransparentHits {
			pt := POINT{int32(sx), int32(sy)}
			// 必须用窗口真实原点 WinX/WinY：飞行模式下窗口比 BaseY 高出一截，
			// 用 BaseY 会让命中区整体下移，点在 NONO 身上也判成透明而穿透（点不动）。
			cx := int(pt.X) - g.WinX
			cy := int(pt.Y) - g.WinY
			if g.alphaAt(cx, cy) == 0 {
				return ^uintptr(0) // HTTRANSPARENT (-1)
			}
		}
		return HTCLIENT

case WM_LBUTTONDOWN:
		// 2026-11-02 E-08：删掉每点必打的 logf("LBUTTONDOWN")——纯噪音，
		// 点击结果（面板弹出/拖动）本身可见，日志留给下方的分支级日志。
		// 每轮按下先清掉上一轮登记的快捷栏动作：Wine 下 LBUTTONUP 偶尔丢失，
		// 残留的 PendingAction 会在下一次「按下-抬起且没拖动」时被当成新点击执行，
		// 表现成点了别处却弹出一个功能面板（子渊 2026-10-24 反馈）。
		g.PendingAction = ""
		// 快捷栏是独立分层窗口，Wine 下命中后仍会把点击发给本体；
		// 这里先按屏幕坐标判定是否落在快捷栏按钮上，命中就直接执行，避免误触发拖动。
		if shortcutWin != nil && shortcutWin.Visible {
			var pt POINT
			pt.X, pt.Y = int32(GET_X_LPARAM(lp)), int32(GET_Y_LPARAM(lp))
			pClientToScreen.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pt)))
			cx, cy := int(pt.X)-shortcutWin.X, int(pt.Y)-shortcutWin.Y
			if idx := shortcutWin.hitAt(cx, cy); idx >= 0 && idx < len(shortcutWin.Btns) {
				// 只登记，等抬起时若没拖动才触发：否则拖 NONO 会误弹主面板
				g.PendingAction = shortcutWin.Btns[idx].Action
				logf("shortcut pending %s", g.PendingAction)
			}
		}
		g.Down = true
		g.Moved = false
		// 按下即收起快捷栏：拖动时图标不能钉在原地
		hideShortcut()
		// 拖动基准取光标屏幕坐标 + 当前 Base：移动时只叠加位移量，不反算窗口
		// 内偏移（那个偏移在定格/飞行/大 clip 动作下每帧都不同，反算必偏）。
		pt0 := POINT{}
		pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt0)))
		g.DragScrX, g.DragScrY = int(pt0.X), int(pt0.Y)
		g.DragBaseX, g.DragBaseY = g.BaseX, g.BaseY
		pSetCapture.Call(uintptr(hwnd))
		return 0

	case WM_MOUSEMOVE:
		// 游戏内 ROLL_OVER：鼠标移到 NONO 身上弹出快捷栏
		// 关机态不弹（20261029-01）：关机动画期间没有可用的快捷功能，弹出来
		// 也只会僵在原地（tick 在动作分支提前 return，快捷栏不跟随）。
		if g != nil && !g.Down && !anyPanelVisible() && g.Save.Open {
			if mmLogged < 3 {
				mmLogged++
				logf("mm %d,%d", GET_X_LPARAM(lp), GET_Y_LPARAM(lp))
			}
			pt := POINT{}
			pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			if g.alphaAt(int(pt.X)-g.WinX, int(pt.Y)-g.WinY) > 8 {
				g.showShortcut()
			}
		}
		if g.Down {
			pt := POINT{}
			pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			dx := int(pt.X) - g.DragScrX
			dy := int(pt.Y) - g.DragScrY
			if absInt(dx) > 3 || absInt(dy) > 3 {
				g.Moved = true
			}
			if g.Moved {
				// 纯位移叠加：本体与光标严格同步，窗口内落位怎么变都不影响
				// 拖动落点（飞行高度、气泡、动作 clip 都不需要再补偿）。
				g.BaseX = g.DragBaseX + dx
				g.BaseY = g.DragBaseY + dy
				g.draw()
			}
		}
		return 0

	case WM_LBUTTONUP:
		if g.Down {
			g.Down = false
			pReleaseCapture.Call()
			if g.Moved {
				// 拖动即换家：活动区中心跟着本体走。否则落地后第一个 tick
				// 还被旧活动区 clamp 拉回原处（子渊 2026-10-27：「拖到其他
				// 位置也会回去刚刚那个位置」）。
				g.WalkHomeX, g.WalkHomeY = g.BaseX, g.BaseY
				g.WalkHomeSet = true
				// 2026-11-02 E-02：拖动结束立即落盘（强杀/崩溃不再丢位置）。
				g.savePosThrottled()
			}
			if !g.Moved {
				if g.PendingAction != "" && shortcutWin != nil && shortcutWin.OnAction != nil {
					logf("shortcut fired %s", g.PendingAction)
					act := g.PendingAction
					g.PendingAction = ""
					shortcutWin.OnAction(act)
				} else {
					g.onClick()
				}
			}
			g.PendingAction = ""
		}
		return 0

	case 0x0203: // WM_LBUTTONDBLCLK
		g.onClickEgg()
		return 0

case WM_RBUTTONUP:
		// 2026-11-02 E-08：删掉每次右键都打的 RBUTTONUP 噪音日志
		// （右键结果=菜单弹出本身可见）。
		g.showMenu()
		return 0

	case WM_APP + 2:
		showInfoPanel()
		return 0
	case WM_APP + 3:
		g.useChip(700301)
		return 0
	case WM_APP + 36:
		g.showLabPanel()
		return 0
	case WM_APP + 4:
		g.useChip(700308)
		return 0
	case WM_APP + 5:
		g.resetAppearance()
		return 0
	case WM_APP + 6:
		g.useChip(700012)
		return 0
	case WM_APP + 7:
		g.useChip(700017)
		return 0
	case WM_APP + 8:
		pShowWindow.Call(uintptr(panel.Hwnd), SW_HIDE)
		return 0
	case WM_APP + 10:
		snapshotSettingsPanel("panel_set.bmp")
		return 0
	case WM_APP + 19:
		selftestStatic++
		saveRGBA(g.CurFrame, "static_"+itoa(selftestStatic)+".bmp")
		return 0
	case WM_APP + 17:
		g.setBubble("普通形态缺少动作 5，已随机播放一个表情给你看")
		g.draw()
		if g.R != nil {
			saveBMP(g.R, "shot_bubble_long.bmp")
		}
		return 0
	case WM_APP + 15:
		setAutoStart(true)
		logf("autostart on=%v", autoStartEnabled())
		return 0
	case WM_APP + 16:
		setAutoStart(false)
		logf("autostart off=%v", autoStartEnabled())
		return 0
	case WM_APP + 11:
		saveRGBA(g.CurFrame, "shot_pink.bmp")
		return 0
	case WM_APP + 12:
		saveRGBA(g.CurFrame, "shot_black.bmp")
		return 0
	case WM_APP + 13:
		saveRGBA(g.CurFrame, "shot_reset.bmp")
		return 0
	case WM_APP + 14:
		saveRGBA(g.CurFrame, "shot_shield.bmp")
		return 0
	case WM_APP + 30:
		g.showMainPanel()
		return 0
	case WM_APP + 31:
		g.showShortcut()
		return 0
	case WM_APP + 32:
		showInfoPanel()
		return 0
	case WM_APP + 33:
		showStoragePanel()
		return 0
	case WM_APP + 34:
		showChipPanel()
		return 0
	case WM_APP + 35:
		// 自测入口：模拟托盘「开机 NONO」（关机态下窗口隐藏，无法用鼠标点）
		if g != nil {
			g.turnOn()
		}
		return 0
	case WM_TIMER:
		switch wp {
		case 1: // 40ms 主计时
			if g != nil {
				g.tick()
			}
			// 改名输入是隐形 EDIT 缓冲 + 面板自绘，这里按拍把内容同步上屏
			if nickHwnd != 0 {
				syncNickEditText()
			}
		case 3: // 自测步骤
			pKillTimer.Call(uintptr(g.Hwnd), 3)
			if selftestIdx < len(selftestSteps) {
				st := selftestSteps[selftestIdx]
				pSendMessageW.Call(uintptr(g.Hwnd), st.msg, 0, 0)
				selftestIdx++
				if selftestIdx < len(selftestSteps) {
					pSetTimer.Call(uintptr(g.Hwnd), 3,
						uintptr(selftestSteps[selftestIdx].wait), 0)
				}
			}
		}
		return 0

	case appTick:
		if g != nil {
			g.tick()
		}
		return 0

	case 0x0001: // WM_CREATE
		logf("WM_CREATE")
		return 0
	case WM_TRAY:
		if int(uint16(lp)) == WM_RBUTTONUP || int(uint16(lp)) == 0x0203 { // WM_LBUTTONDBLCLK
			g.showTrayMenu()
		}
		return 0

	case WM_DESTROY:
		g.removeTray()
		pPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wp, lp)
	return ret
}

func (g *Game) alphaAt(x, y int) byte {
	if g.CurFrame == nil {
		return 0
	}
	if x < 0 || y < 0 || x >= g.CurFrame.Bounds().Dx() || y >= g.CurFrame.Bounds().Dy() {
		return 0
	}
	return g.CurFrame.RGBAAt(x, y).A
}

// onClick 对应原版点本体行为：MachineDogPanel（主面板）打开。
// 桌宠把「点我互动」降为双击彩蛋，单击保持原版语义。
// openPanelAtTick：记录「单击刚打开主面板」的时刻，双击彩蛋（onClickEgg）
// 用它收回刚弹出的面板——否则双击会先弹面板再播彩蛋（2026-11-02 E-05 修复）。
func (g *Game) onClick() {
	// 关机保持态放行（20261029-01 子渊确认）：主面板上的电源键是「开机」的
	// 可视化入口，拦掉的话关机后只剩托盘一条路。开机动作播放期间仍然拦截，
	// 与「动作播放中不响应单击」的既有语义一致。
	if g.Anim != nil && g.Save.Open {
		return
	}
	g.openPanelAtTick = g.TickN
	g.showMainPanel()
}

// onClickEgg 双击彩蛋：随机表情或随机动作（原版没有，桌宠附加）。
// 双击的 Windows 消息序列是 DOWN→UP→DBLCLK→UP：第一次 UP 已经触发过
// onClick()（打开了主面板），这里要把这个「刚弹出来的」面板收回，
// 否则双击彩蛋会带出一个多余的主面板。
func (g *Game) onClickEgg() {
	if g.Anim != nil {
		return
	}
	if mainPanelWin != nil && mainPanelWin.Visible && g.TickN-g.openPanelAtTick < 15 {
		mainPanelWin.hide()
		hideAllSubPanels()
	}
	if rand.Intn(100) < 55 {
		g.playExp(g.randomExpID())
		return
	}
	acts := g.FS.animIDs(1)
	if len(acts) == 0 {
		g.playExp(g.randomExpID())
		return
	}
	g.playAction(acts[rand.Intn(len(acts))])
}

func (g *Game) showMenu() {
	pt := POINT{}
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	h, _, _ := pCreatePopupMenu.Call()
	hm := HMENU(h)
	appendStr(hm, menuInfo, "NONO 信息面板", false)
	appendStr(hm, 0, "", true)

	// 使用芯片（按分类二级菜单）
	chipMenu, _, _ := pCreatePopupMenu.Call()
	groups := []struct {
		kind int
		name string
	}{{2, "变色芯片"}, {3, "能量芯片"}, {4, "智慧芯片"}, {101, "玩具芯片"}, {1, "功能芯片"}}
	for _, grp := range groups {
		sub, _, _ := pCreatePopupMenu.Call()
		n := 0
		for _, c := range chipsByKind(grp.kind) {
			// 20261027-13/14：功能芯片环上已有的、以及显式黑名单里的（机械臂），
			// 右键菜单不再重复列（子渊：「把那一圈有的功能先从菜单去掉」/
			// 「机械臂应该算是已经加载了，而且它是玩球，这个也可以去掉右键菜单」）。
			// 环上没有的其余 10 枚仍然留在这里，它们是原版背包的位置——桌面版
			// 没有背包，这是唯一入口。
			if grp.kind == 1 && hiddenFromChipMenu(c.ID) {
				continue
			}
			label := c.Name
			if g.Save.ColorChip == c.ID {
				label = "● " + label
			}
			appendStr(HMENU(sub), uintptr(c.ID), label, false)
			n++
		}
		if n > 0 {
			pAppendMenuW.Call(chipMenu, MF_POPUP, sub, uintptr(unsafe.Pointer(utf16ptr(grp.name))))
		}
	}
	pAppendMenuW.Call(h, MF_POPUP, chipMenu, uintptr(unsafe.Pointer(utf16ptr("使用芯片"))))

	// 超能形态：本地模拟服务端 9020 的 SuperLevel→形态换算（1-3→1 … 12→5）
	superMenu, _, _ := pCreatePopupMenu.Call()
	if g.Save.SuperLevel > 0 {
		appendStr(HMENU(superMenu), menuSuperOff, "变回普通形态", false)
		appendStr(HMENU(superMenu), 0, "", true)
	}
	for lv := 1; lv <= 12; lv++ {
		label := "超能 " + itoa(lv) + " 级（形态 " + itoa(superLevelToForm(lv)) + "）"
		if g.Save.SuperLevel == lv {
			label = "● " + label
		}
		appendStr(HMENU(superMenu), uintptr(menuSuperBase+lv), label, false)
	}
	// 至尊形态：游戏客户端 annual 分支（superStage>=6），服务端封顶 5 拿不到，
	// 桌宠用超能 13 级单独放出来，素材是 nono/annual/nono.swf。
	{
		label := "至尊 NONO（形态 6）"
		if g.Save.SuperLevel >= 13 {
			label = "● " + label
		}
		appendStr(HMENU(superMenu), uintptr(menuSuperBase+13), label, false)
	}
	pAppendMenuW.Call(h, MF_POPUP, superMenu, uintptr(unsafe.Pointer(utf16ptr("超能形态"))))

	// 20261027-11：删掉「飞行风格」子菜单（子渊：由快捷栏「飞行模式」按钮的
	// 4 格选择面板统一完成，panel_flychoice.go）与「芯片工坊」子菜单
	// （与「使用芯片」42 枚芯片完全重复，且 save.go 的 ensureAllChips 已把
	// 全部芯片默认装上，物品无限，没有付费入口的意义）。

	// 动作：20261027-11 起把原「动作演示」「表情演示」两个一级菜单合并成一个
	// （子渊：「合并成动作，并且和动作都放到动作里」）。两类 ID 区间分开
	// （动作 4001+id、表情 4101+id），互不重叠，switch 分发逻辑不用改。
	actMenu, _, _ := pCreatePopupMenu.Call()
	actIDs := g.FS.animIDs(1)
	expIDs := g.FS.animIDs(2)
	for _, id := range actIDs {
		appendStr(HMENU(actMenu), uintptr(menuActBase+id), actionName(id), false)
	}
	if len(actIDs) == 0 {
		appendStr(HMENU(actMenu), 0, "（本形态没有动作资源）", true)
	}
	if len(expIDs) > 0 {
		if len(actIDs) > 0 {
			appendStr(HMENU(actMenu), 0, "", true) // 动作与表情之间一条分隔线
		}
		for _, id := range expIDs {
			appendStr(HMENU(actMenu), uintptr(menuExpBase+id), "表情·"+expName(id), false)
		}
	} else {
		appendStr(HMENU(actMenu), 0, "（本形态没有表情资源）", true)
	}
	pAppendMenuW.Call(h, MF_POPUP, actMenu, uintptr(unsafe.Pointer(utf16ptr("动作"))))

	appendStr(hm, menuReset, "恢复默认外观", false)
	appendStr(hm, menuLab, "芯片制造机（发明室）", false)
	appendStr(hm, menuSetting, "设置…", false)
	appendStr(hm, 0, "", true)
	appendStr(hm, menuQuit, "退出", false)

	pSetForegroundWindow.Call(uintptr(g.Hwnd))
	cmd, _, _ := pTrackPopupMenu.Call(h, TPM_RETURNCMD|TPM_RIGHTBUTTON,
		uintptr(pt.X), uintptr(pt.Y), 0, uintptr(g.Hwnd), 0)
	pDestroyMenu.Call(h)

	switch int(cmd) {
	case 0:
		return
	case menuInfo:
		showInfoPanel()
	case menuReset:
		g.resetAppearance()
	case menuLab:
		g.showLabPanel()
	case menuSetting:
		g.showSettings()
	case menuQuit:
		g.Save.write()
		pDestroyWindow.Call(uintptr(g.Hwnd))
	default:
		switch {
		case int(cmd) == menuSuperOff:
			g.setSuperLevel(0)
		case int(cmd) > menuSuperBase && int(cmd) <= menuSuperBase+13:
			g.setSuperLevel(int(cmd) - menuSuperBase)
		case int(cmd) > menuActBase && int(cmd) <= menuActBase+20:
			g.playAction(int(cmd) - menuActBase)
		case int(cmd) > menuExpBase && int(cmd) <= menuExpBase+20:
			g.playExp(int(cmd) - menuExpBase)
		case int(cmd) >= 700000:
			g.useChip(int(cmd))
		}
	}
}

func appendStr(h HMENU, id uintptr, label string, sep bool) {
	if sep {
		pAppendMenuW.Call(uintptr(h), MF_SEPARATOR, 0, 0)
		return
	}
	pAppendMenuW.Call(uintptr(h), MF_STRING, id, uintptr(unsafe.Pointer(utf16ptr(label))))
}

func (g *Game) useChip(id int) {
	c := chipByID[id]
	if c == nil {
		return
	}
	logf("useChip id=%d name=%s effect=%s color=%d", c.ID, c.Name, c.Effect, c.Color)
	if mainPanelWin != nil && mainPanelWin.Visible {
		facePlay("loadChip")
		mainPanelWin.paint()
		time.AfterFunc(1500*time.Millisecond, func() {
			if face.name == "loadChip" {
				facePlay("normal")
				if mainPanelWin != nil && mainPanelWin.Visible {
					mainPanelWin.paint()
				}
			}
		})
	}
	if !containsInt(g.Save.Chips, id) {
		g.Save.Chips = append(g.Save.Chips, id)
	}
	msg := ""
	switch c.Effect {
	case "color":
		g.Save.Color = c.Color
		g.Save.ColorChip = c.ID
		msg = "已换上" + c.Name
	case "colorcustom":
		// 系统取色器，取消则原样不动（不能把取消当「选成黑色」）。
		if c2, ok := pickColor(g.Save.ColorCustom); ok {
			g.Save.ColorCustom = c2
			msg = "自定义颜色已应用"
		} else {
			msg = "没有选颜色，保持原样"
		}
		g.Save.Color = g.Save.ColorCustom
		g.Save.ColorChip = c.ID
	case "colorgrad":
		// 依次选起色、止色；任一次取消都整体放弃，不写半截渐变。
		g1, ok1 := pickColor(g.Save.GradA)
		if !ok1 {
			msg = "没有选起始色，保持原样"
			break
		}
		g2, ok2 := pickColor(g.Save.GradB)
		if !ok2 {
			msg = "没有选结束色，保持原样"
			break
		}
		g.Save.GradA, g.Save.GradB = g1, g2
		g.Save.Color = g1
		g.Save.ColorChip = c.ID
		msg = "自定义渐变已应用"
	case "recolor":
		g.Save.Color = uint32(rand.Intn(0xFFFFFF))
		msg = "颜色随机变换"
	case "energy":
		g.Save.Power = clamp(g.Save.Power+c.AddPower, 0, 100)
		g.Save.Mate = clamp(g.Save.Mate+c.AddCloseness, 0, 100)
		msg = fmt.Sprintf("能量 +%d 亲密 +%d", c.AddPower, c.AddCloseness)
		if g.FS.hasAction(5) {
			g.playAction(5)
		}
	case "wisdom":
		// 原版 items.xml：700451 AddIQ=100、700452 AddIQ=500，UseAI 只是使用门槛。
		// 旧实现误用 UseAI（都是 0）→ 只加 1 点，与游戏差两个数量级。
		inc := c.AddIQ
		if inc <= 0 {
			inc = 1
		}
		g.Save.AI += inc
		msg = fmt.Sprintf("智能 +%d", inc)
	case "toy":
		g.Save.Mate = clamp(g.Save.Mate+c.AddCloseness, 0, 100)
		msg = c.Name + "：亲密 +" + itoa(c.AddCloseness)
		if c.Play > 0 {
			if g.FS.hasAction(c.Play) {
				g.playAction(c.Play)
			} else {
				g.playExp(1) // 普通形态没有该类动作，用表情反馈
				msg += "（本形态无此动作）"
			}
		}
	case "follow":
		g.Save.Follow = !g.Save.Follow
		if g.Save.Follow {
			msg = "跟随模式：开"
		} else {
			msg = "跟随模式：关"
		}
	case "fly":
		g.Save.Fly = !g.Save.Fly
		msg = "悬浮模式：" + onOff(g.Save.Fly)
	case "shield":
		g.Save.Shield = !g.Save.Shield
		msg = "超能屏障：" + onOff(g.Save.Shield)
	case "glow":
		g.Save.Glow = !g.Save.Glow
		msg = "超能炫光：" + onOff(g.Save.Glow)
	case "heal":
		g.Save.Power = 100
		msg = "能量已恢复满"
	case "refine":
		g.Save.Power = clamp(g.Save.Power+20, 0, 100)
		msg = "能量 +20"
	case "convert":
		g.Save.Power = clamp(g.Save.Power-c.UsePower, 0, 100)
		g.Save.AI++
		msg = "能量转化为智能 +1"
	case "antimatter":
		g.Save.Power = clamp(g.Save.Power-10, 0, 100)
		g.Save.AI++
		msg = "反物质：能量 -10，智能 +1"
	case "exp":
		g.Save.Exp += 10
		msg = "经验 +10"
	case "expbonus":
		g.Save.ExpBonus = !g.Save.ExpBonus
		msg = "经验加成：" + onOff(g.Save.ExpBonus)
	case "train":
		g.Save.AI++
		msg = "模拟训练：智能 +1"
	case "teleport":
		// 20261028-07：子渊要求关掉随机位移（时空穿梭不再瞬移）。
		// 备注：此功能暂不需要，但代码保留 —— 不删芯片、不删分支，随时可恢复。
		// 恢复方式：取消下面三行注释、删掉 msg 那行即可
		//（原始备份见 变更工程/当前改动/20261028-07/备份/main.go）。
		// vx, vy, vw, vh := virtualScreen()
		// g.BaseX = vx + rand.Intn(maxInt(1, vw-g.BodyW))
		// g.BaseY = vy + rand.Intn(maxInt(1, vh-g.BodyH))
		msg = "时空穿梭已关闭"
	case "scan":
		msg = fmt.Sprintf("坐标 %d,%d", g.BaseX, g.BaseY)
	case "radar":
		msg = "雷达：附近没有野生精灵"
	case "talk":
		// 精灵翻译/语言芯片：从台词池随机，不再固定一句（20261023-04）
		msg = lineForExp(0)
	case "sort":
		msg = "技能已按威力排序"
	case "inventory":
		msg = "芯片背包已打开"
		showInfoPanel()
	case "arm":
		if g.FS.hasAction(4) {
			g.playAction(4)
		} else {
			g.playExp(3)
		}
		msg = "机械臂：玩球"
	default:
		msg = c.Name + " 已安装"
	}
	// 使用芯片不再弹气泡（2026-10-27 子渊）：颜色、能量、智能的变化本身就是反馈。
	// 只有「取色被取消、什么都没改」才提示一句，否则用户会以为程序没响应。
	if msg != "" && strings.Contains(msg, "保持原样") {
		g.setBubble(msg)
	}
	g.Save.write()
	g.draw()
}

func (g *Game) resetAppearance() {
	g.Save.Color = 0xFFFFFF
	g.Save.ColorChip = 0
	g.Save.Fly = false
	g.Save.Shield = false
	g.Save.Glow = false
	g.Save.Scale = 1
	g.SetScale(g.Save.Scale)
	g.setBubble("已恢复默认外观")
	g.Save.write()
}

func (g *Game) SetScale(s float64) {
	g.Save.Scale = s
	if g.R != nil {
		g.R.release()
		g.R = nil
		g.CurW, g.CurH = 0, 0
	}
	g.draw()
}

// savePosThrottled 节流落盘位置（2026-11-02 E-02 修复，对照 seer D-08 修复）。
//
// 为什么需要：旧实现只在 run() 退出时写一次 Save.X/Y——正常退出没事，
// 但「拖动结束」「自动走动中间」都不落盘；进程被单实例强杀 / 崩溃 / 断电时，
// 用户刚刚拖到的新位置全部丢失，重启回到旧点。
// 为什么节流：走动每 tick（40ms）都在变 BaseX，每次都 Rename 写 JSON 既慢
// 又磨损磁盘。这里限定：距上次保存 ≥2s 且位移 >8px 才写。
func (g *Game) savePosThrottled() {
	if g == nil || g.Save == nil {
		return
	}
	now := time.Now()
	if !g.LastSavePosAt.IsZero() && now.Sub(g.LastSavePosAt) < 2*time.Second {
		return
	}
	dx := g.BaseX - g.LastSavePosX
	dy := g.BaseY - g.LastSavePosY
	if dx*dx+dy*dy <= 8*8 {
		return
	}
	g.LastSavePosAt = now
	g.LastSavePosX, g.LastSavePosY = g.BaseX, g.BaseY
	g.Save.X, g.Save.Y = g.BaseX, g.BaseY
	g.Save.write()
	logf("save pos throttled -> %d,%d", g.BaseX, g.BaseY)
}

func onOff(b bool) string {
	if b {
		return "开"
	}
	return "关"
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
func itoa(v int) string { return fmt.Sprintf("%d", v) }

// ---- 入口 ----

func run() {
	// CPU 采样：NONO_CPUPROFILE=1 时写 cpu.prof，用于定位「动作期掉到 13fps」
	// 的真实热点。Wine 下没有外部 profiler，只能靠进程内采样。
	if os.Getenv("NONO_CPUPROFILE") != "" {
		if f, err := os.Create("cpu.prof"); err == nil {
			pprof.StartCPUProfile(f)
			go func() {
				time.Sleep(20 * time.Second)
				pprof.StopCPUProfile()
				f.Close()
				logf("cpu profile 已写入 cpu.prof")
			}()
		}
	}
	// 堆采样：NONO_HEAPPROFILE=1 时在 15s 落一次 heap.prof（inuse_space），
	// 配合 NONO_MEMSTATS 打印，用来定位 649MB/848MB 到底是谁占的。
	// RSS 里同时包含 GDI/DIB 与 Wine 自身映射，Go 堆只是其中一部分，必须对照看。
	if v := os.Getenv("NONO_HEAPPROFILE"); v != "" {
		delay := 15
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			delay = n
		}
		go func() {
			time.Sleep(time.Duration(delay) * time.Second)
			if f, err := os.Create("heap.prof"); err == nil {
				runtime.GC()
				_ = pprof.WriteHeapProfile(f)
				f.Close()
				logf("heap profile 已写入 heap.prof")
			}
		}()
	}
	// 归还可回收页：NONO_FREEMEM=<秒> 时在该时刻做一次 GC + FreeOSMemory。
	// 预热会一次性分配 3GB 以上（7 形态 × 8 方向 × 19 帧 × 4x 图 + glow 浮点缓冲），
	// GCPercent=300 会把堆留在 700~850MB；这里把 idle 页真正还给系统。
	if v := os.Getenv("NONO_FREEMEM"); v != "" {
		sec, _ := strconv.Atoi(v)
		if sec <= 0 {
			sec = 20
		}
		go func() {
			time.Sleep(time.Duration(sec) * time.Second)
			var b, a runtime.MemStats
			runtime.ReadMemStats(&b)
			debug.FreeOSMemory()
			runtime.ReadMemStats(&a)
			logf("FREEMEM heapSys %.1f→%.1fMB heapIdle %.1f→%.1fMB heapAlloc %.1f→%.1fMB",
				float64(b.HeapSys)/(1<<20), float64(a.HeapSys)/(1<<20),
				float64(b.HeapIdle)/(1<<20), float64(a.HeapIdle)/(1<<20),
				float64(b.HeapAlloc)/(1<<20), float64(a.HeapAlloc)/(1<<20))
		}()
	}
	if os.Getenv("NONO_MEMSTATS") != "" {
		go func() {
			for i := 1; i <= 8; i++ {
				time.Sleep(4 * time.Second)
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				logf("MEMSTATS t=%2ds heapAlloc=%6.1fMB heapInuse=%6.1fMB heapIdle=%6.1fMB heapSys=%6.1fMB sys=%6.1fMB totalAlloc=%7.1fMB mallocs=%d gc=%d",
					i*4,
					float64(ms.HeapAlloc)/(1<<20), float64(ms.HeapInuse)/(1<<20),
					float64(ms.HeapIdle)/(1<<20), float64(ms.HeapSys)/(1<<20),
					float64(ms.Sys)/(1<<20), float64(ms.TotalAlloc)/(1<<20), ms.Mallocs, ms.NumGC)
			}
		}()
	}
	initDPI()
	if err := loadChips(); err != nil {
		fatalBox("加载芯片数据失败：%v", err)
		panic(err)
	}
	// 飞行整帧的尺寸表（fullW/bodyW/bodyTopY）：决定飞行器显示缩放与落点补偿，
	// 缺失时 flyImageScaled 会退回 style1 比例，不阻塞启动。
	loadFlyMetrics()
	loadFlyMetricsV2()
	// 台词表缺失不致命：lineForExp 会退回 fallbackLines，但会记一条日志便于排查
	if err := loadLines(); err != nil {
		logf("loadLines failed: %v", err)
	}
	logf("run start")
	// 赛尔桌宠联动：起 UDP 监听（nono-20261031-01）。失败只记日志。
	startSeerFollow()
	sv := loadSave()
	// 按存档里的超能等级选素材目录；超能素材缺失时退回 plain，不能因此崩掉
	formKey := sv.formKey()
	fsx, err := loadFrameSet(formKey)
	if err != nil && formKey != "plain" {
		logf("form %s missing: %v, fallback plain", formKey, err)
		fsx, err = loadFrameSet("plain")
	}
	if err != nil {
		fatalBox("加载 NONO 素材失败：%v", err)
		panic(err)
	}
	logf("form loaded: %s zoom=%d superLevel=%d actions=%d anims=%v exps=%v",
		fsx.Key, fsx.Meta.Zoom, sv.SuperLevel, len(fsx.actionFiles),
		fsx.animIDs(1), fsx.animIDs(2))

	g = &Game{FS: fsx, Save: &sv, Dir: "down"}
	// 原版 startAutoTime() 一启动就排 setTimeout(onExpTime, randomHalve(20000))，
	// 即第一次表情在 10~20 秒后才播，不是开机立刻播一次。
	// 旧实现 ExpTimer 初值 0，首个可播时刻会立刻冒表情，和游戏不一样。
	g.ExpTimer = 250 + rand.Intn(250)
	g.TransparentHits = os.Getenv("NONO_NO_PASSTHROUGH") == ""

	// 上次退出时的坐标可能来自已拔掉的显示器，夹回可见区域
	vx0, vy0, vw0, vh0 := virtualScreen()
	g.BaseX = clamp(sv.X, vx0, vx0+vw0-120)
	g.BaseY = clamp(sv.Y, vy0, vy0+vh0-160)

	hInst, _, _ := pGetModuleHandleW.Call(0)
	className := utf16ptr("NonoDeskpetWnd")
	wc := WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEX{})),
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     HINSTANCE(hInst),
		LpszClassName: className,
	}
	atom, _, rerr := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	logf("RegisterClass atom=%d err=%v", atom, rerr)

	// 先按 2 倍估算窗口尺寸，draw 里会按实际帧调整
	trayCreatedMsg, _, _ = pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(utf16ptr("TaskbarCreated"))))

	hwnd, _, _ := pCreateWindowExW.Call(
		WS_EX_LAYERED|WS_EX_TOPMOST|WS_EX_TOOLWINDOW,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(utf16ptr("NONO 桌宠"))),
		WS_POPUP,
		uintptr(g.BaseX), uintptr(g.BaseY), 160, 180,
		0, 0, hInst, 0)
	g.Hwnd = HWND(hwnd)
	logf("CreateWindow hwnd=%d", hwnd)

	logf("before first draw")
	g.draw()
	g.addTray()
	// 启动即开机（20261029-01 子渊）：无视存档里的关机态，播当前形态的开机动作
	// （action 2，带 boot.wav）。官方 action 表 id=2 是 autoEnd=1 → 播完回待机。
	// 这里不走 powerOn()：powerOn 会因 Power<=0 拒绝开机，而启动必须让本体可见。
	// 存档仍写回 open=true，避免下次启动读到一个"关机态但显示着本体"的状态。
	if !g.Save.Open {
		g.Save.Open = true
		g.Save.write()
		logf("boot: 存档为关机态，启动强制开机")
	}
	// 启动时若存档在飞行态，先抑制飞行器，否则开机动画会被载具整个盖掉；
	// 播完由 idle() 恢复（PowerAnim==2），没有该动作时立刻恢复。
	g.FlyOff = g.Save.Fly
	if !g.playPowerAnim(2, false, false) {
		g.FlyOff = false
	}
	switch os.Getenv("NONO_TEST_AUTOSTART") {
	case "on":
		setAutoStart(true)
		logf("autostart after on = %v", autoStartEnabled())
	case "off":
		setAutoStart(false)
		logf("autostart after off = %v", autoStartEnabled())
	}
	logf("after first draw")
	// 40ms 一帧（25fps）。用窗口定时器而不是跨线程 PostMessage：
	// wine 下 PostMessage 从别的 goroutine/线程发，偶尔会进错队列，定时器消息天然回到窗口线程。
	// 40ms = 25fps，与游戏 SWF 帧率一致（子渊 2026-10-24 要求帧率按游戏来）。
	// 但 Windows 默认计时器分辨率 15.6ms，SetTimer(40) 实际会在 31/47/62ms 间抖动，
	// 观感就是「一卡一卡、不像游戏」。timeBeginPeriod(1) 把分辨率提到 1ms，
	// 40ms 定时才真按 40ms 走。
	if pTimeBeginPeriod.Find() == nil {
		pTimeBeginPeriod.Call(1)
	}
	pSetTimer.Call(uintptr(g.Hwnd), 1, 40, 0)

	// ShowWindow 在部分环境下会同步等窗口管理器，放到独立 goroutine，避免堵住消息循环
	go func() {
		pShowWindow.Call(uintptr(g.Hwnd), SW_SHOW)
		pSetWindowPos.Call(uintptr(g.Hwnd), ^uintptr(0), 0, 0, 0, 0,
			SWP_NOMOVE|SWP_NOSIZE|SWP_SHOWWINDOW)
		logf("after show")
	}()

	// 自测入口：关机态启动后 2 秒模拟托盘开机，验证本体恢复显示
	// 调试钩子：NONO_FLY_STYLE=n 固定飞行形态并直接起飞，用于逐 style 抓图比对
	if v := os.Getenv("NONO_FLY_STYLE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 4 {
			g.Save.FlyStyle = n
			g.Save.Fly = true
			logf("force fly style=%d", n)
		}
	}
	// 调试钩子：NONO_AUTOPLAY=<id> 启动后 1.5s 自动播一次动作，用于抓图核对
	// 「动作帧本体尺寸/落位是否与 idle 一致」（无鼠标环境无法点右键菜单）。
	if v := os.Getenv("NONO_AUTOPLAY"); v != "" {
		go func() {
			time.Sleep(1500 * time.Millisecond)
			if strings.HasPrefix(v, "exp") {
				if n, err := strconv.Atoi(strings.TrimPrefix(v, "exp")); err == nil && n > 0 {
					g.playExp(n)
					logf("autoplay exp id=%d", n)
				}
			} else if n, err := strconv.Atoi(v); err == nil && n > 0 {
				g.playAction(n)
				logf("autoplay action id=%d", n)
			}
		}()
	}
	if os.Getenv("NONO_TEST_TURNON") != "" {
		go func() {
			time.Sleep(2 * time.Second)
			pPostMessageW.Call(uintptr(g.Hwnd), WM_APP+35, 0, 0)
			logf("selftest turnon posted")
		}()
	}

	// UI 自测入口：NONO_UI=main|shortcut|info|storage|chip 直接打开对应面板并截图取证
	if uiTest := os.Getenv("NONO_UI"); uiTest != "" {
		go func() {
			time.Sleep(1200 * time.Millisecond)
			var msg uintptr
			switch uiTest {
			case "main":
				msg = WM_APP + 30
			case "shortcut":
				msg = WM_APP + 31
			case "info":
				msg = WM_APP + 32
			case "storage":
				msg = WM_APP + 33
			case "chip":
				msg = WM_APP + 34
			case "lab":
				msg = WM_APP + 36
			}
			if msg != 0 {
				pPostMessageW.Call(uintptr(g.Hwnd), msg, 0, 0)
			}
			logf("ui test posted: %s", uiTest)
		}()
	}

	if os.Getenv("NONO_SELFTEST") != "" {
		selftestSteps = []selftestStep{
			{WM_APP + 2, 3000}, {WM_APP + 3, 1500}, {WM_APP + 11, 400},
			{WM_APP + 8, 600}, {WM_APP + 4, 1500}, {WM_APP + 12, 400},
			{WM_APP + 5, 1500}, {WM_APP + 13, 400},
			{WM_APP + 19, 1000}, {WM_APP + 19, 400},
			{WM_APP + 6, 1500}, {WM_APP + 14, 400},
			{WM_APP + 7, 800}, {WM_APP + 17, 900},
			// 2026-11-02 E-04：`WM_APP+9` 在 wndProc 里没有 case（死消息，
			// 白白等 800ms），从序列移除。保留 WM_APP+10（设置面板截图）。
			{WM_APP + 10, 800},
		}
		selftestIdx = 0
		pSetTimer.Call(uintptr(g.Hwnd), 3, uintptr(selftestSteps[0].wait), 0)
	}

	var msg MSG
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	g.Save.X, g.Save.Y = g.BaseX, g.BaseY
	g.Save.write()
}

func main() {
	// 单实例：先把旧的那个退掉（它可能已经卡死），再自己启动。
	takeOverOld("NonoDeskpetWnd")
	if alreadyRunning() {
		// 走到这里说明旧实例卡在窗口创建之前、无法安全定位 PID。
		// 宁可不杀，直接退出，保证桌面上只有一个。
		logf("单实例：已有实例在运行且无法接管，本次启动取消")
		return
	}

	// Win32 窗口、消息队列都绑定在创建它的 OS 线程上。Go 的 main goroutine 会在
	// 不同线程间迁移，不锁线程的话窗口可能创建在线程 A、GetMessage 却跑在线程 B，
	// 结果消息永远收不到（wine 下必现，真机偶发）。
	runtime.LockOSThread()

	// 动作播放期间每帧都要重采样 + 渲染，短命对象多。默认 GOGC=100 时
	// GC 会频繁介入，实测同一段代码 head 段耗时在 14~44ms 之间抖动，
	// 表现为动作切换一卡一卡。放宽可以让堆多长一点，用内存换稳定帧率。
	//
	// 2026-10-28 曾试 150 省内存，同日真机反馈「切形态卡顿/启动黑框」后回滚：
	// 待机场景 150 确实更省（501MB vs 645MB），但形态切换/预热这种分配尖峰下
	// 堆目标过小会引来密集 GC，属负优化。内存改由 glow 池化承担（-203MB）。
	gcPercent := 150
	if v := os.Getenv("NONO_GCPERCENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			gcPercent = n
		}
	}
	debug.SetGCPercent(gcPercent)

	// 软内存上限：GOGC=300 允许堆长到「活对象的 4 倍」，实测 heapSys 920MB、
	// RSS 1100MB，而 GC 后活对象只有 ~250MB —— 中间 600 多 MB 是纯空闲页。
	// Go 1.19+ 的 soft memory limit 在接近上限时会更积极地回收并归还页，
	// 不改变活对象的回收时机（不像 GOGC 那样影响短命对象）。
	//
	// 默认固定 200MB（20261030-08 定）。
	//
	// 缘起：动态余量（2026-10-28 引入）实测有 150MB 的无用空闲页。日志证据：
	//   memlimit -> 518MB (live 156MB peak 156MB)
	// 活对象只有 156MB，公式 peak*2.5 + 128MB 却给到 518MB 额度，Go 就按额度
	// 涨到 377MB。倍数太宽是根因，不是「动态」这个思路错。
	//
	// 2026-10-30 对照实测（Wine，7 形态全预热 + 切 6 形态 + action_7，120s）：
	//   动态上限：RSS 178→377MB，fps 分布 47/57 段 25.0，最低 21.6
	//   固定 200：RSS 214~231MB，fps 分布 47/57 段 25.0，最低 21.5（逐段相同）
	//   固定 150：RSS 188MB，但偶发 tick 102.4ms 尖峰（踩到 GC 临界，弃）
	// 200 是「稳 25fps 的最低点」，省 150MB 且帧率零代价。
	//
	// startAutoMemLimit 函数保留不删：它记录了「盯峰值而非瞬时 live」那条教训
	// （按瞬时 live 算会让上限 269↔691 震荡，tick 5.9→11.2ms），也留作回退。
	// NONO_MEMLIMIT=<MB> 仍可覆盖为任意固定值（0 = 完全关闭）。
	if v := os.Getenv("NONO_MEMLIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			if n > 0 {
				debug.SetMemoryLimit(int64(n) << 20)
			}
		}
	} else {
		debug.SetMemoryLimit(200 << 20)
	}

	run()
}

// startAutoMemLimit 每 2 秒按「活对象峰值」重算软内存上限：
//
//	peak = max(peak*0.97, live)      // 3%/2s 衰减，半衰期约 46 秒
//	limit = peak*2 + 128MB，夹在 [minMB, maxMB]
//
// 为什么盯峰值而不是盯瞬时 live（第一版教训，2026-10-28 实测）：
// 补帧期间 live 在 98→340MB 之间来回跳，直接按 live*1.75 算，上限就会
// 269→691→327 地大幅震荡。Go 的软上限一旦降到当前堆以下会立刻触发一次
// GC，于是每 2 秒被强制 GC 一次，tick 从 5.9ms 涨到 11.2ms —— 省内存
// 反而赔了帧时间。峰值 + 慢衰减把这种震荡消掉：短时尖峰被记住，堆自然
// 回落时才缓慢下调。
//
// 变化不足 64MB 就跳过——每次 SetMemoryLimit 都会重算 GC 目标。
// 用 runtime/metrics 而不是 runtime.ReadMemStats：后者会 stop the world，
// 虽然只有几十微秒，但这是常驻 goroutine，没必要留这个尾巴。
func startAutoMemLimit(minMB, maxMB int) {
	// 指标选 /gc/heap/live:bytes（最近一次 GC 结束时标记存活），不是
	// /memory/classes/heap/objects:bytes：后者包含「还没被回收的垃圾」，
	// GOGC=300 时垃圾能堆到活对象的 4 倍，拿它算上限会一路开到 1073MB，
	// 等于把上限放空（2026-10-28 实测 live 读到 472MB，其中大头是垃圾）。
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	go func() {
		cur := int64(-1)
		peak := 0.0
		for {
			time.Sleep(2 * time.Second)
			metrics.Read(sample)
			live := float64(sample[0].Value.Uint64())
			if live > peak {
				peak = live
			} else {
				peak *= 0.97
			}
			want := int64(peak*2.5) + int64(128)<<20
			if lo := int64(minMB) << 20; want < lo {
				want = lo
			}
			if hi := int64(maxMB) << 20; want > hi {
				want = hi
			}
			if cur >= 0 && want < cur+(int64(64)<<20) && want > cur-(int64(64)<<20) {
				continue
			}
			debug.SetMemoryLimit(want)
			cur = want
			logf("memlimit -> %dMB (live %dMB peak %dMB)", want>>20, int64(live)>>20, int64(peak)>>20)
		}
	}()
}

// virtualScreen 返回多显示器联合区域的左上角与尺寸（可能为负，例如副屏在左侧）。
// virtualScreen 返回虚拟桌面范围。
//
// 2026-10-26：Wine 下 GetSystemMetrics 是 X11 往返调用，实测单次 5~10ms，
// 每帧调 4 次直接把 draw 拖到 32~42ms（动作期 fps 掉到 12.9）。桌面范围
// 不会逐帧变化，缓存 1 秒，热路径不再命中系统调用。
var vsCache [4]int
var vsAt time.Time

func virtualScreen() (int, int, int, int) {
	if !vsAt.IsZero() && time.Since(vsAt) < time.Second {
		return vsCache[0], vsCache[1], vsCache[2], vsCache[3]
	}
	get := func(idx uintptr) int {
		v, _, _ := pGetSystemMetrics.Call(idx)
		return int(int32(v))
	}
	vsCache = [4]int{get(76), get(77), get(78), get(79)} // SM_X/Y/CX/CYVIRTUALSCREEN
	vsAt = time.Now()
	return vsCache[0], vsCache[1], vsCache[2], vsCache[3]
}

// vecPrescaleLogKey 记录已经打过预缩放日志的形态，避免每帧刷屏。
var vecPrescaleLogKey string
