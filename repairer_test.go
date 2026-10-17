package fontfix

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"math/rand"
	"testing"
)

// loadIntroFont 取出 intro.ofd 中最大的内嵌 TTF，用作真实字体样本。
func loadIntroFont(t testing.TB) []byte {
	zr, err := zip.OpenReader("/home/go/workspace/ofd/test/testdata/intro.ofd")
	if err != nil {
		t.Skip(err)
	}
	defer zr.Close()
	var best []byte
	for _, f := range zr.File {
		name := f.Name
		if len(name) > 4 && (name[len(name)-4:] == ".ttf" || name[len(name)-4:] == ".otf") {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			if len(b) > len(best) {
				best = b
			}
		}
	}
	if best == nil {
		t.Skip("no font in intro.ofd")
	}
	return best
}

// TestRepairerWithGlyphsMatchesRepairWithGlyphs 校验增量路径与全量路径逐字节
// 一致：Repairer 只应在性能上更优，不得改变任何输出字节。
func TestRepairerWithGlyphsMatchesRepairWithGlyphs(t *testing.T) {
	data := loadIntroFont(t)
	base, err := Repair(data)
	if err != nil {
		t.Fatalf("Repair 失败: %v", err)
	}
	pairs := parseCMapPairs(base)
	glyphs := make([]uint16, 0, 256)
	seenGlyph := make(map[uint16]bool, 256)
	for _, pair := range pairs {
		if pair.glyph != 0 && !seenGlyph[pair.glyph] && len(glyphs) < 256 {
			seenGlyph[pair.glyph] = true
			glyphs = append(glyphs, pair.glyph)
		}
	}
	if len(glyphs) < 8 {
		t.Skip("样本字形太少")
	}

	rnd := rand.New(rand.NewSource(20260927))
	// 覆盖：无映射、全重复、码位与原 cmap 冲突、私有区码位、随机子集。
	cases := [][]GlyphMapping{
		nil,
		{{Rune: 'A', Glyph: glyphs[0]}, {Rune: 'A', Glyph: glyphs[1]}},
		{{Rune: 0x20, Glyph: 0}, {Rune: -1, Glyph: glyphs[0]}, {Rune: 'B', Glyph: 0}},
		{{Rune: rune(uint32(packedGlyphBase)), Glyph: glyphs[0]}},
		{{Rune: rune(uint32(packedGlyphBase)) + 3, Glyph: glyphs[1]}},
	}
	for i := 0; i < 40; i++ {
		n := 1 + rnd.Intn(200)
		list := make([]GlyphMapping, 0, n)
		for j := 0; j < n; j++ {
			var r rune
			switch rnd.Intn(4) {
			case 0:
				r = rune(0x4E00 + rnd.Intn(2000))
			case 1:
				r = rune(uint32(packedGlyphBase) + uint32(rnd.Intn(4000)))
			case 2:
				// 与原 cmap 冲突的码位
				r = rune(pairs[rnd.Intn(len(pairs))].code)
			default:
				r = rune(0x20 + rnd.Intn(0x2000))
			}
			list = append(list, GlyphMapping{Rune: r, Glyph: glyphs[rnd.Intn(len(glyphs))]})
		}
		cases = append(cases, list)
	}

	r := NewRepairer(data)
	for i, mappings := range cases {
		want, wantErr := RepairWithGlyphs(data, mappings)
		got, gotErr := r.WithGlyphs(mappings)
		if (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("用例 %d: 错误不一致 want=%v got=%v", i, wantErr, gotErr)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("用例 %d: 输出与 RepairWithGlyphs 不一致 want=%d got=%d 首个差异=%d",
				i, len(want), len(got), firstDiff(want, got))
		}
	}
}

func firstDiff(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		if len(a) < len(b) {
			return len(a)
		}
		return len(b)
	}
	return -1
}

// TestRepairerDoesNotMutateBase 确认增量修补不会污染 Repairer 的基础状态：
// 多次不同映射调用后，早先的输出必须仍可复现。
func TestRepairerDoesNotMutateBase(t *testing.T) {
	data := loadIntroFont(t)
	base, err := Repair(data)
	if err != nil {
		t.Fatal(err)
	}
	pairs := parseCMapPairs(base)
	if len(pairs) < 4 {
		t.Skip("样本太小")
	}
	g0, g1 := pairs[0].glyph, pairs[1].glyph

	r := NewRepairer(data)
	first, err := r.WithGlyphs([]GlyphMapping{{Rune: 'Q', Glyph: g0}, {Rune: 'Q', Glyph: g1}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := r.WithGlyphs([]GlyphMapping{{Rune: 'Z', Glyph: g1}})
		if err != nil {
			t.Fatal(err)
		}
		_ = again
		repeat, err := r.WithGlyphs([]GlyphMapping{{Rune: 'Q', Glyph: g0}, {Rune: 'Q', Glyph: g1}})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, repeat) {
			t.Fatalf("第 %d 次调用后基础状态被污染，输出不可复现", i)
		}
	}
}

func BenchmarkRepairerWithGlyphs(b *testing.B) {
	data := loadIntroFont(b)
	rnd := rand.New(rand.NewSource(1))
	all := make([]GlyphMapping, 0, 800)
	for i := 0; i < 800; i++ {
		all = append(all, GlyphMapping{Rune: rune(0x4E00 + i), Glyph: uint16(1 + rnd.Intn(400))})
	}
	b.Run("全量RepairWithGlyphs", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		for i := 0; i < b.N; i++ {
			if _, err := RepairWithGlyphs(data, all); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("增量Repairer", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		r := NewRepairer(data)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// 模拟逐页增长：每次用不同的前缀映射。
			n := 1 + i%(len(all))
			if _, err := r.WithGlyphs(all[:n]); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestRepairDoesNotMutateInput 确认 Repair 与 RepairWithGlyphs 都只读输入缓冲。
// 表内容改为来源子切片后，任何就地写入都会破坏调用方持有的字体字节。
func TestRepairDoesNotMutateInput(t *testing.T) {
	data := loadIntroFont(t)
	original := append([]byte(nil), data...)

	if _, err := Repair(data); err != nil {
		t.Fatalf("Repair 失败: %v", err)
	}
	if !bytes.Equal(data, original) {
		t.Fatal("Repair 改写了输入缓冲")
	}

	fixed, _ := Repair(data)
	pairs := parseCMapPairs(fixed)
	if len(pairs) < 2 {
		t.Skip("样本太小")
	}
	mappings := []GlyphMapping{
		{Rune: 0x4E00, Glyph: pairs[0].glyph},
		{Rune: 0x4E01, Glyph: pairs[1].glyph},
	}
	if _, err := RepairWithGlyphs(data, mappings); err != nil {
		t.Fatalf("RepairWithGlyphs 失败: %v", err)
	}
	if !bytes.Equal(data, original) {
		t.Fatal("RepairWithGlyphs 改写了输入缓冲")
	}
}

// syntheticRepairerFont 构建一个自带较大 glyf 表的字体，使表校验和在增量路径中
// 占主导。它不依赖外部样本，可在 CI 中稳定运行。
func syntheticRepairerFont() []byte {
	glyf := make([]byte, 256<<10)
	for i := range glyf {
		glyf[i] = byte(i*7 + 3)
	}
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:20], 1000)
	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:6], 512)
	return buildSFNT([]sfntTestTable{
		{tag: "OS/2", data: make([]byte, 96)},
		{tag: "glyf", data: glyf},
		{tag: "head", data: head},
		{tag: "maxp", data: maxp},
		{tag: "name", data: minimalNameTable()},
		{tag: "post", data: minimalPostTable()},
	})
}

// TestRepairerIncrementalMatchesFullRepair 用一个自带的大表字体校验增量路径与
// 全量路径逐字节一致：缓存各表校验和只能加速，不得改变任何输出字节。
//
// 映射只用普通文本码位（CJK、拉丁）：0xF0000+ 私有区是本库存放字形引用的内部
// 命名空间，不属于调用方文本，不在此覆盖。
func TestRepairerIncrementalMatchesFullRepair(t *testing.T) {
	data := syntheticRepairerFont()
	r := NewRepairer(data)
	rnd := rand.New(rand.NewSource(1))
	for iter := 0; iter < 100; iter++ {
		n := 1 + rnd.Intn(200)
		mappings := make([]GlyphMapping, 0, n)
		for j := 0; j < n; j++ {
			var rn rune
			if rnd.Intn(2) == 0 {
				rn = rune(0x4E00 + rnd.Intn(1000))
			} else {
				rn = rune(0x20 + rnd.Intn(0x1000))
			}
			mappings = append(mappings, GlyphMapping{Rune: rn, Glyph: uint16(1 + rnd.Intn(300))})
		}
		want, wantErr := RepairWithGlyphs(data, mappings)
		got, gotErr := r.WithGlyphs(mappings)
		if (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("用例 %d: 错误不一致 want=%v got=%v", iter, wantErr, gotErr)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("用例 %d: 增量输出与全量不一致 want=%d got=%d", iter, len(want), len(got))
		}
	}
}

// TestRepairerReservedRangeMatchesFullPath 覆盖调用方把码位落在库内部私有区
// （0xF0000+ 字形引用命名空间）时的两条路径一致性：调用方显式给出的映射必须
// 优先于按 GID 生成的私有区别名，Repairer 与 RepairWithGlyphs 不得给出不同 cmap。
func TestRepairerReservedRangeMatchesFullPath(t *testing.T) {
	const numGlyphs = 300
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:20], 1000)
	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:6], numGlyphs)
	// 无 OS/2，Repair 会重建并补入私有区字形引用 cmap，使基础 cmap 覆盖 0xF0000+。
	data := buildSFNT([]sfntTestTable{
		{tag: "head", data: head},
		{tag: "maxp", data: maxp},
		{tag: "glyf", data: make([]byte, 4096)},
	})
	r := NewRepairer(data)
	rnd := rand.New(rand.NewSource(42))
	for iter := 0; iter < 500; iter++ {
		n := 1 + rnd.Intn(80)
		mappings := make([]GlyphMapping, 0, n)
		for j := 0; j < n; j++ {
			var rn rune
			switch rnd.Intn(4) {
			case 0:
				rn = rune(0x4E00 + rnd.Intn(2000))
			case 1, 2:
				rn = rune(uint32(packedGlyphBase) + uint32(rnd.Intn(numGlyphs+20)))
			default:
				rn = rune(0x20 + rnd.Intn(0x2000))
			}
			mappings = append(mappings, GlyphMapping{Rune: rn, Glyph: uint16(rnd.Intn(numGlyphs + 1))})
		}
		want, wantErr := RepairWithGlyphs(data, mappings)
		got, gotErr := r.WithGlyphs(mappings)
		if (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("用例 %d: 错误不一致 want=%v got=%v", iter, wantErr, gotErr)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("用例 %d: 输出不一致 want=%d got=%d", iter, len(want), len(got))
		}
	}
}

// TestRepairerOutputChecksumsValid 校验增量输出的每张表校验和与整字体校验和都
// 与规范一致：缓存校验和不得让 head 的 checksumAdjustment 计算失配。
func TestRepairerOutputChecksumsValid(t *testing.T) {
	data := syntheticRepairerFont()
	out, err := NewRepairer(data).WithGlyphs([]GlyphMapping{{Rune: 'A', Glyph: 2}, {Rune: 'B', Glyph: 3}})
	if err != nil {
		t.Fatal(err)
	}
	numTables := int(binary.BigEndian.Uint16(out[4:6]))
	for i := 0; i < numTables; i++ {
		record := 12 + i*16
		tag := string(out[record : record+4])
		offset := int(binary.BigEndian.Uint32(out[record+8 : record+12]))
		length := int(binary.BigEndian.Uint32(out[record+12 : record+16]))
		body := out[offset : offset+length]
		sum := tableChecksum(body)
		if tag == "head" && length >= 12 {
			sum -= binary.BigEndian.Uint32(body[8:12])
		}
		if stored := binary.BigEndian.Uint32(out[record+4 : record+8]); sum != stored {
			t.Fatalf("表 %q 校验和 = %#x, 目录记录 = %#x", tag, sum, stored)
		}
	}
	var whole uint32
	for offset := 0; offset+4 <= len(out); offset += 4 {
		whole += binary.BigEndian.Uint32(out[offset : offset+4])
	}
	if whole != 0xB1B0AFBA {
		t.Fatalf("整字体校验和 = %#x, want 0xB1B0AFBA", whole)
	}
}
