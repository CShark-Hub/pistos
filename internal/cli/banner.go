package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// houndArt is a Cretan Hound (Kritikos Ichnilatis) in profile, crowned with an olive wreath.
var houndArt = strings.Join([]string{
	"                                           /\\",
	"                                          / /|",
	"                  /)   /)                / / |",
	"                 (/   (/       /)   /)  / /  |",
	"             ~~@~~~~@~~~-.    (/   (/  / /  /)   /)",
	"              (\\   (\\    '~~@~~~~@~~~-. /  (/|  (/",
	"               \\)   \\)     (\\   (\\   /'~~@~~~~@~~~~",
	"                 ____/      \\)   \\) / / (\\   (\\",
	"            ____/                        \\)   \\)",
	"       ____/               _.-._                \\",
	"  ____/                   <_(@)_>                \\",
	" (@)                     _.                       |",
	"  \\__________________.-''                         |",
	"   \\                                              |",
	"    \\_____________________                        |",
	"                          \\__________             |",
	"                                     \\            |",
	"                                      \\           |",
}, "\n")

// houndMask labels every character of houndArt for colouring: L leaf, O olive, B branch, H head,
// E eye, P pupil, N nose. It must stay aligned with houndArt character for character.
var houndMask = strings.Join([]string{
	"                                           HH",
	"                                          H HH",
	"                  LL   LL                H H H",
	"                 LL   LL       LL   LL  H H  H",
	"             BBOBBBBOBBBBB    LL   LL  H H  LL   LL",
	"              LL   LL    BBBOBBBBOBBBBB H  LLH  LL",
	"               LL   LL     LL   LL   HBBBOBBBBOBBBB",
	"                 HHHHH      LL   LL H H LL   LL",
	"            HHHHH                        LL   LL",
	"       HHHHH               EEEEE                H",
	"  HHHHH                   EEEPEEE                H",
	" NNN                     HH                       H",
	"  HHHHHHHHHHHHHHHHHHHHHHH                         H",
	"   H                                              H",
	"    HHHHHHHHHHHHHHHHHHHHHH                        H",
	"                          HHHHHHHHHHH             H",
	"                                     H            H",
	"                                      H           H",
}, "\n")

// houndColors are 256-colour foregrounds that read on dark and light terminals: olive-green leaves,
// ripe purple olives, a brown branch, the breed's fawn coat, an amber eye and a liver nose.
var houndColors = map[byte]string{
	'L': "\x1b[38;5;70m",
	'O': "\x1b[38;5;97m",
	'B': "\x1b[38;5;94m",
	'H': "\x1b[38;5;180m",
	'E': "\x1b[38;5;180m",
	'P': "\x1b[38;5;172m",
	'N': "\x1b[38;5;131m",
}

const ansiReset = "\x1b[0m"

// colorize applies houndColors by mask, switching colour only where the label changes.
func colorize(art, mask string) string {
	artLines, maskLines := strings.Split(art, "\n"), strings.Split(mask, "\n")
	var b strings.Builder
	for i, l := range artLines {
		cur := byte(0)
		for j := 0; j < len(l); j++ {
			cls := byte(' ')
			if i < len(maskLines) && j < len(maskLines[i]) {
				cls = maskLines[i][j]
			}
			if l[j] != ' ' && cls != cur {
				if c, ok := houndColors[cls]; ok {
					b.WriteString(c)
					cur = cls
				}
			}
			b.WriteByte(l[j])
		}
		if cur != 0 {
			b.WriteString(ansiReset)
		}
		if i < len(artLines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// useColor follows the NO_COLOR convention (https://no-color.org) and skips dumb terminals.
func useColor() bool {
	return os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

// printBanner greets interactive `pistos init` runs; scripts and pipes get only the plain output.
func (a *app) printBanner(w io.Writer) {
	if !isTerminal(w) {
		return
	}
	art := houndArt
	if useColor() {
		art = colorize(houndArt, houndMask)
	}
	fmt.Fprintf(w, "%s\n\n  pistos %s · local-first code review\n\n", art, Version)
}
