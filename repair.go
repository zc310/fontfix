// Package fontfix repairs small structural defects in embedded SFNT fonts.
//
// This package is derived from the font repair implementation in
// github.com/xiaoqidun/ofdgo, Copyright 2025-2026 Xiao Qi Dun, and has been
// substantially simplified and modified to expose a standalone Repair API.
package fontfix

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// Repair adds a minimal OS/2 table when an SFNT font is missing it and adds a
// format 12 cmap mapping for each glyph ID when the font has no such mapping.
// TrueType and OpenType fonts are supported. Non-SFNT data is returned
// unchanged so callers can pass through other embedded font formats.
func Repair(data []byte) ([]byte, error) {
	if len(data) < 4 {
		return data, nil
	}
	if isBareCFF(data) {
		return wrapCFF(data)
	}
	tag := string(data[:4])
	if tag != "OTTO" && tag != "true" && binary.BigEndian.Uint32(data[:4]) != 0x00010000 {
		return data, nil
	}
	if len(data) < sfntHeaderSize {
		return data, fmt.Errorf("font header is too short")
	}

	numTables := int(binary.BigEndian.Uint16(data[4:6]))
	directoryEnd := sfntHeaderSize + numTables*sfntTableEntrySize
	if directoryEnd > len(data) {
		return data, fmt.Errorf("font table directory is truncated")
	}

	tables := make([]sfntTable, 0, numTables+1)
	hasOS2 := false
	hasName := false
	hasPost := false
	maxGlyphs := uint16(0)
	cmapIndex := -1
	for offset := sfntHeaderSize; offset < directoryEnd; offset += sfntTableEntrySize {
		tag := string(data[offset : offset+4])
		tableOffset := int(binary.BigEndian.Uint32(data[offset+8 : offset+12]))
		tableLength := int(binary.BigEndian.Uint32(data[offset+12 : offset+16]))
		if tableOffset > len(data) || tableLength > len(data)-tableOffset {
			return data, fmt.Errorf("font table %q is out of bounds", tag)
		}
		tableData := append([]byte(nil), data[tableOffset:tableOffset+tableLength]...)
		tables = append(tables, sfntTable{tag: tag, data: tableData})
		switch tag {
		case "OS/2":
			hasOS2 = true
		case "name":
			hasName = true
		case "post":
			hasPost = true
		case "maxp":
			if tableLength >= 6 {
				maxGlyphs = binary.BigEndian.Uint16(tableData[4:6])
			}
		case "cmap":
			cmapIndex = len(tables) - 1
		}
	}

	needsGlyphMap := cmapIndex >= 0 && maxGlyphs > 0 && !hasPackedGlyphMap(tables[cmapIndex].data)
	if cmapIndex < 0 && maxGlyphs > 0 {
		tables = append(tables, sfntTable{tag: "cmap", data: packedGlyphCmap(maxGlyphs)})
		cmapIndex = len(tables) - 1
		needsGlyphMap = false
	}
	if hasOS2 && hasName && hasPost && !needsGlyphMap {
		return data, nil
	}
	if !hasOS2 {
		tables = append(tables, sfntTable{tag: "OS/2", data: minimalOS2Table()})
	}
	if !hasName {
		tables = append(tables, sfntTable{tag: "name", data: minimalNameTable()})
	}
	if !hasPost {
		tables = append(tables, sfntTable{tag: "post", data: minimalPostTable()})
	}
	if needsGlyphMap {
		tables[cmapIndex].data = addPackedGlyphMap(tables[cmapIndex].data, maxGlyphs)
	}
	sort.Slice(tables, func(i, j int) bool { return tables[i].tag < tables[j].tag })

	return rebuildSFNT(data[:4], tables), nil
}
