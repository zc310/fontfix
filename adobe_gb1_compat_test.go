package fontfix

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// Frozen from the original implementation over every 16-bit CID. Zero denotes
// an unassigned CID; each result is serialized as a big-endian uint32.
func TestAdobeGB1CIDToUnicodePreservesEveryMapping(t *testing.T) {
	h := sha256.New()
	for cid := 0; cid <= 65535; cid++ {
		value, ok := AdobeGB1CIDToUnicode(cid)
		if ok != (value != 0) {
			t.Fatalf("CID %d: rune %U, mapped=%v", cid, value, ok)
		}
		h.Write(binary.BigEndian.AppendUint32(nil, uint32(value)))
	}
	const want = "8ba3ea0e86d4cbd03eb9ecab98eb2b470d4f8a10df2306cf90359328135598df"
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		t.Fatalf("font-repair CID mappings changed: %s, want %s", got, want)
	}
}
