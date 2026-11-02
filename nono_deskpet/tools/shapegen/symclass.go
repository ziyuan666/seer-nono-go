package main

import (
	"encoding/binary"
	"strings"
)

// parseSymbolClass decodes SymbolClass (code 76) raw payload: count u16, then (tagId u16, name cstring)*
func parseSymbolClass(data []byte) map[string]uint16 {
	out := map[string]uint16{}
	if len(data) < 2 {
		return out
	}
	n := int(binary.LittleEndian.Uint16(data[:2]))
	off := 2
	for i := 0; i < n && off+2 <= len(data); i++ {
		id := binary.LittleEndian.Uint16(data[off : off+2])
		off += 2
		end := off
		for end < len(data) && data[end] != 0 {
			end++
		}
		name := strings.TrimSpace(string(data[off:end]))
		if name != "" {
			out[name] = id
		}
		off = end + 1
	}
	return out
}
