# fontfix

Small helpers for repairing embedded TrueType/OpenType fonts.

## Usage

```go
fixed, err := fontfix.Repair(data)
if err != nil {
	return err
}
```

`Repair` adds a minimal `OS/2` table when an SFNT font does not contain one.
It also adds a format 12 `cmap` mapping for glyph IDs; use `GlyphRune` to
obtain the rune for an OFD glyph reference. Existing font data is preserved,
and non-SFNT data is returned unchanged.

## Attribution

The font repair implementation is derived from the font repair code in
[github.com/xiaoqidun/ofdgo](https://github.com/xiaoqidun/ofdgo), Copyright
2025-2026 Xiao Qi Dun, and has been substantially simplified and modified for
this standalone project. See `NOTICE` and `LICENSE`.

`cidmap/gb1_data.go` embeds Adobe's Adobe-GB1 character collection data,
Copyright 1990-2020 Adobe, as redistributed by
[poppler-data](https://gitlab.freedesktop.org/poppler/poppler-data) under
BSD-3-Clause. The independent [`cidmap`](cidmap) package shares one compressed
font mapping with generated differences for PDF text extraction. No system data
or network access is needed at runtime. Regenerate both mappings with:

```sh
go generate ./...            # expects poppler-data under /usr/share/poppler
go run gen_adobe_gb1.go -poppler /usr/local/share/poppler
```

## Adobe-GB1 CID mappings

```go
import "github.com/zc310/fontfix/cidmap"

text, ok := cidmap.GB1Text(580)          // "（", true: PDF text extraction
glyph, ok := cidmap.GB1GlyphUnicode(580) // '︵', true: font repair
```

`GB1Text` preserves Unicode sequences, including variation selectors. A CID
explicitly mapped to U+FFFD still returns `true`; a CID outside 0–30283 returns
`false`. `GB1GlyphUnicode` preserves the original single-code-point font mapping,
including vertical and private-use characters, and returns `false` for unassigned
CIDs. The existing `AdobeGB1CIDToUnicode` API delegates to `GB1GlyphUnicode`.

PDF importers must retain a document's own `ToUnicode` mapping when available.
These collection mappings are fallbacks and do not replace PDF Encoding CMaps,
which map character codes to CIDs. See [cidmap/README.md](cidmap/README.md) for
source hashes and the generation and compatibility checks.
