//go:build ignore

// Command gen_adobe_gb1 regenerates cidmap/gb1_data.go from Adobe's Adobe-GB1
// CID→Unicode tables. The font mapping is stored once; PDF text mappings are
// generated as differences against it.
//
// The table ships with poppler-data, which redistributes Adobe's CMap data
// under BSD-3-Clause. Poppler installs it under /usr/share/poppler; override
// with -poppler when it lives elsewhere (macOS Homebrew, Nix, a container
// image, or a checkout of https://gitlab.freedesktop.org/poppler/poppler-data).
//
// Run from the fontfix directory:
//
//	go generate ./...
//
// The generated file is committed so that building fontfix never requires
// poppler-data to be installed. Regenerating is only needed when switching to a
// different Adobe-GB1 revision.
package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"go/format"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var popplerDir = flag.String("poppler", "/usr/share/poppler", "directory containing cidToUnicode and cMap")

const gb1CIDs = 30284

func main() {
	flag.Parse()
	source := filepath.Join(*popplerDir, "cidToUnicode", "Adobe-GB1")
	data, err := os.ReadFile(source)
	if err != nil {
		log.Fatalf("read %s: %v (point -poppler at the poppler-data installation)", source, err)
	}
	codes := parseCIDToUnicode(string(data))
	if len(codes) != gb1CIDs {
		log.Fatalf("parsed %d entries, want %d; the data format may have changed", len(codes), gb1CIDs)
	}
	textSource := filepath.Join(*popplerDir, "cMap", "Adobe-GB1", "Adobe-GB1-UCS2")
	textData, err := os.ReadFile(textSource)
	if err != nil {
		log.Fatalf("read %s: %v", textSource, err)
	}
	texts, err := parseTextCMap(textData)
	if err != nil {
		log.Fatalf("parse %s: %v", textSource, err)
	}
	packed, err := pack(codes)
	if err != nil {
		log.Fatalf("compress table: %v", err)
	}
	sourceCode, err := render(packed, supplementary(codes), codes, texts)
	if err != nil {
		log.Fatalf("render source: %v", err)
	}
	if err := os.WriteFile("cidmap/gb1_data.go", sourceCode, 0o644); err != nil {
		log.Fatalf("write output: %v", err)
	}
	manifest := fmt.Sprintf("%x  cidToUnicode/Adobe-GB1\n%x  cMap/Adobe-GB1/Adobe-GB1-UCS2\n", sha256.Sum256(data), sha256.Sum256(textData))
	if err := os.WriteFile("cidmap/SHA256SUMS", []byte(manifest), 0o644); err != nil {
		log.Fatalf("write source hashes: %v", err)
	}
	fmt.Printf("wrote cidmap/gb1_data.go: %d CIDs, %d bytes compressed\n", len(codes), len(packed))
}

// parseCIDToUnicode reads poppler's cidToUnicode/Adobe-GB1, a single column of
// hex Unicode values whose line number is the CID.
func parseCIDToUnicode(text string) []uint32 {
	var codes []uint32
	for index, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		value, err := strconv.ParseUint(line, 16, 32)
		if err != nil {
			log.Fatalf("line %d: %q is not hexadecimal: %v", index+1, line, err)
		}
		if !utf8.ValidRune(rune(value)) {
			log.Fatalf("line %d: invalid Unicode code point %x", index+1, value)
		}
		codes = append(codes, uint32(value))
	}
	return codes
}

// parseTextCMap reads the fixed Adobe data used at generation time. It supports
// bfchar, incrementing UTF-16BE bfrange and array bfrange, including supplementary
// characters and strings of multiple code points. No CMap parser is needed at
// runtime in cidmap.
func parseTextCMap(data []byte) ([]string, error) {
	data = regexp.MustCompile(`%[^\r\n]*`).ReplaceAll(data, nil)
	tokens := regexp.MustCompile(`beginbfchar|endbfchar|beginbfrange|endbfrange|<[^<>]*>|\[|\]`).FindAllString(string(data), -1)
	texts := make([]string, gb1CIDs)
	decodeHex := func(token string) ([]byte, error) {
		if len(token) < 3 || token[0] != '<' || token[len(token)-1] != '>' {
			return nil, fmt.Errorf("expected hexadecimal string, got %q", token)
		}
		return hex.DecodeString(strings.Join(strings.Fields(token[1:len(token)-1]), ""))
	}
	readCID := func(token string) (int, error) {
		raw, err := decodeHex(token)
		if err != nil || len(raw) != 2 {
			return 0, fmt.Errorf("invalid CID %q", token)
		}
		cid := int(binary.BigEndian.Uint16(raw))
		if cid >= gb1CIDs {
			return 0, fmt.Errorf("CID outside Adobe-GB1: %d", cid)
		}
		return cid, nil
	}
	put := func(cid int, raw []byte) error {
		if len(raw) == 0 || len(raw)%2 != 0 || texts[cid] != "" {
			return fmt.Errorf("invalid or duplicate Unicode mapping for CID %d", cid)
		}
		units := make([]uint16, len(raw)/2)
		for i := range units {
			units[i] = binary.BigEndian.Uint16(raw[i*2:])
		}
		texts[cid] = string(utf16.Decode(units))
		return nil
	}
	mode := ""
	for i := 0; i < len(tokens); i++ {
		switch tokens[i] {
		case "beginbfchar", "beginbfrange":
			mode = tokens[i]
			continue
		case "endbfchar", "endbfrange":
			mode = ""
			continue
		}
		if mode == "" {
			continue
		}
		first, err := readCID(tokens[i])
		if err != nil {
			return nil, err
		}
		last := first
		i++
		if mode == "beginbfrange" {
			if i >= len(tokens) {
				return nil, fmt.Errorf("truncated range at CID %d", first)
			}
			last, err = readCID(tokens[i])
			if err != nil || last < first {
				return nil, fmt.Errorf("invalid range at CID %d", first)
			}
			i++
		}
		if i >= len(tokens) {
			return nil, fmt.Errorf("missing Unicode mapping for CID %d", first)
		}
		if tokens[i] == "[" {
			for cid := first; cid <= last; cid++ {
				i++
				if i >= len(tokens) {
					return nil, fmt.Errorf("truncated array at CID %d", cid)
				}
				raw, err := decodeHex(tokens[i])
				if err != nil {
					return nil, err
				}
				if err := put(cid, raw); err != nil {
					return nil, err
				}
			}
			i++
			if i >= len(tokens) || tokens[i] != "]" {
				return nil, fmt.Errorf("unterminated array at CID %d", first)
			}
			continue
		}
		raw, err := decodeHex(tokens[i])
		if err != nil {
			return nil, err
		}
		for cid := first; cid <= last; cid++ {
			if err := put(cid, raw); err != nil {
				return nil, err
			}
			for j := len(raw) - 1; j >= 0; j-- {
				raw[j]++
				if raw[j] != 0 {
					break
				}
			}
		}
	}
	for cid, text := range texts {
		if text == "" {
			return nil, fmt.Errorf("missing Unicode mapping for CID %d", cid)
		}
	}
	return texts, nil
}

// pack stores the table as big-endian uint16 (values above the BMP are stored as
// 0 and carried by a sparse supplementary-plane list instead) and deflates it,
// because 30284 raw uint16s would be roughly 240KB of Go source.
func pack(codes []uint32) ([]byte, error) {
	raw := make([]byte, 0, len(codes)*2)
	for _, code := range codes {
		value := uint16(0)
		if code <= 0xFFFF {
			value = uint16(code)
		}
		raw = binary.BigEndian.AppendUint16(raw, value)
	}
	var compressed bytes.Buffer
	writer, err := zlib.NewWriterLevel(&compressed, zlib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(raw); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return compressed.Bytes(), nil
}

// supplementary lists the CIDs whose Unicode is above the BMP, which the packed
// uint16 table cannot hold.
func supplementary(codes []uint32) [][3]uint32 {
	var out [][3]uint32
	for cid, code := range codes {
		if code > 0xFFFF {
			out = append(out, [3]uint32{uint32(cid), 0, code})
		}
	}
	return out
}

// render writes the generated Go source, split into base64 chunks so no single
// line in the repository is pathologically long.
func render(packed []byte, supplement [][3]uint32, codes []uint32, texts []string) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(header)
	const chunk = 96
	encoded := base64.StdEncoding.EncodeToString(packed)
	out.WriteString("const adobeGB1Packed = \"\" +\n")
	for start := 0; start < len(encoded); start += chunk {
		end := min(start+chunk, len(encoded))
		separator := " +\n"
		if end == len(encoded) {
			separator = "\n"
		}
		fmt.Fprintf(&out, "\t%q%s", encoded[start:end], separator)
	}
	out.WriteString("\n// adobeGB1Supplementary carries the CIDs whose Unicode is above the BMP, which\n")
	out.WriteString("// the uint16 table cannot hold. Entries are {CID, _, Unicode}.\n")
	out.WriteString("var adobeGB1Supplementary = [][3]uint32{\n")
	for _, entry := range supplement {
		fmt.Fprintf(&out, "\t{%d, 0, 0x%X},\n", entry[0], entry[2])
	}
	out.WriteString("}\n")
	out.WriteString("\n// adobeGB1TextOverrides contains only the differences from the font mapping.\n")
	out.WriteString("// Explicit replacement characters and variation selectors are preserved.\n")
	out.WriteString("var adobeGB1TextOverrides = map[int]string{\n")
	for cid, text := range texts {
		if codes[cid] == 0 || text != string(rune(codes[cid])) {
			fmt.Fprintf(&out, "\t%d: %q,\n", cid, text)
		}
	}
	out.WriteString("}\n")
	return format.Source(out.Bytes())
}

const header = `// Code generated by gen_adobe_gb1.go; DO NOT EDIT.

// Regenerate with ` + "`go generate ./...`" + ` in the fontfix directory. The generator reads
// Adobe's Adobe-GB1 CID→Unicode table and Adobe-GB1-UCS2 CMap as redistributed
// by poppler-data (BSD-3-Clause, Copyright 1990-2020 Adobe). See ../NOTICE for
// the full notice and SHA256SUMS for the source hashes.
//
// The table is deflated because 30284 uint16 values as a Go slice literal would
// be roughly 240KB of source. Unicode above the BMP does not fit in uint16 and is
// carried by adobeGB1Supplementary instead.

package cidmap

// adobeGB1CIDs is the number of CIDs covered by the table. Adobe-GB1 defines
// CID 0 through 30283.
const adobeGB1CIDs = 30284

// adobeGB1Packed is the deflated Adobe-GB1 CID→Unicode table: CID c maps to the
// big-endian uint16 at offset c*2. Zero means the CID has no mapping, or its
// Unicode lives in the supplementary plane.
`
