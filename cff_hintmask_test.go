package fontfix

import "testing"

func TestRewriteCntrMaskOperators(t *testing.T) {
	// hstem(2 stems) + 隐式 vstem(1 stem) + cntrmask + 1 个掩码字节 + rmoveto
	charstring := []byte{139, 139, 139, 139, 1, 139, 139, 20, 0xAA, 139, 139, 21}
	rewriteCntrMaskOperators(charstring)
	if charstring[7] != 19 {
		t.Fatalf("cntrmask not rewritten to hintmask: %v", charstring)
	}
	if charstring[8] != 0xAA {
		t.Fatalf("mask byte changed: %v", charstring)
	}
	for i, want := range []byte{139, 139, 139, 139, 1, 139, 139, 19, 0xAA, 139, 139, 21} {
		if charstring[i] != want {
			t.Fatalf("byte %d = %d, want %d (%v)", i, charstring[i], want, charstring)
		}
	}
}

func TestRewriteCntrMaskOperatorsSkipsNumbersAndMasks(t *testing.T) {
	// 255 四字节数、28 两字节数中的 0x14 不能当作 cntrmask 改写。
	charstring := []byte{255, 0, 0, 0, 20, 28, 0x00, 0x14, 21}
	rewriteCntrMaskOperators(charstring)
	if charstring[4] != 20 {
		t.Fatalf("number byte was rewritten: %v", charstring)
	}
	if charstring[7] != 0x14 {
		t.Fatalf("number operand byte was rewritten: %v", charstring)
	}
}
