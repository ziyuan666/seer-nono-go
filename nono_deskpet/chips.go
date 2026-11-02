//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Chip struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Kind         int    `json:"kind"` // 1功能 2变色 3能量 4智慧 101玩具
	UseAI        int    `json:"useAI"`
	UsePower     int    `json:"usePower"`
	AddPower     int    `json:"addPower"`
	AddIQ        int    `json:"addIQ"` // 智慧芯片：原版 AddIQ（700451=100、700452=500）
	AddCloseness int    `json:"addCloseness"`
	Color        uint32 `json:"color"`
	Play         int    `json:"play"`
	// Effect 是本程序定义的本地行为，不来自原配置
	Effect string `json:"-"`
}

var chips []Chip
var chipByID = map[int]*Chip{}

func loadChips() error {
	b, err := os.ReadFile(filepath.Join(assetsRoot(), "chips.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &chips); err != nil {
		return err
	}
	for i := range chips {
		chipByID[chips[i].ID] = &chips[i]
	}
	for i := range chips {
		chips[i].Effect = localEffect(chips[i].ID)
	}
	return nil
}

// localEffect 把原版功能芯片映射成桌宠里的可见行为。
// 原版没有桌宠上下文的功能，统一降级成“数值 + 气泡反馈”，不做空转。
func localEffect(id int) string {
	switch id {
	case 700001:
		return "heal" // 能量恢复到 100
	case 700002:
		return "convert" // 能量转智能
	case 700003:
		return "exp" // 加经验
	case 700004:
		return "train" // 智能 +1
	case 700005, 700015:
		return "follow" // 跟随鼠标
	case 700006:
		return "arm" // 播玩球（缺资源时用表情）
	case 700007:
		return "expbonus" // 经验加成开关
	case 700008:
		return "scan" // 气泡报坐标
	case 700009:
		return "radar" // 气泡“附近无精灵”
	case 700010, 700061:
		return "talk" // 随机台词
	case 700011:
		return "inventory" // 打开信息面板
	case 700012:
		return "shield" // 护盾（外观，可恢复）
	case 700013:
		return "antimatter" // 能量 -10 / 智能 +1
	case 700014:
		return "refine" // 能量 +20
	case 700016:
		return "sort" // 气泡“技能已排序”
	case 700017:
		return "fly" // 悬浮（外观，可恢复）
	case 700313:
		return "colorcustom" // 本地新增：弹系统取色器，记住最后一次选择
	case 700314:
		return "colorgrad" // 本地新增：依次选「起色/止色」，竖向渐变
	case 700018:
		return "recolor" // 随机颜色（可恢复）
	case 700019:
		return "teleport" // 瞬移
	case 700020:
		return "glow" // 闪烁（外观，可恢复）
	}
	switch kindOf(id) {
	case 2:
		return "color"
	case 3:
		return "energy"
	case 4:
		return "wisdom"
	case 101:
		return "toy"
	}
	return "passive"
}

func kindOf(id int) int {
	if c, ok := chipByID[id]; ok {
		return c.Kind
	}
	return 0
}

func chipsByKind(kind int) []Chip {
	var out []Chip
	for _, c := range chips {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

func rgb(c uint32) (uint8, uint8, uint8) {
	return uint8(c >> 16), uint8(c >> 8), uint8(c)
}
