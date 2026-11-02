package main

import (
	"bytes"

	"git.gammaspectra.live/WeebDataHoarder/swf-go/types"
	"github.com/icza/bitio"
)

// ButtonRec 是 DefineButton2 的一条 ButtonRecord。
type ButtonRec struct {
	Flags  uint8
	CharID uint16
	Depth  uint16
	Matrix types.MATRIX
	CXForm types.CXFORMWITHALPHA
}

type Button struct {
	ID      uint16
	Records []ButtonRec
}

// parseButton2 手写解析 DefineButton2（swf-go 未实现 code 34）。
// 布局：buttonId u16 | flags u8 | actionOffset u16 | ButtonRecord*，ButtonRecord 以 flags==0 结束。
// ButtonRecord：stateFlags u8, charId u16, depth u16, matrix, cxform。
func parseButton2(data []byte, ver uint8) (*Button, error) {
	if len(data) < 5 {
		return nil, nil
	}
	b := &Button{ID: uint16(data[0]) | uint16(data[1])<<8}
	r := bitio.NewReader(bytes.NewReader(data[5:]))
	ctx := types.ReaderContext{Version: ver}
	for i := 0; i < 256; i++ {
		var flags uint8
		if err := types.ReadU8(r, &flags); err != nil {
			break
		}
		r.Align()
		if flags == 0 {
			break
		}
		var charID, depth uint16
		if err := types.ReadU16(r, &charID); err != nil {
			break
		}
		if err := types.ReadU16(r, &depth); err != nil {
			break
		}
		var m types.MATRIX
		if err := types.ReadType(r, ctx, &m); err != nil {
			break
		}
		var cx types.CXFORMWITHALPHA
		_ = types.ReadType(r, ctx, &cx)
		b.Records = append(b.Records, ButtonRec{Flags: flags, CharID: charID, Depth: depth, Matrix: m, CXForm: cx})
	}
	return b, nil
}
