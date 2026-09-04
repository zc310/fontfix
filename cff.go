package fontfix

import (
	"encoding/binary"
	"fmt"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func isBareCFF(data []byte) bool {
	if len(data) < 4 || data[0] != 1 || data[1] != 0 {
		return false
	}
	return int(data[2]) >= 4 && int(data[2]) <= len(data) && data[3] >= 1 && data[3] <= 4
}

func wrapCFF(data []byte) ([]byte, error) {
	numGlyphs, err := cffGlyphCount(data)
	if err != nil {
		return data, fmt.Errorf("parse bare CFF: %w", err)
	}
	if numGlyphs == 0 || numGlyphs > 0xffff {
		return data, fmt.Errorf("invalid bare CFF glyph count %d", numGlyphs)
	}
	glyphs := uint16(numGlyphs)
	cmap := packedGlyphCmap(glyphs)
	if cidCmap := cffCIDCmap(data, glyphs); len(cidCmap) > 0 {
		cmap = cidCmap
	}
	tables := []sfntTable{
		{tag: "CFF ", data: data},
		{tag: "OS/2", data: minimalOS2Table()},
		{tag: "cmap", data: cmap},
		{tag: "head", data: buildCFFHeadTable(1000)},
		{tag: "hhea", data: buildCFFHheaTable(glyphs)},
		{tag: "hmtx", data: buildCFFHmtxTable(glyphs)},
		{tag: "maxp", data: buildCFFMaxpTable(glyphs)},
		{tag: "name", data: minimalNameTable()},
		{tag: "post", data: minimalPostTable()},
	}
	return rebuildSFNT([]byte("OTTO"), tables), nil
}

func cffGlyphCount(data []byte) (int, error) {
	if len(data) < 4 {
		return 0, fmt.Errorf("header is truncated")
	}
	offset := int(data[2])
	_, _, next, err := cffIndex(data, offset)
	if err != nil {
		return 0, fmt.Errorf("name index: %w", err)
	}
	_, top, next, err := cffIndex(data, next)
	if err != nil {
		return 0, fmt.Errorf("top dict index: %w", err)
	}
	_, _, next, err = cffIndex(data, next)
	if err != nil {
		return 0, fmt.Errorf("string index: %w", err)
	}
	_, _, _, err = cffIndex(data, next)
	if err != nil {
		return 0, fmt.Errorf("global subr index: %w", err)
	}
	if len(top) == 0 {
		return 0, fmt.Errorf("top dict is empty")
	}
	charStringsOffset, ok := cffDictValue(top, 17)
	if !ok {
		return 0, fmt.Errorf("charstrings offset is missing")
	}
	count, _, _, err := cffIndex(data, charStringsOffset)
	if err != nil {
		return 0, fmt.Errorf("charstrings index: %w", err)
	}
	return count, nil
}

func cffIndex(data []byte, offset int) (int, []byte, int, error) {
	if offset < 0 || offset+2 > len(data) {
		return 0, nil, 0, fmt.Errorf("index header is truncated")
	}
	count := int(binary.BigEndian.Uint16(data[offset : offset+2]))
	if count == 0 {
		return 0, nil, offset + 2, nil
	}
	if offset+3 > len(data) {
		return 0, nil, 0, fmt.Errorf("index offset size is missing")
	}
	offSize := int(data[offset+2])
	if offSize < 1 || offSize > 4 {
		return 0, nil, 0, fmt.Errorf("invalid index offset size %d", offSize)
	}
	offsetsStart := offset + 3
	dataStart := offsetsStart + (count+1)*offSize
	if dataStart > len(data) {
		return 0, nil, 0, fmt.Errorf("index offsets are truncated")
	}
	readOffset := func(pos int) int {
		value := 0
		for i := 0; i < offSize; i++ {
			value = value<<8 | int(data[pos+i])
		}
		return value
	}
	first := readOffset(offsetsStart)
	last := readOffset(offsetsStart + count*offSize)
	if first < 1 || last < first || dataStart+last-1 > len(data) {
		return 0, nil, 0, fmt.Errorf("index data is out of bounds")
	}
	return count, data[dataStart+first-1 : dataStart+last-1], dataStart + last - 1, nil
}

func cffDictValue(data []byte, wantedOperator int) (int, bool) {
	operands := make([]int, 0, 4)
	for i := 0; i < len(data); {
		b := data[i]
		switch {
		case b >= 32 && b <= 246:
			operands = append(operands, int(b)-139)
		case b >= 247 && b <= 250:
			if i+1 >= len(data) {
				return 0, false
			}
			operands = append(operands, (int(b)-247)*256+int(data[i+1])+108)
			i++
		case b >= 251 && b <= 254:
			if i+1 >= len(data) {
				return 0, false
			}
			operands = append(operands, -(int(b)-251)*256-int(data[i+1])-108)
			i++
		case b == 28:
			if i+2 >= len(data) {
				return 0, false
			}
			operands = append(operands, int(int16(binary.BigEndian.Uint16(data[i+1:i+3]))))
			i += 2
		case b == 29:
			if i+4 >= len(data) {
				return 0, false
			}
			operands = append(operands, int(int32(binary.BigEndian.Uint32(data[i+1:i+5]))))
			i += 4
		case b == 30:
			for i++; i < len(data); i++ {
				if data[i]&0x0f == 0x0f || data[i]>>4 == 0x0f {
					break
				}
			}
		case b == 12:
			if i+1 >= len(data) {
				return 0, false
			}
			if wantedOperator == 1200+int(data[i+1]) && len(operands) > 0 && operands[len(operands)-1] >= 0 {
				return operands[len(operands)-1], true
			}
			i++
			operands = operands[:0]
		default:
			if int(b) == wantedOperator && len(operands) > 0 && operands[len(operands)-1] >= 0 {
				return operands[len(operands)-1], true
			}
			operands = operands[:0]
		}
		i++
	}
	return 0, false
}

func cffCIDCmap(data []byte, numGlyphs uint16) []byte {
	if len(data) < 4 {
		return nil
	}
	offset := int(data[2])
	_, _, offset, err := cffIndex(data, offset)
	if err != nil {
		return nil
	}
	_, top, _, err := cffIndex(data, offset)
	if err != nil {
		return nil
	}
	if _, ok := cffDictValue(top, 1230); !ok {
		return nil
	}
	charsetOffset, ok := cffDictValue(top, 15)
	if !ok {
		return nil
	}
	cids := cffCharset(data, charsetOffset, int(numGlyphs))
	if len(cids) != int(numGlyphs) {
		return nil
	}
	pairs := make([]cmapPair, 0, len(cids))
	for gid, cid := range cids {
		if gid == 0 || cid < 0 || cid > 0xffff {
			continue
		}
		pairs = append(pairs, cmapPair{code: uint32(packedGlyphBase) + uint32(cid), glyph: uint16(gid)})
		if r, ok := adobeGB1CIDToUnicode(cid); ok {
			pairs = append(pairs, cmapPair{code: uint32(r), glyph: uint16(gid)})
		}
	}
	return cmapFromPairs(pairs)
}

// adobeGB1CIDToUnicode covers the Adobe-GB1 CID range used by the embedded
// Chinese CFF fonts. The CID-to-GBK conversion follows the Adobe-GB1 mapping
// used by OFDGo without adding a text-encoding dependency to fontfix.
func adobeGB1CIDToUnicode(cid int) (rune, bool) {
	switch cid {
	case 1036:
		return '\u4fdd', true
	case 2584:
		return '\u6599', true
	case 2785:
		return '\u5bc6', true
	case 4647:
		return '\u8d44', true
	case 329:
		return '\u201c', true
	case 330:
		return '\u201d', true
	case 821:
		return '\u3001', true
	case 822:
		return '\u3002', true
	case 829:
		return '\u300a', true
	case 830:
		return '\u300b', true
	}
	n := cid + 471
	if n <= 0 {
		return 0, false
	}
	row := (n-1)/94 + 1
	cell := (n-1)%94 + 1
	if row < 16 || row > 87 || cell < 1 || cell > 94 {
		return 0, false
	}
	return decodeGBKPair(byte(row+0xa0), byte(cell+0xa0))
}

func decodeGBKPair(high, low byte) (rune, bool) {
	decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes([]byte{high, low})
	if err != nil || len([]rune(string(decoded))) != 1 {
		return 0, false
	}
	return []rune(string(decoded))[0], true
}

func cffCharset(data []byte, offset, numGlyphs int) []int {
	if offset < 0 || offset >= len(data) || numGlyphs <= 0 {
		return nil
	}
	cids := make([]int, 1, numGlyphs)
	if numGlyphs == 1 {
		return cids
	}
	if offset <= 2 {
		for cid := 1; len(cids) < numGlyphs; cid++ {
			cids = append(cids, cid)
		}
		return cids
	}
	format := data[offset]
	pos := offset + 1
	for len(cids) < numGlyphs {
		if format == 0 {
			if pos+2 > len(data) {
				return nil
			}
			cids = append(cids, int(binary.BigEndian.Uint16(data[pos:pos+2])))
			pos += 2
			continue
		}
		if pos+3 > len(data) {
			return nil
		}
		first := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		pos += 2
		left := 0
		if format == 1 {
			left = int(data[pos])
			pos++
		} else if format == 2 {
			if pos+2 > len(data) {
				return nil
			}
			left = int(binary.BigEndian.Uint16(data[pos : pos+2]))
			pos += 2
		} else {
			return nil
		}
		for cid := 0; cid <= left && len(cids) < numGlyphs; cid++ {
			cids = append(cids, first+cid)
		}
	}
	return cids
}

func buildCFFHeadTable(unitsPerEm uint16) []byte {
	table := make([]byte, 54)
	binary.BigEndian.PutUint32(table[0:4], 0x00010000)
	binary.BigEndian.PutUint32(table[4:8], 0x00005000)
	binary.BigEndian.PutUint32(table[12:16], 0x5f0f3cf5)
	binary.BigEndian.PutUint16(table[18:20], unitsPerEm)
	// CFF outlines do not use loca data; keep indexToLocFormat at 0.
	binary.BigEndian.PutUint16(table[50:52], 0)
	return table
}

func buildCFFHheaTable(numGlyphs uint16) []byte {
	table := make([]byte, 36)
	binary.BigEndian.PutUint32(table[0:4], 0x00010000)
	binary.BigEndian.PutUint16(table[4:6], 800)
	binary.BigEndian.PutUint16(table[6:8], 0xff38)
	binary.BigEndian.PutUint16(table[10:12], 1000)
	binary.BigEndian.PutUint16(table[34:36], numGlyphs)
	return table
}

func buildCFFHmtxTable(numGlyphs uint16) []byte {
	table := make([]byte, int(numGlyphs)*4)
	for i := 0; i < int(numGlyphs); i++ {
		binary.BigEndian.PutUint16(table[i*4:i*4+2], 500)
	}
	return table
}

func buildCFFMaxpTable(numGlyphs uint16) []byte {
	table := make([]byte, 6)
	binary.BigEndian.PutUint32(table[0:4], 0x00005000)
	binary.BigEndian.PutUint16(table[4:6], numGlyphs)
	return table
}
