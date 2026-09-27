package fontfix

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// rebuildSFNTReference 是 rebuildSFNT 的参考实现，逐字复制自优化前的版本。
// 优化后的 rebuildSFNT 必须与它输出完全相同的字节。
func rebuildSFNTReference(scaler []byte, tables []sfntTable) []byte {
	result := bytes.NewBuffer(make([]byte, 0, sfntHeaderSize+len(tables)*sfntTableEntrySize))
	result.Write(scaler)
	writeUint16(result, uint16(len(tables)))
	entrySelector := 0
	for 1<<(entrySelector+1) <= len(tables) {
		entrySelector++
	}
	searchRange := 1 << (entrySelector + 4)
	writeUint16(result, uint16(searchRange))
	writeUint16(result, uint16(entrySelector))
	writeUint16(result, uint16(len(tables)*sfntTableEntrySize-searchRange))

	tableOffset := sfntHeaderSize + len(tables)*sfntTableEntrySize
	records := make([]sfntRecord, 0, len(tables))
	for _, table := range tables {
		if table.tag == "head" && len(table.data) >= 12 {
			binary.BigEndian.PutUint32(table.data[8:12], 0)
		}
		records = append(records, sfntRecord{tag: table.tag, data: table.data, offset: tableOffset})
		tableOffset += paddedLength(len(table.data))
	}
	for _, record := range records {
		result.WriteString(record.tag)
		writeUint32(result, tableChecksum(record.data))
		writeUint32(result, uint32(record.offset))
		writeUint32(result, uint32(len(record.data)))
	}
	for _, record := range records {
		result.Write(record.data)
		result.Write(make([]byte, paddedLength(len(record.data))-len(record.data)))
	}

	// 优化前的 fixHeadChecksumAdjustment：条件判断的是整字体长度而非 head 表
	// 自身长度，这里原样保留以作为对照基线。
	fixed := result.Bytes()
	for _, record := range records {
		if record.tag != "head" || record.offset+12 > len(fixed) {
			continue
		}
		binary.BigEndian.PutUint32(fixed[record.offset+8:record.offset+12], 0)
		checksum := refTableChecksum(fixed)
		binary.BigEndian.PutUint32(fixed[record.offset+8:record.offset+12], 0xB1B0AFBA-checksum)
		break
	}
	return fixed
}

func refTableChecksum(data []byte) uint32 {
	var sum uint32
	for offset := 0; offset < len(data); offset += 4 {
		var word uint32
		for i := 0; i < 4 && offset+i < len(data); i++ {
			word |= uint32(data[offset+i]) << uint(24-8*i)
		}
		sum += word
	}
	return sum
}

func referenceCases() []struct {
	name   string
	scaler []byte
	tables []sfntTable
} {
	big := func(tag string, n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = uint8(i*31 + 7)
		}
		return append([]byte(tag), b...)
	}
	return []struct {
		name   string
		scaler []byte
		tables []sfntTable
	}{
		{name: "空表集", scaler: []byte{0, 1, 0, 0}},
		{
			name:   "单表",
			scaler: []byte{0, 1, 0, 0},
			tables: []sfntTable{{tag: "cmap", data: []byte{1, 2, 3, 4}}},
		},
		{
			name:   "表长度非 4 对齐",
			scaler: []byte{0, 1, 0, 0},
			tables: []sfntTable{
				{tag: "cmap", data: []byte{1, 2, 3, 4, 5}},
				{tag: "glyf", data: []byte{9, 8, 7}},
			},
		},
		{
			name:   "含 head（需清零校验调整字段）",
			scaler: []byte{0, 1, 0, 0},
			tables: []sfntTable{
				{tag: "head", data: big("head", 54)},
				{tag: "cmap", data: big("cmap", 40)},
				{tag: "maxp", data: big("maxp", 6)},
			},
		},
		{
			name:   "OTTO 标量",
			scaler: []byte("OTTO"),
			tables: []sfntTable{
				{tag: "CFF ", data: big("CFF ", 200)},
				{tag: "head", data: big("head", 54)},
			},
		},
		{
			name:   "表数非 2 的幂",
			scaler: []byte{0, 1, 0, 0},
			tables: []sfntTable{
				{tag: "cmap", data: big("cmap", 12)},
				{tag: "glyf", data: big("glyf", 33)},
				{tag: "head", data: big("head", 54)},
				{tag: "loca", data: big("loca", 7)},
				{tag: "maxp", data: big("maxp", 6)},
			},
		},
		{
			name:   "空表内容",
			scaler: []byte{0, 1, 0, 0},
			tables: []sfntTable{
				{tag: "cmap", data: nil},
				{tag: "glyf", data: []byte{}},
			},
		},
	}
}

// rebuildSFNT 优化后每张表只复制一次，输出必须与参考实现逐字节一致。
func TestRebuildSFNTMatchesReference(t *testing.T) {
	for _, tc := range referenceCases() {
		t.Run(tc.name, func(t *testing.T) {
			want := rebuildSFNTReference(append([]byte(nil), tc.scaler...), cloneTables(tc.tables))
			got := rebuildSFNT(append([]byte(nil), tc.scaler...), cloneTables(tc.tables))
			if !bytes.Equal(want, got) {
				t.Fatalf("输出与参考实现不一致:\n参考 %d 字节 %x\n实际 %d 字节 %x", len(want), want, len(got), got)
			}
		})
	}
}

// rebuildSFNT 不得改写调用方传入的表内容（优化后表内容可能是来源缓冲的子切片）。
func TestRebuildSFNTDoesNotMutateInputTables(t *testing.T) {
	tables := []sfntTable{
		{tag: "head", data: append([]byte(nil), bytes.Repeat([]byte{0xAB}, 54)...)},
		{tag: "cmap", data: append([]byte(nil), bytes.Repeat([]byte{0xCD}, 40)...)},
	}
	before := make([][]byte, len(tables))
	for i, table := range tables {
		before[i] = append([]byte(nil), table.data...)
	}
	rebuildSFNT([]byte{0, 1, 0, 0}, tables)
	for i, table := range tables {
		if !bytes.Equal(before[i], table.data) {
			t.Fatalf("表 %q 的内容被改写", table.tag)
		}
	}
}

// 畸形字体的 head 表可能短于 12 字节。此时旧实现按整字体长度判断，会把
// checksumAdjustment 写到 head 表边界之外、破坏相邻表数据；新实现必须跳过。
func TestRebuildSFNTShortHeadDoesNotWriteOutsideTable(t *testing.T) {
	tables := []sfntTable{
		{tag: "head", data: []byte{1, 2, 3}},
		{tag: "cmap", data: bytes.Repeat([]byte{0xCD}, 16)},
	}
	// 旧实现在这里会越界写入，复现该行为以确认问题真实存在。
	legacy := rebuildSFNTReference([]byte{0, 1, 0, 0}, cloneTables(tables))
	headEnd := sfntHeaderSize + 2*sfntTableEntrySize + paddedLength(3)
	if bytes.Equal(legacy[headEnd:], bytes.Repeat([]byte{0xCD}, len(legacy)-headEnd)) {
		t.Skip("旧实现未越界写入，无法验证修复")
	}

	got := rebuildSFNT([]byte{0, 1, 0, 0}, cloneTables(tables))
	headEnd = sfntHeaderSize + 2*sfntTableEntrySize + paddedLength(3)
	if !bytes.Equal(got[headEnd:], bytes.Repeat([]byte{0xCD}, len(got)-headEnd)) {
		t.Errorf("head 短于 12 字节时写到了相邻表:\n%x", got[headEnd:])
	}
}

func cloneTables(tables []sfntTable) []sfntTable {
	out := make([]sfntTable, len(tables))
	for i, table := range tables {
		out[i] = sfntTable{tag: table.tag, data: append([]byte(nil), table.data...)}
	}
	return out
}
