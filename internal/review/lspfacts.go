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
	"time"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/fsutil"
	"example.com/pistos/internal/lsp"
	"example.com/pistos/internal/plugin"
	"example.com/pistos/internal/runner"
)

const factsSchema = 1

var factMethods = map[string]string{
	"definition":     "textDocument/definition",
	"references":     "textDocument/references",
	"implementation": "textDocument/implementation",
	"diagnostics":    "textDocument/diagnostic",
}

type FactsQuery struct {
	Repo   string
	Method string
	Path   string
	Line   int
	Column int
	// LoadTimeout bounds how long the server may keep loading the workspace before the answer
	// is reported as incomplete.
	LoadTimeout time.Duration
}

type FactLocation struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	External bool   `json:"external,omitempty"`
}

type FactDiagnostic struct {
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message"`
}

// FactsResult is a semantic answer mapped back to repository paths, with its completeness.
type FactsResult struct {
	Schema      int              `json:"schema"`
	Backend     string           `json:"backend"`
	Version     string           `json:"version"`
	Project     string           `json:"project"`
	Tree        string           `json:"tree"`
	Method      string           `json:"method"`
	Path        string           `json:"path"`
	Line        int              `json:"line"`
	Column      int              `json:"column"`
	Status      finding.Status   `json:"status"`
	Reason      string           `json:"reason,omitempty"`
	Locations   []FactLocation   `json:"locations,omitempty"`
	Diagnostics []FactDiagnostic `json:"diagnostics,omitempty"`
	Cached      bool             `json:"-"`
}

// Facts answers one semantic query with the language server of the project that owns the file.
// Only that one server is started, inside the working snapshot, and only for this request.
func Facts(ctx context.Context, q FactsQuery) (*FactsResult, error) {
	method, ok := factMethods[q.Method]
	if !ok {
		return nil, &ConfigError{fmt.Errorf("unknown fact %q (definition, references, implementation, diagnostics)", q.Method)}
	}
	rel, err := config.CleanRel(q.Path)
	if err != nil {
		return nil, &ConfigError{err}
	}
	cfg, err := config.Load(q.Repo)
	if err != nil {
		return nil, &ConfigError{err}
	}
	e, err := newEngine(ctx, Options{Repo: q.Repo}, cfg)
	if err != nil {
		return nil, err
	}
	defer e.toolbox.Save()

	project, err := owningProject(cfg, rel)
	if err != nil {
		return nil, &ConfigError{err}
	}
	m, err := e.manifest(project.Ecosystem)
	if err != nil {
		return nil, err
	}
	res := &FactsResult{Schema: factsSchema, Project: project.ID, Method: q.Method, Path: rel, Line: q.Line, Column: q.Column}
	if m.LSP == nil {
		res.Status, res.Reason = finding.StatusUnsupported, "pack "+m.Name+" declares no language server"
		return res, nil
	}
	languageID, ok := m.LSP.Languages[filepath.Ext(rel)]
	if !ok {
		res.Status, res.Reason = finding.StatusUnsupported, "the "+m.Name+" language server does not handle "+filepath.Ext(rel)+" files"
		return res, nil
	}

	tree, err := e.git.WorkingTree(ctx)
	if err != nil {
		return nil, err
	}
	res.Tree = tree
	workRoot, err := e.materialize(ctx, tree)
	if err != nil {
		return nil, err
	}
	file := filepath.Join(workRoot, filepath.FromSlash(rel))
	if _, err := os.Stat(file); err != nil {
		return nil, &ConfigError{fmt.Errorf("%s is not part of the working snapshot", rel)}
	}
	projectDir := filepath.Join(workRoot, filepath.FromSlash(project.Root))
	repoProject := filepath.Join(q.Repo, filepath.FromSlash(project.Root))

	tools := []string{m.LSP.Tool}
	if rt := m.Tools[m.LSP.Tool].Runtime; rt != "" {
		tools = append(tools, rt)
	}
	tools = append(tools, m.LSP.Uses...)
	var resolved []plugin.ResolvedTool
	for _, name := range tools {
		rt, err := e.toolbox.Resolve(ctx, m, name, repoProject)
		if err != nil {
			res.Status, res.Reason = finding.StatusIncomplete, err.Error()+" (language servers are installed with `pistos plugins install --lsp`)"
			return res, nil
		}
		resolved = append(resolved, rt)
	}
	server := resolved[0]
	res.Backend, res.Version = server.Name, server.Version

	t := task{project: project, tree: tree, Task: finding.Task{Project: project.ID, Capability: "lsp", Side: sideHead}}
	scratch := filepath.Join(e.store.CacheDir(), "scratch", project.ID, "lsp")
	prep, err := e.prepare(t, workRoot, scratch)
	if err != nil {
		return nil, err
	}
	key := fsutil.HashStrings("lsp-facts", fmt.Sprint(factsSchema), server.Identity, project.ID, e.git.ObjectID(ctx, tree, project.Root),
		prep.fingerprint, method, rel, fmt.Sprint(q.Line, ":", q.Column))
	if cached, ok := loadFacts(e.store.CacheDir(), key); ok {
		cached.Cached = true
		return cached, nil
	}

	cmd, settings, err := lspCommand(ctx, e, m, resolved, projectDir, prep.vars)
	if err != nil {
		return nil, err
	}
	timeout := q.LoadTimeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := lsp.Start(qctx, cmd, projectDir, nil, settings)
	if err != nil {
		res.Status, res.Reason = finding.StatusFailed, err.Error()
		return res, nil
	}
	defer client.Kill()
	uri, err := client.Open(file, languageID)
	if err != nil {
		return nil, err
	}
	if err := client.WaitIdle(qctx, 300*time.Millisecond); err != nil {
		res.Status, res.Reason = finding.StatusIncomplete, "language server did not finish loading the workspace: "+err.Error()
		return res, nil
	}
	lines := strings.Split(readText(file), "\n")
	line := ""
	if q.Line >= 1 && q.Line <= len(lines) {
		line = lines[q.Line-1]
	}
	pos := lsp.Position{Line: q.Line - 1, Character: lsp.ByteColumnToCharacter(line, max(q.Column-1, 0), client.Encoding)}

	if q.Method == "diagnostics" {
		d, err := client.Diagnostics(qctx, uri, 10*time.Second)
		if err != nil {
			res.Status, res.Reason = finding.StatusFailed, err.Error()
			return res, nil
		}
		for _, x := range d.Diagnostics {
			lt := ""
			if x.Range.Start.Line < len(lines) {
				lt = lines[x.Range.Start.Line]
			}
			res.Diagnostics = append(res.Diagnostics, FactDiagnostic{
				Line: x.Range.Start.Line + 1, Column: lsp.CharacterToByteColumn(lt, x.Range.Start.Character, client.Encoding) + 1,
				Severity: lspSeverity(x.Severity), Code: strings.Trim(string(x.Code), `"`), Message: x.Message,
			})
		}
		res.Status, res.Reason = finding.StatusComplete, ""
		if !d.Complete {
			res.Status, res.Reason = finding.StatusIncomplete, d.Reason
		}
	} else {
		locs, err := client.Locations(qctx, method, uri, pos)
		switch {
		case errors.Is(err, lsp.ErrUnsupported):
			res.Status, res.Reason = finding.StatusUnsupported, err.Error()
			return res, nil
		case err != nil:
			res.Status, res.Reason = finding.StatusFailed, err.Error()
			return res, nil
		}
		realRoot, _ := filepath.EvalSymlinks(workRoot)
		for _, l := range locs {
			res.Locations = append(res.Locations, mapLocation(l, workRoot, realRoot, client.Encoding))
		}
		sort.Slice(res.Locations, func(i, j int) bool {
			a, b := res.Locations[i], res.Locations[j]
			if a.Path != b.Path {
				return a.Path < b.Path
			}
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return a.Column < b.Column
		})
		res.Status = finding.StatusComplete
	}
	_ = client.Shutdown(ctx)
	if res.Status == finding.StatusComplete {
		saveFacts(e.store.CacheDir(), key, res)
	}
	return res, nil
}

func owningProject(cfg *config.Config, rel string) (finding.Project, error) {
	var best *config.Project
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		root, _ := config.CleanRel(p.Root)
		if (root == "." || rel == root || strings.HasPrefix(rel, root+"/")) && (best == nil || len(root) > len(best.Root)) {
			best = p
		}
	}
	if best == nil {
		return finding.Project{}, fmt.Errorf("%s belongs to no configured project", rel)
	}
	eco, _ := config.NormalizeLanguage(best.Ecosystem)
	root, _ := config.CleanRel(best.Root)
	return finding.Project{ID: best.ID, Root: root, Ecosystem: eco, Checks: best.Checks, Options: best.Options}, nil
}

// lspCommand builds the server command with the same allowlisted environment as analyzers.
func lspCommand(ctx context.Context, e *engine, m *plugin.Manifest, tools []plugin.ResolvedTool, projectDir string, vars map[string]string) (runner.Cmd, map[string]any, error) {
	env := map[string]string{}
	for _, kv := range plugin.BaseEnv(m.EnvPassthrough) {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	var dirs []string
	for _, u := range tools[1:] {
		dirs = append(dirs, filepath.Dir(u.Path))
	}
	if len(dirs) > 0 {
		env["PATH"] = strings.Join(append(dirs, env["PATH"]), string(os.PathListSeparator))
	}
	expand := func(s string) string {
		for k, v := range vars {
			s = strings.ReplaceAll(s, "{var:"+k+"}", v)
		}
		return s
	}
	for k, v := range m.LSP.Env {
		env[k] = expand(v)
	}
	server := tools[0]
	if m.Tools[server.Name].DotnetRoot {
		root, err := e.toolbox.DotnetRoot(ctx, m, projectDir)
		if err != nil {
			return runner.Cmd{}, nil, err
		}
		env["DOTNET_ROOT"] = root
	}
	var envList []string
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}
	sort.Strings(envList)
	path, args := server.Path, append([]string{}, m.LSP.Args...)
	if rt := m.Tools[server.Name].Runtime; rt != "" {
		for _, u := range tools[1:] {
			if u.Name == rt {
				path, args = u.Path, append([]string{server.Path}, args...)
			}
		}
	}
	settings := map[string]any{}
	for section, values := range m.LSP.Settings {
		sec := map[string]string{}
		for k, v := range values {
			if x := expand(v); !strings.Contains(x, "{var:") && x != "" {
				sec[k] = x
			}
		}
		settings[section] = sec
	}
	return runner.Cmd{Path: path, Args: args, Dir: projectDir, Env: envList}, settings, nil
}

func mapLocation(l lsp.Location, workRoot, realRoot, encoding string) FactLocation {
	p, ok := lsp.URIToPath(l.URI)
	if !ok {
		return FactLocation{Path: l.URI, Line: l.Range.Start.Line + 1, Column: l.Range.Start.Character + 1, External: true}
	}
	lines := strings.Split(readText(p), "\n")
	lt := ""
	if l.Range.Start.Line < len(lines) {
		lt = lines[l.Range.Start.Line]
	}
	col := lsp.CharacterToByteColumn(lt, l.Range.Start.Character, encoding) + 1
	if rel, ok := repoRelative(p, workRoot, realRoot, workRoot); ok {
		return FactLocation{Path: rel, Line: l.Range.Start.Line + 1, Column: col}
	}
	return FactLocation{Path: filepath.ToSlash(p), Line: l.Range.Start.Line + 1, Column: col, External: true}
}

func lspSeverity(s int) string {
	switch s {
	case 1:
		return "error"
	case 2:
		return "warning"
	}
	return "info"
}

func readText(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

func factsPath(cacheDir, key string) string {
	return filepath.Join(cacheDir, "facts", key[:2], key+".json")
}

func loadFacts(cacheDir, key string) (*FactsResult, bool) {
	b, err := os.ReadFile(factsPath(cacheDir, key))
	if err != nil {
		return nil, false
	}
	var r FactsResult
	if json.Unmarshal(b, &r) != nil || r.Schema != factsSchema || r.Status != finding.StatusComplete {
		return nil, false
	}
	return &r, true
}

func saveFacts(cacheDir, key string, r *FactsResult) {
	if b, err := json.Marshal(r); err == nil {
		_ = fsutil.WriteFileAtomic(factsPath(cacheDir, key), b, 0o644)
	}
}
