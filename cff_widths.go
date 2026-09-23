package fontfix

import "encoding/binary"

// cffGlyphAdvances 返回 bare CFF 中每个字形的 advance 宽度（CFF 单位）。
//
// 宽度取 CharString 的首个宽度操作数（nominalWidthX + 操作数）；CharString 未
// 显式给出宽度时使用 Private DICT 的 defaultWidthX。CID CFF 的 Private DICT 位于
// FDArray 的各 Font DICT 中，这里取首个 Font DICT（OFD 内嵌子集通常为单 Font
// DICT）。解析失败返回 nil。
//
// 此前 wrapCFF 对所有字形使用固定 advance 500，对 CJK（整字宽 1000）会让查看器
// 按半宽前进，相邻文字相互叠压。
func cffGlyphAdvances(data []byte, numGlyphs int) []uint16 {
	if len(data) < 4 || numGlyphs <= 0 {
		return nil
	}
	headerSize := int(data[2])
	_, _, offset, err := cffIndex(data, headerSize)
	if err != nil {
		return nil
	}
	_, top, offset, err := cffIndex(data, offset)
	if err != nil {
		return nil
	}
	_, _, offset, err = cffIndex(data, offset)
	if err != nil {
		return nil
	}
	_, _, offset, err = cffIndex(data, offset)
	if err != nil {
		return nil
	}

	nominal, def := 0, 0
	if _, isCID := cffDictValue(top, 1230); isCID {
		fdArrayOffset, ok := cffDictValue(top, 1236)
		if !ok {
			return nil
		}
		fds, err := cffIndexEntries(data, fdArrayOffset)
		if err != nil || len(fds) == 0 {
			return nil
		}
		nominal, def = cffPrivateWidths(data, fds[0])
	} else {
		nominal, def = cffPrivateWidths(data, top)
	}

	entries, err := cffCharStringsEntries(data)
	if err != nil || len(entries) < numGlyphs {
		return nil
	}
	advances := make([]uint16, numGlyphs)
	for i := 0; i < numGlyphs; i++ {
		width := cffCharstringWidth(entries[i], nominal, def)
		if width < 0 {
			width = 0
		}
		if width > 0xffff {
			width = 0xffff
		}
		advances[i] = uint16(width)
	}
	return advances
}

// cffPrivateWidths 从 DICT 的 Private（操作符 18）取 nominalWidthX/defaultWidthX。
func cffPrivateWidths(data, dict []byte) (int, int) {
	ops := cffDictOperands(dict, 18) // Private: [size, offset]
	if len(ops) < 2 || ops[1] < 0 || ops[1] > len(data) {
		return 0, 0
	}
	start, end := ops[1], ops[1]+ops[0]
	if ops[0] < 0 || end > len(data) || end < start {
		end = len(data)
	}
	priv := data[start:end]
	nominal, def := 0, 0
	if v := cffDictOperands(priv, 21); len(v) > 0 {
		nominal = v[0]
	}
	if v := cffDictOperands(priv, 20); len(v) > 0 {
		def = v[0]
	}
	return nominal, def
}

// cffCharstringWidth 解析 Type2 CharString 的首个宽度操作数。存在宽度时返回
// nominalWidthX + 操作数，否则返回 defaultWidthX。
func cffCharstringWidth(cs []byte, nominal, def int) int {
	operands := 0
	first := 0
	for i := 0; i < len(cs); {
		b := cs[i]
		switch {
		case b == 28 || b == 255 || b >= 32:
			value, size := cffCharstringOperand(cs, i)
			if operands == 0 {
				first = value
			}
			operands++
			i += size
			continue
		case b == 12:
			// 转义操作符不会出现在首个清栈操作符之前，按无显式宽度处理。
			return def
		default:
			switch b {
			case 1, 3, 18, 23, 19, 20: // hstem/vstem/hstemhm/vstemhm/hintmask/cntrmask
				if operands%2 == 1 {
					return nominal + first
				}
				return def
			case 21: // rmoveto
				if operands == 3 {
					return nominal + first
				}
				return def
			case 22, 4: // hmoveto/vmoveto
				if operands == 2 {
					return nominal + first
				}
				return def
			case 14: // endchar
				if operands == 1 || operands == 5 {
					return nominal + first
				}
				return def
			default:
				return def
			}
		}
	}
	return def
}

// cffCharstringOperand 解码 Type2 CharString 中位置 i 的操作数，返回值与字节数。
func cffCharstringOperand(cs []byte, i int) (int, int) {
	b := cs[i]
	switch {
	case b >= 32 && b <= 246:
		return int(b) - 139, 1
	case b >= 247 && b <= 250:
		if i+1 < len(cs) {
			return (int(b)-247)*256 + int(cs[i+1]) + 108, 2
		}
	case b >= 251 && b <= 254:
		if i+1 < len(cs) {
			return -(int(b)-251)*256 - int(cs[i+1]) - 108, 2
		}
	case b == 28:
		if i+2 < len(cs) {
			return int(int16(binary.BigEndian.Uint16(cs[i+1 : i+3]))), 3
		}
	case b == 255:
		if i+4 < len(cs) {
			return int(int32(binary.BigEndian.Uint32(cs[i+1:i+5]))) >> 16, 5
		}
	}
	return 0, 1
}

// cffDictOperands 返回 DICT 中指定操作符的全部操作数。
func cffDictOperands(data []byte, wantedOperator int) []int {
	operands := make([]int, 0, 4)
	for i := 0; i < len(data); {
		b := data[i]
		switch {
		case b >= 32 && b <= 246:
			operands = append(operands, int(b)-139)
		case b >= 247 && b <= 250:
			if i+1 >= len(data) {
				return nil
			}
			operands = append(operands, (int(b)-247)*256+int(data[i+1])+108)
			i++
		case b >= 251 && b <= 254:
			if i+1 >= len(data) {
				return nil
			}
			operands = append(operands, -(int(b)-251)*256-int(data[i+1])-108)
			i++
		case b == 28:
			if i+2 >= len(data) {
				return nil
			}
			operands = append(operands, int(int16(binary.BigEndian.Uint16(data[i+1:i+3]))))
			i += 2
		case b == 29:
			if i+4 >= len(data) {
				return nil
			}
			operands = append(operands, int(int32(binary.BigEndian.Uint32(data[i+1:i+5]))))
			i += 4
		case b == 30:
			for i++; i < len(data); i++ {
				if data[i]&0x0f == 0x0f || data[i]>>4 == 0x0f {
					break
				}
			}
		case b == 12:
			if i+1 >= len(data) {
				return nil
			}
			if wantedOperator == 1200+int(data[i+1]) {
				return append([]int(nil), operands...)
			}
			i++
			operands = operands[:0]
		default:
			if int(b) == wantedOperator {
				return append([]int(nil), operands...)
			}
			operands = operands[:0]
		}
		i++
	}
	return nil
}
