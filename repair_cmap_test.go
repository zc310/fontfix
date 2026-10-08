package fontfix

import (
	"encoding/binary"
	"testing"
)

func TestRepairAddsPackedGlyphCmap(t *testing.T) {
	fixed, err := Repair(testFontWithoutPackedGlyphCmap())
	if err != nil {
		t.Fatal(err)
	}
	cmap, ok := findTable(fixed, "cmap")
	if !ok {
		t.Fatal("cmap table was removed")
	}
	if !hasPackedGlyphMap(cmap) {
		t.Fatal("packed glyph cmap was not added")
	}
	if got, want := packedGlyphStart(cmap), uint32(packedGlyphBase); got != want {
		t.Fatalf("packed glyph cmap starts at %#x, want %#x", got, want)
	}
	if got, want := packedGlyphEnd(cmap), uint32(packedGlyphBase)+2; got != want {
		t.Fatalf("packed glyph cmap ends at %#x, want %#x", got, want)
	}

	second, err := Repair(fixed)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(fixed) {
		t.Fatal("repair with packed glyph cmap was not idempotent")
	}
}

func TestAdobeGB1CIDToUnicode(t *testing.T) {
	for cid, want := range map[int]rune{
		1036: '\u4fdd',
		2584: '\u6599',
		2785: '\u5bc6',
		4647: '\u8d44',
	} {
		if got, ok := AdobeGB1CIDToUnicode(cid); !ok || got != want {
			t.Fatalf("CID %d = %q, %v; want %q", cid, got, ok, want)
		}
	}
}

func testFontWithoutPackedGlyphCmap() []byte {
	head := make([]byte, 12)
	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:6], 3)
	cmap := make([]byte, 24)
	binary.BigEndian.PutUint16(cmap[2:4], 1)
	binary.BigEndian.PutUint16(cmap[4:6], 3)
	binary.BigEndian.PutUint16(cmap[6:8], 1)
	binary.BigEndian.PutUint32(cmap[8:12], 12)
	binary.BigEndian.PutUint16(cmap[12:14], 4)
	binary.BigEndian.PutUint16(cmap[14:16], 12)
	return buildSFNT([]sfntTestTable{
		{tag: "head", data: head},
		{tag: "maxp", data: maxp},
		{tag: "cmap", data: cmap},
	})
}

type sfntTestTable struct {
	tag  string
	data []byte
}

func buildSFNT(tables []sfntTestTable) []byte {
	data := make([]byte, 12+len(tables)*16)
	binary.BigEndian.PutUint32(data[:4], 0x00010000)
	binary.BigEndian.PutUint16(data[4:6], uint16(len(tables)))
	offset := len(data)
	for i, table := range tables {
		record := 12 + i*16
		copy(data[record:record+4], table.tag)
		binary.BigEndian.PutUint32(data[record+8:record+12], uint32(offset))
		binary.BigEndian.PutUint32(data[record+12:record+16], uint32(len(table.data)))
		data = append(data, table.data...)
		for len(data)%4 != 0 {
			data = append(data, 0)
		}
		offset = len(data)
	}
	return data
}

func packedGlyphStart(data []byte) uint32 {
	return packedGlyphRange(data, 16)
}

func packedGlyphEnd(data []byte) uint32 {
	return packedGlyphRange(data, 20)
}

func packedGlyphRange(data []byte, field int) uint32 {
	count := int(binary.BigEndian.Uint16(data[2:4]))
	for i := 0; i < count; i++ {
		offset := int(binary.BigEndian.Uint32(data[4+i*8+4 : 4+i*8+8]))
		if offset+16 <= len(data) && binary.BigEndian.Uint16(data[offset:offset+2]) == 12 && binary.BigEndian.Uint32(data[offset+12:offset+16]) > 0 {
			return binary.BigEndian.Uint32(data[offset+field : offset+field+4])
		}
	}
	return 0
}

func TestRepairWithGlyphsReplacesStaleGlyphMapping(t *testing.T) {
	// 字体可能已有「陈旧」的 Unicode→字形映射（例如 CID CFF 由 cffCIDCmap 补入
	// 的 Adobe-GB1 标准 CID→Unicode 映射）。调用方用文档权威映射覆盖同一字形
	// 时，必须移除旧码位，否则字体 cmap 反查（GlyphToUnicode）会按码位大小返回
	// 陈旧码位，使 PDF ToUnicode 把文字提取成错误字符。
	base := testFontWithoutPackedGlyphCmap()
	base, err := replaceTable(base, "cmap", cmapFromPairs([]cmapPair{{code: 'A', glyph: 2}}))
	if err != nil {
		t.Fatal(err)
	}

	fixed, err := RepairWithGlyphs(base, []GlyphMapping{{Rune: 'B', Glyph: 2}})
	if err != nil {
		t.Fatal(err)
	}
	glyphs := make(map[uint32]uint16)
	for _, pair := range parseCMapPairs(fixed) {
		glyphs[pair.code] = pair.glyph
	}
	if glyph, ok := glyphs[uint32('A')]; ok {
		t.Fatalf("stale mapping cmap['A'] still present: %d", glyph)
	}
	if glyphs[uint32('B')] != 2 {
		t.Fatalf("cmap['B'] = %d, want 2", glyphs[uint32('B')])
	}
	if glyphs[uint32(packedGlyphBase)+2] != 2 {
		t.Fatalf("PUA mapping lost: cmap[PUA+2] = %d", glyphs[uint32(packedGlyphBase)+2])
	}
}

// format12Glyphs 读取 cmap 中 (3,10) format 12 子表的码位到字形映射。
func format12Glyphs(cmap []byte) map[uint32]uint16 {
	glyphs := map[uint32]uint16{}
	numTables := int(binary.BigEndian.Uint16(cmap[2:4]))
	for i := 0; i < numTables; i++ {
		record := 4 + i*8
		if record+8 > len(cmap) {
			break
		}
		offset := int(binary.BigEndian.Uint32(cmap[record+4 : record+8]))
		if offset+16 > len(cmap) || binary.BigEndian.Uint16(cmap[offset:offset+2]) != 12 {
			continue
		}
		groups := int(binary.BigEndian.Uint32(cmap[offset+12 : offset+16]))
		for group := 0; group < groups; group++ {
			pos := offset + 16 + group*12
			if pos+12 > len(cmap) {
				break
			}
			start := binary.BigEndian.Uint32(cmap[pos : pos+4])
			end := binary.BigEndian.Uint32(cmap[pos+4 : pos+8])
			gid := binary.BigEndian.Uint32(cmap[pos+8 : pos+12])
			for code := start; code <= end && code-start < 0x10000; code++ {
				glyphs[code] = uint16(gid + (code - start))
			}
		}
	}
	return glyphs
}

// testFormat4Subtable 生成把 U+56FD、U+6587 映射到字形 1、2 的 format 4 子表。
func testFormat4Subtable() []byte {
	const segCount = 3
	subtable := make([]byte, 16+segCount*8)
	binary.BigEndian.PutUint16(subtable[0:2], 4)
	binary.BigEndian.PutUint16(subtable[2:4], uint16(len(subtable)))
	binary.BigEndian.PutUint16(subtable[4:6], 0) // language
	binary.BigEndian.PutUint16(subtable[6:8], segCount*2)
	binary.BigEndian.PutUint16(subtable[8:10], 2)  // searchRange
	binary.BigEndian.PutUint16(subtable[10:12], 0) // entrySelector
	binary.BigEndian.PutUint16(subtable[12:14], 0) // rangeShift
	endCode := subtable[14 : 14+segCount*2]
	binary.BigEndian.PutUint16(endCode[0:2], 0x56FD)
	binary.BigEndian.PutUint16(endCode[2:4], 0x6587)
	binary.BigEndian.PutUint16(endCode[4:6], 0xFFFF)
	startCode := subtable[14+segCount*2+2 : 14+segCount*4+2]
	binary.BigEndian.PutUint16(startCode[0:2], 0x56FD)
	binary.BigEndian.PutUint16(startCode[2:4], 0x6587)
	binary.BigEndian.PutUint16(startCode[4:6], 0xFFFF)
	idDelta := subtable[14+segCount*4+2 : 14+segCount*6+2]
	binary.BigEndian.PutUint16(idDelta[0:2], 0x10001-0x56FD)
	binary.BigEndian.PutUint16(idDelta[2:4], 0x10002-0x6587)
	binary.BigEndian.PutUint16(idDelta[4:6], 1)
	return subtable
}

// TestRepairKeepsUnicodeInPackedCmap 验证合成的 (3,10) 子表同时覆盖字体原有
// 码位与私有区字形引用。整形器只使用优先级最高的一张 cmap 子表，而 (3,10)
// 排在 (0,3) 之前；若合成子表仅含私有区，真实字符会全部变成 .notdef 方框。
func TestRepairKeepsUnicodeInPackedCmap(t *testing.T) {
	head := make([]byte, 12)
	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:6], 3)

	format4 := testFormat4Subtable()
	cmap := make([]byte, 12+len(format4))
	binary.BigEndian.PutUint16(cmap[2:4], 1)
	binary.BigEndian.PutUint16(cmap[4:6], 0) // platformID: Unicode
	binary.BigEndian.PutUint16(cmap[6:8], 3) // encodingID: BMP
	binary.BigEndian.PutUint32(cmap[8:12], 12)
	copy(cmap[12:], format4)

	fixed, err := Repair(buildSFNT([]sfntTestTable{
		{tag: "head", data: head},
		{tag: "maxp", data: maxp},
		{tag: "cmap", data: cmap},
	}))
	if err != nil {
		t.Fatal(err)
	}
	fixedCmap, ok := findTable(fixed, "cmap")
	if !ok {
		t.Fatal("cmap table was removed")
	}
	if !hasPackedGlyphMap(fixedCmap) {
		t.Fatal("packed glyph cmap was not added")
	}
	glyphs := format12Glyphs(fixedCmap)
	for _, code := range []uint32{0x56FD, 0x6587} {
		if glyphs[code] == 0 {
			t.Errorf("U+%04X missing from the synthesized (3,10) subtable", code)
		}
	}
	for glyphID := uint32(0); glyphID < 3; glyphID++ {
		if got := glyphs[uint32(packedGlyphBase)+glyphID]; got != uint16(glyphID) {
			t.Errorf("cmap[PUA+%d] = %d, want %d", glyphID, got, glyphID)
		}
	}
}
