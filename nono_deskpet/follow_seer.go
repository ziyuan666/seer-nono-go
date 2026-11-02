//go:build windows

package main

// 赛尔桌宠 → NONO 的跟随联动（nono-20261031-01）。
//
// 赛尔侧（seer_player/follow.go）每 100ms 往 127.0.0.1:47110 发一包：
//
//	`seer <cx> <cy> <bw> <bh>`   cx,cy = 本体中心 x / 脚底 y
//
// 这里只做两件事：
//  1. 后台 goroutine 收包，把落点写进 atomic（主线程只读，避免与 tick 抢字段）；
//  2. 给 main.go 的跟随逻辑提供 seerPos()——**赛尔在跑就跟赛尔，赛尔不在就跟鼠标**，
//     偏移/停步距离/换边死区全部沿用原有那套（不复制第二份跟随算法）。
//
// 超时 1500ms：赛尔退出/关掉后 1.5 秒自动退回「跟随鼠标」，不会卡在最后一点。

import (
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const seerFollowPort = 47110

var (
	seerFollowX, seerFollowY int32
	seerFollowW              int32
	seerFollowH              int32       // 赛尔本体高：算脚底要用（官方偏移以脚下为基准）
	seerFollowDir            int32       // 朝向 0..7，顺序同赛尔 dirNames
	seerFollowScale          int32       // 赛尔本体缩放×1000（飞行挂载时飞行器按它画，官方同比例）
	seerSeenAt               int64       // 最后一次心跳（unix 毫秒）
	seerSummoned             atomic.Bool // 赛尔环绕栏「召唤 NONO」位：1 = 强制跟随
	seerFollowLogged         bool        // 只在状态切换时打日志，避免每 tick 刷屏
	seerGotOnce              bool        // 首个心跳只记一次，确认链路通

	// seerLift：飞行态要让赛尔抬起的像素（主线程每 tick 写，心跳 goroutine 读）。
	// 官方 NonoFlyModel.config() 用 keepUp 把主人骨架抬起、再把飞行器挂到主人
	// 原点，画面是「人坐在飞行器里」；我们两只桌宠是两个进程，只能靠回包告诉
	// 赛尔抬多少（协议见 seer_player/follow.go 顶部注释）。
	seerLift int32

	// liftAddr = 127.0.0.1:47111（赛尔侧的抬升监听口），启动时解析一次。
	liftAddr *net.UDPAddr
	// liftSentLogged 只记一次「已把非零抬升发出去」，避免每 400ms 刷日志。
	liftSentLogged int32 = -1
)

func seerNowMs() int64 { return time.Now().UnixNano() / int64(time.Millisecond) }

// startSeerFollow 起 UDP 监听。端口占用/绑定失败只记日志，不影响桌宠本体。
func startSeerFollow() {
	addr, err := net.ResolveUDPAddr("udp4", "127.0.0.1:"+strconv.Itoa(seerFollowPort))
	if err != nil {
		logf("跟随监听：解析地址失败 %v", err)
		return
	}
	pc, err := net.ListenUDP("udp4", addr)
	if err != nil {
		// 常见原因：已经开了一个 NONO（单实例会拦），或端口被别的进程占着
		logf("跟随监听：%v（赛尔联动不可用，跟随仍跟鼠标）", err)
		return
	}
	logf("跟随监听 ← 127.0.0.1:%d（等赛尔桌宠心跳）", seerFollowPort)
	if la, e := net.ResolveUDPAddr("udp4", "127.0.0.1:47111"); e == nil {
		liftAddr = la
		logf("抬升回包 → 127.0.0.1:47111（飞行态告诉赛尔抬高多少）")
	} else {
		logf("抬升回包：解析 47111 失败 %v（飞行挂载抬升不可用）", e)
	}
	go func() {
		buf := make([]byte, 128)
		var lastLift int32 = -1
		var lastSent int64
		for {
			n, _, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			f := strings.Fields(string(buf[:n]))
			if len(f) < 3 || f[0] != "seer" {
				continue
			}
			x, e1 := strconv.Atoi(f[1])
			y, e2 := strconv.Atoi(f[2])
			if e1 != nil || e2 != nil {
				continue
			}
			// 赛尔进程刚起、第一 tick 还没跑时原子量是 0，会先发一包 "0 0 0 0"；
			// 不滤掉的话 NONO 会朝屏幕左上角扑一下（2026-10-31 实测）。
			if x == 0 && y == 0 {
				continue
			}
			if !seerGotOnce {
				seerGotOnce = true
				logf("收到赛尔心跳 %d,%d（跟随开关：%v）", x, y, g != nil && g.Save != nil && g.Save.Follow)
			}
			if len(f) >= 4 {
				if w, ew := strconv.Atoi(f[3]); ew == nil && w > 0 {
					atomic.StoreInt32(&seerFollowW, int32(w))
				}
			}
			if len(f) >= 5 {
				if h, eh := strconv.Atoi(f[4]); eh == nil && h > 0 {
					atomic.StoreInt32(&seerFollowH, int32(h))
				}
			}
			// 字段：seer x y w h summon dir scale → 召唤位 f[5]、朝向 f[6]、缩放×1000 f[7]
			if len(f) >= 8 {
				if sc, es := strconv.Atoi(f[7]); es == nil && sc > 100 && sc < 8000 {
					atomic.StoreInt32(&seerFollowScale, int32(sc))
				}
			}
			if len(f) >= 7 {
				if d, ed := strconv.Atoi(f[6]); ed == nil && d >= 0 && d < 8 {
					atomic.StoreInt32(&seerFollowDir, int32(d))
				}
			}
			if len(f) >= 6 {
				on := f[5] == "1"
				if on != seerSummoned.Load() {
					seerSummoned.Store(on)
					if on {
						logf("赛尔召唤：开始跟随（环绕栏召唤按钮）")
					} else {
						logf("赛尔收回召唤：回到自己的跟随开关")
					}
				}
			}
			atomic.StoreInt32(&seerFollowX, int32(x))
			atomic.StoreInt32(&seerFollowY, int32(y))
			atomic.StoreInt64(&seerSeenAt, seerNowMs())

			// 回包：把「飞行器要赛尔抬多少」告诉赛尔（`nono fly <lift>`）。
			// 发到固定的 127.0.0.1:47111（赛尔那边 ListenUDP 收）。
			//
			// 不走「回心跳源地址」那条路：赛尔的心跳 socket 是 connected UDP，
			// 2026-11-01 实测 Windows 上只收到第一包，之后它那边 Read 一直阻塞
			// （ICMP 端口不可达把 socket 弄进错误态）。固定端口双向广播最稳。
			//
			// 值变了立刻发；没变也每 400ms 重发一次 —— 赛尔后启动/重启时能接上，
			// 而且它那边 2s 收不到就归零，重发等于心跳保活。
			if liftAddr != nil {
				lift := atomic.LoadInt32(&seerLift)
				now := seerNowMs()
				if lift != lastLift || now-lastSent > 400 {
					lastLift, lastSent = lift, now
					msg := []byte("nono fly " + strconv.Itoa(int(lift)))
					if _, werr := pc.WriteToUDP(msg, liftAddr); werr != nil {
						logf("回包失败 → %v：%v", liftAddr, werr)
					} else if lift != 0 && liftSentLogged != lift {
						liftSentLogged = lift
						logf("回包 → %v：%s", liftAddr, string(msg))
					}
				}
			}
		}
	}()
}

// seerDirNames 是赛尔侧朝向索引 0..7 对应的方向名（顺序同 seer_player/walk.go 的 dirNames：
// 0 right、1 rightdown、2 down、3 leftdown、4 left、5 leftup、6 up、7 rightup）。
//
// 注意：fly.go 里那个 dirNames 是**帧标签顺序**（down 起），两套顺序不同，
// 不能混用 —— 飞行挂载时按赛尔朝向取帧必须走这一份。
var seerDirNames = [8]string{"right", "rightdown", "down", "leftdown",
	"left", "leftup", "up", "rightup"}

// seerPos 返回赛尔本体中心、宽高与朝向；心跳超过 1500ms 视为不在。
func seerPos() (int, int, int, int, int, bool) {
	if seerNowMs()-atomic.LoadInt64(&seerSeenAt) > 1500 {
		return 0, 0, 0, 0, 0, false
	}
	return int(atomic.LoadInt32(&seerFollowX)), int(atomic.LoadInt32(&seerFollowY)),
		int(atomic.LoadInt32(&seerFollowW)), int(atomic.LoadInt32(&seerFollowH)),
		int(atomic.LoadInt32(&seerFollowDir)), true
}

// seerSummonOn 赛尔是否在召唤（强制跟随），与本地 follow 开关取或。
func seerSummonOn() bool { return seerSummoned.Load() }

// nonoBallRadius：NONO 球心到脚下的距离（像素）。
// 官方 _dirPos 的 y 偏移以「脚下」为基准（SpriteModel.pos = 角色脚下），
// 我们这边锚点用球心 BallSX/BallSY，所以要减掉这个半径才等价。
// 实测机器人可见高度约 80px（body 盒子 162 里大半是透明留白）。
const nonoBallRadius = 40

// officialFollowOffset 还原官方 NonoModel.moveForPeople 的站位偏移。
//
// 出处：com/robot/core/mode/NonoModel.as
//
//	MAX = 30（第 42 行）
//	朝 LEFT / DOWN / LEFT_DOWN / LEFT_UP  → _dirPos.x = +MAX（NONO 站主人右侧）
//	朝 UP / RIGHT / RIGHT_DOWN / RIGHT_UP → _dirPos.x = -MAX（站左侧）
//	朝 DOWN / LEFT_DOWN / RIGHT_DOWN      → _dirPos.y = -MAX（NONO 在主人上方）
//	朝 UP / LEFT_UP / RIGHT_UP            → _dirPos.y = +10（略低于主人）
//	朝 LEFT / RIGHT                       → _dirPos.y = 0（齐平）
//
// dirIdx 与赛尔侧 dirNames 同序：0 right、1 rightdown、2 down、3 leftdown、
// 4 left、5 leftup、6 up、7 rightup。
func officialFollowOffset(dirIdx int) (int, int) {
	const max = 30
	switch dirIdx {
	case 0: // right
		return -max, 0
	case 1: // rightdown
		return -max, -max
	case 2: // down
		return max, -max
	case 3: // leftdown
		return max, -max
	case 4: // left
		return max, 0
	case 5: // leftup
		return max, 10
	case 6: // up
		return -max, 10
	case 7: // rightup
		return -max, 10
	}
	return max, -max
}
