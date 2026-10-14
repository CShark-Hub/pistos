package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/fsutil"
	"example.com/pistos/internal/report"
	"example.com/pistos/internal/review"
	"example.com/pistos/internal/snapshot"
)

const (
	hookPreCommit = "pre-commit"
	hookPrePush   = "pre-push"
	hookClaude    = "claude"
	// hookMarker identifies scripts pistos wrote; anything without it is never replaced or removed.
	hookMarker = "# pistos-hook: managed by `pistos hooks install`"
	// claudeStopCommand is the command registered as a Claude Code Stop hook.
	claudeStopCommand = "pistos hooks run claude-stop"
	claudeSettings    = ".claude/settings.local.json"
)

var gitHookKinds = []string{hookPreCommit, hookPrePush}

// hookScript is deliberately tiny: it calls whichever pistos is on PATH, so upgrading pistos never
// needs a reinstall, and a machine without pistos can still commit.
func hookScript(kind string) string {
	return "#!/bin/sh\n" +
		hookMarker + "; remove with `pistos hooks uninstall`.\n" +
		"if ! command -v pistos >/dev/null 2>&1; then\n" +
		"\techo \"pistos: not found on PATH; skipping the " + kind + " review\" >&2\n" +
		"\texit 0\n" +
		"fi\n" +
		"exec pistos hooks run " + kind + " \"$@\"\n"
}

func (a *app) hooksCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install git pre-commit/pre-push hooks and the Claude Code Stop hook",
		Long: `Git hooks review what is being committed (pre-commit: the index, profile hooks.pre_commit_profile)
or pushed (pre-push: each pushed ref against the remote, profile hooks.pre_push_profile). Blocking
issues stop the commit or push; incomplete reviews only warn unless hooks.block_on_incomplete is set.
Bypass once with git's --no-verify or PISTOS_SKIP_HOOKS=1. See docs/hooks.md.`,
	}
	cmd.AddCommand(a.hooksInstallCommand(), a.hooksUninstallCommand(), a.hooksStatusCommand(), a.hooksRunCommand())
	return cmd
}

func parseHookKinds(args []string, allowClaude bool) ([]string, error) {
	if len(args) == 0 {
		return gitHookKinds, nil
	}
	var kinds []string
	for _, k := range args {
		if !contains(gitHookKinds, k) && !(allowClaude && k == hookClaude) {
			return nil, withCode(ExitError, fmt.Errorf("unknown hook %q (pre-commit, pre-push, claude)", k))
		}
		if !contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	return kinds, nil
}

// hookEnv describes where git looks for hooks and whether another hook manager owns them.
type hookEnv struct {
	repo    string
	dir     string
	manager string
}

func (a *app) hookEnvironment(ctx context.Context) (*hookEnv, error) {
	repo, err := a.repoRoot()
	if err != nil {
		return nil, err
	}
	g := snapshot.Git{Repo: repo}
	if !g.IsRepo(ctx) {
		return nil, withCode(ExitError, errors.New("pistos hooks requires a git repository"))
	}
	// --git-path honors core.hooksPath and linked worktrees.
	dir, err := g.Output(ctx, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return nil, withCode(ExitError, err)
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repo, dir)
	}
	hooksPath, _ := g.Output(ctx, "config", "--get", "core.hooksPath")
	return &hookEnv{repo: repo, dir: dir, manager: detectHookManager(repo, hooksPath)}, nil
}

// detectHookManager recognizes hook managers whose configuration should carry the pistos call
// instead of a script pistos writes into their hooks directory.
func detectHookManager(repo, hooksPath string) string {
	if strings.Contains(filepath.ToSlash(hooksPath), ".husky") {
		return "husky"
	}
	for _, f := range []string{"lefthook.yml", ".lefthook.yml", "lefthook.yaml", ".lefthook.yaml"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err == nil {
			return "lefthook"
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".pre-commit-config.yaml")); err == nil {
		return "pre-commit"
	}
	return ""
}

func managerSnippet(manager string, kinds []string) string {
	var b strings.Builder
	switch manager {
	case "husky":
		for _, k := range kinds {
			args := ""
			if k == hookPrePush {
				args = ` "$@"`
			}
			fmt.Fprintf(&b, "# .husky/%s\npistos hooks run %s%s\n", k, k, args)
		}
	case "lefthook":
		b.WriteString("# lefthook.yml\n")
		for _, k := range kinds {
			if k == hookPrePush {
				b.WriteString("pre-push:\n  commands:\n    pistos:\n      run: pistos hooks run pre-push {1} {2}\n      use_stdin: true\n")
			} else {
				b.WriteString("pre-commit:\n  commands:\n    pistos:\n      run: pistos hooks run pre-commit\n")
			}
		}
	case "pre-commit":
		b.WriteString("# .pre-commit-config.yaml\nrepos:\n  - repo: local\n    hooks:\n")
		b.WriteString("      - id: pistos\n        name: pistos review (staged)\n        entry: pistos hooks run pre-commit\n        language: system\n        pass_filenames: false\n        always_run: true\n        stages: [pre-commit]\n")
		if contains(kinds, hookPrePush) {
			b.WriteString("# pre-commit does not forward git's pre-push stdin, so `pistos hooks run pre-push` cannot\n# see the pushed refs there; run `pistos review --base <remote branch> --head HEAD` instead.\n")
		}
	}
	return b.String()
}

type hookState int

const (
	hookMissing hookState = iota
	hookOurs
	hookForeign
)

func readHook(path string) (hookState, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return hookMissing, ""
	}
	if strings.Contains(string(b), hookMarker) {
		return hookOurs, string(b)
	}
	return hookForeign, string(b)
}

func (a *app) hooksInstallCommand() *cobra.Command {
	var write bool
	cmd := &cobra.Command{
		Use:   "install [pre-commit] [pre-push] [claude]",
		Short: "Install git hooks (default: pre-commit and pre-push) or print the Claude Code hook settings",
		Long: `Write small hook scripts that run ` + "`pistos hooks run <kind>`" + ` into the directory git uses for hooks
(core.hooksPath is respected). Existing hooks not written by pistos are never replaced. When husky,
lefthook or the pre-commit framework manages hooks, nothing is written and the snippet to add to
that manager's configuration is printed instead.

` + "`install claude`" + ` prints the Claude Code Stop hook for .claude/settings.json; with --write it is merged
into ` + claudeSettings + ` (personal, not committed).`,
		Example: `  pistos hooks install
  pistos hooks install pre-commit
  pistos hooks install claude --write`,
		RunE: func(cmd *cobra.Command, args []string) error {
			kinds, err := parseHookKinds(args, true)
			if err != nil {
				return err
			}
			var gitKinds []string
			for _, k := range kinds {
				if k == hookClaude {
					if err := a.installClaude(write); err != nil {
						return err
					}
				} else {
					gitKinds = append(gitKinds, k)
				}
			}
			if len(gitKinds) == 0 {
				return nil
			}
			he, err := a.hookEnvironment(cmd.Context())
			if err != nil {
				return err
			}
			if he.manager != "" {
				fmt.Fprintf(a.stdout, "hooks are managed by %s; nothing was installed. Add this to its configuration:\n\n%s", he.manager, managerSnippet(he.manager, gitKinds))
				return nil
			}
			var refused []string
			for _, k := range gitKinds {
				p := filepath.Join(he.dir, k)
				state, content := readHook(p)
				switch {
				case state == hookForeign:
					refused = append(refused, k)
					fmt.Fprintf(a.stderr, "pistos: %s exists and was not written by pistos; left untouched.\n"+
						"        add `pistos hooks run %s \"$@\"` to it, or move it away and rerun `pistos hooks install %s`\n", rel(he.repo, p), k, k)
					continue
				case state == hookOurs && content == hookScript(k):
					fmt.Fprintf(a.stdout, "kept     %s (already installed)\n", rel(he.repo, p))
					continue
				}
				if err := os.MkdirAll(he.dir, 0o755); err != nil {
					return withCode(ExitError, err)
				}
				if err := fsutil.WriteFileAtomic(p, []byte(hookScript(k)), 0o755); err != nil {
					return withCode(ExitError, err)
				}
				if err := os.Chmod(p, 0o755); err != nil {
					return withCode(ExitError, err)
				}
				fmt.Fprintf(a.stdout, "installed %s\n", rel(he.repo, p))
			}
			if len(refused) > 0 {
				return withCode(ExitError, nil)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "claude: merge the Stop hook into "+claudeSettings)
	return cmd
}

func (a *app) hooksUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall [pre-commit] [pre-push] [claude]",
		Short: "Remove hooks installed by pistos (others are left untouched)",
		RunE: func(cmd *cobra.Command, args []string) error {
			kinds, err := parseHookKinds(args, true)
			if err != nil {
				return err
			}
			var he *hookEnv
			for _, k := range kinds {
				if k == hookClaude {
					if err := a.uninstallClaude(); err != nil {
						return err
					}
					continue
				}
				if he == nil {
					if he, err = a.hookEnvironment(cmd.Context()); err != nil {
						return err
					}
				}
				p := filepath.Join(he.dir, k)
				switch state, _ := readHook(p); state {
				case hookMissing:
					fmt.Fprintf(a.stdout, "absent   %s (not installed)\n", rel(he.repo, p))
				case hookForeign:
					fmt.Fprintf(a.stdout, "kept     %s (not written by pistos)\n", rel(he.repo, p))
				default:
					if err := os.Remove(p); err != nil {
						return withCode(ExitError, err)
					}
					fmt.Fprintf(a.stdout, "removed  %s\n", rel(he.repo, p))
				}
			}
			return nil
		},
	}
}

func (a *app) hooksStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which pistos hooks are installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			he, err := a.hookEnvironment(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "hooks dir  %s\n", rel(he.repo, he.dir))
			if he.manager != "" {
				fmt.Fprintf(a.stdout, "manager    %s (add the snippet from `pistos hooks install` to its configuration)\n", he.manager)
			}
			for _, k := range gitHookKinds {
				status := "not installed"
				switch state, content := readHook(filepath.Join(he.dir, k)); {
				case state == hookOurs && content == hookScript(k):
					status = "installed"
				case state == hookOurs:
					status = "installed (outdated script; rerun `pistos hooks install`)"
				case state == hookForeign && strings.Contains(content, "pistos hooks run "+k):
					status = "foreign hook calling pistos"
				case state == hookForeign:
					status = "foreign hook (not managed by pistos)"
				}
				fmt.Fprintf(a.stdout, "%-10s %s\n", k, status)
			}
			var where []string
			for _, f := range []string{".claude/settings.json", claudeSettings} {
				if s, err := readClaudeSettings(filepath.Join(he.repo, f)); err == nil && hasClaudeStop(s) {
					where = append(where, f)
				}
			}
			if len(where) > 0 {
				fmt.Fprintf(a.stdout, "%-10s Stop hook in %s\n", hookClaude, strings.Join(where, ", "))
			} else {
				fmt.Fprintf(a.stdout, "%-10s not configured (pistos hooks install claude)\n", hookClaude)
			}
			return nil
		},
	}
}

func (a *app) hooksRunCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "run pre-commit|pre-push|claude-stop [git hook arguments]",
		Short: "Run a hook (called by the installed hook scripts)",
		Long: `pre-commit reviews the index (GIT_INDEX_FILE is honored, so commit -a and commit <paths> are
reviewed as committed). pre-push reads git's ref lines from stdin and reviews each pushed ref against
what the remote has. claude-stop reads Claude Code's Stop hook input and exits 2 with the issues on
stderr when the agent's changes have blocking issues. PISTOS_SKIP_HOOKS=1 skips every hook.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := args[0]
			if kind != hookPreCommit && kind != hookPrePush && kind != "claude-stop" {
				return withCode(ExitError, fmt.Errorf("unknown hook %q (pre-commit, pre-push, claude-stop)", kind))
			}
			if os.Getenv("PISTOS_SKIP_HOOKS") == "1" {
				fmt.Fprintf(a.stderr, "pistos: %s review skipped (PISTOS_SKIP_HOOKS=1)\n", kind)
				return nil
			}
			if kind == "claude-stop" {
				return a.runClaudeStop(cmd.Context())
			}
			return a.runGitHook(cmd.Context(), kind, args[1:])
		},
	}
}

func (a *app) runGitHook(ctx context.Context, kind string, args []string) error {
	repo, err := a.repoRoot()
	if err != nil {
		return err
	}
	cfg, err := config.Load(repo)
	if err != nil {
		fmt.Fprintf(a.stderr, "pistos: %s review skipped: %v\n", kind, err)
		return nil
	}
	if kind == hookPreCommit {
		idx, err := inheritedIndexFile()
		if err != nil {
			return withCode(ExitError, err)
		}
		o := review.Options{Repo: repo, Staged: true, IndexFile: idx, Profile: cfg.Hooks.PreCommitProfile}
		return hookExit(a.hookReview(ctx, o, cfg, "commit"))
	}

	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}
	refs, err := parsePushRefs(a.stdin)
	if err != nil {
		fmt.Fprintf(a.stderr, "pistos: pre-push review skipped: %v\n", err)
		return a.incompleteExit(cfg)
	}
	code := 0
	for _, p := range planPush(ctx, snapshot.Git{Repo: repo}, remote, refs) {
		if p.Skip != "" {
			fmt.Fprintf(a.stderr, "pistos: %s: %s\n", p.Ref.LocalRef, p.Skip)
			continue
		}
		fmt.Fprintf(a.stderr, "pistos: reviewing %s → %s (%s..%s)\n", p.Ref.LocalRef, p.Ref.RemoteRef, short(p.Base), short(p.Head))
		o := review.Options{Repo: repo, Base: p.Base, Head: p.Head, Profile: cfg.Hooks.PrePushProfile}
		// A blocked ref decides the exit code; otherwise the first non-zero one does.
		if c := a.hookReview(ctx, o, cfg, "push"); c == ExitBlocking || code == 0 {
			code = c
		}
	}
	return hookExit(code)
}

func hookExit(code int) error {
	if code == 0 {
		return nil
	}
	return withCode(code, nil)
}

// hookReview runs one review for a git hook and maps it to the hook's exit code: blocking issues
// stop the operation, an incomplete review or a configuration error warns (unless
// hooks.block_on_incomplete), cancellation stops it.
func (a *app) hookReview(ctx context.Context, o review.Options, cfg *config.Config, what string) int {
	env, err := a.executeReview(ctx, o, reviewOutput{
		w: a.stdout, format: "text", text: report.TextOptions{Brief: true}, html: true, announce: true,
	})
	if err != nil {
		var ee *exitError
		if errors.As(err, &ee) && ee.code == ExitCancelled {
			fmt.Fprintf(a.stderr, "pistos: review cancelled; the %s is blocked\n", what)
			return ExitBlocking
		}
		fmt.Fprintf(a.stderr, "pistos: review failed: %v\n", err)
		return a.incompleteCode(cfg, what)
	}
	v := env.Canonical.Verdict
	if v.Blocking > 0 {
		fmt.Fprintf(a.stderr, "pistos: %s blocked by %d issue(s). Fix them, record a decision (pistos tag N irrelevant),\n"+
			"        or bypass once with --no-verify or PISTOS_SKIP_HOOKS=1\n", what, v.Blocking)
		return ExitBlocking
	}
	if v.ExitCode == ExitError {
		fmt.Fprintf(a.stderr, "pistos: review incomplete: %s\n", v.Reason)
		return a.incompleteCode(cfg, what)
	}
	return ExitOK
}

func (a *app) incompleteCode(cfg *config.Config, what string) int {
	if cfg.Hooks.BlockOnIncomplete {
		fmt.Fprintf(a.stderr, "pistos: the %s is blocked (hooks.block_on_incomplete = true)\n", what)
		return ExitError
	}
	fmt.Fprintf(a.stderr, "pistos: not blocking the %s (set hooks.block_on_incomplete = true to block)\n", what)
	return ExitOK
}

func (a *app) incompleteExit(cfg *config.Config) error {
	return hookExit(a.incompleteCode(cfg, "push"))
}

// pushRef is one line git writes to a pre-push hook's stdin.
type pushRef struct {
	LocalRef, LocalSHA, RemoteRef, RemoteSHA string
}

func parsePushRefs(r io.Reader) ([]pushRef, error) {
	var refs []pushRef
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 4 {
			return nil, fmt.Errorf("unexpected pre-push input line %q", sc.Text())
		}
		refs = append(refs, pushRef{f[0], f[1], f[2], f[3]})
	}
	return refs, sc.Err()
}

type pushPlan struct {
	Ref        pushRef
	Base, Head string
	// Skip explains why this ref is not reviewed.
	Skip string
}

// isZeroID reports git's all-zeros object name (SHA-1 or SHA-256) used for absent refs.
func isZeroID(id string) bool { return id != "" && strings.Trim(id, "0") == "" }

// planPush decides the base of each pushed ref: what the remote already has, its remote-tracking
// ref when that commit is not available locally, or the remote's default branch for a new branch.
func planPush(ctx context.Context, g snapshot.Git, remote string, refs []pushRef) []pushPlan {
	resolves := func(ref string) bool {
		_, _, err := g.Commit(ctx, ref)
		return err == nil
	}
	var plans []pushPlan
	for _, r := range refs {
		p := pushPlan{Ref: r, Head: r.LocalSHA}
		switch {
		case isZeroID(r.LocalSHA):
			p.Skip = "deletes " + r.RemoteRef + "; nothing to review"
		case isZeroID(r.RemoteSHA):
			def := "refs/remotes/" + remote + "/HEAD"
			if remote != "" && resolves(def) {
				p.Base = def
			} else {
				p.Skip = fmt.Sprintf("new branch and %s is unknown, so there is no base to compare against (git remote set-head %s --auto)", def, remote)
			}
		case resolves(r.RemoteSHA):
			p.Base = r.RemoteSHA
		default:
			tracking := "refs/remotes/" + remote + "/" + strings.TrimPrefix(r.RemoteRef, "refs/heads/")
			if remote != "" && strings.HasPrefix(r.RemoteRef, "refs/heads/") && resolves(tracking) {
				p.Base = tracking
			} else {
				p.Skip = fmt.Sprintf("the remote's %s is not present locally; fetch first to review this push", short(r.RemoteSHA))
			}
		}
		plans = append(plans, p)
	}
	return plans
}

// claudeStopInput is the part of Claude Code's Stop hook input pistos uses.
type claudeStopInput struct {
	StopHookActive bool   `json:"stop_hook_active"`
	Cwd            string `json:"cwd"`
}

// runClaudeStop reviews the working snapshot when a Claude Code agent tries to stop. Exit code 2 makes
// Claude Code keep going with stderr as the reason; every other outcome lets it stop.
func (a *app) runClaudeStop(ctx context.Context) error {
	b, err := io.ReadAll(io.LimitReader(a.stdin, 1<<20))
	if err != nil {
		fmt.Fprintln(a.stderr, "pistos: cannot read the Stop hook input; not reviewing")
		return nil
	}
	var in claudeStopInput
	if err := json.Unmarshal(b, &in); err != nil {
		fmt.Fprintln(a.stderr, "pistos: the Stop hook input is not JSON; not reviewing")
		return nil
	}
	// Claude Code is already continuing because of an earlier block: let it stop.
	if in.StopHookActive {
		return nil
	}
	repo, err := a.repoRoot()
	if err != nil {
		return nil
	}
	if a.repo == "" && in.Cwd != "" && filepath.IsAbs(in.Cwd) {
		repo = enclosingRepo(in.Cwd)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return nil
	}
	cfg, err := config.Load(repo)
	if err != nil {
		fmt.Fprintf(a.stderr, "pistos: review skipped: %v\n", err)
		return nil
	}
	// Decisions come from the committed baseline, so an agent editing feedback.yaml cannot silence issues.
	o := review.Options{Repo: repo, Profile: cfg.Hooks.ClaudeProfile, FeedbackFromBase: true}
	var out bytes.Buffer
	env, err := a.executeReview(ctx, o, reviewOutput{w: &out, format: "agent", maxIssues: 20, html: true})
	if err != nil {
		fmt.Fprintf(a.stderr, "pistos: review failed (not blocking): %v\n", err)
		return nil
	}
	if env.Canonical.Verdict.Blocking > 0 {
		a.stderr.Write(out.Bytes())
		return withCode(2, nil)
	}
	if env.Canonical.Verdict.Outcome == "incomplete" {
		fmt.Fprintf(a.stderr, "pistos: review incomplete (not blocking): %s\n", env.Canonical.Verdict.Reason)
	}
	return nil
}

func readClaudeSettings(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s map[string]any
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s == nil {
		s = map[string]any{}
	}
	return s, nil
}

// stopGroups returns settings.hooks.Stop, rejecting shapes pistos would have to rewrite to merge into.
func stopGroups(s map[string]any) ([]any, error) {
	hooks, ok := s["hooks"]
	if !ok {
		return nil, nil
	}
	hm, ok := hooks.(map[string]any)
	if !ok {
		return nil, errors.New(`"hooks" is not an object`)
	}
	stop, ok := hm["Stop"]
	if !ok {
		return nil, nil
	}
	groups, ok := stop.([]any)
	if !ok {
		return nil, errors.New(`"hooks.Stop" is not an array`)
	}
	return groups, nil
}

func isClaudeStopHook(h any) bool {
	m, ok := h.(map[string]any)
	if !ok {
		return false
	}
	c, _ := m["command"].(string)
	return strings.Contains(c, claudeStopCommand)
}

func hasClaudeStop(s map[string]any) bool {
	groups, _ := stopGroups(s)
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			if isClaudeStopHook(h) {
				return true
			}
		}
	}
	return false
}

func claudeStopGroup() map[string]any {
	return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": claudeStopCommand}}}
}

func encodeSettings(s map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(s)
	return buf.Bytes(), err
}

func (a *app) installClaude(write bool) error {
	if !write {
		b, _ := encodeSettings(map[string]any{"hooks": map[string]any{"Stop": []any{claudeStopGroup()}}})
		// Only the JSON goes to stdout, so it can be redirected into a file as is.
		fmt.Fprintf(a.stderr, "Claude Code Stop hook: add to .claude/settings.json (shared) or %s (personal),\nor run `pistos hooks install claude --write` to merge it into %s.\n", claudeSettings, claudeSettings)
		a.stdout.Write(b)
		return nil
	}
	repo, err := a.repoRoot()
	if err != nil {
		return err
	}
	p := filepath.Join(repo, filepath.FromSlash(claudeSettings))
	s, err := readClaudeSettings(p)
	if errors.Is(err, os.ErrNotExist) {
		s, err = map[string]any{}, nil
	}
	if err != nil {
		return withCode(ExitError, fmt.Errorf("%v; left untouched", err))
	}
	groups, err := stopGroups(s)
	if err != nil {
		return withCode(ExitError, fmt.Errorf("%s: %v; left untouched", claudeSettings, err))
	}
	if hasClaudeStop(s) {
		fmt.Fprintf(a.stdout, "kept     %s (Stop hook already configured)\n", claudeSettings)
		return nil
	}
	hm, _ := s["hooks"].(map[string]any)
	if hm == nil {
		hm = map[string]any{}
		s["hooks"] = hm
	}
	hm["Stop"] = append(groups, claudeStopGroup())
	b, err := encodeSettings(s)
	if err != nil {
		return withCode(ExitError, err)
	}
	if err := fsutil.WriteFileAtomic(p, b, 0o644); err != nil {
		return withCode(ExitError, err)
	}
	fmt.Fprintf(a.stdout, "updated  %s (Stop hook: %s)\n", claudeSettings, claudeStopCommand)
	return nil
}

func (a *app) uninstallClaude() error {
	repo, err := a.repoRoot()
	if err != nil {
		return err
	}
	p := filepath.Join(repo, filepath.FromSlash(claudeSettings))
	s, err := readClaudeSettings(p)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !hasClaudeStop(s)) {
		fmt.Fprintf(a.stdout, "absent   %s has no pistos Stop hook\n", claudeSettings)
		return nil
	}
	if err != nil {
		return withCode(ExitError, fmt.Errorf("%v; left untouched", err))
	}
	groups, _ := stopGroups(s)
	var kept []any
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		if !ok || hs == nil {
			kept = append(kept, g)
			continue
		}
		var keep []any
		for _, h := range hs {
			if !isClaudeStopHook(h) {
				keep = append(keep, h)
			}
		}
		if len(keep) > 0 {
			gm["hooks"] = keep
			kept = append(kept, gm)
		}
	}
	hm := s["hooks"].(map[string]any)
	if len(kept) > 0 {
		hm["Stop"] = kept
	} else {
		delete(hm, "Stop")
	}
	if len(hm) == 0 {
		delete(s, "hooks")
	}
	b, err := encodeSettings(s)
	if err != nil {
		return withCode(ExitError, err)
	}
	if err := fsutil.WriteFileAtomic(p, b, 0o644); err != nil {
		return withCode(ExitError, err)
	}
	fmt.Fprintf(a.stdout, "removed  pistos Stop hook from %s\n", claudeSettings)
	return nil
}

func rel(repo, p string) string {
	if r, err := filepath.Rel(repo, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return p
}

// short abbreviates object names in hook notes.
func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
