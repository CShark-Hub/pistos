package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"example.com/pistos/internal/facts"
	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/runner"
)

// baseEnvAllowlist is everything an analyzer may inherit from the user's environment besides the
// pack's declared passthrough variables. Secrets such as provider keys are never on it.
var baseEnvAllowlist = []string{
	"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "TEMP", "TMP", "LANG", "LC_ALL", "TZ",
	"XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
	"SystemRoot", "SYSTEMROOT", "ComSpec", "PATHEXT", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
	"ProgramData", "ProgramFiles", "ProgramFiles(x86)", "WINDIR",
}

// BaseEnv builds an analyzer environment from the allowlist plus declared passthrough names.
func BaseEnv(passthrough []string) []string {
	var env []string
	for _, k := range append(append([]string{}, baseEnvAllowlist...), passthrough...) {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

type Invocation struct {
	Manifest   *Manifest
	CheckID    string
	Tool       ResolvedTool
	Uses       []ResolvedTool
	ProjectDir string
	Options    map[string]string
	Jobs       int
	ScratchDir string
	Timeout    time.Duration
	// Tree identifies the snapshot so workers can restate the scope they covered.
	Tree TreeScope
	// Vars are values computed by pistos for {var:NAME} placeholders (scratch paths, interpreters).
	Vars map[string]string
	// RepoRoot is the project's repository-relative root; facts paths are reported relative to the repository.
	RepoRoot string
}

type TreeScope struct {
	ID      string
	Project string
}

var placeholder = regexp.MustCompile(`\{(opt|var):([a-z_]+)\}`)

// expand fills placeholders; it reports false when any referenced value is empty so the caller can
// drop the whole argument rather than pass a dangling flag.
func (inv Invocation) expand(s string) (string, bool) {
	empty := false
	s = placeholder.ReplaceAllStringFunc(s, func(m string) string {
		sub := placeholder.FindStringSubmatch(m)
		v := inv.Options[sub[2]]
		if sub[1] == "var" {
			v = inv.Vars[sub[2]]
		}
		if v == "" {
			empty = true
		}
		return v
	})
	s = strings.ReplaceAll(s, "{jobs}", strconv.Itoa(max(inv.Jobs, 1)))
	s = strings.ReplaceAll(s, "{cache}", inv.ScratchDir)
	s = strings.ReplaceAll(s, "{root}", inv.ProjectDir)
	return s, !empty
}

// Command renders the check's argument array; arguments referencing an unset option are dropped.
func (inv Invocation) Command() runner.Cmd {
	c := inv.Manifest.Checks[inv.CheckID]
	var args []string
	for _, a := range c.Args {
		if v, ok := inv.expand(a); ok {
			args = append(args, v)
		}
	}
	env := map[string]string{}
	for _, kv := range BaseEnv(inv.Manifest.EnvPassthrough) {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	var pathDirs []string
	for _, u := range inv.Uses {
		pathDirs = append(pathDirs, filepath.Dir(u.Path))
	}
	if len(pathDirs) > 0 {
		env["PATH"] = strings.Join(append(pathDirs, env["PATH"]), string(os.PathListSeparator))
	}
	for k, v := range c.Env {
		if ev, ok := inv.expand(v); ok {
			env[k] = ev
		}
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	envList := make([]string, 0, len(keys))
	for _, k := range keys {
		envList = append(envList, k+"="+env[k])
	}

	path := inv.Tool.Path
	if rt := inv.Manifest.Tools[inv.Tool.Name].Runtime; rt != "" {
		for _, u := range inv.Uses {
			if u.Name == rt {
				args = append([]string{inv.Tool.Path}, args...)
				path = u.Path
			}
		}
	}
	return runner.Cmd{Path: path, Args: args, Dir: inv.ProjectDir, Env: envList, Timeout: inv.Timeout}
}

// Execute runs the check and classifies the outcome. Only a recognized exit code with parsable
// output can be complete; everything else is failed or incomplete, never clean.
func Execute(ctx context.Context, inv Invocation) (Parsed, finding.Status, error) {
	c := inv.Manifest.Checks[inv.CheckID]
	if err := os.MkdirAll(inv.ScratchDir, 0o755); err != nil {
		return Parsed{}, finding.StatusFailed, err
	}
	if c.Facts != "" {
		return executeFacts(ctx, inv)
	}
	if c.Format == FormatWorker {
		return executeWorker(ctx, inv)
	}
	artifactDir := ""
	if c.Artifacts != "" {
		artifactDir, _ = inv.expand(c.Artifacts)
		// Stale artifacts from an earlier run must never be read as this run's results.
		if err := os.RemoveAll(artifactDir); err != nil {
			return Parsed{}, finding.StatusFailed, err
		}
		if err := os.MkdirAll(artifactDir, 0o755); err != nil {
			return Parsed{}, finding.StatusFailed, err
		}
	}
	res, err := runner.Run(ctx, inv.Command())
	switch {
	case res.TimedOut:
		return Parsed{}, finding.StatusIncomplete, fmt.Errorf("%s timed out after %s", inv.CheckID, inv.Timeout)
	case ctx.Err() != nil:
		return Parsed{}, finding.StatusIncomplete, ctx.Err()
	case err != nil:
		return Parsed{}, finding.StatusFailed, fmt.Errorf("%s: %w", inv.CheckID, err)
	}
	_, known := c.ExitCodes[strconv.Itoa(res.ExitCode)]
	out := Output{Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.ExitCode}
	if artifactDir != "" {
		if out.Artifacts, err = readArtifacts(artifactDir); err != nil {
			return Parsed{}, finding.StatusFailed, err
		}
	}
	parsed, perr := Parse(c.Format, out)
	if !known {
		msg := fmt.Sprintf("%s exited with unexpected code %d", inv.CheckID, res.ExitCode)
		if perr != nil {
			msg += ": " + perr.Error()
		} else if t := tail(strings.Split(string(res.Stderr), "\n")); t != "(no output)" {
			msg += ": " + t
		}
		return parsed, finding.StatusFailed, errors.New(msg)
	}
	if perr != nil {
		return parsed, finding.StatusFailed, perr
	}
	if res.Truncated {
		return parsed, finding.StatusIncomplete, fmt.Errorf("%s output exceeded the capture limit", inv.CheckID)
	}
	if parsed.Incomplete != "" {
		return parsed, finding.StatusIncomplete, errors.New(parsed.Incomplete)
	}
	return parsed, finding.StatusComplete, nil
}

// FormatWorker marks checks served by an out-of-process worker instead of a wrapped CLI.
// FormatBuiltinGo marks Go facts computed in-process with go/ast; no tool is started.
const (
	FormatWorker    = "worker"
	FormatBuiltinGo = "builtin-go"
)

func executeWorker(ctx context.Context, inv Invocation) (Parsed, finding.Status, error) {
	cmd := inv.Command()
	if inv.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, inv.Timeout)
		defer cancel()
	}
	w, err := StartWorker(ctx, cmd, inv.Manifest.Name, inv.Manifest.Version)
	if err != nil {
		return Parsed{}, finding.StatusFailed, fmt.Errorf("%s: %w", inv.CheckID, err)
	}
	res, err := w.Analyze(ctx, AnalyzeParams{Capability: inv.CheckID, Project: inv.Tree.Project, Root: inv.ProjectDir, Tree: inv.Tree.ID, Options: inv.Options})
	if err != nil {
		w.Kill()
		if ctx.Err() != nil {
			return Parsed{}, finding.StatusIncomplete, fmt.Errorf("%s: %w", inv.CheckID, ctx.Err())
		}
		return Parsed{}, finding.StatusFailed, fmt.Errorf("%s: %w", inv.CheckID, err)
	}
	_ = w.Shutdown(ctx)
	p := Parsed{Diagnostics: res.Diagnostics, Limitations: res.Limitations}
	if res.Tree != inv.Tree.ID || res.Project != inv.Tree.Project {
		return p, finding.StatusIncomplete, fmt.Errorf("%s: worker reported scope %s/%s, expected %s/%s", inv.CheckID, res.Project, res.Tree, inv.Tree.Project, inv.Tree.ID)
	}
	if res.Status != string(finding.StatusComplete) {
		return p, finding.StatusIncomplete, fmt.Errorf("%s: worker reported status %q", inv.CheckID, res.Status)
	}
	return p, finding.StatusComplete, nil
}

func readArtifacts(dir string) ([][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func executeFacts(ctx context.Context, inv Invocation) (Parsed, finding.Status, error) {
	c := inv.Manifest.Checks[inv.CheckID]
	if c.Format == FormatBuiltinGo {
		f, limits, err := facts.GoFacts(inv.ProjectDir, inv.RepoRoot)
		if err != nil {
			return Parsed{}, finding.StatusFailed, err
		}
		if c.Facts == "metrics" {
			f.Imports = nil
		} else {
			f.Functions = nil
		}
		return Parsed{Facts: f, Limitations: limits}, finding.StatusComplete, nil
	}
	if inv.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, inv.Timeout)
		defer cancel()
	}
	w, err := StartWorker(ctx, inv.Command(), inv.Manifest.Name, inv.Manifest.Version)
	if err != nil {
		return Parsed{}, finding.StatusFailed, fmt.Errorf("%s: %w", inv.CheckID, err)
	}
	res, err := w.Facts(ctx, FactsParams{Kind: c.Facts, Root: inv.ProjectDir, RepoRoot: inv.RepoRoot, Tree: inv.Tree.ID, Project: inv.Tree.Project})
	if err != nil {
		w.Kill()
		if ctx.Err() != nil {
			return Parsed{}, finding.StatusIncomplete, fmt.Errorf("%s: %w", inv.CheckID, ctx.Err())
		}
		return Parsed{}, finding.StatusFailed, fmt.Errorf("%s: %w: %s", inv.CheckID, err, tail(strings.Split(string(w.Stderr()), "\n")))
	}
	_ = w.Shutdown(ctx)
	p := Parsed{Facts: res.Facts, Limitations: res.Limitations}
	switch {
	case res.Facts == nil:
		return p, finding.StatusFailed, fmt.Errorf("%s: worker returned no facts", inv.CheckID)
	case res.Tree != inv.Tree.ID || res.Project != inv.Tree.Project:
		return p, finding.StatusIncomplete, fmt.Errorf("%s: worker reported scope %s/%s, expected %s/%s", inv.CheckID, res.Project, res.Tree, inv.Tree.Project, inv.Tree.ID)
	case res.Status != string(finding.StatusComplete):
		return p, finding.StatusIncomplete, fmt.Errorf("%s: worker reported status %q", inv.CheckID, res.Status)
	}
	return p, finding.StatusComplete, nil
}
