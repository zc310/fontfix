package fontfix

import (
	"bytes"
	"encoding/binary"
)

const (
	sfntHeaderSize     = 12
	sfntTableEntrySize = 16
)

type sfntTable struct {
	tag  string
	data []byte
}

type sfntRecord struct {
	tag    string
	data   []byte
	offset int
}

func minimalOS2Table(unitsPerEm uint16) []byte {
	table := make([]byte, 96)
	binary.BigEndian.PutUint16(table[0:2], 2)
	binary.BigEndian.PutUint16(table[2:4], scaleCFFMetric(500, unitsPerEm))
	binary.BigEndian.PutUint16(table[4:6], 400)
	binary.BigEndian.PutUint16(table[6:8], 5)
	copy(table[58:62], "PfEd")
	binary.BigEndian.PutUint16(table[62:64], 0x0040)
	binary.BigEndian.PutUint16(table[64:66], 0)
	binary.BigEndian.PutUint16(table[66:68], 255)
	binary.BigEndian.PutUint16(table[68:70], scaleCFFMetric(800, unitsPerEm))
	binary.BigEndian.PutUint16(table[70:72], uint16(scaleCFFSigned(-200, unitsPerEm)))
	binary.BigEndian.PutUint16(table[74:76], scaleCFFMetric(800, unitsPerEm))
	binary.BigEndian.PutUint16(table[76:78], scaleCFFMetric(200, unitsPerEm))
	return table
}

func minimalNameTable() []byte {
	name := []byte{0, 0, 0, 1, 0, 18, 0, 3, 0, 1, 4, 9, 0, 1, 0, 14, 0, 0}
	return append(name, 0, 'f', 0, 'o', 0, 'n', 0, 't', 0, 'f', 0, 'i', 0, 'x')
}

func minimalPostTable() []byte {
	table := make([]byte, 32)
	binary.BigEndian.PutUint32(table[0:4], 0x00030000)
	binary.BigEndian.PutUint32(table[4:8], 0x00010000)
	return table
}

// rebuildSFNT 按表目录重排字体并输出新字节。
//
// 每张表只被复制一次：先算出总长度并分配结果缓冲，写入表内容后再从结果缓冲
// 计算校验和并回填表目录。表内容可以是来源缓冲的子切片，因此本函数不得改写
// tables 中任何表内容——head 的 checksumAdjustment 字段在结果缓冲上清零。
func rebuildSFNT(scaler []byte, tables []sfntTable) []byte {
	total := sfntHeaderSize + len(tables)*sfntTableEntrySize
	for _, table := range tables {
		total += paddedLength(len(table.data))
	}
	result := make([]byte, total)

	copy(result, scaler)
	pos := len(scaler)
	putUint16(result[pos:], uint16(len(tables)))
	pos += 2
	entrySelector := 0
	for 1<<(entrySelector+1) <= len(tables) {
		entrySelector++
	}
	searchRange := 1 << (entrySelector + 4)
	putUint16(result[pos:], uint16(searchRange))
	pos += 2
	putUint16(result[pos:], uint16(entrySelector))
	pos += 2
	putUint16(result[pos:], uint16(len(tables)*sfntTableEntrySize-searchRange))
	pos += 2

	// 先写入表内容并记录偏移；表目录暂时留空，待校验和算好后回填。
	records := make([]sfntRecord, 0, len(tables))
	for _, table := range tables {
		offset := sfntHeaderSize + len(tables)*sfntTableEntrySize
		if len(records) > 0 {
			offset = records[len(records)-1].offset + paddedLength(len(records[len(records)-1].data))
		}
		copy(result[offset:], table.data)
		records = append(records, sfntRecord{tag: table.tag, data: table.data, offset: offset})
	}

	// head 的 checksumAdjustment 必须以 0 参与表校验和与整字体校验和的计算。
	// 条件按 head 表自身长度判断：畸形字体里 head 可能短于 12 字节，此时若按
	// 整字体长度判断，写入位置会越过 head 表边界、破坏相邻表数据。
	for _, record := range records {
		if record.tag == "head" && len(record.data) >= 12 {
			putUint32(result[record.offset+8:], 0)
		}
	}

	dir := sfntHeaderSize
	for _, record := range records {
		copy(result[dir:], record.tag)
		putUint32(result[dir+4:], tableChecksum(result[record.offset:record.offset+len(record.data)]))
		putUint32(result[dir+8:], uint32(record.offset))
		putUint32(result[dir+12:], uint32(len(record.data)))
		dir += sfntTableEntrySize
	}

	fixHeadChecksumAdjustment(result, records)
	return result
}

func paddedLength(length int) int {
	return (length + 3) &^ 3
}

func putUint16(buf []byte, value uint16) {
	binary.BigEndian.PutUint16(buf, value)
}

func putUint32(buf []byte, value uint32) {
	binary.BigEndian.PutUint32(buf, value)
}

func writeUint16(buf *bytes.Buffer, value uint16) {
	_ = binary.Write(buf, binary.BigEndian, value)
}

func writeUint32(buf *bytes.Buffer, value uint32) {
	_ = binary.Write(buf, binary.BigEndian, value)
}

// tableChecksum 按大端 4 字节字累加表校验和。整字走 binary.BigEndian，
// 尾部不足 4 字节时右侧补零（与逐字节左对齐累加等价）。校验和要对整张表
// 逐字节扫描，是重建 SFNT 时最热的循环之一。
func tableChecksum(data []byte) uint32 {
	var sum uint32
	offset := 0
	for ; offset+4 <= len(data); offset += 4 {
		sum += binary.BigEndian.Uint32(data[offset : offset+4])
	}
	if offset < len(data) {
		var word uint32
		for i := offset; i < len(data); i++ {
			word |= uint32(data[i]) << uint(24-8*(i-offset))
		}
		sum += word
	}
	return sum
}

// fixHeadChecksumAdjustment 在结果缓冲上回填 head 的 checksumAdjustment。
// 调用前 head 的该字段已被清零，整字体校验和因此与规范一致。
func fixHeadChecksumAdjustment(data []byte, records []sfntRecord) {
	for _, record := range records {
		if record.tag != "head" || len(record.data) < 12 || record.offset+12 > len(data) {
			continue
		}
		putUint32(data[record.offset+8:], 0)
		checksum := tableChecksum(data)
		putUint32(data[record.offset+8:], 0xB1B0AFBA-checksum)
		return
	}
}
