# Adobe CID mappings

This package contains Adobe-GB1 mapping data shared by font repair and PDF text
extraction. It depends only on the Go standard library and includes no PDF or
font-repair implementation.

| API | Upstream source | Result |
| --- | --- | --- |
| `GB1GlyphUnicode(cid)` | `cidToUnicode/Adobe-GB1` | One Unicode code point for building a font cmap; preserves vertical, private-use and compatibility characters. |
| `GB1Text(cid)` | `cMap/Adobe-GB1/Adobe-GB1-UCS2` | A Unicode string for text extraction; preserves ordinary punctuation and multi-code-point variation sequences. |

The collection contains CIDs 0–30283. The font mapping has 30,010 assigned CIDs.
The PDF mapping explicitly covers all 30,284 CIDs, including entries mapped to
U+FFFD and entries absent from the font mapping. Both APIs return `false` for
out-of-range CIDs; `GB1GlyphUnicode` also returns `false` for unassigned font CIDs.
The package never applies Unicode normalization.

## Data and generation

`gb1_data.go` stores one zlib-compressed BMP font mapping, six supplementary-plane
entries and 402 text overrides: 128 different mappings for assigned font CIDs and
274 mappings for CIDs unassigned in the font table. It does not store two complete
tables. Both APIs reuse one lazily initialized, read-only BMP table.

The sources are from poppler-data 0.4.12, which redistributes Adobe data:

- <https://github.com/adobe-type-tools/mapping-resources-pdf>
- <https://gitlab.freedesktop.org/poppler/poppler-data>

[`SHA256SUMS`](SHA256SUMS) records the unmodified source hashes. The complete
Adobe BSD notice is retained in [`../NOTICE`](../NOTICE).

From the fontfix repository root:

```sh
go generate ./...
# Or select another poppler-data installation:
go run gen_adobe_gb1.go -poppler /usr/local/share/poppler
```

The generator reads both source files, validates coverage and derives every text
override. Generated data is committed; building and running the package needs no
poppler-data installation or network access. A source update requires reviewing
the new hashes and mapping changes before updating the compatibility fingerprints.

## Compatibility checks

Tests fingerprint both original lookup results for all 65,536 16-bit CIDs:
font repair through the existing root API and PDF text extraction in this package.
Focused tests cover vertical punctuation, supplementary characters, all four
variation sequences, unassigned CIDs, range checks and concurrent lookups.

```sh
go test ./...
go test -race ./...
```
