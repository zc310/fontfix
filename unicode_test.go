package fontfix

import (
	"encoding/binary"
	"testing"
)

func TestRepairWithGlyphsAddsUnicodeMapping(t *testing.T) {
	// 无 cmap 的子集字体经 Repair 只会得到 PUA 映射；RepairWithGlyphs 应补上
	// 调用方提供的 Unicode→字形映射，使原始文本可以走正常文字接口渲染。
	fixed, err := RepairWithGlyphs(testFontWithoutPackedGlyphCmap(), []GlyphMapping{{Rune: '中', Glyph: 2}})
	if err != nil {
		t.Fatal(err)
	}
	pairs := parseCMapPairs(fixed)
	glyphs := make(map[uint32]uint16, len(pairs))
	for _, pair := range pairs {
		glyphs[pair.code] = pair.glyph
	}
	if glyphs[uint32('中')] != 2 {
		t.Fatalf("cmap['中'] = %d, want 2", glyphs[uint32('中')])
	}
	if glyphs[uint32(packedGlyphBase)+2] != 2 {
		t.Fatalf("cmap[PUA+2] = %d, want 2", glyphs[uint32(packedGlyphBase)+2])
	}
}

func TestRepairWithGlyphsPreservesExistingMappings(t *testing.T) {
	// 字体已有 cmap 时，未冲突的既有映射必须保留。
	base := testFontWithoutPackedGlyphCmap()
	fixed, err := Repair(base)
	if err != nil {
		t.Fatal(err)
	}
	unicode, err := RepairWithGlyphs(fixed, []GlyphMapping{{Rune: 'A', Glyph: 1}})
	if err != nil {
		t.Fatal(err)
	}
	pairs := parseCMapPairs(unicode)
	glyphs := make(map[uint32]uint16, len(pairs))
	for _, pair := range pairs {
		glyphs[pair.code] = pair.glyph
	}
	if glyphs[uint32('A')] != 1 {
		t.Fatalf("cmap['A'] = %d, want 1", glyphs[uint32('A')])
	}
	if glyphs[uint32(packedGlyphBase)+1] != 1 {
		t.Fatalf("PUA mapping lost: cmap[PUA+1] = %d", glyphs[uint32(packedGlyphBase)+1])
	}
}

func TestRepairWithGlyphsNoMappingsIsRepair(t *testing.T) {
	base := testFontWithoutPackedGlyphCmap()
	fixed, err := RepairWithGlyphs(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := Repair(base)
	if err != nil {
		t.Fatal(err)
	}
	if string(fixed) != string(repaired) {
		t.Fatal("RepairWithGlyphs without mappings should equal Repair")
	}
}

func TestParseCMapFormat4(t *testing.T) {
	// 构造一个最小 format 4 子表：码位 0x41→字形 1。
	segCount := 2 // 含结尾 0xFFFF 段
	subtable := make([]byte, 14+segCount*8+2)
	binary.BigEndian.PutUint16(subtable[0:2], 4)
	binary.BigEndian.PutUint16(subtable[2:4], uint16(len(subtable)))
	binary.BigEndian.PutUint16(subtable[6:8], uint16(segCount*2))
	endCodes := 14
	startCodes := endCodes + segCount*2 + 2
	idDeltas := startCodes + segCount*2
	binary.BigEndian.PutUint16(subtable[endCodes:endCodes+2], 0x41)
	binary.BigEndian.PutUint16(subtable[startCodes:startCodes+2], 0x41)
	delta := uint16(1)
	delta -= uint16(0x41)
	binary.BigEndian.PutUint16(subtable[idDeltas:idDeltas+2], delta)
	binary.BigEndian.PutUint16(subtable[endCodes+2:endCodes+4], 0xFFFF)
	binary.BigEndian.PutUint16(subtable[startCodes+2:startCodes+4], 0xFFFF)
	binary.BigEndian.PutUint16(subtable[idDeltas+2:idDeltas+4], 1)

	pairs := parseCMapFormat4(subtable)
	if len(pairs) != 1 || pairs[0].code != 0x41 || pairs[0].glyph != 1 {
		t.Fatalf("format4 pairs = %+v", pairs)
	}
}
