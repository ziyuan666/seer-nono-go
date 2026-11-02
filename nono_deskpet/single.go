//go:build windows

package main

// 单实例：同名 exe 只允许跑一个。
//
// 为什么是「后来者接管」而不是「后来者自己退出」：桌宠卡死时消息泵停摆，
// 旧实例既不会响应 WM_CLOSE 也不会自己退，用户再双击一次就必须能救回来
// （子渊 2026-10-30：「退出之前那个吧，以防之前那个是有问题卡了」）。
//
// 锁名带 appTag：赛尔与 NONO 要能同目录同时跑（P0 共存），
// 用全局锁会把另一个桌宠踢掉。

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	errorAlreadyExists  = 183    // ERROR_ALREADY_EXISTS
	processTerminate    = 0x0001 // PROCESS_TERMINATE
	processQueryLimited = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION
	synchronize         = 0x00100000
	waitTimeoutMs       = 2000 // 等旧实例优雅退出的上限
)

// takeOverOld 结束同类的旧实例：先 WM_CLOSE 让它自己退，超时没反应就强杀。
//
// 只动「窗口类匹配 且 exe 基名与自己相同」的进程：杀进程不可逆，
// 多一道基名校验，避免把别的程序干掉。
func takeOverOld(class string) {
	hwnd, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(utf16ptr(class))), 0)
	if hwnd == 0 {
		return
	}
	var pid uint32
	pGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		// 2026-11-02 对照 seer-20261102-05 D-01 修复：窗口在但拿不到有效 pid
		// （僵尸窗口 / 权限），不能再静默返回——记日志后继续走 alreadyRunning
		// 兜底，mutex 还在就被拦、mutex 已释放（进程真死了）就正常启动。
		logf("单实例：找到窗口但 pid 无效（僵尸窗口？），交给互斥体兜底")
		return
	}
	h, _, _ := pOpenProcess.Call(
		processTerminate|synchronize|processQueryLimited, 0, uintptr(pid))
	if h == 0 {
		logf("单实例：旧实例 pid=%d 打不开（权限？），跳过接管", pid)
		return
	}
	defer pCloseHandle.Call(h)

	if !sameExeName(h) {
		logf("单实例：pid=%d 的 exe 基名与本程序不同，跳过接管（不误杀）", pid)
		return
	}

	logf("单实例：发现旧实例 pid=%d，发 WM_CLOSE", pid)
	pPostMessageW.Call(hwnd, WM_CLOSE, 0, 0)
	if waitExit(h, waitTimeoutMs) {
		logf("单实例：旧实例已退出（WM_CLOSE）")
		return
	}

	// 消息泵停摆时 WM_CLOSE 永远不会被处理，只能强杀兜底。
	logf("单实例：旧实例 %dms 未响应，强制结束 pid=%d", waitTimeoutMs, pid)
	pTerminateProcess.Call(h, 1)
	if waitExit(h, waitTimeoutMs) {
		logf("单实例：旧实例已强制结束")
	} else {
		logf("单实例：强制结束后仍未退出 pid=%d", pid)
	}
}

// waitExit 等进程句柄变信号（进程退出），超时返回 false。
func waitExit(h uintptr, ms int) bool {
	r, _, _ := pWaitForSingleObject.Call(h, uintptr(ms))
	return r == 0 // WAIT_OBJECT_0
}

// sameExeName 核对目标进程的可执行文件基名与本进程是否一致。
// 大小写不敏感：Windows 文件系统本来就不区分。
func sameExeName(h uintptr) bool {
	buf := make([]uint16, 512)
	n := uint32(len(buf))
	ok, _, _ := pQueryFullProcessImageNameW.Call(h, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if ok == 0 || n == 0 {
		return false
	}
	other := strings.ToLower(filepath.Base(syscall.UTF16ToString(buf[:n])))
	self, err := os.Executable()
	if err != nil {
		return false
	}
	return other == strings.ToLower(filepath.Base(self))
}

// alreadyRunning 用命名互斥体兜底。
//
// 覆盖 takeOverOld 找不到窗口的情况（旧实例卡在窗口创建之前）：这时无法
// 安全定位 PID，宁可不杀，由互斥体保证「只有一个」——新实例直接退出。
// 句柄故意不 CloseHandle：进程退出时系统回收，提前关锁就失效了。
func alreadyRunning() bool {
	h, _, err := pCreateMutexW.Call(0, 0,
		uintptr(unsafe.Pointer(utf16ptr("Local\\deskpet_"+appTagOrUnknown()))))
	if h == 0 {
		return false // 拿不到锁就不拦，避免权限问题导致起不来
	}
	if err != syscall.Errno(errorAlreadyExists) {
		return false
	}
	// 2026-11-02 对照 seer-20261102-05 D-01 修复：旧实例刚被 WM_CLOSE/强杀时
	// mutex 由系统释放有延迟（窗口销毁、句柄回收不在同一瞬间），立刻判定
	// ALREADY_EXISTS 会误拦「其实已经退了」的新实例。重试 3 次×200ms。
	for i := 1; i <= 3; i++ {
		time.Sleep(200 * time.Millisecond)
		r, _, _ := pWaitForSingleObject.Call(h, 0) // 0 = 不等待
		if r == 0 {                                // WAIT_OBJECT_0：mutex 已释放
			return false
		}
	}
	return true
}

func appTagOrUnknown() string {
	if t := appTag(); t != "" {
		return t
	}
	return "unknown"
}
