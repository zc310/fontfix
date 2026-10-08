//go:generate go run gen_adobe_gb1.go -poppler /usr/share/poppler

package fontfix

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"sync"
)

// adobeGB1Table is the lazily inflated CID→Unicode table, indexed by CID.
var adobeGB1Table = sync.OnceValue(func() []uint16 {
	packed, err := base64.StdEncoding.DecodeString(adobeGB1Packed)
	if err != nil {
		return nil
	}
	reader, err := zlib.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil
	}
	defer reader.Close()
	raw := make([]byte, 0, adobeGB1CIDs*2)
	buffer := make([]byte, 4096)
	for {
		n, readErr := reader.Read(buffer)
		raw = append(raw, buffer[:n]...)
		if readErr != nil {
			break
		}
	}
	if len(raw) < adobeGB1CIDs*2 {
		return nil
	}
	table := make([]uint16, adobeGB1CIDs)
	for cid := range table {
		table[cid] = binary.BigEndian.Uint16(raw[cid*2:])
	}
	// Values above the BMP do not fit in the uint16 table; patch them in.
	for _, entry := range adobeGB1Supplementary {
		if int(entry[0]) < len(table) {
			table[entry[0]] = 0
		}
	}
	return table
})

// adobeGB1SupplementaryLookup resolves the CIDs whose Unicode is above the BMP.
// Kept separate from adobeGB1Table so the common path stays a single indexed
// load.
var adobeGB1SupplementaryLookup = sync.OnceValue(func() map[uint16]rune {
	if len(adobeGB1Supplementary) == 0 {
		return nil
	}
	table := make(map[uint16]rune, len(adobeGB1Supplementary))
	for _, entry := range adobeGB1Supplementary {
		table[uint16(entry[0])] = rune(entry[2])
	}
	return table
})

// AdobeGB1CIDToUnicode resolves an Adobe-GB1 CID to Unicode.
//
// PDF does not put characters in a CID: Identity-H only says "these two bytes are
// a CID". The only authority for what that CID means is the CMap's charset, which
// is Adobe's Adobe-GB1 ordering. Callers that cannot consult it otherwise end up
// with U+FFFD, which then renders as the wrong glyph.
//
// CIDs outside the table (Adobe-GB1 defines 0 through 30283) report false.
func AdobeGB1CIDToUnicode(cid int) (rune, bool) {
	if cid < 0 {
		return 0, false
	}
	if supplementary := adobeGB1SupplementaryLookup(); supplementary != nil {
		if value, ok := supplementary[uint16(cid)]; ok {
			return value, true
		}
	}
	table := adobeGB1Table()
	if table == nil || cid >= len(table) {
		return 0, false
	}
	value := table[cid]
	if value == 0 {
		return 0, false
	}
	return rune(value), true
}
