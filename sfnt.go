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

func minimalOS2Table() []byte {
	table := make([]byte, 96)
	binary.BigEndian.PutUint16(table[0:2], 2)
	binary.BigEndian.PutUint16(table[2:4], 500)
	binary.BigEndian.PutUint16(table[4:6], 400)
	binary.BigEndian.PutUint16(table[6:8], 5)
	copy(table[58:62], "PfEd")
	binary.BigEndian.PutUint16(table[62:64], 0x0040)
	binary.BigEndian.PutUint16(table[64:66], 0)
	binary.BigEndian.PutUint16(table[66:68], 255)
	binary.BigEndian.PutUint16(table[68:70], 800)
	binary.BigEndian.PutUint16(table[70:72], uint16(0xff38))
	binary.BigEndian.PutUint16(table[74:76], 800)
	binary.BigEndian.PutUint16(table[76:78], 200)
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

func rebuildSFNT(scaler []byte, tables []sfntTable) []byte {
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

	fixed := result.Bytes()
	fixHeadChecksumAdjustment(fixed, records)
	return fixed
}

func paddedLength(length int) int {
	return (length + 3) &^ 3
}

func writeUint16(buf *bytes.Buffer, value uint16) {
	_ = binary.Write(buf, binary.BigEndian, value)
}

func writeUint32(buf *bytes.Buffer, value uint32) {
	_ = binary.Write(buf, binary.BigEndian, value)
}

func tableChecksum(data []byte) uint32 {
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

func fixHeadChecksumAdjustment(data []byte, records []sfntRecord) {
	for _, record := range records {
		if record.tag != "head" || record.offset+12 > len(data) {
			continue
		}
		binary.BigEndian.PutUint32(data[record.offset+8:record.offset+12], 0)
		checksum := tableChecksum(data)
		binary.BigEndian.PutUint32(data[record.offset+8:record.offset+12], 0xB1B0AFBA-checksum)
		return
	}
}
