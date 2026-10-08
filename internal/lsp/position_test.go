package lsp

import "testing"

func TestPositionEncodings(t *testing.T) {
	line := "a := \"żółw 🐢\"; x"
	byteX := len(line) - 1
	if got := ByteColumnToCharacter(line, byteX, "utf-8"); got != byteX {
		t.Fatal(got)
	}
	ch := ByteColumnToCharacter(line, byteX, "utf-16")
	if ch != 16 {
		t.Fatalf("utf-16 offset = %d", ch)
	}
	if CharacterToByteColumn(line, ch, "utf-16") != byteX {
		t.Fatal("round trip failed")
	}
}
