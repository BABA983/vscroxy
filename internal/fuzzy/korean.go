package fuzzy

// This file ported getKoreanAltChars from VS Code's
// src/vs/base/common/naturalLanguage/korean.ts. The packed tables are copied
// verbatim; only the scratch buffer became local to each call.

const (
	comboFA = 0x61_00 | 'f' // 'a'<<8 | 'f'
	comboFG = 0x67_00 | 'f'
	comboFQ = 0x71_00 | 'f'
	comboFR = 0x72_00 | 'f'
	comboFT = 0x74_00 | 'f'
	comboFV = 0x76_00 | 'f'
	comboFX = 0x78_00 | 'f'
	comboHK = 0x6B_00 | 'h'
	comboHL = 0x6C_00 | 'h'
	comboHO = 0x6F_00 | 'h'
	comboML = 0x6C_00 | 'm'
	comboNJ = 0x6A_00 | 'n'
	comboNL = 0x6C_00 | 'n'
	comboNP = 0x70_00 | 'n'
	comboQT = 0x74_00 | 'q'
	comboRT = 0x74_00 | 'r'
	comboSG = 0x67_00 | 's'
	comboSW = 0x77_00 | 's'
)

var modernConsonants = []int{
	'r', 'R', 's', 'e', 'E', 'f', 'a', 'q', 'Q', 't', 'T', 'd', 'w', 'W', 'c', 'z', 'x', 'v', 'g',
}

var modernVowels = []int{
	'k', 'o', 'i', 'O', 'j', 'p', 'u', 'P', 'h', comboHK, comboHO, comboHL, 'y', 'n', comboNJ, comboNP, comboNL, 'b', 'm', comboML, 'l',
}

var modernFinalConsonants = []int{
	'r', 'R', comboRT, 's', comboSW, comboSG, 'e', 'f', comboFR, comboFA, comboFQ, comboFT, comboFX, comboFV, comboFG, 'a', 'q', comboQT, 't', 'T', 'd', 'w', 'c', 'z', 'x', 'v', 'g',
}

var compatibilityJamo = []int{
	'r', 'R', comboRT, 's', comboSW, comboSG, 'e', 'E', 'f', comboFR, comboFA, comboFQ, comboFT, comboFX, comboFV, comboFG,
	'a', 'q', 'Q', comboQT, 't', 'T', 'd', 'w', 'W', 'c', 'z', 'x', 'v', 'g', 'k', 'o',
	'i', 'O', 'j', 'p', 'u', 'P', 'h', comboHK, comboHO, comboHL, 'y', 'n', comboNJ, comboNP, comboNL,
	'b', 'm', comboML, 'l',
}

const (
	hangulInitialConsonant = 0x1100
	hangulVowel            = 0x1161
	hangulFinalConsonant   = 0x11A8
	hangulCompatibility    = 0x3131
)

// getAlternateCodes corresponds to getKoreanAltChars: it returns the ASCII
// key sequence a Hangul character can be typed with.
func getAlternateCodes(code rune) []rune {
	buffer := make([]int, 0, 10)
	disassembleKorean(int(code), &buffer)
	if len(buffer) == 0 {
		return nil
	}
	result := make([]rune, len(buffer))
	for i, c := range buffer {
		result[i] = rune(c)
	}
	return result
}

func disassembleKorean(code int, buffer *[]int) {
	addCodesFromTable(code, modernConsonants, hangulInitialConsonant, buffer)
	if len(*buffer) > 0 {
		return
	}
	addCodesFromTable(code, modernVowels, hangulVowel, buffer)
	if len(*buffer) > 0 {
		return
	}
	addCodesFromTable(code, modernFinalConsonants, hangulFinalConsonant, buffer)
	if len(*buffer) > 0 {
		return
	}
	addCodesFromTable(code, compatibilityJamo, hangulCompatibility, buffer)
	if len(*buffer) > 0 {
		return
	}

	if code < 0xAC00 || code > 0xD7A3 {
		return
	}
	hangulIndex := code - 0xAC00
	vowelAndFinal := hangulIndex % 588
	initialIndex := hangulIndex / 588
	vowelIndex := vowelAndFinal / 28
	finalIndex := vowelAndFinal%28 - 1

	if initialIndex < len(modernConsonants) {
		addCodesFromTable(initialIndex, modernConsonants, 0, buffer)
	} else if hangulInitialConsonant+initialIndex-hangulCompatibility < len(compatibilityJamo) {
		addCodesFromTable(hangulInitialConsonant+initialIndex, compatibilityJamo, hangulCompatibility, buffer)
	}
	if vowelIndex < len(modernVowels) {
		addCodesFromTable(vowelIndex, modernVowels, 0, buffer)
	} else if hangulVowel+vowelIndex-hangulCompatibility < len(compatibilityJamo) {
		addCodesFromTable(hangulVowel+vowelIndex, compatibilityJamo, hangulCompatibility, buffer)
	}
	if finalIndex >= 0 {
		if finalIndex < len(modernFinalConsonants) {
			addCodesFromTable(finalIndex, modernFinalConsonants, 0, buffer)
		} else if hangulFinalConsonant+finalIndex-hangulCompatibility < len(compatibilityJamo) {
			addCodesFromTable(hangulFinalConsonant+finalIndex, compatibilityJamo, hangulCompatibility, buffer)
		}
	}
}

func addCodesFromTable(code int, table []int, start int, buffer *[]int) {
	if code >= start && code < start+len(table) {
		addCodesToBuffer(table[code-start], buffer)
	}
}

func addCodesToBuffer(codes int, buffer *[]int) {
	if codes == 0 {
		return
	}
	*buffer = append(*buffer, codes&0xFF)
	if codes>>8 != 0 {
		*buffer = append(*buffer, (codes>>8)&0xFF)
	}
	if codes>>16 != 0 {
		*buffer = append(*buffer, (codes>>16)&0xFF)
	}
}
