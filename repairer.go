package fontfix

import "slices"

// Repairer 复用一次基础修复的结果，反复按不同的字形映射修补 cmap。
//
// OFD 文档逐页登记 Unicode→字形映射：某一页出现新字符时，该内嵌字体的解析
// 缓存就失效并被完整重新修复。整篇文档因此会反复对同一份字体字节跑
// RepairWithGlyphs，而 Repair 与 cmap 解析的结果只取决于原始字节，与映射集合
// 无关。Repairer 把这部分固定下来，映射增长时只需重新合并与输出 cmap 表。
//
// NewRepairer 与 WithGlyphs 不修改已缓存的 base、pairs，可由多个 goroutine
// 并发调用；但 data 必须在整个生命周期内保持不变。
type Repairer struct {
	data []byte
	// base 是 Repair 之后的基础字体，nil 表示尚不可修补。
	base []byte
	// pairs 是 base 的 cmap 映射，按 code 升序且 code 唯一。
	pairs  []cmapPair
	usable bool
}

// NewRepairer 为一份内嵌字体字节创建可复用的修复器。data 必须在 Repairer
// 生命周期内保持不变且不被改写。
func NewRepairer(data []byte) *Repairer {
	r := &Repairer{data: data}
	base, err := Repair(data)
	if err != nil || len(base) == 0 {
		return r
	}
	// 非 SFNT 输入 Repair 会原样返回，此时没有可修补的 cmap。
	if !isSFNT(base) {
		return r
	}
	pairs := parseCMapPairs(base)
	slices.SortFunc(pairs, compareCMapPair)
	r.base = base
	r.pairs = pairs
	r.usable = true
	return r
}

// WithGlyphs 返回按 mappings 修补 cmap 后的字体副本，语义与
// RepairWithGlyphs(r.data, mappings) 一致：字体已有的 cmap 映射会保留，
// 码位冲突时以 mappings 为准。
func (r *Repairer) WithGlyphs(mappings []GlyphMapping) ([]byte, error) {
	if r == nil {
		return nil, errTruncated
	}
	if !r.usable {
		return RepairWithGlyphs(r.data, mappings)
	}
	if len(mappings) == 0 {
		return append([]byte(nil), r.base...), nil
	}
	overrides := make(map[uint32]uint16, len(mappings))
	for _, mapping := range mappings {
		if mapping.Rune <= 0 || mapping.Glyph == 0 {
			continue
		}
		overrides[uint32(mapping.Rune)] = mapping.Glyph
	}
	if len(overrides) == 0 {
		return append([]byte(nil), r.base...), nil
	}

	// Caller mappings are authoritative: remove other Unicode codes for the same glyph,
	// such as standard Adobe-GB1 CID mappings, or GlyphToUnicode may return stale codes.
	// Private-use entries are keyed by GID; caller GIDs take precedence over CID aliases.
	overrideGlyph, overridePacked := glyphCMapOverrides(overrides)

	// r.pairs 已按 code 升序。把 overrides 也排好序后做一次有序归并，
	// 避免每次修补都对全部码位重新排序。
	additions := make([]cmapPair, 0, len(overrides))
	for code, glyph := range overrides {
		additions = append(additions, cmapPair{code: code, glyph: glyph})
	}
	slices.SortFunc(additions, compareCMapPair)

	merged := make([]cmapPair, 0, len(r.pairs)+len(additions))
	seen := make(map[uint32]bool, len(r.pairs)+len(additions))
	i, j := 0, 0
	for i < len(r.pairs) || j < len(additions) {
		var next cmapPair
		// fromBase 区分该条来自基础 cmap 还是映射表：让位规则只作用于前者。
		fromBase := false
		switch {
		case j >= len(additions) || (i < len(r.pairs) && r.pairs[i].code < additions[j].code):
			next = r.pairs[i]
			fromBase = true
			i++
		case i >= len(r.pairs) || additions[j].code < r.pairs[i].code:
			next = additions[j]
			j++
		default:
			// 码位相同：基础 cmap 与映射表都有，以映射表为准。
			next = additions[j]
			i++
			j++
		}
		if glyph, ok := overridePacked[next.code]; ok {
			next.glyph = glyph
		}
		if fromBase && next.code < uint32(packedGlyphBase) {
			if code, ok := overrideGlyph[next.glyph]; ok && code != next.code {
				continue
			}
		}
		if seen[next.code] {
			continue
		}
		seen[next.code] = true
		merged = append(merged, next)
	}
	for code, glyph := range overridePacked {
		if seen[code] {
			continue
		}
		seen[code] = true
		merged = append(merged, cmapPair{code: code, glyph: glyph})
	}

	cmap := cmapFromPairs(merged)
	if cmap == nil {
		return append([]byte(nil), r.base...), nil
	}
	result, err := replaceTable(r.base, "cmap", cmap)
	if err != nil {
		return append([]byte(nil), r.base...), nil
	}
	return result, nil
}

// compareCMapPair 以 code 为首键、glyph 为次键，构成全序，保证排序结果确定。
func compareCMapPair(a, b cmapPair) int {
	if a.code != b.code {
		if a.code < b.code {
			return -1
		}
		return 1
	}
	return int(a.glyph) - int(b.glyph)
}

// isSFNT 判断字节是否以 SFNT 版本号开头。
func isSFNT(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	tag := string(data[:4])
	return tag == "OTTO" || tag == "true" || beUint32(data[:4]) == 0x00010000
}

func beUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
