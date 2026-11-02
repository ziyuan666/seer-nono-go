//go:build windows

package main

import "testing"

// TestOfficialFollowOffset 逐条核对 officialFollowOffset 与官方 AS3 是否一致。
//
// 出处：com/robot/core/mode/NonoModel.as
//   - MAX = 30（第 42 行）
//   - moveForPeople()（第 790~842 行）两段 switch：
//       第一段按主人朝向定 _dirPos.y：
//         LEFT_DOWN / DOWN / RIGHT_DOWN → -MAX
//         UP / RIGHT_UP / LEFT_UP       → +10
//         LEFT / RIGHT                  → 0
//       第二段按主人朝向定 _dirPos.x：
//         LEFT / DOWN / LEFT_DOWN / LEFT_UP → +MAX
//         UP / RIGHT / RIGHT_DOWN / RIGHT_UP → -MAX
//
// 这是 2026-11-01 飞行改动后挂账的回归项之一（「非飞行跟随 30px」）。
// 之所以用单测而不是实拍：这个函数是纯计算，实拍只能看到「大概位置对」，
// 单测能把 8 个方向 × 2 个轴全部钉死。
func TestOfficialFollowOffset(t *testing.T) {
	// 索引顺序同 seer_player/walk.go 的 dirNames：
	// 0 right、1 rightdown、2 down、3 leftdown、4 left、5 leftup、6 up、7 rightup
	want := map[int][2]int{
		0: {-30, 0},   // right      : x=-MAX, y=0
		1: {-30, -30}, // rightdown  : x=-MAX, y=-MAX
		2: {30, -30},  // down       : x=+MAX, y=-MAX
		3: {30, -30},  // leftdown   : x=+MAX, y=-MAX
		4: {30, 0},    // left       : x=+MAX, y=0
		5: {30, 10},   // leftup     : x=+MAX, y=+10
		6: {-30, 10},  // up         : x=-MAX, y=+10
		7: {-30, 10},  // rightup    : x=-MAX, y=+10
	}
	for idx := 0; idx < 8; idx++ {
		dx, dy := officialFollowOffset(idx)
		w := want[idx]
		if dx != w[0] || dy != w[1] {
			t.Errorf("dirIdx=%d(%s) 得到 (%d,%d)，官方 (%d,%d)",
				idx, seerDirNames[idx], dx, dy, w[0], w[1])
		}
	}
}

// TestSeerDirNamesOrder 确认 NONO 侧的方向索引与赛尔侧 dirNames 同序。
//
// 两套顺序不同会静默错位：NONO 会站到主人的错误一侧，而且只在特定朝向
// 才看得出来（左右对称的方向看不出）。fly.go 里那个 dirNames 是**帧标签**
// 顺序（down 起），不是这一套，别混。
func TestSeerDirNamesOrder(t *testing.T) {
	want := [8]string{"right", "rightdown", "down", "leftdown",
		"left", "leftup", "up", "rightup"}
	if seerDirNames != want {
		t.Fatalf("seerDirNames=%v，赛尔侧 dirNames=%v", seerDirNames, want)
	}
}
