package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/testutil/gitrepo"
)

// useGoToolchain puts a real Go toolchain on PATH and isolates the machine-wide pistos cache.
func useGoToolchain(t *testing.T) {
	t.Helper()
	t.Setenv("PISTOS_CACHE_DIR", t.TempDir())
	if _, err := exec.LookPath("go"); err == nil {
		return
	}
	bin := filepath.Join(runtime.GOROOT(), "bin")
	if _, err := os.Stat(filepath.Join(bin, "go")); err != nil {
		t.Skip("no Go toolchain available for live Go pack tests")
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func configFor(projects string) string {
	return "schema = 1\nlanguages = [\"go\"]\n\n[execution]\njobs = 4\n" + projects
}

const goProject = "\n[[projects]]\nid = \"app\"\nroot = \".\"\necosystem = \"go\"\nchecks = [\"go-vet\"]\n"

// setupGo creates a committed repository with pistos configured and the Go toolchain locked.
func setupGo(t *testing.T, fixture, cfg string) *gitrepo.Repo {
	t.Helper()
	useGoToolchain(t)
	r := gitrepo.New(t, fixture)
	r.Write(".pistos/config.toml", cfg)
	r.Write(".pistos/.gitignore", "cache/\nstate/\n")
	if code, out, errs := run(t, r.Dir, "plugins", "install"); code != 0 {
		t.Fatalf("plugins install: %s %s", out, errs)
	}
	r.Commit("configure pistos")
	return r
}

func runReview(t *testing.T, dir string, args ...string) (int, *finding.Envelope, string) {
	t.Helper()
	code, out, errs := run(t, dir, append([]string{"review", "--format", "json"}, args...)...)
	var env finding.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("review output not JSON (code %d): %v\n%s\n%s", code, err, out, errs)
	}
	return code, &env, errs
}

func capability(env *finding.Envelope, project, capName, side string) finding.CapabilityResult {
	for _, c := range env.Canonical.Coverage.Capabilities {
		if c.Project == project && c.Capability == capName && c.Side == side {
			return c
		}
	}
	return finding.CapabilityResult{}
}

func TestGoReviewReportsKnownDiagnostic(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Write("main.go", r.Read("main.go")+"\nfunc bad(n string) string { return fmt.Sprintf(\"%d\", n) }\n")

	code, env, errs := runReview(t, r.Dir)
	if code != ExitBlocking {
		t.Fatalf("want exit 1, got %d: %s", code, errs)
	}
	is := env.Canonical.Issues
	if len(is) != 1 || is[0].Family != "go-vet/printf" || is[0].Change != "new" || !is[0].Blocking || is[0].Location.Path != "main.go" {
		t.Fatalf("unexpected issues: %+v", is)
	}
	if is[0].Anchor.Symbol != "bad" {
		t.Fatalf("symbol = %q", is[0].Anchor.Symbol)
	}
	if c := capability(env, "app", "go-vet", "head"); c.Status != finding.StatusComplete || !c.Required {
		t.Fatalf("head capability: %+v", c)
	}
}

func TestGoReviewCleanChangePasses(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Write("main.go", strings.Replace(r.Read("main.go"), "hello", "hi", 1))
	code, env, errs := runReview(t, r.Dir)
	if code != ExitOK || env.Canonical.Verdict.Outcome != "pass" || len(env.Canonical.Issues) != 0 {
		t.Fatalf("code=%d verdict=%+v %s", code, env.Canonical.Verdict, errs)
	}
}

func TestPreexistingIssueIsNotNew(t *testing.T) {
	r := setupGo(t, "go-defect", configFor(goProject))
	r.Write("extra.go", "package main\n\nfunc extra() int { return 1 }\n")
	code, env, _ := runReview(t, r.Dir)
	if code != ExitOK || len(env.Canonical.Issues) != 1 || env.Canonical.Issues[0].Change != "existing" || env.Canonical.Issues[0].Blocking {
		t.Fatalf("code=%d issues=%+v", code, env.Canonical.Issues)
	}
	code, env, _ = runReview(t, r.Dir, "--all")
	if code != ExitBlocking || env.Canonical.Issues[0].Change != "in-scope" {
		t.Fatalf("--all must gate on everything: code=%d %+v", code, env.Canonical.Issues)
	}
}

func TestNoChangeReviewStartsNothing(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	code, env, _ := runReview(t, r.Dir)
	c := env.Canonical
	if code != ExitOK || !c.Scope.Empty || len(c.Coverage.Capabilities) != 0 || len(env.Telemetry.Tasks) != 0 {
		t.Fatalf("code=%d scope=%+v caps=%d tasks=%d", code, c.Scope, len(c.Coverage.Capabilities), len(env.Telemetry.Tasks))
	}
	_, out, _ := run(t, r.Dir, "review")
	if !strings.Contains(out, "nothing was analyzed") {
		t.Fatalf("text output must state the empty scope:\n%s", out)
	}
}

func TestCommitHeadIgnoresDirtyWorkingTree(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Git("checkout", "-q", "-b", "feature")
	r.Write("main.go", r.Read("main.go")+"\nfunc bad(n string) string { return fmt.Sprintf(\"%d\", n) }\n")
	r.Commit("introduce defect")
	r.Write("main.go", r.Read("main.go")+"\nfunc worse(n int) string { return fmt.Sprintf(\"%s\", n) }\n")

	code, env, _ := runReview(t, r.Dir, "--base", "main", "--head", "feature")
	if code != ExitBlocking || len(env.Canonical.Issues) != 1 || env.Canonical.Scope.Mode != "commit" {
		t.Fatalf("commit review must see only the committed defect: code=%d %+v", code, env.Canonical.Issues)
	}
	if env.Canonical.Scope.BaseCommit == "" || env.Canonical.Scope.BaseTree == "" || env.Canonical.Scope.HeadTree == "" {
		t.Fatalf("resolved refs/trees must be recorded: %+v", env.Canonical.Scope)
	}
	code, env, _ = runReview(t, r.Dir, "--base", "main")
	if code != ExitBlocking || len(env.Canonical.Issues) != 2 {
		t.Fatalf("working review must include the dirty defect: %+v", env.Canonical.Issues)
	}
	if code, _, errs := run(t, r.Dir, "review", "--head", "feature"); code != ExitError || !strings.Contains(errs, "--head requires --base") {
		t.Fatalf("code=%d %s", code, errs)
	}
}

func TestReviewIsDeterministicAcrossRunsAndJobs(t *testing.T) {
	r := setupGo(t, "go-defect", configFor(goProject))
	r.Write("more.go", "package main\n\nimport \"fmt\"\n\nfunc a(x int) string { return fmt.Sprintf(\"%s %s\", x, x) }\n\nfunc b(y string) { fmt.Printf(\"%d\\n\", y) }\n")
	var first []byte
	for i, jobs := range []string{"1", "2", "4", "4", "4"} {
		os.RemoveAll(filepath.Join(r.Dir, ".pistos", "cache"))
		code, out, errs := run(t, r.Dir, "review", "--all", "--format", "canonical-json", "-j", jobs)
		if code != ExitBlocking {
			t.Fatalf("run %d: code %d %s", i, code, errs)
		}
		if first == nil {
			first = []byte(out)
			continue
		}
		if !bytes.Equal(first, []byte(out)) {
			t.Fatalf("canonical output differs on run %d (jobs %s):\n%s\n---\n%s", i, jobs, first, out)
		}
	}
}

func TestCacheReuseAndInvalidation(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Write("main.go", strings.Replace(r.Read("main.go"), "hello", "hi", 1))
	_, env, _ := runReview(t, r.Dir)
	if s := capability(env, "app", "go-vet", "head").Status; s != finding.StatusComplete {
		t.Fatalf("first run: %s", s)
	}
	_, env, _ = runReview(t, r.Dir)
	if s := capability(env, "app", "go-vet", "head").Status; s != finding.StatusCachedComplete {
		t.Fatalf("unchanged inputs must hit the cache: %s", s)
	}
	for _, step := range []struct {
		name string
		edit func()
	}{
		{"source", func() { r.Write("util.go", "package main\n\nfunc util() {}\n") }},
		{"go.mod", func() { r.Write("go.mod", strings.Replace(r.Read("go.mod"), "go 1.24", "go 1.25", 1)) }},
		{"generated", func() { r.Write("zz_generated.go", "// Code generated by hand. DO NOT EDIT.\npackage main\n") }},
		{"deletion", func() { os.Remove(filepath.Join(r.Dir, "util.go")) }},
		{"rename", func() { os.Rename(filepath.Join(r.Dir, "zz_generated.go"), filepath.Join(r.Dir, "gen.go")) }},
		{"build tags", func() {
			r.Write(".pistos/config.toml", strings.Replace(configFor(goProject), "checks", "options = { tags = \"integration\" }\nchecks", 1))
		}},
	} {
		name := step.name
		step.edit()
		_, env, errs := runReview(t, r.Dir)
		if s := capability(env, "app", "go-vet", "head").Status; s != finding.StatusComplete {
			t.Fatalf("%s change must invalidate the cache, got %s %s", name, s, errs)
		}
	}
}

func TestUnrelatedFileChangeSkipsProject(t *testing.T) {
	cfg := configFor("\n[[projects]]\nid = \"svc\"\nroot = \"svc\"\necosystem = \"go\"\nchecks = [\"go-vet\"]\n")
	r := setupGo(t, "mixed", cfg)
	r.Write("tools/report.py", "def total(v):\n    return 0\n")
	code, env, _ := runReview(t, r.Dir)
	if code != ExitOK || len(env.Canonical.Scope.Projects) != 0 || len(env.Telemetry.Tasks) != 0 {
		t.Fatalf("changes outside the Go project must not start Go tools: %+v %+v", env.Canonical.Scope, env.Telemetry.Tasks)
	}
}

func writeScript(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestBrokenAndMissingToolsAreIncomplete(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake tools")
	}
	t.Setenv("PISTOS_CACHE_DIR", t.TempDir())
	fake := t.TempDir()
	writeScript(t, fake, "go", "if [ \"$1\" = env ]; then echo go1.26.9; exit 0; fi\necho 'internal compiler crash' >&2\nexit 3\n")
	t.Setenv("PATH", fake+":/usr/bin:/bin")
	r := gitrepo.New(t, "go-clean")
	r.Write(".pistos/config.toml", configFor(goProject))
	if code, out, errs := run(t, r.Dir, "plugins", "install"); code != 0 {
		t.Fatalf("%s %s", out, errs)
	}
	r.Commit("configure")
	r.Write("main.go", r.Read("main.go")+"\n// edit\n")

	code, env, _ := runReview(t, r.Dir)
	c := capability(env, "app", "go-vet", "head")
	if code != ExitError || c.Status != finding.StatusFailed || !strings.Contains(c.Error, "unexpected code 3") {
		t.Fatalf("crashing tool must fail the review: code=%d %+v", code, c)
	}

	writeScript(t, fake, "go", "if [ \"$1\" = env ]; then echo go1.27.0; exit 0; fi\nexit 0\n")
	code, env, _ = runReview(t, r.Dir)
	c = capability(env, "app", "go-vet", "head")
	if code != ExitError || c.Status != finding.StatusIncomplete || !strings.Contains(c.Error, "changed since it was locked") {
		t.Fatalf("silently upgraded tool must be rejected: code=%d %+v", code, c)
	}

	os.Remove(filepath.Join(fake, "go"))
	code, env, _ = runReview(t, r.Dir)
	c = capability(env, "app", "go-vet", "head")
	if code != ExitError || c.Status != finding.StatusIncomplete || !strings.Contains(c.Error, "not found") {
		t.Fatalf("missing tool must be incomplete: code=%d %+v", code, c)
	}
}

func TestCancellationLeavesNoChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake tools")
	}
	t.Setenv("PISTOS_CACHE_DIR", t.TempDir())
	fake := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	writeScript(t, fake, "go", "if [ \"$1\" = env ]; then echo go1.26.9; exit 0; fi\nsleep 3600 &\necho $! > "+pidFile+"\nwait\n")
	t.Setenv("PATH", fake+":/usr/bin:/bin")
	r := gitrepo.New(t, "go-clean")
	r.Write(".pistos/config.toml", configFor(goProject))
	if code, _, _ := run(t, r.Dir, "plugins", "install"); code != 0 {
		t.Fatal("install failed")
	}
	r.Commit("configure")
	r.Write("main.go", r.Read("main.go")+"\n// edit\n")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int)
	go func() {
		var out, errb bytes.Buffer
		done <- Run(ctx, []string{"-C", r.Dir, "review"}, strings.NewReader(""), &out, &errb)
	}()
	waitForFile(t, pidFile)
	cancel()
	if code := <-done; code != ExitCancelled {
		t.Fatalf("want exit 130, got %d", code)
	}
	assertPidGone(t, pidFile)
}

// TestStaticcheckLive needs staticcheck on PATH built for a compatible Go toolchain.
func TestStaticcheckLive(t *testing.T) {
	if _, err := exec.LookPath("staticcheck"); err != nil {
		t.Skip("staticcheck not on PATH; live staticcheck coverage skipped")
	}
	cfg := strings.Replace(configFor(goProject), `checks = ["go-vet"]`, `checks = ["go-vet", "staticcheck"]`, 1)
	r := setupGo(t, "go-clean", cfg)
	r.Write("main.go", strings.Replace(r.Read("main.go"), "hello, %s", "hello, %d", 1))
	code, env, _ := runReview(t, r.Dir)
	sc := capability(env, "app", "staticcheck", "head")
	switch sc.Status {
	case finding.StatusComplete:
		if code != ExitBlocking || len(env.Canonical.Issues) != 1 || len(env.Canonical.Issues[0].Sources) != 2 {
			t.Fatalf("vet and staticcheck must merge into one issue: %+v", env.Canonical.Issues)
		}
	case finding.StatusIncomplete:
		if code != ExitError || !strings.Contains(sc.Error, "incompatible") {
			t.Fatalf("incompatible staticcheck must be reported explicitly: %+v", sc)
		}
		t.Log("staticcheck is incompatible with this Go toolchain; verified explicit incomplete outcome")
	default:
		t.Fatalf("unexpected staticcheck status %+v", sc)
	}
}

func TestDoctorReportsLockedTools(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	code, out, errs := run(t, r.Dir, "doctor")
	if code != 0 || !strings.Contains(out, "✓ go       go") {
		t.Fatalf("code=%d %s %s", code, out, errs)
	}
}
