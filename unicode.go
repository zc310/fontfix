package fontfix

import (
	"encoding/binary"
	"errors"
	"sort"
)

var errTruncated = errors.New("font table directory is truncated")

// GlyphMapping 表示一个 Unicode 码位到字形 ID 的映射。
type GlyphMapping struct {
	Rune  rune
	Glyph uint16
}

// RepairWithGlyphs 在 Repair 的基础上，用调用方提供的 Unicode→字形映射补齐
// 字体的 cmap。部分 OFD 子集字体（尤其是只保留 glyf、无 cmap 的 TrueType 子集）
// 只能通过字形 ID 定位字形，正常文字接口无法按原始文本成形；调用方可以从文档
// 自身的 TextCode.Value 与 CGTransform.Glyphs 得到权威映射，交给本函数注入后
// 即可用原始 Unicode 文本渲染，从而在 PDF 等输出中保留可复制、可搜索的文字。
//
// 字体已有的 cmap 映射会保留，码位冲突时以 mappings 为准。非 SFNT 输入按
// Repair 的规则原样返回。
func RepairWithGlyphs(data []byte, mappings []GlyphMapping) ([]byte, error) {
	fixed, err := Repair(data)
	if err != nil {
		return data, err
	}
	if len(mappings) == 0 {
		return fixed, nil
	}
	overrides := make(map[uint32]uint16, len(mappings))
	for _, mapping := range mappings {
		if mapping.Rune <= 0 || mapping.Glyph == 0 {
			continue
		}
		overrides[uint32(mapping.Rune)] = mapping.Glyph
	}
	if len(overrides) == 0 {
		return fixed, nil
	}

	pairs := parseCMapPairs(fixed)
	// Repair 会为每个字形补齐私有区映射。CID CFF 的私有区按 CID 编号，而调用方
	// 从 OFD CGTransform.Glyphs 拿到的是 CID，不是字形索引；这里先按私有区映射
	// 把 CID 翻译成真正的字形 ID，TrueType 字体的调用值本就是字形 ID，翻译后不变。
	glyphByPacked := make(map[uint32]uint16, len(pairs))
	for _, pair := range pairs {
		if pair.code >= uint32(packedGlyphBase) {
			glyphByPacked[pair.code] = pair.glyph
		}
	}
	resolve := func(value uint16) uint16 {
		if glyph, ok := glyphByPacked[uint32(packedGlyphBase)+uint32(value)]; ok {
			return glyph
		}
		return value
	}

	// 调用方映射是文档的权威映射：同一字形若还存在其它 Unicode 码位映射
	// （例如 CID CFF 由 cffCIDCmap 补入的 Adobe-GB1 标准 CID→Unicode 映射），
	// 必须移除。否则字体 cmap 反查（GlyphToUnicode）会按码位大小返回陈旧码位，
	// 使 PDF ToUnicode 把文字提取成错误字符。私有区映射（packedGlyphBase+CID）
	// 用于按字形 ID 渲染，予以保留。
	overrideGlyph := make(map[uint16]uint32, len(overrides))
	for code, glyph := range overrides {
		overrideGlyph[resolve(glyph)] = code
	}
	merged := make([]cmapPair, 0, len(pairs)+len(overrides))
	seen := make(map[uint32]bool, len(pairs)+len(overrides))
	for _, pair := range pairs {
		if glyph, ok := overrides[pair.code]; ok {
			pair.glyph = resolve(glyph)
		} else if pair.code < uint32(packedGlyphBase) {
			if code, ok := overrideGlyph[pair.glyph]; ok && code != pair.code {
				continue
			}
		}
		if seen[pair.code] {
			continue
		}
		seen[pair.code] = true
		merged = append(merged, pair)
	}
	for code, glyph := range overrides {
		if seen[code] {
			continue
		}
		seen[code] = true
		merged = append(merged, cmapPair{code: code, glyph: resolve(glyph)})
	}

	cmap := cmapFromPairs(merged)
	if cmap == nil {
		return fixed, nil
	}
	result, err := replaceTable(fixed, "cmap", cmap)
	if err != nil {
		return fixed, nil
	}
	return result, nil
}

// parseCMapPairs 解析字体 cmap 表中的 code→glyph 映射，支持常见的 format 4
// 和 format 12 子表。
func parseCMapPairs(data []byte) []cmapPair {
	cmap, ok := tableData(data, "cmap")
	if !ok || len(cmap) < 4 {
		return nil
	}
	numTables := int(binary.BigEndian.Uint16(cmap[2:4]))
	pairs := make([]cmapPair, 0)
	parsed := make(map[uint32]bool)
	for i := 0; i < numTables; i++ {
		record := 4 + i*8
		if record+8 > len(cmap) {
			break
		}
		offset := int(binary.BigEndian.Uint32(cmap[record+4 : record+8]))
		if offset < 0 || offset+2 > len(cmap) || parsed[uint32(offset)] {
			continue
		}
		parsed[uint32(offset)] = true
		switch binary.BigEndian.Uint16(cmap[offset : offset+2]) {
		case 4:
			pairs = append(pairs, parseCMapFormat4(cmap[offset:])...)
		case 12:
			pairs = append(pairs, parseCMapFormat12(cmap[offset:])...)
		}
	}
	return pairs
}

func parseCMapFormat4(data []byte) []cmapPair {
	if len(data) < 14 {
		return nil
	}
	segCountX2 := int(binary.BigEndian.Uint16(data[6:8]))
	segCount := segCountX2 / 2
	if segCount == 0 {
		return nil
	}
	endCodes := 14
	startCodes := endCodes + segCountX2 + 2
	idDeltas := startCodes + segCountX2
	idRangeOffsets := idDeltas + segCountX2
	if idRangeOffsets+segCountX2 > len(data) {
		return nil
	}
	pairs := make([]cmapPair, 0, segCount)
	for segment := 0; segment < segCount; segment++ {
		start := binary.BigEndian.Uint16(data[startCodes+segment*2 : startCodes+segment*2+2])
		end := binary.BigEndian.Uint16(data[endCodes+segment*2 : endCodes+segment*2+2])
		delta := binary.BigEndian.Uint16(data[idDeltas+segment*2 : idDeltas+segment*2+2])
		rangeOffsetPos := idRangeOffsets + segment*2
		rangeOffset := binary.BigEndian.Uint16(data[rangeOffsetPos : rangeOffsetPos+2])
		if start == 0xFFFF {
			continue
		}
		for code := uint32(start); code <= uint32(end); code++ {
			var glyph uint16
			if rangeOffset == 0 {
				glyph = uint16(code) + delta
			} else {
				index := rangeOffsetPos + int(rangeOffset) + int(code-uint32(start))*2
				if index+2 > len(data) {
					continue
				}
				glyph = binary.BigEndian.Uint16(data[index : index+2])
				if glyph != 0 {
					glyph += delta
				}
			}
			if glyph != 0 {
				pairs = append(pairs, cmapPair{code: code, glyph: glyph})
			}
		}
	}
	return pairs
}

func parseCMapFormat12(data []byte) []cmapPair {
	if len(data) < 16 {
		return nil
	}
	groups := int(binary.BigEndian.Uint32(data[12:16]))
	if groups <= 0 {
		return nil
	}
	if 16+groups*12 > len(data) {
		return nil
	}
	pairs := make([]cmapPair, 0, groups)
	for group := 0; group < groups; group++ {
		pos := 16 + group*12
		start := binary.BigEndian.Uint32(data[pos : pos+4])
		end := binary.BigEndian.Uint32(data[pos+4 : pos+8])
		glyph := binary.BigEndian.Uint32(data[pos+8 : pos+12])
		if end < start || end-start > 0x10000 {
			continue
		}
		for code := start; code <= end; code++ {
			if glyph > 0xFFFF {
				break
			}
			if glyph != 0 {
				pairs = append(pairs, cmapPair{code: code, glyph: uint16(glyph)})
			}
			glyph++
		}
	}
	return pairs
}

// tableData 从 SFNT 字体目录中返回指定表的副本。
func tableData(data []byte, tag string) ([]byte, bool) {
	if len(data) < sfntHeaderSize {
		return nil, false
	}
	numTables := int(binary.BigEndian.Uint16(data[4:6]))
	for i := 0; i < numTables; i++ {
		record := sfntHeaderSize + i*sfntTableEntrySize
		if record+sfntTableEntrySize > len(data) {
			return nil, false
		}
		if string(data[record:record+4]) != tag {
			continue
		}
		offset := int(binary.BigEndian.Uint32(data[record+8 : record+12]))
		length := int(binary.BigEndian.Uint32(data[record+12 : record+16]))
		if offset > len(data) || length > len(data)-offset {
			return nil, false
		}
		return data[offset : offset+length], true
	}
	return nil, false
}

// replaceTable 返回把指定表替换为新数据的字体副本。
func replaceTable(data []byte, tag string, replacement []byte) ([]byte, error) {
	if len(data) < sfntHeaderSize {
		return nil, errTruncated
	}
	numTables := int(binary.BigEndian.Uint16(data[4:6]))
	directoryEnd := sfntHeaderSize + numTables*sfntTableEntrySize
	if directoryEnd > len(data) {
		return nil, errTruncated
	}
	tables := make([]sfntTable, 0, numTables+1)
	replaced := false
	for i := 0; i < numTables; i++ {
		record := sfntHeaderSize + i*sfntTableEntrySize
		current := string(data[record : record+4])
		offset := int(binary.BigEndian.Uint32(data[record+8 : record+12]))
		length := int(binary.BigEndian.Uint32(data[record+12 : record+16]))
		if offset > len(data) || length > len(data)-offset {
			return nil, errTruncated
		}
		if current == tag {
			tables = append(tables, sfntTable{tag: current, data: append([]byte(nil), replacement...)})
			replaced = true
			continue
		}
		tables = append(tables, sfntTable{tag: current, data: append([]byte(nil), data[offset:offset+length]...)})
	}
	if !replaced {
		tables = append(tables, sfntTable{tag: tag, data: append([]byte(nil), replacement...)})
	}
	sort.Slice(tables, func(i, j int) bool { return tables[i].tag < tables[j].tag })
	return rebuildSFNT(data[:4], tables), nil
}
