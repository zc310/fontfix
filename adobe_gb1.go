//go:generate go run gen_adobe_gb1.go -poppler /usr/share/poppler

package fontfix

import "github.com/zc310/fontfix/cidmap"

// AdobeGB1CIDToUnicode resolves an Adobe-GB1 CID to the Unicode code point used
// for font repair. It preserves vertical and compatibility characters from
// poppler-data's cidToUnicode/Adobe-GB1 table. For PDF text extraction, use
// cidmap.GB1Text instead; that mapping may contain multiple Unicode code points.
// CIDs outside 0 through 30283 and unassigned CIDs report false.
func AdobeGB1CIDToUnicode(cid int) (rune, bool) {
	return cidmap.GB1GlyphUnicode(cid)
}
