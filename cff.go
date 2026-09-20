package fontfix

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// cffDefaultUnitsPerEm is the units-per-em assumed when a bare CFF has no
// usable FontMatrix. It matches the CFF default FontMatrix of 0.001.
const cffDefaultUnitsPerEm = 1000

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
	// CFF glyph outlines are expressed in 1/FontMatrix[0] units per em, which
	// is not always 1000 (Type1C subsets such as TimesNewRomanPSMT use 2048).
	// The generated head/hhea/hmtx/OS2 metrics must use the same scale, or
	// readers render the glyphs by the wrong factor.
	unitsPerEm := cffUnitsPerEm(data)
	cmap := packedGlyphCmap(glyphs)
	if cidCmap := cffCIDCmap(data, glyphs); len(cidCmap) > 0 {
		cmap = cidCmap
	}
	data = fixHintMaskOperators(data)
	tables := []sfntTable{
		{tag: "CFF ", data: data},
		{tag: "OS/2", data: minimalOS2Table(unitsPerEm)},
		{tag: "cmap", data: cmap},
		{tag: "head", data: buildCFFHeadTable(unitsPerEm)},
		{tag: "hhea", data: buildCFFHheaTable(glyphs, unitsPerEm)},
		{tag: "hmtx", data: buildCFFHmtxTable(glyphs, unitsPerEm)},
		{tag: "maxp", data: buildCFFMaxpTable(glyphs)},
		{tag: "name", data: minimalNameTable()},
		{tag: "post", data: minimalPostTable()},
	}
	return rebuildSFNT([]byte("OTTO"), tables), nil
}

// cffUnitsPerEm returns the units-per-em implied by the CFF Top DICT
// FontMatrix. The CFF default FontMatrix is 0.001 (1000 units per em), so a
// missing or invalid matrix falls back to 1000.
func cffUnitsPerEm(data []byte) uint16 {
	perEm, ok := cffFontMatrixUnitsPerEm(data)
	if !ok {
		return cffDefaultUnitsPerEm
	}
	return perEm
}

func cffFontMatrixUnitsPerEm(data []byte) (uint16, bool) {
	if len(data) < 4 {
		return 0, false
	}
	offset := int(data[2])
	_, _, offset, err := cffIndex(data, offset)
	if err != nil {
		return 0, false
	}
	_, top, _, err := cffIndex(data, offset)
	if err != nil || len(top) == 0 {
		return 0, false
	}
	scale, ok := cffFontMatrix0(top)
	if !ok || scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return 0, false
	}
	units := int(math.Round(1 / scale))
	if units < 16 || units > 0xffff {
		return 0, false
	}
	return uint16(units), true
}

// cffFontMatrix0 extracts FontMatrix[0] (Top DICT operator 12 7). Unlike
// cffDictValue it evaluates real-number operands, which FontMatrix uses.
func cffFontMatrix0(dict []byte) (float64, bool) {
	operands := make([]float64, 0, 8)
	for i := 0; i < len(dict); {
		b := dict[i]
		switch {
		case b == 12:
			if i+1 >= len(dict) {
				return 0, false
			}
			if int(dict[i+1]) == 7 && len(operands) >= 6 {
				return operands[len(operands)-6], true
			}
			operands = operands[:0]
			i += 2
		case b <= 21:
			operands = operands[:0]
			i++
		case b == 28:
			if i+2 >= len(dict) {
				return 0, false
			}
			operands = append(operands, float64(int16(binary.BigEndian.Uint16(dict[i+1:i+3]))))
			i += 3
		case b == 29:
			if i+4 >= len(dict) {
				return 0, false
			}
			operands = append(operands, float64(int32(binary.BigEndian.Uint32(dict[i+1:i+5]))))
			i += 5
		case b == 30:
			value, next, ok := cffParseReal(dict, i+1)
			if !ok {
				return 0, false
			}
			operands = append(operands, value)
			i = next
		case b >= 32 && b <= 246:
			operands = append(operands, float64(int(b)-139))
			i++
		case b >= 247 && b <= 250:
			if i+1 >= len(dict) {
				return 0, false
			}
			operands = append(operands, float64((int(b)-247)*256+int(dict[i+1])+108))
			i += 2
		case b >= 251 && b <= 254:
			if i+1 >= len(dict) {
				return 0, false
			}
			operands = append(operands, float64(-(int(b)-251)*256-int(dict[i+1])-108))
			i += 2
		default:
			return 0, false
		}
	}
	return 0, false
}

// cffParseReal decodes a CFF real number starting after the 0x1e byte.
func cffParseReal(data []byte, start int) (float64, int, bool) {
	var builder []byte
	index := start
	finished := false
	for index < len(data) && !finished {
		b := data[index]
		index++
		for _, nibble := range []byte{b >> 4, b & 0x0f} {
			switch {
			case nibble <= 9:
				builder = append(builder, '0'+nibble)
			case nibble == 0x0a:
				builder = append(builder, '.')
			case nibble == 0x0b:
				builder = append(builder, 'E')
			case nibble == 0x0c:
				builder = append(builder, 'E', '-')
			case nibble == 0x0e:
				builder = append(builder, '-')
			case nibble == 0x0f:
				finished = true
			default:
				return 0, 0, false
			}
			if finished {
				break
			}
		}
	}
	if !finished {
		return 0, 0, false
	}
	value, err := strconv.ParseFloat(string(builder), 64)
	if err != nil {
		return 0, 0, false
	}
	return value, index, true
}

// scaleCFFMetric scales a non-negative metric from the CFF default 1000
// units-per-em to the font's actual units-per-em.
func scaleCFFMetric(value uint16, unitsPerEm uint16) uint16 {
	if unitsPerEm == 0 || unitsPerEm == cffDefaultUnitsPerEm {
		return value
	}
	scaled := int(math.Round(float64(value) * float64(unitsPerEm) / float64(cffDefaultUnitsPerEm)))
	if scaled < 0 {
		return 0
	}
	if scaled > 0xffff {
		return 0xffff
	}
	return uint16(scaled)
}

// scaleCFFSigned scales a signed metric from 1000 units-per-em to the
// font's actual units-per-em.
func scaleCFFSigned(value int, unitsPerEm uint16) int16 {
	if unitsPerEm == 0 || unitsPerEm == cffDefaultUnitsPerEm {
		return int16(value)
	}
	scaled := int(math.Round(float64(value) * float64(unitsPerEm) / float64(cffDefaultUnitsPerEm)))
	if scaled < -32768 {
		scaled = -32768
	}
	if scaled > 32767 {
		scaled = 32767
	}
	return int16(scaled)
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

func buildCFFHheaTable(numGlyphs, unitsPerEm uint16) []byte {
	table := make([]byte, 36)
	binary.BigEndian.PutUint32(table[0:4], 0x00010000)
	binary.BigEndian.PutUint16(table[4:6], scaleCFFMetric(800, unitsPerEm))
	binary.BigEndian.PutUint16(table[6:8], uint16(scaleCFFSigned(-200, unitsPerEm)))
	binary.BigEndian.PutUint16(table[10:12], scaleCFFMetric(1000, unitsPerEm))
	binary.BigEndian.PutUint16(table[34:36], numGlyphs)
	return table
}

func buildCFFHmtxTable(numGlyphs, unitsPerEm uint16) []byte {
	advance := scaleCFFMetric(500, unitsPerEm)
	table := make([]byte, int(numGlyphs)*4)
	for i := 0; i < int(numGlyphs); i++ {
		binary.BigEndian.PutUint16(table[i*4:i*4+2], advance)
	}
	return table
}

func buildCFFMaxpTable(numGlyphs uint16) []byte {
	table := make([]byte, 6)
	binary.BigEndian.PutUint32(table[0:4], 0x00005000)
	binary.BigEndian.PutUint16(table[4:6], numGlyphs)
	return table
}

// fixHintMaskOperators 把 CharStrings 中的 cntrmask 操作符改写为 hintmask。
//
// 一些 CFF 解析器（如 tdewolff/font）在遇到 cntrmask 时不会把其前面的隐式
// vstem 操作数计入 hint 数量，导致后续 hintmask 掩码字节长度计算错误，字符
// 串解析错位并丢失字形轮廓（表现为个别文字空白）。cntrmask 与 hintmask 长度
// 相同、语义只在命中信息上有区别，而本库总是以 NoHinting 渲染，因此把
// cntrmask 改写为 hintmask 可让解析器按正确分支处理隐式 vstem，且不影响外观。
func fixHintMaskOperators(src []byte) []byte {
	entries, err := cffCharStringsEntries(src)
	if err != nil || len(entries) == 0 {
		return src
	}
	out := append([]byte(nil), src...)
	fixed, err := cffCharStringsEntries(out)
	if err != nil {
		return src
	}
	for _, charstring := range fixed {
		rewriteCntrMaskOperators(charstring)
	}
	return out
}

// rewriteCntrMaskOperators 就地扫描单个 Type2 CharString，把操作符位置上的
// cntrmask(20) 改写为 hintmask(19)，同时按正确的 hint 数量跳过掩码字节。
func rewriteCntrMaskOperators(charstring []byte) {
	hints := 0
	operands := 0
	for i := 0; i < len(charstring); {
		b := charstring[i]
		switch {
		case b == 28:
			i += 3
			operands++
		case b == 255:
			i += 5
			operands++
		case b >= 247 && b <= 254:
			i += 2
			operands++
		case b >= 32:
			i++
			operands++
		case b == 12:
			i += 2
			operands = 0
		default:
			switch b {
			case 1, 3, 18, 23: // hstem/vstem/hstemhm/vstemhm
				hints += operands / 2
				operands = 0
			case 19, 20: // hintmask/cntrmask，前面操作数为隐式 vstemhm
				hints += operands / 2
				operands = 0
				if b == 20 {
					charstring[i] = 19
				}
				i += 1 + (hints+7)/8
				continue
			default:
				operands = 0
			}
			i++
		}
	}
}

// cffCharStringsEntries 返回 bare CFF 中每个 CharString 的字节切片（指向 data）。
func cffCharStringsEntries(data []byte) ([][]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("cff header is truncated")
	}
	headerSize := int(data[2])
	if headerSize < 4 || headerSize > len(data) {
		return nil, fmt.Errorf("invalid cff header size %d", headerSize)
	}
	_, _, offset, err := cffIndex(data, headerSize) // Name INDEX
	if err != nil {
		return nil, err
	}
	_, top, offset, err := cffIndex(data, offset) // Top DICT INDEX
	if err != nil {
		return nil, err
	}
	_, _, offset, err = cffIndex(data, offset) // String INDEX
	if err != nil {
		return nil, err
	}
	_, _, offset, err = cffIndex(data, offset) // Global Subr INDEX
	if err != nil {
		return nil, err
	}
	charStringsOffset, ok := cffDictValue(top, 17)
	if !ok {
		return nil, fmt.Errorf("cff CharStrings offset not found")
	}
	return cffIndexEntries(data, charStringsOffset)
}

// cffIndexEntries 返回 INDEX 中每个条目的字节切片（指向 data）。
func cffIndexEntries(data []byte, offset int) ([][]byte, error) {
	if offset < 0 || offset+2 > len(data) {
		return nil, fmt.Errorf("index header is truncated")
	}
	count := int(binary.BigEndian.Uint16(data[offset : offset+2]))
	if count == 0 {
		return nil, nil
	}
	if offset+3 > len(data) {
		return nil, fmt.Errorf("index offset size is missing")
	}
	offSize := int(data[offset+2])
	if offSize < 1 || offSize > 4 {
		return nil, fmt.Errorf("invalid index offset size %d", offSize)
	}
	offsetsStart := offset + 3
	dataStart := offsetsStart + (count+1)*offSize
	if dataStart > len(data) {
		return nil, fmt.Errorf("index offsets are truncated")
	}
	readOffset := func(pos int) int {
		value := 0
		for i := 0; i < offSize; i++ {
			value = value<<8 | int(data[pos+i])
		}
		return value
	}
	entries := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		start := readOffset(offsetsStart + i*offSize)
		end := readOffset(offsetsStart + (i+1)*offSize)
		if start < 1 || end < start || dataStart+end-1 > len(data) {
			return nil, fmt.Errorf("index entry %d is out of bounds", i)
		}
		entries = append(entries, data[dataStart+start-1:dataStart+end-1])
	}
	return entries, nil
}
