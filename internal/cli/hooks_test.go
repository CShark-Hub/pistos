package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"example.com/pistos/internal/snapshot"
	"example.com/pistos/internal/testutil/gitrepo"
)

const vetDefect = "\nfunc bad(n string) string { return fmt.Sprintf(\"%d\", n) }\n"

// runStdin is run with the given standard input (hook payloads).
func runStdin(t *testing.T, dir, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(context.Background(), append([]string{"-C", dir}, args...), strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// gitTry runs git like gitrepo.Repo.Git but returns failures instead of failing the test.
func gitTry(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Pistos Test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=Pistos Test", "GIT_COMMITTER_EMAIL=test@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readIndex(t *testing.T, r *gitrepo.Repo) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.Dir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStagedReviewsIndexOnly(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	clean := strings.Replace(r.Read("main.go"), "hello", "hi", 1)
	r.Write("main.go", clean)
	r.Git("add", "main.go")
	r.Write("main.go", clean+vetDefect)
	before := readIndex(t, r)

	code, env, errs := runReview(t, r.Dir, "--staged")
	if code != ExitOK || env.Canonical.Scope.Mode != "staged" || len(env.Canonical.Issues) != 0 {
		t.Fatalf("unstaged defect must be ignored: code=%d scope=%+v issues=%+v %s", code, env.Canonical.Scope, env.Canonical.Issues, errs)
	}
	if code, env, _ := runReview(t, r.Dir); code != ExitBlocking || len(env.Canonical.Issues) != 1 {
		t.Fatalf("the working snapshot still sees the defect: code=%d", code)
	}
	if !bytes.Equal(before, readIndex(t, r)) {
		t.Fatal("reviews must not modify the user's index")
	}

	r.Git("add", "main.go")
	r.Write("main.go", clean)
	code, env, _ = runReview(t, r.Dir, "--staged")
	if code != ExitBlocking || len(env.Canonical.Issues) != 1 || env.Canonical.Issues[0].Family != "go-vet/printf" {
		t.Fatalf("staged defect must block even when the working tree fixed it: code=%d %+v", code, env.Canonical.Issues)
	}
	code, out, _ := run(t, r.Dir, "review", "--staged", "--base", "main", "--format", "agent", "--no-html")
	if code != ExitBlocking || !strings.HasPrefix(out, "pistos: FAIL — 1 blocking, 0 advisory (staged changes vs merge-base(main))") {
		t.Fatalf("--staged --base: code=%d\n%s", code, out)
	}
}

func TestStagedRejectsHeadAndAll(t *testing.T) {
	r := gitrepo.New(t, "go-clean")
	r.Write(".pistos/config.toml", configFor(goProject))
	for _, args := range [][]string{{"--staged", "--all"}, {"--staged", "--base", "main", "--head", "HEAD"}} {
		code, _, errs := run(t, r.Dir, append([]string{"review"}, args...)...)
		if code != ExitError || !strings.Contains(errs, "--staged reviews the index") {
			t.Fatalf("%v: code=%d %s", args, code, errs)
		}
	}
}

func TestInheritedIndexFileDoesNotLeak(t *testing.T) {
	r := gitrepo.New(t, "go-clean")
	r.Write("main.go", r.Read("main.go")+"// edit\n")
	g := snapshot.Git{Repo: r.Dir}
	want, err := g.WorkingTree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	head, _ := g.Output(context.Background(), "rev-parse", "HEAD^{tree}")
	// A bogus inherited index must neither redirect the working snapshot nor the index snapshot.
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "missing-index"))
	if got, err := g.WorkingTree(context.Background()); err != nil || got != want {
		t.Fatalf("working tree with inherited GIT_INDEX_FILE: %s %v (want %s)", got, err, want)
	}
	if got, err := g.IndexTree(context.Background(), ""); err != nil || got != head {
		t.Fatalf("index tree must use the repository index: %s %v (want %s)", got, err, head)
	}
	if _, err := g.IndexTree(context.Background(), os.Getenv("GIT_INDEX_FILE")); err == nil {
		t.Fatal("an explicit missing index file must be an error, not an empty tree")
	}
}

func TestGitCommitHooksReviewWhatIsCommitted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook scripts")
	}
	r := setupGo(t, "go-clean", configFor(goProject+"\n[hooks]\npre_commit_profile = \"balanced\"\n"))
	bin := t.TempDir()
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "pistos"), "../../cmd/pistos").CombinedOutput(); err != nil {
		t.Fatalf("build pistos: %v\n%s", err, out)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if code, out, errs := run(t, r.Dir, "hooks", "install", "pre-commit"); code != 0 || !strings.Contains(out, "installed") {
		t.Fatalf("install: %d %s %s", code, out, errs)
	}

	r.Write("main.go", r.Read("main.go")+vetDefect)
	r.Write("other.go", "package main\n\nfunc other() int { return 1 }\n")
	r.Git("add", "other.go")
	if out, err := gitTry(r.Dir, "commit", "-q", "-m", "clean staged change"); err != nil {
		t.Fatalf("a commit of clean staged content must pass although the working tree has a defect: %v\n%s", err, out)
	}
	head := r.Git("rev-parse", "HEAD")
	before := readIndex(t, r)

	for _, args := range [][]string{{"commit", "-a", "-m", "all"}, {"commit", "-m", "only", "main.go"}} {
		out, err := gitTry(r.Dir, args...)
		if err == nil || !strings.Contains(out, "go-vet/printf") || !strings.Contains(out, "commit blocked") {
			t.Fatalf("%v must be blocked by the hook reviewing git's temporary index: %v\n%s", args, err, out)
		}
		if r.Git("rev-parse", "HEAD") != head {
			t.Fatalf("%v: blocked commit was created", args)
		}
		if !bytes.Equal(before, readIndex(t, r)) {
			t.Fatalf("%v: the user's index changed", args)
		}
	}

	if out, err := gitTry(r.Dir, "commit", "--no-verify", "-q", "-m", "bypass", "--allow-empty"); err != nil {
		t.Fatalf("--no-verify: %v %s", err, out)
	}
	r.Write("main.go", strings.Replace(r.Read("main.go"), vetDefect, "\n", 1))
	if out, err := gitTry(r.Dir, "commit", "-a", "-q", "-m", "fixed"); err != nil {
		t.Fatalf("clean commit -a must pass: %v\n%s", err, out)
	}

	// Without pistos on PATH the hook warns and lets the commit through.
	r.Write("other.go", "package main\n\nfunc other() int { return 2 }\n")
	t.Setenv("PATH", "/usr/bin:/bin")
	if _, err := exec.LookPath("pistos"); err == nil {
		t.Skip("a pistos binary is installed system-wide")
	}
	out, err := gitTry(r.Dir, "commit", "-a", "-q", "-m", "no pistos")
	if err != nil || !strings.Contains(out, "pistos: not found on PATH; skipping the pre-commit review") {
		t.Fatalf("missing binary must warn and pass: %v\n%s", err, out)
	}
}

func TestPrePushInputAndPlan(t *testing.T) {
	r := gitrepo.New(t, "go-clean")
	mainSHA := r.Git("rev-parse", "HEAD")
	r.Git("checkout", "-q", "-b", "feature")
	r.Write("x.go", "package main\n")
	feature := r.Commit("feature")
	zero := strings.Repeat("0", 40)
	missing := strings.Repeat("ab", 20)
	in := strings.Join([]string{
		"refs/heads/feature " + feature + " refs/heads/feature " + mainSHA,
		"(delete) " + zero + " refs/heads/old " + mainSHA,
		"refs/heads/feature " + feature + " refs/heads/new " + zero,
		"refs/heads/feature " + feature + " refs/heads/feature " + missing,
		"refs/heads/feature " + feature + " refs/heads/gone " + missing,
		"",
	}, "\n")
	refs, err := parsePushRefs(strings.NewReader(in))
	if err != nil || len(refs) != 5 {
		t.Fatalf("parse: %v %+v", err, refs)
	}
	if _, err := parsePushRefs(strings.NewReader("refs/heads/x abc\n")); err == nil {
		t.Fatal("malformed lines must be rejected")
	}

	g := snapshot.Git{Repo: r.Dir}
	plans := planPush(context.Background(), g, "origin", refs)
	if plans[0].Base != mainSHA || plans[0].Head != feature || plans[0].Skip != "" {
		t.Fatalf("existing branch: %+v", plans[0])
	}
	if !strings.Contains(plans[1].Skip, "deletes refs/heads/old") {
		t.Fatalf("deletion: %+v", plans[1])
	}
	if !strings.Contains(plans[2].Skip, "refs/remotes/origin/HEAD is unknown") {
		t.Fatalf("new branch without remote HEAD: %+v", plans[2])
	}
	if !strings.Contains(plans[3].Skip, "not present locally") || !strings.Contains(plans[4].Skip, "not present locally") {
		t.Fatalf("missing remote sha without tracking ref: %+v %+v", plans[3], plans[4])
	}

	r.Git("update-ref", "refs/remotes/origin/main", mainSHA)
	r.Git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	r.Git("update-ref", "refs/remotes/origin/feature", mainSHA)
	plans = planPush(context.Background(), g, "origin", refs)
	if plans[2].Base != "refs/remotes/origin/HEAD" || plans[2].Skip != "" {
		t.Fatalf("new branch compares against the remote's default branch: %+v", plans[2])
	}
	if plans[3].Base != "refs/remotes/origin/feature" || plans[3].Skip != "" {
		t.Fatalf("missing remote sha falls back to the tracking ref: %+v", plans[3])
	}
	if plans[4].Skip == "" {
		t.Fatalf("no tracking ref for refs/heads/gone: %+v", plans[4])
	}
}

func TestPrePushHookBlocksDefectivePush(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	mainSHA := r.Git("rev-parse", "HEAD")
	r.Git("checkout", "-q", "-b", "feature")
	r.Write("main.go", r.Read("main.go")+vetDefect)
	feature := r.Commit("defect")
	zero := strings.Repeat("0", 40)

	code, out, errs := runStdin(t, r.Dir, "refs/heads/feature "+feature+" refs/heads/feature "+mainSHA+"\n", "hooks", "run", "pre-push", "origin", "git@example.invalid:x.git")
	if code != ExitBlocking || !strings.Contains(out, "go-vet/printf") || !strings.Contains(errs, "push blocked") || !strings.Contains(errs, "report ") {
		t.Fatalf("defective push must be blocked: code=%d\n%s\n%s", code, out, errs)
	}
	code, _, errs = runStdin(t, r.Dir, "(delete) "+zero+" refs/heads/old "+mainSHA+"\n", "hooks", "run", "pre-push", "origin", "x")
	if code != ExitOK || !strings.Contains(errs, "nothing to review") {
		t.Fatalf("deletion: code=%d %s", code, errs)
	}
	t.Setenv("PISTOS_SKIP_HOOKS", "1")
	code, _, errs = runStdin(t, r.Dir, "refs/heads/feature "+feature+" refs/heads/feature "+mainSHA+"\n", "hooks", "run", "pre-push", "origin", "x")
	if code != ExitOK || !strings.Contains(errs, "PISTOS_SKIP_HOOKS=1") {
		t.Fatalf("skip: code=%d %s", code, errs)
	}
}

func TestHookIncompleteWarnsUnlessConfigured(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake tools")
	}
	t.Setenv("PISTOS_CACHE_DIR", t.TempDir())
	fake := t.TempDir()
	writeScript(t, fake, "go", "if [ \"$1\" = env ]; then echo go1.26.9; exit 0; fi\necho 'internal compiler crash' >&2\nexit 3\n")
	t.Setenv("PATH", fake+":/usr/bin:/bin")
	r := gitrepo.New(t, "go-clean")
	cfg := configFor(goProject + "\n[hooks]\npre_commit_profile = \"balanced\"\nclaude_profile = \"balanced\"\n")
	r.Write(".pistos/config.toml", cfg)
	if code, out, errs := run(t, r.Dir, "plugins", "install"); code != 0 {
		t.Fatalf("%s %s", out, errs)
	}
	r.Commit("configure")
	r.Write("main.go", r.Read("main.go")+"\n// edit\n")
	r.Git("add", "main.go")

	code, _, errs := run(t, r.Dir, "hooks", "run", "pre-commit")
	if code != ExitOK || !strings.Contains(errs, "review incomplete") || !strings.Contains(errs, "not blocking the commit") {
		t.Fatalf("incomplete must warn: code=%d %s", code, errs)
	}
	code, _, errs = runStdin(t, r.Dir, `{"session_id":"s","hook_event_name":"Stop","stop_hook_active":false}`, "hooks", "run", "claude-stop")
	if code != ExitOK || !strings.Contains(errs, "review incomplete (not blocking)") {
		t.Fatalf("claude-stop must not block on an incomplete review: code=%d %s", code, errs)
	}

	r.Write(".pistos/config.toml", strings.Replace(cfg, "[hooks]\n", "[hooks]\nblock_on_incomplete = true\n", 1))
	code, _, errs = run(t, r.Dir, "hooks", "run", "pre-commit")
	if code == ExitOK || !strings.Contains(errs, "block_on_incomplete = true") {
		t.Fatalf("block_on_incomplete must block: code=%d %s", code, errs)
	}
}

func TestHooksInstallUninstallStatus(t *testing.T) {
	r := gitrepo.New(t)
	hook := filepath.Join(r.Dir, ".git", "hooks", "pre-commit")

	code, out, _ := run(t, r.Dir, "hooks", "install")
	if code != 0 || strings.Count(out, "installed") != 2 {
		t.Fatalf("install: %d %s", code, out)
	}
	b, _ := os.ReadFile(hook)
	fi, _ := os.Stat(hook)
	if string(b) != hookScript("pre-commit") || fi.Mode()&0o111 == 0 {
		t.Fatalf("hook script: mode %v\n%s", fi.Mode(), b)
	}
	if code, out, _ = run(t, r.Dir, "hooks", "install"); code != 0 || strings.Count(out, "already installed") != 2 {
		t.Fatalf("install must be idempotent: %s", out)
	}
	_, out, _ = run(t, r.Dir, "hooks", "status")
	for _, want := range []string{"hooks dir  .git/hooks", "pre-commit installed", "pre-push   installed", "claude     not configured"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}
	if code, out, _ = run(t, r.Dir, "hooks", "uninstall"); code != 0 || strings.Count(out, "removed") != 2 {
		t.Fatalf("uninstall: %s", out)
	}
	if code, out, _ = run(t, r.Dir, "hooks", "uninstall"); code != 0 || strings.Count(out, "not installed") != 2 {
		t.Fatalf("uninstall must be idempotent: %s", out)
	}

	foreign := "#!/bin/sh\necho mine\n"
	os.WriteFile(hook, []byte(foreign), 0o755)
	code, out, errs := run(t, r.Dir, "hooks", "install", "pre-commit")
	if b, _ := os.ReadFile(hook); code != ExitError || string(b) != foreign || !strings.Contains(errs, "not written by pistos") {
		t.Fatalf("foreign hook must be refused and kept: code=%d %s %s", code, out, errs)
	}
	if _, out, _ = run(t, r.Dir, "hooks", "uninstall", "pre-commit"); !strings.Contains(out, "not written by pistos") {
		t.Fatalf("uninstall must keep foreign hooks: %s", out)
	}
	if b, _ := os.ReadFile(hook); string(b) != foreign {
		t.Fatal("foreign hook removed")
	}
	if _, out, _ = run(t, r.Dir, "hooks", "status"); !strings.Contains(out, "foreign hook (not managed by pistos)") {
		t.Fatalf("status: %s", out)
	}

	r.Git("config", "core.hooksPath", ".githooks")
	if code, out, _ = run(t, r.Dir, "hooks", "install", "pre-push"); code != 0 || !strings.Contains(out, "installed .githooks/pre-push") {
		t.Fatalf("core.hooksPath must be respected: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, ".githooks", "pre-push")); err != nil {
		t.Fatal(err)
	}
	if code, _, _ = run(t, r.Dir, "hooks", "install", "pre-commit", "bogus"); code != ExitError {
		t.Fatal("unknown hook kinds must be rejected")
	}
}

func TestHookManagersGetSnippets(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		setup      func(r *gitrepo.Repo)
	}{
		{"husky", "# .husky/pre-push\npistos hooks run pre-push \"$@\"", func(r *gitrepo.Repo) { r.Git("config", "core.hooksPath", ".husky/_") }},
		{"lefthook", "run: pistos hooks run pre-commit", func(r *gitrepo.Repo) { r.Write("lefthook.yml", "pre-commit:\n") }},
		{"pre-commit", "entry: pistos hooks run pre-commit", func(r *gitrepo.Repo) { r.Write(".pre-commit-config.yaml", "repos: []\n") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gitrepo.New(t)
			tc.setup(r)
			code, out, _ := run(t, r.Dir, "hooks", "install")
			if code != 0 || !strings.Contains(out, "managed by "+tc.name) || !strings.Contains(out, tc.want) {
				t.Fatalf("code=%d\n%s", code, out)
			}
			for _, dir := range []string{".git/hooks", ".husky/_"} {
				for _, k := range gitHookKinds {
					if _, err := os.Stat(filepath.Join(r.Dir, dir, k)); err == nil {
						t.Fatalf("nothing may be installed when %s manages hooks (%s/%s)", tc.name, dir, k)
					}
				}
			}
		})
	}
}

func TestClaudeStopHook(t *testing.T) {
	// The loop guard answers before anything else, even outside a repository.
	code, out, errs := runStdin(t, t.TempDir(), `{"hook_event_name":"Stop","stop_hook_active":true}`, "hooks", "run", "claude-stop")
	if code != ExitOK || out != "" || errs != "" {
		t.Fatalf("stop_hook_active must let Claude stop silently: %d %q %q", code, out, errs)
	}
	if code, _, _ := runStdin(t, t.TempDir(), "not json", "hooks", "run", "claude-stop"); code != ExitOK {
		t.Fatalf("unreadable input must not block: %d", code)
	}

	r := setupGo(t, "go-clean", configFor(goProject+"\n[hooks]\nclaude_profile = \"balanced\"\n"))
	input := `{"session_id":"abc","transcript_path":"/tmp/t.jsonl","cwd":"` + r.Dir + `","hook_event_name":"Stop","stop_hook_active":false}`
	if code, out, errs := runStdin(t, r.Dir, input, "hooks", "run", "claude-stop"); code != ExitOK || out != "" || errs != "" {
		t.Fatalf("clean tree: %d %q %q", code, out, errs)
	}

	r.Write("main.go", r.Read("main.go")+vetDefect)
	code, out, errs = runStdin(t, r.Dir, input, "hooks", "run", "claude-stop")
	if code != 2 || out != "" || !strings.HasPrefix(errs, "pistos: FAIL — 1 blocking") || !strings.Contains(errs, "BLOCKING") || strings.Contains(errs, "report.html") {
		t.Fatalf("blocking issues must exit 2 with the agent report on stderr: %d %q\n%s", code, out, errs)
	}
	// An agent recording a decision in the working copy cannot silence the issue.
	_, env, _ := runReview(t, r.Dir)
	n := env.Numbers[env.Canonical.Issues[0].Key]
	if !strings.Contains(errs, "#"+strconv.Itoa(n)+" warning go-vet/printf") {
		t.Fatalf("agent output must use ledger number %d:\n%s", n, errs)
	}
	if code, out, errs := run(t, r.Dir, "tag", strconv.Itoa(n), "irrelevant"); code != 0 {
		t.Fatalf("tag: %s %s", out, errs)
	}
	if code, _, errs = runStdin(t, r.Dir, input, "hooks", "run", "claude-stop"); code != 2 {
		t.Fatalf("working-copy feedback must not apply to the Stop hook: %d %s", code, errs)
	}
	// The hook input's cwd locates the repository when pistos runs elsewhere.
	var o, e bytes.Buffer
	if code := Run(context.Background(), []string{"hooks", "run", "claude-stop"}, strings.NewReader(input), &o, &e); code != 2 {
		t.Fatalf("cwd from hook input: %d %s", code, e.String())
	}
}

func TestClaudeSettingsWriteMerges(t *testing.T) {
	r := gitrepo.New(t)
	code, out, _ := run(t, r.Dir, "hooks", "install", "claude")
	if code != 0 || !strings.Contains(out, `"command": "pistos hooks run claude-stop"`) {
		t.Fatalf("print: %s", out)
	}
	var snippet map[string]any
	if err := json.Unmarshal([]byte(out), &snippet); err != nil || !hasClaudeStop(snippet) {
		t.Fatalf("printed snippet must be valid settings JSON: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, ".claude")); err == nil {
		t.Fatal("printing must not write files")
	}

	existing := `{"permissions": {"allow": ["Bash(ls)"]}, "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "echo done"}]}], "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "true"}]}]}}`
	r.Write(claudeSettings, existing)
	if code, out, _ = run(t, r.Dir, "hooks", "install", "claude", "--write"); code != 0 || !strings.Contains(out, "updated") {
		t.Fatalf("write: %d %s", code, out)
	}
	first := r.Read(claudeSettings)
	if code, out, _ = run(t, r.Dir, "hooks", "install", "claude", "--write"); code != 0 || !strings.Contains(out, "already configured") || r.Read(claudeSettings) != first {
		t.Fatalf("write must be idempotent: %s", out)
	}
	var s struct {
		Permissions map[string][]string `json:"permissions"`
		Hooks       map[string][]struct {
			Matcher string           `json:"matcher"`
			Hooks   []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(first), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Permissions["allow"]) != 1 || len(s.Hooks["PreToolUse"]) != 1 || len(s.Hooks["Stop"]) != 2 ||
		s.Hooks["Stop"][0].Hooks[0]["command"] != "echo done" || s.Hooks["Stop"][1].Hooks[0]["command"] != claudeStopCommand {
		t.Fatalf("existing settings must be preserved and the hook appended once:\n%s", first)
	}
	if _, out, _ = run(t, r.Dir, "hooks", "status"); !strings.Contains(out, "claude     Stop hook in "+claudeSettings) {
		t.Fatalf("status: %s", out)
	}

	if code, out, _ = run(t, r.Dir, "hooks", "uninstall", "claude"); code != 0 || !strings.Contains(out, "removed") {
		t.Fatalf("uninstall: %s", out)
	}
	after := r.Read(claudeSettings)
	if strings.Contains(after, claudeStopCommand) || !strings.Contains(after, "echo done") || !strings.Contains(after, "Bash(ls)") {
		t.Fatalf("uninstall must remove only the pistos hook:\n%s", after)
	}

	r.Write(claudeSettings, `{"hooks": []}`)
	if code, _, errs := run(t, r.Dir, "hooks", "install", "claude", "--write"); code != ExitError || !strings.Contains(errs, "left untouched") || r.Read(claudeSettings) != `{"hooks": []}` {
		t.Fatalf("unexpected shapes must not be rewritten: %d %s", code, errs)
	}
}
