package cli

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHoundArtFitsNarrowTerminals(t *testing.T) {
	for i, l := range strings.Split(houndArt, "\n") {
		if strings.ContainsRune(l, '\t') || utf8.RuneCountInString(l) > 72 || strings.TrimRight(l, " ") != l {
			t.Errorf("line %d must be spaces-only, at most 72 columns, without trailing spaces: %q", i+1, l)
		}
	}
	if !strings.Contains(houndArt, "(/") {
		t.Error("the hound has lost its olive crown")
	}
}

func TestInitBannerOnlyOnTerminals(t *testing.T) {
	if code, out, _ := run(t, t.TempDir(), "init"); code != 0 || strings.Contains(out, "(/") {
		t.Fatalf("piped init output must stay plain: %d\n%s", code, out)
	}
}

func TestHoundMaskMatchesArt(t *testing.T) {
	art, mask := strings.Split(houndArt, "\n"), strings.Split(houndMask, "\n")
	if len(art) != len(mask) {
		t.Fatalf("mask has %d lines, art has %d", len(mask), len(art))
	}
	for i := range art {
		if len(art[i]) != len(mask[i]) {
			t.Fatalf("line %d: mask is %d wide, art is %d", i+1, len(mask[i]), len(art[i]))
		}
		for j := range art[i] {
			if (art[i][j] == ' ') != (mask[i][j] == ' ') {
				t.Fatalf("line %d col %d: art %q but mask %q", i+1, j+1, art[i][j], mask[i][j])
			}
			if mask[i][j] != ' ' && houndColors[mask[i][j]] == "" {
				t.Fatalf("line %d col %d: unknown colour class %q", i+1, j+1, mask[i][j])
			}
		}
	}
	if plain := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(colorize(houndArt, houndMask), ""); plain != houndArt {
		t.Fatal("colouring must not change the characters")
	}
}
