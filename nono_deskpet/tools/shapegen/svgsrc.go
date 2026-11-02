package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ShapeSVG 是 FFDec 导出的单个 shape 的 SVG 片段。
type ShapeSVG struct {
	Defs   string
	Body   string
	Width  float64
	Height float64
}

var reSVGSize = regexp.MustCompile(`height="([0-9.]+)px" width="([0-9.]+)px"`)
var reSVGSize2 = regexp.MustCompile(`width="([0-9.]+)px" height="([0-9.]+)px"`)
var reID = regexp.MustCompile(`id="([^"]+)"`)
var reDefsBlock = regexp.MustCompile(`(?s)<defs>(.*?)</defs>`)

// LoadShapeSVG 读取 FFDec 导出的 shape SVG，抽出内部内容（保留 <g> 结构）与 defs。
// 之所以不用严格正则匹配 <g>，是因为部分 shape 没有 <g> 包裹或多层嵌套。
func LoadShapeSVG(path string) (*ShapeSVG, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := string(b)
	out := &ShapeSVG{}
	if m := reSVGSize.FindStringSubmatch(s); len(m) == 3 {
		fmt.Sscanf(m[1], "%f", &out.Height)
		fmt.Sscanf(m[2], "%f", &out.Width)
	} else if m := reSVGSize2.FindStringSubmatch(s); len(m) == 3 {
		fmt.Sscanf(m[1], "%f", &out.Width)
		fmt.Sscanf(m[2], "%f", &out.Height)
	}
	open := strings.Index(s, "<svg")
	if open < 0 {
		return nil, fmt.Errorf("not svg: %s", path)
	}
	start := strings.Index(s[open:], ">")
	if start < 0 {
		return nil, fmt.Errorf("bad svg header: %s", path)
	}
	start += open + 1
	end := strings.LastIndex(s, "</svg>")
	if end <= start {
		return nil, fmt.Errorf("bad svg end: %s", path)
	}
	inner := s[start:end]
	if m := reDefsBlock.FindStringSubmatch(inner); len(m) == 2 {
		out.Defs = strings.TrimSpace(m[1])
		inner = strings.Replace(inner, m[0], "", 1)
	}
	out.Body = strings.TrimSpace(inner)
	if out.Body == "" {
		return nil, fmt.Errorf("empty body: %s", path)
	}
	return out, nil
}

// prefixIDs 给渐变 id 加前缀，避免合成时冲突。
func (s *ShapeSVG) prefixIDs(prefix string) (defs, body string) {
	ids := map[string]bool{}
	for _, m := range reID.FindAllStringSubmatch(s.Defs, -1) {
		ids[m[1]] = true
	}
	defs, body = s.Defs, s.Body
	for id := range ids {
		defs = strings.ReplaceAll(defs, `id="`+id+`"`, `id="`+prefix+id+`"`)
		body = strings.ReplaceAll(body, "url(#"+id+")", "url(#"+prefix+id+")")
	}
	return defs, body
}
