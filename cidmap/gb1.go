// Package cidmap provides Adobe CID mappings without depending on font repair
// or document formats. Text extraction and font repair have distinct mappings.
package cidmap

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"io"
	"sync"
)

// Both lookups share this lazily inflated BMP table. Supplementary characters
// and the differences between the two upstream mappings are stored separately.
var gb1Once sync.Once
var gb1BMP []uint16

func gb1Table() []uint16 {
	gb1Once.Do(func() { gb1BMP = loadGB1Table() })
	return gb1BMP
}

func loadGB1Table() []uint16 {
	packed, err := base64.StdEncoding.DecodeString(adobeGB1Packed)
	if err != nil {
		return nil
	}
	reader, err := zlib.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, adobeGB1CIDs*2+1))
	if err != nil || len(raw) != adobeGB1CIDs*2 {
		return nil
	}
	table := make([]uint16, adobeGB1CIDs)
	for cid := range table {
		table[cid] = binary.BigEndian.Uint16(raw[cid*2:])
	}
	return table
}

// GB1GlyphUnicode returns the single Unicode code point used to repair an
// Adobe-GB1 font's cmap. It preserves the vertical, private-use and compatibility
// characters in poppler-data's cidToUnicode/Adobe-GB1 table. Unassigned CIDs and
// CIDs outside the collection (0 through 30283) report false.
func GB1GlyphUnicode(cid int) (rune, bool) {
	if cid < 0 || cid >= adobeGB1CIDs {
		return 0, false
	}
	for _, entry := range adobeGB1Supplementary {
		if int(entry[0]) == cid {
			return rune(entry[2]), true
		}
	}
	table := gb1Table()
	if table == nil || table[cid] == 0 {
		return 0, false
	}
	return rune(table[cid]), true
}

// GB1Text returns the Unicode string for PDF text extraction from Adobe's
// Adobe-GB1-UCS2 CMap. It preserves multi-code-point mappings, including variation
// selectors; vertical glyphs may map to ordinary punctuation. An explicit U+FFFD
// mapping is returned with true. CIDs outside the collection report false.
func GB1Text(cid int) (string, bool) {
	if cid < 0 || cid >= adobeGB1CIDs {
		return "", false
	}
	if text, ok := adobeGB1TextOverrides[cid]; ok {
		return text, true
	}
	value, ok := GB1GlyphUnicode(cid)
	if !ok {
		return "", false
	}
	return string(value), true
}
