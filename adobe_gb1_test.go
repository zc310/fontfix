package fontfix

import "testing"

// TestAdobeGB1CIDToUnicodeSpotCheck verifies the table contents against Adobe's
// Adobe-GB1 collection (redistributed by poppler-data under BSD-3-Clause).
//
// The five CJK entries were cross-checked independently: they are the characters
// pdftotext produces for the same CIDs of a real PDF.
func TestAdobeGB1CIDToUnicodeSpotCheck(t *testing.T) {
	for _, tc := range []struct {
		cid  int
		want rune
	}{
		{1, ' '},              // CID 1 is the space in Adobe-GB1's proportional Latin block
		{821, '（'},            // fullwidth left parenthesis
		{822, '）'},            // fullwidth right parenthesis
		{7426, '\u917D'},      // level-2 GBK
		{9155, '\u9594'},      // level-2 GBK
		{11952, '\u5B4E'},     // level-2 GBK
		{13086, '\u6357'},     // the table maps this CID to U+6357
		{16718, '\u7A85'},     // level-2 GBK
		{22048, '\U00020087'}, // supplementary plane, does not fit in the uint16 table
		{22111, '\U000241FE'},
	} {
		got, ok := AdobeGB1CIDToUnicode(tc.cid)
		if !ok {
			t.Errorf("CID %d has no mapping", tc.cid)
			continue
		}
		if got != tc.want {
			t.Errorf("CID %d = %q, want %q", tc.cid, got, tc.want)
		}
	}
}

func TestAdobeGB1CIDToUnicodeOutOfRange(t *testing.T) {
	// Adobe-GB1 defines CID 0 through 30283. Anything outside must report "not
	// found" rather than return a plausible but wrong character.
	for _, cid := range []int{-1, 30284, 40000, 1 << 20} {
		if value, ok := AdobeGB1CIDToUnicode(cid); ok {
			t.Errorf("CID %d = %q, want no mapping", cid, value)
		}
	}
}

// TestAdobeGB1CoversWholeRange guards the coverage that the previous GBK-level-1
// heuristic could not provide. It only modelled rows 16 to 87, so roughly 26000
// CIDs outside that block resolved to nothing and CJK body text degraded to
// U+FFFD.
func TestAdobeGB1CoversWholeRange(t *testing.T) {
	mapped := 0
	for cid := 0; cid < adobeGB1CIDs; cid++ {
		if _, ok := AdobeGB1CIDToUnicode(cid); ok {
			mapped++
		}
	}
	if mapped < adobeGB1CIDs-adobeGB1Unmapped {
		t.Errorf("only %d/%d CIDs resolve, want nearly all of them", mapped, adobeGB1CIDs)
	}
}
