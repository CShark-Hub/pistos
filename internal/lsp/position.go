package lsp

import "unicode/utf8"

// ByteColumnToCharacter converts a 0-based byte offset within line to the server's position encoding.
func ByteColumnToCharacter(line string, byteCol int, encoding string) int {
	if byteCol > len(line) {
		byteCol = len(line)
	}
	if encoding == "utf-8" {
		return byteCol
	}
	n := 0
	for _, r := range line[:byteCol] {
		n += utf16Len(r)
	}
	return n
}

// CharacterToByteColumn converts a server character offset back to a 0-based byte offset.
func CharacterToByteColumn(line string, character int, encoding string) int {
	if encoding == "utf-8" {
		if character > len(line) {
			return len(line)
		}
		return character
	}
	units := 0
	for i, r := range line {
		if units >= character {
			return i
		}
		units += utf16Len(r)
	}
	return len(line)
}

func utf16Len(r rune) int {
	if r >= 0x10000 && utf8.ValidRune(r) {
		return 2
	}
	return 1
}
