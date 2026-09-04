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
		if got, ok := adobeGB1CIDToUnicode(cid); !ok || got != want {
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
