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
