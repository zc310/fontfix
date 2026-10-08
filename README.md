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

`adobe_gb1_data.go` embeds Adobe's Adobe-GB1 character collection data,
Copyright 1990-2018 Adobe Systems Incorporated, as redistributed by
[poppler-data](https://gitlab.freedesktop.org/poppler/poppler-data) under
BSD-3-Clause. It resolves a CID-keyed font's CID to a character when the font
provides no ToUnicode CMap, which is otherwise the only remaining authority for
what the CID means. Regenerate it with:

```sh
go generate ./...            # expects poppler-data under /usr/share/poppler
go run gen_adobe_gb1.go -poppler /usr/local/share/poppler
```
