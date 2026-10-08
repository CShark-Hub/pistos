// Package review orchestrates one review: snapshots, check plan, execution, baseline comparison,
// feedback and verdict.
package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/feedback"
	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/fsutil"
	"example.com/pistos/internal/plugin"
	"example.com/pistos/internal/snapshot"
	"example.com/pistos/internal/store"

	"golang.org/x/sync/semaphore"
)

type Options struct {
	Repo    string
	Base    string
	Head    string
	All     bool
	Profile string
	Offline bool
	Jobs    int
	// Progress receives human-readable progress lines; nil discards them.
	Progress func(string)
	// FeedbackFromBase applies the decisions committed in the baseline instead of the working copy,
	// so an analyzed change cannot exempt itself.
	FeedbackFromBase bool
}

// ConfigError marks failures caused by invalid configuration or arguments (exit code 2).
type ConfigError struct{ Err error }

func (e *ConfigError) Error() string { return e.Err.Error() }
func (e *ConfigError) Unwrap() error { return e.Err }

const (
	sideHead = "head"
	sideBase = "base"
)

type engine struct {
	opts      Options
	cfg       *config.Config
	git       snapshot.Git
	store     store.Store
	toolbox   *plugin.Toolbox
	manifests map[string]*plugin.Manifest
	sources   *sourceCache
	treeRoot  string
	trees     map[string]*treeOnce
	treesMu   sync.Mutex
	telemetry []finding.TaskTelemetry
	telMu     sync.Mutex
}

type treeOnce struct {
	once sync.Once
	dir  string
	err  error
}

// Run performs a review and records it as the current report.
func Run(ctx context.Context, o Options) (*finding.Envelope, error) {
	started := time.Now()
	cfg, err := config.Load(o.Repo)
	if err != nil {
		return nil, &ConfigError{err}
	}
	if o.Profile == "" {
		o.Profile = cfg.Profile
	}
	if !contains(config.Profiles, o.Profile) {
		return nil, &ConfigError{fmt.Errorf("unknown profile %q", o.Profile)}
	}
	if o.Jobs == 0 {
		o.Jobs = cfg.Execution.Jobs
	}
	if o.Head != "" && o.Base == "" {
		return nil, &ConfigError{errors.New("--head requires --base")}
	}
	if o.All && (o.Base != "" || o.Head != "") {
		return nil, &ConfigError{errors.New("--all reviews the working snapshot as a whole and cannot be combined with --base/--head")}
	}
	if cfg.Execution.Mode != "native" {
		return nil, &ConfigError{fmt.Errorf("execution.mode %q is not available in this build", cfg.Execution.Mode)}
	}
	lock, err := config.LoadLock(o.Repo)
	if err != nil {
		return nil, &ConfigError{err}
	}

	e := &engine{
		opts: o, cfg: cfg, git: snapshot.Git{Repo: o.Repo}, store: store.Store{Repo: o.Repo},
		manifests: map[string]*plugin.Manifest{}, sources: newSourceCache(), trees: map[string]*treeOnce{},
	}
	if !e.git.IsRepo(ctx) {
		return nil, &ConfigError{errors.New("pistos review requires a git repository")}
	}
	root, err := plugin.CacheRoot()
	if err != nil {
		return nil, err
	}
	e.treeRoot = filepath.Join(root, "trees")
	e.toolbox = plugin.NewToolbox(o.Repo, e.store.StateDir(), lock, plugin.BaseEnv(nil))
	defer e.toolbox.Save()

	scope, changes, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := e.affectedProjects(ctx, scope, changes)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		scope.Projects = append(scope.Projects, p.ID)
	}

	rep := &finding.Report{Schema: finding.SchemaVersion, Scope: scope}
	rep.Coverage = finding.Coverage{Profile: o.Profile, AI: string(finding.StatusNotRequested)}
	if cfg.AI.Enabled {
		rep.Coverage.AI = "unsupported: AI discovery is not available in this build"
	}

	if !scope.Empty {
		tasks, results := e.plan(projects, scope)
		e.progress("running %d checks with %d jobs", len(tasks), o.Jobs)
		e.execute(ctx, tasks, results)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		rep.Coverage.Capabilities, rep.Issues = assemble(results, scope, e.manifests, projects)
	}

	fb, note, err := e.loadFeedback(ctx, scope)
	if err != nil {
		return nil, &ConfigError{err}
	}
	if note != "" {
		rep.Coverage.Capabilities = append(rep.Coverage.Capabilities, finding.CapabilityResult{
			Capability: "feedback-policy", Project: "-", Side: sideHead, Status: finding.StatusComplete, Limitations: []string{note},
		})
	}
	rep.Feedback = fb.Hash()
	Finalize(rep, cfg.Policy.BlockOn, feedback.NewMatcher(fb, e.matchEnvironment(ctx, scope.HeadTree)).Apply)

	snapshot.Prune(e.treeRoot, 8)
	env := &finding.Envelope{Canonical: rep}
	env.Telemetry = finding.Telemetry{StartedAt: started.UTC().Format(time.RFC3339), DurationMS: time.Since(started).Milliseconds(), Jobs: o.Jobs}
	sort.Slice(e.telemetry, func(i, j int) bool { return e.telemetry[i].Task < e.telemetry[j].Task })
	env.Telemetry.Tasks = e.telemetry

	ledger := e.store.LoadLedger()
	env.Numbers = ledger.AssignAll(rep.Issues, scope.HeadTree)
	if err := e.store.SaveLedger(ledger); err != nil {
		return env, err
	}
	return env, e.store.SaveCurrent(env)
}

func (e *engine) progress(format string, a ...any) {
	if e.opts.Progress != nil {
		e.opts.Progress(fmt.Sprintf(format, a...))
	}
}

func (e *engine) resolveScope(ctx context.Context) (finding.Scope, []snapshot.Change, error) {
	s := finding.Scope{}
	var headCommit string
	if e.opts.Head != "" {
		c, tree, err := e.git.Commit(ctx, e.opts.Head)
		if err != nil {
			return s, nil, &ConfigError{err}
		}
		s.Mode, s.HeadRef, s.HeadTree, s.HeadCommit, headCommit = "commit", e.opts.Head, tree, c, c
	} else {
		tree, err := e.git.WorkingTree(ctx)
		if err != nil {
			return s, nil, err
		}
		s.Mode, s.HeadRef, s.HeadTree = "working", "(working tree)", tree
		if e.git.HasHead(ctx) {
			headCommit, _, _ = e.git.Commit(ctx, "HEAD")
		}
	}
	if e.opts.All {
		s.Mode = "all"
		return s, nil, nil
	}

	switch {
	case e.opts.Base != "":
		if _, _, err := e.git.Commit(ctx, e.opts.Base); err != nil {
			return s, nil, &ConfigError{err}
		}
		if headCommit == "" {
			return s, nil, &ConfigError{errors.New("--base needs a repository with at least one commit")}
		}
		mb, err := e.git.MergeBase(ctx, e.opts.Base, headCommit)
		if err != nil {
			return s, nil, &ConfigError{err}
		}
		_, tree, err := e.git.Commit(ctx, mb)
		if err != nil {
			return s, nil, err
		}
		s.BaseRef, s.BaseCommit, s.BaseTree = e.opts.Base, mb, tree
	case headCommit != "":
		_, tree, _ := e.git.Commit(ctx, "HEAD")
		s.BaseRef, s.BaseCommit, s.BaseTree = "HEAD", headCommit, tree
	default:
		s.BaseRef, s.BaseTree = "(empty repository)", snapshot.EmptyTree
	}

	changes, err := e.git.Diff(ctx, s.BaseTree, s.HeadTree)
	if err != nil {
		return s, nil, err
	}
	s.ChangedFiles = []string{}
	for _, c := range changes {
		s.ChangedFiles = append(s.ChangedFiles, c.Path)
	}
	s.Empty = len(changes) == 0
	return s, changes, nil
}

func (e *engine) manifest(eco string) (*plugin.Manifest, error) {
	if m, ok := e.manifests[eco]; ok {
		return m, nil
	}
	m, err := plugin.LoadManifest(eco)
	if err != nil {
		return nil, err
	}
	e.manifests[eco] = m
	return m, nil
}

// affectedProjects selects projects whose own subtree, shared inputs or referenced projects changed.
func (e *engine) affectedProjects(ctx context.Context, s finding.Scope, changes []snapshot.Change) ([]finding.Project, error) {
	byID := map[string]config.Project{}
	for _, p := range e.cfg.Projects {
		byID[p.ID] = p
	}
	changed := func(p config.Project) bool {
		root, _ := config.CleanRel(p.Root)
		for _, c := range changes {
			for _, path := range []string{c.Path, c.From} {
				if path != "" && (root == "." || path == root || strings.HasPrefix(path, root+"/")) {
					return true
				}
			}
		}
		return false
	}
	var out []finding.Project
	for _, p := range e.cfg.Projects {
		eco, _ := config.NormalizeLanguage(p.Ecosystem)
		m, err := e.manifest(eco)
		if err != nil {
			return nil, &ConfigError{err}
		}
		for _, c := range p.Checks {
			if _, ok := m.Checks[c]; !ok {
				return nil, &ConfigError{fmt.Errorf("project %s: pack %s has no check %q (available: %s)", p.ID, eco, c, strings.Join(m.CheckIDs(), ", "))}
			}
		}
		for k := range p.Options {
			if !contains(m.Options, k) {
				return nil, &ConfigError{fmt.Errorf("project %s: option %q is not supported by pack %s (supported: %s)", p.ID, k, eco, strings.Join(m.Options, ", "))}
			}
		}
		include := s.Mode == "all" || changed(p) || e.sharedInputChanged(m, p, changes)
		for _, ref := range p.References {
			include = include || changed(byID[ref])
		}
		if include {
			root, _ := config.CleanRel(p.Root)
			out = append(out, finding.Project{ID: p.ID, Root: root, Ecosystem: eco, Checks: p.Checks, Options: p.Options, References: p.References})
		}
	}
	return out, nil
}

func (e *engine) sharedInputChanged(m *plugin.Manifest, p config.Project, changes []snapshot.Change) bool {
	root, _ := config.CleanRel(p.Root)
	for _, c := range changes {
		for _, name := range m.SharedInputs {
			for dir := root; ; dir = parentDir(dir) {
				if c.Path == joinRel(dir, name) {
					return true
				}
				if dir == "." {
					break
				}
			}
		}
	}
	return false
}

type task struct {
	finding.Task
	project finding.Project
	tree    string
}

type taskResult struct {
	task   task
	result finding.CapabilityResult
}

// plan builds the fixed check plan. The profile, not any AI output, decides which checks run.
func (e *engine) plan(projects []finding.Project, s finding.Scope) ([]task, []*taskResult) {
	var tasks []task
	for _, p := range projects {
		m := e.manifests[p.Ecosystem]
		for _, id := range p.Checks {
			c := m.Checks[id]
			requested := e.opts.Profile != "fast" || c.Scope == "file"
			sides := []string{sideHead}
			if s.Mode != "all" {
				sides = append(sides, sideBase)
			}
			for _, side := range sides {
				tree := s.HeadTree
				if side == sideBase {
					tree = s.BaseTree
				}
				t := task{Task: finding.Task{
					ID: p.ID + "/" + id + "@" + side, Project: p.ID, Capability: id, Side: side,
					Scope: c.Scope, Weight: max(1, min(c.Weight, e.opts.Jobs)), Required: !c.Optional && side == sideHead,
				}, project: p, tree: tree}
				if !requested {
					t.Required = false
				}
				tasks = append(tasks, t)
			}
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	results := make([]*taskResult, len(tasks))
	for i, t := range tasks {
		results[i] = &taskResult{task: t}
		c := e.manifests[t.project.Ecosystem].Checks[t.Capability]
		if e.opts.Profile == "fast" && c.Scope != "file" {
			results[i].result = finding.CapabilityResult{Status: finding.StatusNotRequested, Limitations: []string{"project-aware check skipped by the fast profile"}}
		}
	}
	return tasks, results
}

// execute runs all requested tasks within the job budget; it never stops early on findings.
func (e *engine) execute(ctx context.Context, tasks []task, results []*taskResult) {
	sem := semaphore.NewWeighted(int64(e.opts.Jobs))
	var wg sync.WaitGroup
	for i, t := range tasks {
		if results[i].result.Status == finding.StatusNotRequested {
			continue
		}
		wg.Add(1)
		go func(i int, t task) {
			defer wg.Done()
			if err := sem.Acquire(ctx, int64(t.Weight)); err != nil {
				results[i].result = finding.CapabilityResult{Status: finding.StatusIncomplete, Error: "cancelled before start"}
				return
			}
			defer sem.Release(int64(t.Weight))
			start := time.Now()
			r, hit := e.runTask(ctx, t)
			results[i].result = r
			e.telMu.Lock()
			e.telemetry = append(e.telemetry, finding.TaskTelemetry{Task: t.ID, DurationMS: time.Since(start).Milliseconds(), CacheHit: hit})
			e.telMu.Unlock()
		}(i, t)
	}
	wg.Wait()
	for i, t := range tasks {
		r := &results[i].result
		r.Capability, r.Project, r.Side, r.Required, r.Scope = t.Capability, t.Project, t.Side, t.Required, t.Scope
	}
}

func (e *engine) materialize(ctx context.Context, tree string) (string, error) {
	e.treesMu.Lock()
	t, ok := e.trees[tree]
	if !ok {
		t = &treeOnce{dir: filepath.Join(e.treeRoot, tree)}
		e.trees[tree] = t
	}
	e.treesMu.Unlock()
	t.once.Do(func() { t.err = e.git.Materialize(ctx, tree, t.dir) })
	return t.dir, t.err
}

func (e *engine) runTask(ctx context.Context, t task) (finding.CapabilityResult, bool) {
	m := e.manifests[t.project.Ecosystem]
	check := m.Checks[t.Capability]
	fail := func(status finding.Status, err error) (finding.CapabilityResult, bool) {
		return finding.CapabilityResult{Status: status, Error: err.Error()}, false
	}

	if e.git.ObjectID(ctx, t.tree, t.project.Root) == "" {
		return finding.CapabilityResult{Status: finding.StatusComplete, Limitations: []string{"project root absent in this tree"}}, false
	}
	workRoot, err := e.materialize(ctx, t.tree)
	if err != nil {
		return fail(finding.StatusFailed, err)
	}
	projectDir := filepath.Join(workRoot, filepath.FromSlash(t.project.Root))

	var tools []plugin.ResolvedTool
	for _, name := range m.ToolsFor(t.Capability) {
		rt, err := e.toolbox.Resolve(ctx, m, name, filepath.Join(e.opts.Repo, filepath.FromSlash(t.project.Root)))
		if err != nil {
			return fail(finding.StatusIncomplete, err)
		}
		tools = append(tools, rt)
	}

	key := e.inputKey(ctx, t, m, tools)
	if cached, ok := e.store.GetResult(key); ok {
		cached.Status = finding.StatusCachedComplete
		return *cached, true
	}

	inv := plugin.Invocation{
		Manifest: m, CheckID: t.Capability, Tool: tools[0], Uses: tools[1:], ProjectDir: projectDir,
		Options: t.project.Options, Jobs: t.Weight, Timeout: e.cfg.Execution.Timeout.Duration,
		ScratchDir: filepath.Join(e.store.CacheDir(), "scratch", t.project.ID, t.Capability),
		Tree:       plugin.TreeScope{ID: t.tree, Project: t.project.ID},
	}
	e.progress("%s: %s", t.ID, tools[0].Path)
	parsed, status, err := plugin.Execute(ctx, inv)
	findings, limits := e.normalize(t, m, workRoot, projectDir, parsed.Diagnostics)
	res := finding.CapabilityResult{
		Status: status, Findings: findings, Limitations: append(append(limits, check.Limitations...), parsed.Limitations...),
		InputKey: key, Tool: tools[0].Name, ToolVersion: tools[0].Version,
	}
	if err != nil {
		res.Error = err.Error()
	}
	if status == finding.StatusComplete {
		_ = e.store.PutResult(key, res)
	}
	return res, false
}

// inputKey hashes everything the result depends on except the job count, which cannot change findings.
func (e *engine) inputKey(ctx context.Context, t task, m *plugin.Manifest, tools []plugin.ResolvedTool) string {
	in := finding.InputManifest{
		Tree:        t.tree,
		ProjectHash: e.git.ObjectID(ctx, t.tree, t.project.Root),
		Tool:        tools[0].Name, ToolVersion: tools[0].Version, ToolSHA256: tools[0].Identity,
		Args: m.Checks[t.Capability].Args, Pack: m.Name, PackVersion: m.Version,
	}
	// The tree ID is deliberately dropped: unrelated edits elsewhere in the repository must not
	// invalidate a project's result.
	in.Tree = ""
	var shared []string
	for _, name := range m.SharedInputs {
		for dir := t.project.Root; ; dir = parentDir(dir) {
			if id := e.git.ObjectID(ctx, t.tree, joinRel(dir, name)); id != "" {
				shared = append(shared, joinRel(dir, name)+"="+id)
			}
			if dir == "." {
				break
			}
		}
	}
	for _, ref := range t.project.References {
		for _, p := range e.cfg.Projects {
			if p.ID == ref {
				shared = append(shared, "ref:"+ref+"="+e.git.ObjectID(ctx, t.tree, p.Root))
			}
		}
	}
	in.SharedHash = fsutil.HashStrings(shared...)
	for _, u := range tools[1:] {
		in.EnvHash += u.Name + "@" + u.Version + ":" + u.Identity + ";"
	}
	in.Env = map[string]string{}
	for k, v := range m.Checks[t.Capability].Env {
		in.Env[k] = v
	}
	for k, v := range t.project.Options {
		in.Env["opt:"+k] = v
	}
	b, _ := json.Marshal(struct {
		In      finding.InputManifest
		Project string
		Root    string
		Offline bool
		Check   string
	}{in, t.project.ID, t.project.Root, e.opts.Offline, t.Capability})
	return fsutil.HashBytes(b)
}

// normalize maps tool diagnostics onto repository-relative findings with stable identity.
func (e *engine) normalize(t task, m *plugin.Manifest, workRoot, projectDir string, diags []plugin.Diagnostic) ([]finding.Finding, []string) {
	var out []finding.Finding
	outside := 0
	realRoot, _ := filepath.EvalSymlinks(workRoot)
	for _, d := range diags {
		rel, ok := repoRelative(d.Path, workRoot, realRoot, projectDir)
		if !ok {
			outside++
			continue
		}
		abs := filepath.Join(workRoot, filepath.FromSlash(rel))
		src := e.sources.get(abs, t.project.Ecosystem, func() ([]byte, error) { return os.ReadFile(abs) })
		sev := finding.Severity(d.Severity)
		if sev.Rank() == 0 {
			sev = finding.SevWarning
		}
		f := finding.Finding{
			Lane: finding.LaneDeterministic, Project: t.project.ID, Family: m.Family(t.Capability, d.Rule),
			Severity: sev, Message: d.Message, Capability: t.Capability,
			Location:  finding.Location{Path: rel, Line: d.Line, Column: d.Column, EndLine: d.EndLine, EndColumn: d.EndColumn},
			Anchor:    finding.Anchor{Symbol: src.enclosing(d.Line), TextHash: finding.AnchorHash(src.line(d.Line))},
			Sources:   []finding.Source{{Tool: t.Capability, Rule: d.Rule, Severity: sev, Message: d.Message}},
			Reproduce: []string{reproduceCommand(t, m)},
		}
		out = append(out, f)
	}
	finding.AssignIdentity(out)
	for i := range out {
		out[i].RefreshEvidenceHash()
	}
	var limits []string
	if outside > 0 {
		limits = append(limits, fmt.Sprintf("%d diagnostics outside the repository were ignored", outside))
	}
	return out, limits
}

func reproduceCommand(t task, m *plugin.Manifest) string {
	c := m.Checks[t.Capability]
	var env []string
	for k, v := range c.Env {
		if !strings.Contains(v, "{") {
			env = append(env, k+"="+v)
		}
	}
	sort.Strings(env)
	var args []string
	for _, a := range c.Args {
		if strings.Contains(a, "{opt:") && t.project.Options[optName(a)] == "" {
			continue
		}
		args = append(args, strings.NewReplacer("{jobs}", "4", "{cache}", ".pistos/cache/scratch").Replace(optReplace(a, t.project.Options)))
	}
	cmd := strings.TrimSpace(strings.Join(env, " ") + " " + m.Tools[c.Tool].Command + " " + strings.Join(args, " "))
	if t.project.Root != "." {
		return "(cd " + t.project.Root + " && " + cmd + ")"
	}
	return cmd
}

func optName(a string) string {
	i := strings.Index(a, "{opt:")
	j := strings.Index(a[i:], "}")
	return a[i+5 : i+j]
}

func optReplace(a string, opts map[string]string) string {
	for strings.Contains(a, "{opt:") {
		name := optName(a)
		a = strings.Replace(a, "{opt:"+name+"}", opts[name], 1)
	}
	return a
}

// repoRelative maps a tool path (absolute in the snapshot or relative to the project) to a
// repository-relative path, rejecting anything outside the snapshot.
func repoRelative(p, workRoot, realRoot, projectDir string) (string, bool) {
	if p == "" {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(projectDir, p)
	}
	p = filepath.Clean(p)
	for _, root := range []string{workRoot, realRoot} {
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(root, p)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return filepath.ToSlash(rel), true
		}
	}
	return "", false
}

func parentDir(dir string) string {
	if dir == "." || !strings.Contains(dir, "/") {
		return "."
	}
	return dir[:strings.LastIndex(dir, "/")]
}

func joinRel(dir, name string) string {
	if dir == "." {
		return name
	}
	return dir + "/" + name
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
