package fontfix

import (
	"encoding/binary"
	"testing"
)

func TestRepairAddsOS2Table(t *testing.T) {
	font := testFontWithoutOS2()
	fixed, err := Repair(font)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.BigEndian.Uint16(fixed[4:6]); got != 4 {
		t.Fatalf("table count = %d, want 4", got)
	}
	table, ok := findTable(fixed, "OS/2")
	if !ok {
		t.Fatal("OS/2 table was not added")
	}
	if len(table) != 96 || binary.BigEndian.Uint16(table[:2]) != 2 {
		t.Fatalf("OS/2 table = %d bytes, version %d", len(table), binary.BigEndian.Uint16(table[:2]))
	}
	for _, tag := range []string{"name", "post"} {
		if _, ok := findTable(fixed, tag); !ok {
			t.Fatalf("%s table was not added", tag)
		}
	}
}

func TestRepairIsNoopWhenOS2Exists(t *testing.T) {
	font := testFontWithoutOS2()
	font, err := Repair(font)
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := Repair(font)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixed) != len(font) || string(fixed) != string(font) {
		t.Fatal("font with OS/2 table was changed")
	}
}

func TestRepairLeavesNonSFNTDataUnchanged(t *testing.T) {
	data := []byte("not a font")
	fixed, err := Repair(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(fixed) != string(data) {
		t.Fatal("non-SFNT data was changed")
	}
}

func testFontWithoutOS2() []byte {
	data := make([]byte, 12+16+4)
	binary.BigEndian.PutUint32(data[:4], 0x00010000)
	binary.BigEndian.PutUint16(data[4:6], 1)
	copy(data[12:16], "head")
	binary.BigEndian.PutUint32(data[12+8:12+12], 28)
	binary.BigEndian.PutUint32(data[12+12:12+16], 4)
	copy(data[28:], "test")
	return data
}

func findTable(data []byte, wanted string) ([]byte, bool) {
	count := int(binary.BigEndian.Uint16(data[4:6]))
	for i := 0; i < count; i++ {
		offset := 12 + i*16
		if string(data[offset:offset+4]) == wanted {
			start := int(binary.BigEndian.Uint32(data[offset+8 : offset+12]))
			length := int(binary.BigEndian.Uint32(data[offset+12 : offset+16]))
			return data[start : start+length], true
		}
	}
	return nil, false
}
