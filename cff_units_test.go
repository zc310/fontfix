package fontfix

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// cffRealNibbles encodes a decimal real as CFF nibbles (with the trailing
// 0x0f terminator).
func cffRealNibbles(decimal string) []byte {
	nibbles := make([]byte, 0, len(decimal)+1)
	for _, r := range decimal {
		switch {
		case r >= '0' && r <= '9':
			nibbles = append(nibbles, byte(r-'0'))
		case r == '.':
			nibbles = append(nibbles, 0x0a)
		case r == '-':
			nibbles = append(nibbles, 0x0e)
		case r == 'E' || r == 'e':
			nibbles = append(nibbles, 0x0b)
		}
	}
	nibbles = append(nibbles, 0x0f)
	if len(nibbles)%2 != 0 {
		nibbles = append(nibbles, 0x0f)
	}
	out := make([]byte, len(nibbles)/2)
	for i := 0; i < len(nibbles); i += 2 {
		out[i/2] = nibbles[i]<<4 | nibbles[i+1]
	}
	return out
}

// cffFontMatrixEntry builds a Top DICT FontMatrix entry with the given
// diagonal scale.
func cffFontMatrixEntry(scale []byte) []byte {
	entry := []byte{0x1e}
	entry = append(entry, scale...)
	entry = append(entry, 0x8b, 0x8b) // 0 0
	entry = append(entry, 0x1e)
	entry = append(entry, scale...)
	entry = append(entry, 0x8b, 0x8b) // 0 0
	entry = append(entry, 0x0c, 0x07) // FontMatrix
	return entry
}

// testBareCFF builds a one-glyph bare CFF whose Top DICT optionally contains
// the given FontMatrix entry.
func testBareCFF(fontMatrix []byte) []byte {
	charStrings := []byte{0, 1, 1, 1, 2, 0x0e}
	nameIndex := []byte{0, 1, 1, 1, 5, 'T', 'e', 's', 't'}
	dictLength := len(fontMatrix) + 2 // CharStrings offset operand + operator 17
	// prefix = header(4) + nameIndex(9) + topDictIndex(5+dictLength) + stringIndex(2) + globalSubr(2)
	charStringsOffset := 22 + dictLength
	operand := byte(139 + charStringsOffset) // offsets below 108 use a single byte
	dict := make([]byte, 0, dictLength)
	dict = append(dict, fontMatrix...)
	dict = append(dict, operand, 0x11)

	topIndex := []byte{0, 1, 1, 1, byte(1 + len(dict))}
	topIndex = append(topIndex, dict...)

	out := []byte{1, 0, 4, 1}
	out = append(out, nameIndex...)
	out = append(out, topIndex...)
	out = append(out, 0, 0) // String INDEX
	out = append(out, 0, 0) // Global Subr INDEX
	return append(out, charStrings...)
}

func TestRepairUsesCFFFontMatrixUnitsPerEm(t *testing.T) {
	// Type1C subsets such as TimesNewRomanPSMT use a FontMatrix of 1/2048,
	// so their glyph coordinates are in 2048 units per em. The generated
	// head/hhea/hmtx/OS2 metrics must use the same scale, or readers scale
	// the glyphs by 2048/1000.
	fixed, err := Repair(testBareCFF(cffFontMatrixEntry(cffRealNibbles("0.00048828125"))))
	if err != nil {
		t.Fatal(err)
	}
	head, ok := findTable(fixed, "head")
	if !ok || len(head) < 20 {
		t.Fatal("head table missing")
	}
	if units := binary.BigEndian.Uint16(head[18:20]); units != 2048 {
		t.Fatalf("head.unitsPerEm = %d, want 2048", units)
	}
	hhea, ok := findTable(fixed, "hhea")
	if !ok || len(hhea) < 36 {
		t.Fatal("hhea table missing")
	}
	if maxAdvance := binary.BigEndian.Uint16(hhea[10:12]); maxAdvance != 2048 {
		t.Fatalf("hhea.advanceWidthMax = %d, want 2048", maxAdvance)
	}
	hmtx, ok := findTable(fixed, "hmtx")
	if !ok || len(hmtx) < 2 {
		t.Fatal("hmtx table missing")
	}
	if advance := binary.BigEndian.Uint16(hmtx[0:2]); advance != 1024 {
		t.Fatalf("hmtx advance = %d, want 1024", advance)
	}
	os2, ok := findTable(fixed, "OS/2")
	if !ok || len(os2) < 78 {
		t.Fatal("OS/2 table missing")
	}
	if ascent := binary.BigEndian.Uint16(os2[74:76]); ascent != 1638 {
		t.Fatalf("OS/2 usWinAscent = %d, want 1638", ascent)
	}

	second, err := Repair(fixed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second, fixed) {
		t.Fatal("repairing an already repaired font was not idempotent")
	}
}

func TestRepairDefaultsTo1000UnitsPerEm(t *testing.T) {
	// A FontMatrix of 0.001 (the default) keeps 1000 units per em and the original advance.
	fixed, err := Repair(testBareCFF(cffFontMatrixEntry(cffRealNibbles("0.001"))))
	if err != nil {
		t.Fatal(err)
	}
	head, _ := findTable(fixed, "head")
	if units := binary.BigEndian.Uint16(head[18:20]); units != 1000 {
		t.Fatalf("head.unitsPerEm = %d, want 1000", units)
	}
	hmtx, _ := findTable(fixed, "hmtx")
	if advance := binary.BigEndian.Uint16(hmtx[0:2]); advance != 500 {
		t.Fatalf("hmtx advance = %d, want 500", advance)
	}
}

func TestRepairWithoutFontMatrixUsesDefaultUnitsPerEm(t *testing.T) {
	// A missing FontMatrix falls back to the CFF default of 1000 units per em.
	fixed, err := Repair(testBareCFF(nil))
	if err != nil {
		t.Fatal(err)
	}
	head, _ := findTable(fixed, "head")
	if units := binary.BigEndian.Uint16(head[18:20]); units != 1000 {
		t.Fatalf("head.unitsPerEm = %d, want 1000", units)
	}
}
