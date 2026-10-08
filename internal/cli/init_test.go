package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/pistos/internal/testutil/gitrepo"
)

func run(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(context.Background(), append([]string{"-C", dir}, args...), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestHelpWorksWithoutRepo(t *testing.T) {
	code, out, _ := run(t, t.TempDir(), "--help")
	if code != 0 || !strings.Contains(out, "init") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestInitDetectsAndIsIdempotent(t *testing.T) {
	r := gitrepo.New(t, "mixed")
	code, out, errs := run(t, r.Dir, "init")
	if code != 0 {
		t.Fatalf("init failed: %s %s", out, errs)
	}
	cfg := r.Read(".pistos/config.toml")
	for _, want := range []string{`languages = ["go", "python"]`, `id = "go-svc"`, `id = "python-tools"`, `checks = ["ruff", "pyright", "metrics"]`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %q:\n%s", want, cfg)
		}
	}
	r.Write(".pistos/feedback.yaml", "# my notes\nschema: 1\ndecisions: []\n")
	r.Write(".pistos/config.toml", cfg+"\n# hand edit\n")

	code, out, _ = run(t, r.Dir, "init", "--lang", "go")
	if code != 0 || strings.Contains(out, "created") {
		t.Fatalf("second init must not create files: %s", out)
	}
	if got := r.Read(".pistos/config.toml"); got != cfg+"\n# hand edit\n" {
		t.Fatal("init overwrote config")
	}
	if !strings.HasPrefix(r.Read(".pistos/feedback.yaml"), "# my notes") {
		t.Fatal("init overwrote feedback")
	}
}

func TestInitLangSelection(t *testing.T) {
	r := gitrepo.New(t, "mixed")
	if code, out, errs := run(t, r.Dir, "init", "--lang", "c#", "--lang", "golang"); code != 0 {
		t.Fatalf("%s %s", out, errs)
	}
	cfg := r.Read(".pistos/config.toml")
	if !strings.Contains(cfg, `languages = ["csharp", "go"]`) || strings.Contains(cfg, "python") {
		t.Fatalf("unexpected config:\n%s", cfg)
	}
}

func TestInitRejectsUnknownLanguageAndInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	if code, _, errs := run(t, dir, "init", "--lang", "cobol"); code != ExitError || !strings.Contains(errs, "unknown language") {
		t.Fatalf("code=%d %s", code, errs)
	}
	os.MkdirAll(filepath.Join(dir, ".pistos"), 0o755)
	os.WriteFile(filepath.Join(dir, ".pistos", "config.toml"), []byte("schema = 9\n"), 0o644)
	if code, _, errs := run(t, dir, "init"); code != ExitError || !strings.Contains(errs, "left untouched") {
		t.Fatalf("code=%d %s", code, errs)
	}
	if code, _, errs := run(t, dir, "doctor"); code != ExitError || !strings.Contains(errs, "schema = 9") {
		t.Fatalf("doctor must reject invalid config: code=%d %s", code, errs)
	}
}
