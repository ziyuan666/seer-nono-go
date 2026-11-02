//go:build windows

package main

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
)

// NONO 台词池。
// 数据来源：原版 MachXMLInfo.xml 的 lines 段（27 条，2026-10-23 从
// /app/working/workspaces/a2ZDuB/kaiyuanhao/swf解包/NieoCore/binaryData/22_...MachXMLInfo_xmlClass.bin 提取）。
//
// 与原版的唯一差异：原版 getLinesIDForExp 会按 energy/mate 档位过滤台词，
// 子渊 2026-10-23 要求「不按等级，全部通用」，因此这里不做任何档位筛选；
// 只保留 exp 段 lines 属性定义的「哪个表情配哪几句」结构，
// 避免开心表情配「能源不足」这类语义错位。

type lineItem struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

type lineConfig struct {
	Lines    []lineItem       `json:"lines"`
	ExpLines map[string][]int `json:"expLines"`
}

var (
	lineCfg  *lineConfig
	lineByID = map[int]string{}
)

// loadLines 读取 assets/lines.json。
// 失败不阻塞启动（exe 与 assets 必须同目录，缺文件时退回 fallbackLines）。
func loadLines() error {
	b, err := os.ReadFile(filepath.Join(assetsRoot(), "lines.json"))
	if err != nil {
		return err
	}
	var cfg lineConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	lineCfg = &cfg
	for _, it := range cfg.Lines {
		lineByID[it.ID] = it.Text
	}
	return nil
}

// lineForExp 返回指定表情配的一句台词。
// expID 为 0 或该表情没有专属台词时从全池随机，保证任何形态都能开口。
func lineForExp(expID int) string {
	if lineCfg == nil || len(lineCfg.Lines) == 0 {
		return fallbackLines[rand.Intn(len(fallbackLines))]
	}
	if ids := lineCfg.ExpLines[itoa(expID)]; len(ids) > 0 {
		if s := lineByID[ids[rand.Intn(len(ids))]]; s != "" {
			return s
		}
	}
	return lineCfg.Lines[rand.Intn(len(lineCfg.Lines))].Text
}

// fallbackLines 仅在 assets/lines.json 缺失时使用。
var fallbackLines = []string{"早上好！", "叭叭，叭叭…", "主人，主人～", "交给我吧", "太好了"}

// sayFor 冒一句台词气泡，时长按秒给（原版独立态 showBox(name, 12)）。
// 刻意不复用 setBubble 的固定 90 tick，否则台词只闪 3.6 秒。
func (g *Game) sayFor(s string, seconds int) {
	if s == "" || seconds <= 0 {
		return
	}
	g.Bubble = s
	g.BubbleW = estimateTextW(s)
	g.BubbleTicks = seconds * 1000 / tickMs
}
