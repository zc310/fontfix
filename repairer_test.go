package fontfix

import (
	"archive/zip"
	"bytes"
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
		return min(len(a), len(b))
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
