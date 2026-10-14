package cli

import (
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// viewer knows how to show a file or URL on this machine and how to tell a person to do it.
type viewer struct {
	// argv opens the target; nil when nothing on this machine can open a browser.
	argv func(target string) []string
	// hint is a command the person can paste into their shell, or "" when there is none.
	hint func(target string) string
	// remote is set over SSH without a display: opening would happen on the wrong machine.
	remote bool
	wsl    string
}

func detectViewer() viewer {
	switch runtime.GOOS {
	case "darwin":
		return viewer{
			argv: func(t string) []string { return []string{"open", t} },
			hint: func(t string) string { return "open " + shQuote(t) },
		}
	case "windows":
		return viewer{
			argv: func(t string) []string { return []string{"rundll32", "url.dll,FileProtocolHandler", t} },
			// explorer.exe works the same in cmd, PowerShell and Git Bash, unlike `start`.
			hint: func(t string) string { return `explorer.exe "` + t + `"` },
		}
	}
	if distro := wslDistro(); distro != "" {
		v := viewer{wsl: distro}
		if _, err := exec.LookPath("wslview"); err == nil {
			v.argv = func(t string) []string { return []string{"wslview", t} }
			v.hint = func(t string) string { return "wslview " + shQuote(t) }
			return v
		}
		v.argv = func(t string) []string {
			if !isURL(t) {
				t = wslWindowsPath(distro, t)
			}
			return []string{"explorer.exe", t}
		}
		v.hint = func(t string) string {
			if isURL(t) {
				return "explorer.exe " + shQuote(t)
			}
			return `explorer.exe "$(wslpath -w ` + shQuote(t) + `)"`
		}
		return v
	}
	v := viewer{remote: (os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "") && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""}
	for _, cand := range [][]string{{"xdg-open"}, {"gio", "open"}} {
		if _, err := exec.LookPath(cand[0]); err == nil {
			prefix := strings.Join(cand, " ")
			v.argv = func(t string) []string { return append(append([]string(nil), cand...), t) }
			v.hint = func(t string) string { return prefix + " " + shQuote(t) }
			break
		}
	}
	if v.remote {
		v.argv = nil
	}
	return v
}

func (v viewer) open(target string) error {
	if v.argv == nil {
		return exec.ErrNotFound
	}
	argv := v.argv(target)
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Openers hand off to the browser and exit; reap them without blocking the caller.
	go cmd.Wait()
	return nil
}

func (v viewer) hintFor(target string) string {
	if v.hint == nil || v.remote {
		return ""
	}
	return v.hint(target)
}

// fileURL is the link a terminal opens for path; under WSL it points Windows at the Linux file.
func (v viewer) fileURL(path string) string {
	if v.wsl != "" {
		return (&url.URL{Scheme: "file", Host: "wsl.localhost", Path: "/" + v.wsl + path}).String()
	}
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func wslDistro() string {
	if d := os.Getenv("WSL_DISTRO_NAME"); d != "" {
		return d
	}
	return ""
}

func wslWindowsPath(distro, p string) string {
	if out, err := exec.Command("wslpath", "-w", p).Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return `\\wsl.localhost\` + distro + strings.ReplaceAll(p, "/", `\`)
}

func isURL(s string) bool { return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") }

func shQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:=@,+%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hyperlink wraps text in an OSC 8 link when w is a terminal that is likely to render it. The
// visible text is the URL itself, so terminals without OSC 8 still auto-link or show it plainly.
func hyperlink(w io.Writer, target, text string) string {
	switch os.Getenv("PISTOS_HYPERLINKS") {
	case "0":
		return text
	case "1":
		return "\x1b]8;;" + target + "\x1b\\" + text + "\x1b]8;;\x1b\\"
	}
	if !isTerminal(w) || os.Getenv("TERM") == "dumb" || os.Getenv("INSIDE_EMACS") != "" {
		return text
	}
	return "\x1b]8;;" + target + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
