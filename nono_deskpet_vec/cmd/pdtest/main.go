package main

import (
	"encoding/json"
	"fmt"
	"os"

	"nonovec/pkg/pathd"
)

type partFile struct {
	ID    string `json:"id"`
	Paths []struct {
		D        string     `json:"d"`
		Matrix   [6]float64 `json:"matrix"`
		FillRule string     `json:"fillRule"`
		Gradient *struct {
			Type   string     `json:"type"`
			Matrix [6]float64 `json:"matrix"`
		} `json:"gradient"`
	} `json:"paths"`
}

func main() {
	b, _ := os.ReadFile(os.Args[1])
	var pf partFile
	json.Unmarshal(b, &pf)
	for i, p := range pf.Paths {
		segs, err := pathd.Parse(p.D)
		if err != nil {
			fmt.Printf("[%d] Parse 失败: %v\n", i, err)
			continue
		}
		poly := pathd.Polyline(segs, 1.0)
		np := 0
		for _, q := range poly {
			np += len(q)
		}
		fmt.Printf("[%d] segs=%d polys=%d points=%d grad=%v\n", i, len(segs), len(poly), np, p.Gradient != nil)
	}
}
