package fontfix

import "slices"

// Repairer 复用一次基础修复的结果，反复按不同的字形映射修补 cmap。
//
// OFD 文档逐页登记 Unicode→字形映射：某一页出现新字符时，该内嵌字体的解析
// 缓存就失效并被完整重新修复。整篇文档因此会反复对同一份字体字节跑
// RepairWithGlyphs，而 Repair 与 cmap 解析的结果只取决于原始字节，与映射集合
// 无关。Repairer 把这部分固定下来，映射增长时只需重新合并与输出 cmap 表。
//
// NewRepairer 与 WithGlyphs 都不会改写 Repairer 已缓存的字段（base、pairs、
// glyphByPacked 只读），因此多个 goroutine 可共享同一个 Repairer 并发调用
// WithGlyphs；但 data 必须在整个生命周期内保持不变。
type Repairer struct {
	data []byte
	// base 是 Repair 之后的基础字体，nil 表示尚不可修补。
	base []byte
	// pairs 是 base 的 cmap 映射，按 code 升序且 code 唯一。
	pairs []cmapPair
	// glyphByPacked 把私有区码位翻译成真正字形 ID，仅 CID CFF 需要。
	glyphByPacked map[uint32]uint16
	// resolveNone 表示 glyphByPacked 为空、调用方映射无需翻译。
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

	// 调用方从 OFD CGTransform.Glyphs 拿到的是 CID 而非字形索引，CID CFF 的
	// 私有区按 CID 编号，需要按私有区映射翻译回真正字形 ID。TrueType 字体的
	// 调用值本就是字形 ID，翻译结果不变，此时整张映射表都可以省掉。
	if cff, ok := tableData(base, "CFF "); ok && cffCIDKeyed(cff) {
		byPacked := make(map[uint32]uint16, len(pairs))
		for _, pair := range pairs {
			if pair.code >= uint32(packedGlyphBase) {
				byPacked[pair.code] = pair.glyph
			}
		}
		r.glyphByPacked = byPacked
	}
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

	// 调用方映射是文档的权威映射：同一字形若还存在其它 Unicode 码位映射
	// （例如 CID CFF 由 cffCIDCmap 补入的 Adobe-GB1 标准 CID→Unicode 映射），
	// 必须移除。否则字体 cmap 反查（GlyphToUnicode）会按码位大小返回陈旧码位，
	// 使 PDF ToUnicode 把文字提取成错误字符。私有区映射（packedGlyphBase+CID）
	// 用于按字形 ID 渲染，予以保留。
	overrideGlyph := make(map[uint16]uint32, len(overrides))
	for code, glyph := range overrides {
		overrideGlyph[r.resolve(glyph)] = code
	}

	// r.pairs 已按 code 升序。把 overrides 也排好序后做一次有序归并，
	// 避免每次修补都对全部码位重新排序。
	additions := make([]cmapPair, 0, len(overrides))
	for code, glyph := range overrides {
		additions = append(additions, cmapPair{code: code, glyph: r.resolve(glyph)})
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

// resolve 把 CID CFF 的私有区 CID 翻译成真正字形 ID；其它字体原样返回。
func (r *Repairer) resolve(value uint16) uint16 {
	if len(r.glyphByPacked) == 0 {
		return value
	}
	if glyph, ok := r.glyphByPacked[uint32(packedGlyphBase)+uint32(value)]; ok {
		return glyph
	}
	return value
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
