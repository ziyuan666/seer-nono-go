//go:build windows

package main

import (
	"math/rand"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

// 超能形态：等级 → 形态换算与素材切换。
// 映射与服务端一致（/app/working/workspaces/a2ZDuB/kaiyuanhao/internal/core/nonoformcache/cache.go:98 SuperLevelToForm）：
// 1-3 → 形态1，4-6 → 形态2，7-8 → 形态3，9-11 → 形态4，12 → 形态5。
//
// 13 → 形态6（至尊 / annual）：游戏客户端里 annual 分支由 superStage>=6 触发
// （NonoModel.as:112-135 的 _resPath = "annual/"），而服务端 nonoFormForClient
// 封顶 superStage 5，正常流程拿不到这套素材；桌宠把 13 级开放给用户，
// 素材取 nono/annual/nono.swf（根 DefineSprite_138，8 方向 × 17 帧）。

// superLevelToForm 返回超能等级对应的形态号；0 表示非超能。
func superLevelToForm(lv int) int {
	if lv < 1 {
		return 0
	}
	if lv >= 13 {
		return 6
	}
	if lv >= 12 {
		return 5
	}
	if lv >= 9 {
		return 4
	}
	if lv >= 7 {
		return 3
	}
	if lv >= 4 {
		return 2
	}
	return 1
}

// formKey 返回当前应加载的素材目录名（assets/ 下的子目录）。
// 形态 6 的素材目录叫 annual（游戏资源路径 nono/annual/），不是 super6。
func (s *Save) formKey() string {
	f := superLevelToForm(s.SuperLevel)
	if f >= 6 {
		return "annual"
	}
	if f > 0 {
		return "super" + itoa(f)
	}
	return "plain"
}

// 形态素材缓存与异步加载状态。
//
// 2026-10-26 实测（NONO_TEST_PANEL=formbench + NONO_TEST_FORMS）：
// 切一次形态 loadFrameSet 耗时 super1 526ms / super2 427ms / super3 376ms /
// super4 392ms / super5 400ms / plain 135ms，全部在主线程同步跑，切完必然顿
// 一下（子渊 R2「各个形态切换后会卡顿」的直接原因）。
// 处理两条：① 加载过的形态进缓存，切回去不再重复解析；② 非首次启动的加载
// 扔到 goroutine，主线程继续画旧形态，加载完在下个 tick 换装——切形态期间
// 不再阻塞，代价是形态晚约 0.4 秒才变（比卡 0.4 秒好）。
// spec/raster/svg 三个包都没有包级可变状态，goroutine 里加载是安全的。
var (
	formCacheMu sync.Mutex
	formCache   = map[string]*FrameSet{}
	formPending *FrameSet
	// formLoadingKey 正在后台加载的形态 key，受 formCacheMu 保护。
	// 不能挂在 Game 上：goroutine 里清理它属于跨线程写，会和主线程的读竞争。
	formLoadingKey string
)

// 形态缓存策略（子渊 2026-10-28 定）：切换过的形态**全部保留**，不做定时淘汰。
// 取舍是「切形态要快」优先 —— 切回去是纯指针替换。代价是切得越多驻留越多
// （每套含已渲染的 4x 待机帧，单方向 51 帧 ≈ 74MB），靠 NONO_MEMLIMIT 兜底。

// applyForm 在形态变化后重新加载帧集。
// 素材缺失时退回 plain 并记日志：宁可显示普通形态，也不能让整只 NONO 消失。
func (g *Game) applyForm() {
	if g == nil {
		return
	}
	key := g.Save.formKey()
	if g.FS != nil && g.FS.Key == key {
		return
	}
	formCacheMu.Lock()
	cached := formCache[key]
	pending := formPending
	formCacheMu.Unlock()
	if pending != nil && pending.Key == key {
		g.installForm(pending) // 后台刚好已经加载完
		return
	}
	if cached != nil {
		g.installForm(cached)
		return
	}
	if g.FS == nil {
		// 首次启动必须同步：异步会让窗口先空着，观感像"启动失败"。
		tLoad := time.Now()
		fs, err := loadFrameSet(key)
		if err != nil {
			logf("loadFrameSet(%s) failed: %v, fallback plain", key, err)
			if fs2, err2 := loadFrameSet("plain"); err2 == nil {
				g.FS = fs2
			}
			return
		}
		logf("form -> %s (superLevel=%d) loadMs=%d", key, g.Save.SuperLevel, time.Since(tLoad)/1e6)
		g.installForm(fs)
		formCacheMu.Lock()
		formCache[key] = fs
		formCacheMu.Unlock()
		return
	}
	formCacheMu.Lock()
	busy := formLoadingKey != ""
	if !busy {
		formLoadingKey = key
	}
	formCacheMu.Unlock()
	if busy {
		// 已有一个后台加载在跑。不再并发发起第二个：实测同时加载 5 个形态时
		// 每个要 1500~1700ms（同步单个才 376~526ms），CPU/磁盘全被抢，
		// 主线程也跟着掉帧。等它结束后由 installPendingForm 清位重试。
		return
	}
	go func(want string) {
		t0 := time.Now()
		nfs, err := loadFrameSet(want)
		if err != nil {
			logf("loadFrameSet(%s) failed: %v", want, err)
			formCacheMu.Lock()
			formPending = nil
			formLoadingKey = ""
			formCacheMu.Unlock()
			return
		}
		logf("form load async %s %dms", want, time.Since(t0)/1e6)
		formCacheMu.Lock()
		formCache[want] = nfs
		formPending = nfs
		formLoadingKey = ""
		formCacheMu.Unlock()
	}(key)
}

// installForm 把某个形态帧集换上来（只在主线程调用）。
func (g *Game) installForm(fs *FrameSet) {
	if fs == nil || g.FS == fs {
		return
	}
	// 换形态前必须在**旧** FS 上收尾：停掉正在播的动作/表情的后台渲染 worker，
	// 并释放 vecAnim 缓冲。
	//
	// 2026-10-26 子渊打回「切换后不管什么时候右键 NONO 所有面板都会非常卡」：
	// installForm 原来只把 g.Anim 置 nil，3 个预取 worker 还在旧形态的帧上继续
	// 渲染，CPU 一直满载；面板/右键菜单是另开的窗口，主线程拿不到时间片就卡。
	// 这个泄漏在切形态后永久存在（worker 没人停），所以「不管什么时候」都卡。
	if g.Anim != nil && g.FS != nil {
		g.FS.animStopWorker(g.Anim.Kind, g.Anim.ID)
		g.Anim = nil
	}
	if g.FS != nil {
		g.FS.releaseAnim()
	}
	g.FS = fs
	g.Frame = 0
	g.FrameTick = 0
	formCacheMu.Lock()
	if formPending == fs {
		formPending = nil
	}
	formCacheMu.Unlock()
	logf("form -> %s (superLevel=%d)", fs.Key, g.Save.SuperLevel)
	// 关机态切形态（20261029-01）：上面把旧形态的动作动画停掉了，关机保持也就
	// 断了 —— 这里用新形态自己的 action 3 重新播一遍并定格末帧（20261029-03
	// 由循环改为保持最后一帧；plain 没有该资源时仍然停在原处，和 powerOff 的
	// 回退一致）。开机态切换不补播开机动画（那不是切换语义）。
	if !g.Save.Open {
		if !g.playPowerAnim(3, false, true) {
			g.Anim = nil
			g.PowerAnim = 0
		}
		logf("form -> %s: 关机态，重播关机动画(定格末帧)=%v", fs.Key, g.Anim != nil)
	} else {
		// 开机态切形态没有补播的动作，这里顺手解除飞行抑制：否则在开机动画
		// 播到一半切形态（installForm 直接清 Anim，不经过 idle）会永久停在
		// 「不画飞行器」的状态，直到下次开关机。
		g.FlyOff = false
	}
}

// installPendingForm 在 tick 开头调用：把后台加载好的形态换上来。
// 期间用户又切了别的形态就丢弃（以 Save.SuperLevel 为准）。
func (g *Game) installPendingForm() {
	formCacheMu.Lock()
	nfs := formPending
	formCacheMu.Unlock()
	if nfs == nil {
		return
	}
	if nfs.Key != g.Save.formKey() {
		// 加载完发现用户已经切走了：丢掉，并放开加载位，立刻按最新目标
		// 重新发起（不重新发起的话会停在上一个形态，永远追不上）。
		formCacheMu.Lock()
		formPending = nil
		formLoadingKey = ""
		formCacheMu.Unlock()
		g.applyForm()
		return
	}
	g.installForm(nfs)
}

// setSuperLevel 设置超能等级并同步 Super 标志、落盘、换形态。
// lv<=0 表示退回普通形态。
func (g *Game) setSuperLevel(lv int) {
	if lv < 0 {
		lv = 0
	}
	if lv > 13 { // 13 = 至尊（形态 6），见 superLevelToForm 注释
		lv = 13
	}
	g.Save.SuperLevel = lv
	g.Save.Super = lv > 0
	g.Save.write()
	g.applyForm()
	g.refreshPanelsAfterForm()
}

// actionName 返回动作 id 的中文名（MachXMLInfo action 段）。
func actionName(id int) string {
	switch id {
	case 1:
		return "充电"
	case 2:
		return "开机"
	case 3:
		return "关机"
	case 4:
		return "玩球"
	case 5:
		return "吃电池"
	case 6:
		return "召唤"
	case 7:
		return "玩魔方"
	}
	return "动作" + itoa(id)
}

// expName 表情名称，取自 MachXMLInfo 的 <exp>/<superExp> 段
// （binaryData/22_...MachXMLInfo_xmlClass.bin）：
//   1 开心 / 2 生气 / 3 惊讶 / 4 悲哀
// 普通与超能两套 id→名字完全一致，所以不用按形态区分。
func expName(id int) string {
	switch id {
	case 1:
		return "开心"
	case 2:
		return "生气"
	case 3:
		return "惊讶"
	case 4:
		return "悲哀"
	}
	return "表情" + itoa(id)
}

// randomExpID 返回本次要播的表情 id（1 开心 / 2 生气 / 3 惊讶 / 4 悲哀）。
// 普通形态等概率；超能形态开心权重 3:1:1:1，对齐原版 exp 段的 odds 配置。
func (g *Game) randomExpID() int {
	if g.Save.SuperLevel > 0 {
		r := rand.Intn(6)
		if r < 3 {
			return 1
		}
		return 2 + (r - 3) // 3→2 4→3 5→4
	}
	return 1 + rand.Intn(4)
}

// summonAction 播放召唤动作（action/6）。只有超能/年费形态有这套资源，
// 普通形态返回 false，调用方照常进入跟随。
func (g *Game) summonAction() bool {
	fr := g.FS.actionFrames(6)
	if len(fr) == 0 {
		return false
	}
	g.Anim = &Anim{Kind: 1, ID: 6, Frames: fr}
	return true
}
// 面板的底图尺寸、图标坐标都按形态素材算，换形态后旧命中区一定错位；
// refreshPanelsAfterForm 换形态后直接收掉所有面板。
// 与其逐个重建，不如关掉让用户重新打开（原版切形态也是在面板关闭态发生）。
func (g *Game) refreshPanelsAfterForm() {
	hideAllSubPanels()
	if mainPanelWin != nil && mainPanelWin.Visible {
		mainPanelWin.hide()
	}
}

// prewarmForms 后台把除当前形态外的所有形态读进缓存。
//
// 目的：真机上 loadFrameSet 要 2 秒左右（Wine 400~500ms），异步只是「不卡」，
// 用户仍要等形态真的换过来。启动后在空闲时预读，切形态就变成纯指针替换。
// 与 applyForm 共用 formLoadingKey 串行：同一时刻只有一个解析在跑，
// 避免两个 goroutine 抢磁盘把各自拖慢 4 倍（实测过的坑）。
func prewarmForms(cur string) {
	for _, k := range []string{"plain", "super1", "super2", "super3", "super4", "super5", "annual"} {
		if k == cur {
			continue
		}
		formCacheMu.Lock()
		_, have := formCache[k]
		busy := formLoadingKey != ""
		if !have && !busy {
			formLoadingKey = "prewarm:" + k
		}
		formCacheMu.Unlock()
		if have {
			continue
		}
		if busy {
			// 前台正在加载相反形态，让路，下次启动再预热。
			time.Sleep(300 * time.Millisecond)
			continue
		}
		t0 := time.Now()
		fs, err := loadFrameSet(k)
		formCacheMu.Lock()
		if err == nil {
			formCache[k] = fs
		}
		formLoadingKey = ""
		formCacheMu.Unlock()
		if err != nil {
			logf("prewarm %s failed: %v", k, err)
			continue
		}
		logf("prewarm %s %dms", k, time.Since(t0)/1e6)
		// 顺带把动作/表情解析掉：它们现在是懒加载，首次点播要现场读几百个
		// 小文件（Windows 上几百毫秒），预热阶段提前做掉就完全无感。
		if fs != nil && fs.vec != nil {
			ta := time.Now()
			fs.vec.warmAnims()
			logf("prewarm-anims %s %dms", k, time.Since(ta)/1e6)
		}
	}
	logf("prewarm done")
	// 归还空闲页默认关闭：真机反馈「启动黑框/切形态卡顿」后改成显式开关。
	// FreeOSMemory 会把堆页还给系统，之后任何大分配（切形态要重新光栅化一整套
	// 4x 帧）都要重新缺页，真机上表现为卡顿；Wine 的缺页更便宜，测不出来。
	// 需要省内存时用 NONO_FREEMEM=<秒> 显式开启（main.go 里有同名开关）。
	if v := os.Getenv("NONO_PREWARM_FREEMEM"); v != "" {
		go func() {
			time.Sleep(2 * time.Second)
			debug.FreeOSMemory()
			logf("post-prewarm FreeOSMemory done")
		}()
	}
}
