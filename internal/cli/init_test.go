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
	for _, want := range []string{`languages = ["go", "python", "secrets"]`, `id = "go-svc"`, `id = "python-tools"`, `checks = ["ruff", "pyright", "metrics"]`,
		"id = \"secrets\"\nroot = \".\"\necosystem = \"secrets\"\nchecks = [\"" + "git" + "leaks\"]"} {
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
	if !strings.Contains(cfg, `languages = ["csharp", "go", "secrets"]`) || strings.Contains(cfg, "python") {
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

func TestInitSecretsPackSelection(t *testing.T) {
	r := gitrepo.New(t, "mixed")
	if code, out, errs := run(t, r.Dir, "init", "--lang", "go", "--no-secrets"); code != 0 {
		t.Fatalf("%s %s", out, errs)
	}
	if cfg := r.Read(".pistos/config.toml"); !strings.Contains(cfg, `languages = ["go"]`) || strings.Contains(cfg, "secrets") {
		t.Fatalf("--no-secrets must leave the secret scan out:\n%s", cfg)
	}
	// Re-running init on an existing configuration only suggests the secret scan.
	if code, out, _ := run(t, r.Dir, "init"); code != 0 || !strings.Contains(out, "secret scanning") || strings.Contains(out, "created") {
		t.Fatalf("code=%d %s", code, out)
	}

	r = gitrepo.New(t, "mixed")
	if code, out, errs := run(t, r.Dir, "init", "--lang", "secrets"); code != 0 {
		t.Fatalf("%s %s", out, errs)
	}
	cfg := r.Read(".pistos/config.toml")
	if !strings.Contains(cfg, `languages = ["secrets"]`) || strings.Count(cfg, "[[projects]]") != 1 || !strings.Contains(cfg, `id = "secrets"`) {
		t.Fatalf("--lang secrets alone configures only the secret scan:\n%s", cfg)
	}
	// The configuration is valid; doctor lists the pack and checks its tool.
	if _, out, _ := run(t, r.Dir, "doctor"); !strings.Contains(out, "packs    secrets\n") || !strings.Contains(out, "project  secrets") {
		t.Fatalf("doctor: %s", out)
	}

	if code, _, errs := run(t, t.TempDir(), "init", "--lang", "secrets", "--no-secrets"); code != ExitError || !strings.Contains(errs, "contradicts") {
		t.Fatalf("code=%d %s", code, errs)
	}
}
