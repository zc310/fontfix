package fontfix

import (
	"encoding/binary"
	"testing"
)

// cffDictInt 以固定 5 字节（操作符 29 + int32）编码 DICT 整数操作数，便于先
// 计算长度再回填偏移。
func cffDictInt(v int) []byte {
	b := make([]byte, 5)
	b[0] = 29
	binary.BigEndian.PutUint32(b[1:], uint32(int32(v)))
	return b
}

// testBareCFFWithWidth 构建一个单字形 bare CFF，Top DICT 含可选 FontMatrix、
// CharStrings 与 Private；Private DICT 给出 defaultWidthX/nominalWidthX，字形
// CharString 不显式给出宽度。
func testBareCFFWithWidth(fontMatrix []byte, defaultWidthX, nominalWidthX int) []byte {
	charStrings := []byte{0, 1, 1, 1, 2, 0x0e}
	private := append(cffDictInt(defaultWidthX), 0x14) // defaultWidthX
	private = append(private, cffDictInt(nominalWidthX)...)
	private = append(private, 0x15) // nominalWidthX

	dict := append([]byte{}, fontMatrix...)
	dict = append(dict, cffDictInt(0)...) // CharStrings offset 占位
	dict = append(dict, 0x11)
	dict = append(dict, cffDictInt(len(private))...) // Private size
	dict = append(dict, cffDictInt(0)...)            // Private offset 占位
	dict = append(dict, 0x12)

	// 固定宽度操作数保证 dict 长度稳定，可先回填偏移再拼入 Top DICT INDEX。
	charStringsOffset := 22 + len(dict)
	privateOffset := charStringsOffset + len(charStrings)
	base := len(fontMatrix)
	binary.BigEndian.PutUint32(dict[base+1:base+5], uint32(charStringsOffset))
	binary.BigEndian.PutUint32(dict[base+12:base+16], uint32(privateOffset))

	nameIndex := []byte{0, 1, 1, 1, 5, 'T', 'e', 's', 't'}
	topIndex := []byte{0, 1, 1, 1, byte(1 + len(dict))}
	topIndex = append(topIndex, dict...)

	out := []byte{1, 0, 4, 1}
	out = append(out, nameIndex...)
	out = append(out, topIndex...)
	out = append(out, 0, 0) // String INDEX
	out = append(out, 0, 0) // Global Subr INDEX
	out = append(out, charStrings...)
	return append(out, private...)
}

func TestRepairExtractsCFFGlyphAdvance(t *testing.T) {
	// bare CFF 的 advance 应取自 CharString 宽度 / Private DICT 的 defaultWidthX，
	// 而不是固定常量；此前固定 500 会让 CJK（整字宽 1000）按半宽前进、相邻文字
	// 相互叠压。
	fixed, err := Repair(testBareCFFWithWidth(nil, 1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	hmtx, ok := findTable(fixed, "hmtx")
	if !ok || len(hmtx) < 2 {
		t.Fatal("hmtx table missing")
	}
	if advance := binary.BigEndian.Uint16(hmtx[0:2]); advance != 1000 {
		t.Fatalf("hmtx advance = %d, want 1000", advance)
	}
}

func TestCFFCharstringWidthExplicitAndDefault(t *testing.T) {
	// rmoveto 前有 3 个操作数时，首个操作数为宽度：nominalWidthX + 操作数。
	explicit := []byte{139 + 50, 139 + 20, 139 + 10, 21} // 50 20 10 rmoveto
	if got := cffCharstringWidth(explicit, 100, 0); got != 150 {
		t.Fatalf("explicit width = %d, want 150", got)
	}
	// 只有 2 个操作数时无显式宽度，使用 defaultWidthX。
	implicit := []byte{139 + 20, 139 + 10, 21}
	if got := cffCharstringWidth(implicit, 100, 600); got != 600 {
		t.Fatalf("default width = %d, want 600", got)
	}
}
