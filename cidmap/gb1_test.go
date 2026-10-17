package cidmap

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"testing"
)

func TestGB1TextPreservesEveryPDFMapping(t *testing.T) {
	// Frozen from OFD's original Adobe-GB1-UCS2 parser over every 16-bit CID.
	// The serialization is uint32(UTF-8 byte length), followed by UTF-8 bytes;
	// out-of-collection CIDs use the PDF reader's U+FFFD fallback.
	h := sha256.New()
	for cid := 0; cid <= 65535; cid++ {
		text, ok := GB1Text(cid)
		if ok != (cid < 30284) {
			t.Fatalf("CID %d: mapped=%v", cid, ok)
		}
		if !ok {
			text = "\uFFFD"
		}
		h.Write(binary.BigEndian.AppendUint32(nil, uint32(len(text))))
		h.Write([]byte(text))
	}
	const want = "371b4cf0c50dc00db8f6d0e941ee4ef73755d8f8f23133773680de88ba6ada03"
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		t.Fatalf("PDF CID mappings changed: %s, want %s", got, want)
	}
}

func TestGB1TextAndGlyphMappingsRemainDistinct(t *testing.T) {
	for _, tc := range []struct {
		cid   int
		text  string
		glyph rune
	}{
		{1, " ", ' '},
		{4559, "中", '中'},
		{573, "，", '︐'},
		{574, "。", '︒'},
		{580, "（", '︵'},
		{22048, "𠂇", '\U00020087'},
	} {
		if text, ok := GB1Text(tc.cid); !ok || text != tc.text {
			t.Errorf("text CID %d: %q, %v; want %q", tc.cid, text, ok, tc.text)
		}
		if glyph, ok := GB1GlyphUnicode(tc.cid); !ok || glyph != tc.glyph {
			t.Errorf("glyph CID %d: %U, %v; want %U", tc.cid, glyph, ok, tc.glyph)
		}
	}
	for cid, want := range map[int]string{22031: "郎\uFE00", 22032: "嗀\uFE00", 22038: "礼\uFE00", 22040: "蘒\uFE00"} {
		if got, ok := GB1Text(cid); !ok || got != want {
			t.Errorf("CID %d variation sequence: %q, %v; want %q", cid, got, ok, want)
		}
	}
}

func TestGB1UnassignedAndOutOfRange(t *testing.T) {
	for cid, wantText := range map[int]string{0: "\uFFFD", 22127: " ", 22352: " "} {
		if value, ok := GB1GlyphUnicode(cid); ok || value != 0 {
			t.Errorf("unassigned glyph CID %d: %U, %v", cid, value, ok)
		}
		if value, ok := GB1Text(cid); !ok || value != wantText {
			t.Errorf("explicit text mapping CID %d: %q, %v; want %q", cid, value, ok, wantText)
		}
	}
	for _, cid := range []int{-1, 30284, 65535, 65536 + 22048, 1 << 20} {
		if value, ok := GB1GlyphUnicode(cid); ok || value != 0 {
			t.Errorf("out-of-range glyph CID %d: %U, %v", cid, value, ok)
		}
		if value, ok := GB1Text(cid); ok || value != "" {
			t.Errorf("out-of-range text CID %d: %q, %v", cid, value, ok)
		}
	}
}

func TestGB1ConcurrentLookups(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 100; j++ {
				if text, ok := GB1Text(22031); !ok || text != "郎\uFE00" {
					t.Errorf("concurrent text lookup: %q, %v", text, ok)
				}
				if glyph, ok := GB1GlyphUnicode(4559); !ok || glyph != '中' {
					t.Errorf("concurrent glyph lookup: %U, %v", glyph, ok)
				}
			}
		})
	}
	wg.Wait()
}
