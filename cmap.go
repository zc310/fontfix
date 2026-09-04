package fontfix

import (
	"bytes"
	"encoding/binary"
	"sort"
)

const packedGlyphBase rune = 0xF0000

// GlyphRune returns the private-use Unicode scalar assigned to a glyph ID.
// Repair adds this mapping to the font cmap so callers can render OFD glyph
// references through normal text APIs.
func GlyphRune(glyphID uint16) rune {
	return packedGlyphBase + rune(glyphID)
}

func hasPackedGlyphMap(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	numTables := int(binary.BigEndian.Uint16(data[2:4]))
	for i := 0; i < numTables; i++ {
		offset := 4 + i*8
		if offset+8 > len(data) {
			return false
		}
		subtable := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		if subtable < 0 || subtable+16 > len(data) || binary.BigEndian.Uint16(data[subtable:subtable+2]) != 12 {
			continue
		}
		groups := int(binary.BigEndian.Uint32(data[subtable+12 : subtable+16]))
		for group := 0; group < groups; group++ {
			pos := subtable + 16 + group*12
			if pos+12 > len(data) {
				break
			}
			if binary.BigEndian.Uint32(data[pos:pos+4]) <= uint32(packedGlyphBase) && binary.BigEndian.Uint32(data[pos+4:pos+8]) >= uint32(packedGlyphBase) {
				return true
			}
		}
	}
	return false
}

func addPackedGlyphMap(data []byte, numGlyphs uint16) []byte {
	if len(data) < 4 || numGlyphs == 0 {
		return data
	}
	numTables := int(binary.BigEndian.Uint16(data[2:4]))
	if 4+numTables*8 > len(data) {
		return data
	}

	tableOffsets := make([]int, 0, numTables)
	for i := 0; i < numTables; i++ {
		offset := 4 + i*8
		subtable := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		if subtable >= 4 && subtable < len(data) {
			tableOffsets = append(tableOffsets, subtable)
		}
	}
	sort.Ints(tableOffsets)
	uniqueOffsets := tableOffsets[:0]
	for _, offset := range tableOffsets {
		if len(uniqueOffsets) == 0 || uniqueOffsets[len(uniqueOffsets)-1] != offset {
			uniqueOffsets = append(uniqueOffsets, offset)
		}
	}
	parts := make(map[int][]byte, len(uniqueOffsets))
	for i, offset := range uniqueOffsets {
		end := len(data)
		if i+1 < len(uniqueOffsets) {
			end = uniqueOffsets[i+1]
		}
		if end > offset {
			parts[offset] = append([]byte(nil), data[offset:end]...)
		}
	}

	packed := make([]byte, 28)
	binary.BigEndian.PutUint16(packed[0:2], 12)
	binary.BigEndian.PutUint32(packed[4:8], uint32(len(packed)))
	binary.BigEndian.PutUint32(packed[12:16], 1)
	binary.BigEndian.PutUint32(packed[16:20], uint32(packedGlyphBase))
	binary.BigEndian.PutUint32(packed[20:24], uint32(packedGlyphBase)+uint32(numGlyphs)-1)

	result := bytes.NewBuffer(make([]byte, 0, len(data)+len(packed)+8))
	writeUint16(result, binary.BigEndian.Uint16(data[0:2]))
	writeUint16(result, uint16(numTables+1))
	recordPos := result.Len()
	result.Write(make([]byte, (numTables+1)*8))
	newOffsets := make(map[int]int, len(parts))
	for _, oldOffset := range uniqueOffsets {
		newOffsets[oldOffset] = result.Len()
		result.Write(parts[oldOffset])
	}
	packedOffset := result.Len()
	result.Write(packed)
	fixed := result.Bytes()
	for i := 0; i < numTables; i++ {
		oldRecord := 4 + i*8
		newRecord := recordPos + i*8
		copy(fixed[newRecord:newRecord+4], data[oldRecord:oldRecord+4])
		oldOffset := int(binary.BigEndian.Uint32(data[oldRecord+4 : oldRecord+8]))
		newOffset, ok := newOffsets[oldOffset]
		if !ok {
			return data
		}
		binary.BigEndian.PutUint32(fixed[newRecord+4:newRecord+8], uint32(newOffset))
	}
	newRecord := recordPos + numTables*8
	copy(fixed[newRecord:newRecord+4], []byte{0, 3, 0, 10})
	binary.BigEndian.PutUint32(fixed[newRecord+4:newRecord+8], uint32(packedOffset))
	return fixed
}

func packedGlyphCmap(numGlyphs uint16) []byte {
	packed := make([]byte, 28)
	binary.BigEndian.PutUint16(packed[0:2], 12)
	binary.BigEndian.PutUint32(packed[4:8], uint32(len(packed)))
	binary.BigEndian.PutUint32(packed[12:16], 1)
	binary.BigEndian.PutUint32(packed[16:20], uint32(packedGlyphBase))
	binary.BigEndian.PutUint32(packed[20:24], uint32(packedGlyphBase)+uint32(numGlyphs)-1)

	cmap := make([]byte, 12+len(packed))
	binary.BigEndian.PutUint16(cmap[2:4], 1)
	binary.BigEndian.PutUint16(cmap[4:6], 3)
	binary.BigEndian.PutUint16(cmap[6:8], 10)
	binary.BigEndian.PutUint32(cmap[8:12], 12)
	copy(cmap[12:], packed)
	return cmap
}

type cmapPair struct {
	code  uint32
	glyph uint16
}

func cmapFromPairs(pairs []cmapPair) []byte {
	if len(pairs) == 0 {
		return nil
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].code < pairs[j].code })
	type group struct {
		startCode uint32
		endCode   uint32
		startGID  uint32
	}
	groups := make([]group, 0, len(pairs))
	for _, pair := range pairs {
		if len(groups) > 0 {
			last := &groups[len(groups)-1]
			if pair.code == last.endCode+1 && uint32(pair.glyph) == last.startGID+(last.endCode-last.startCode)+1 {
				last.endCode = pair.code
				continue
			}
		}
		groups = append(groups, group{startCode: pair.code, endCode: pair.code, startGID: uint32(pair.glyph)})
	}

	subtableLength := 16 + len(groups)*12
	cmap := make([]byte, 12+subtableLength)
	binary.BigEndian.PutUint16(cmap[2:4], 1)
	binary.BigEndian.PutUint16(cmap[4:6], 3)
	binary.BigEndian.PutUint16(cmap[6:8], 10)
	binary.BigEndian.PutUint32(cmap[8:12], 12)
	subtable := cmap[12:]
	binary.BigEndian.PutUint16(subtable[0:2], 12)
	binary.BigEndian.PutUint32(subtable[4:8], uint32(subtableLength))
	binary.BigEndian.PutUint32(subtable[12:16], uint32(len(groups)))
	for i, group := range groups {
		pos := 16 + i*12
		binary.BigEndian.PutUint32(subtable[pos:pos+4], group.startCode)
		binary.BigEndian.PutUint32(subtable[pos+4:pos+8], group.endCode)
		binary.BigEndian.PutUint32(subtable[pos+8:pos+12], group.startGID)
	}
	return cmap
}
