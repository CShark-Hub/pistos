package review

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"example.com/pistos/internal/ai"
	"example.com/pistos/internal/feedback"
	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/snapshot"
)

var sourceExtensions = map[string]bool{
	".go": true, ".py": true, ".pyi": true, ".ts": true, ".tsx": true, ".mts": true, ".cts": true,
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".cs": true,
}

// runAI captures one evidence snapshot (changed files of analyzed projects, or every source file
// for --all) and hands the same snapshot to every discovery pass.
func (e *engine) runAI(ctx context.Context, s finding.Scope, changes []snapshot.Change, projects []finding.Project, issues []finding.Issue, fb *feedback.File) (ai.Output, error) {
	workRoot, err := e.materialize(ctx, s.HeadTree)
	if err != nil {
		return ai.Output{}, err
	}
	ownerOf := func(p string) (finding.Project, bool) {
		best, ok := finding.Project{}, false
		for _, pr := range projects {
			if (pr.Root == "." || strings.HasPrefix(p, pr.Root+"/")) && (!ok || len(pr.Root) > len(best.Root)) {
				best, ok = pr, true
			}
		}
		return best, ok
	}
	var paths []string
	if s.Mode == "all" {
		for p := range e.treeFiles(ctx, s.HeadTree) {
			if sourceExtensions[path.Ext(p)] {
				paths = append(paths, p)
			}
		}
	} else {
		for _, c := range changes {
			if c.Status != "D" {
				paths = append(paths, c.Path)
			}
		}
	}
	sortStrings(paths)

	in := ai.Input{Repo: e.opts.Repo, Config: e.cfg, Tree: s.HeadTree, Refresh: e.opts.RefreshAI, Offline: e.opts.Offline}
	budget := e.cfg.AI.Budget.MaxEvidenceChars
	used := 0
	for _, p := range paths {
		pr, ok := ownerOf(p)
		// Pistos' own files (decisions, config) are policy, not code under review.
		if !ok || strings.HasPrefix(p, ".pistos/") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(workRoot, filepath.FromSlash(p)))
		if err != nil || bytes.IndexByte(b, 0) >= 0 {
			continue
		}
		if used+len(b) > budget {
			in.Omitted = append(in.Omitted, p)
			continue
		}
		used += len(b)
		in.Files = append(in.Files, ai.File{Path: p, Project: pr.ID, Lines: strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")})
	}
	for _, is := range issues {
		if is.Change != ChangeExisting {
			in.Deterministic = append(in.Deterministic, is.Finding)
		}
	}
	for _, d := range fb.Decisions {
		if !d.Suppresses() {
			continue
		}
		dd := ai.Decision{Tags: d.Tags, Reason: d.Reason}
		if d.Scope == feedback.ScopePattern {
			dd.Family, dd.Path = d.Pattern.Family, strings.Join(d.Pattern.Paths, ",")
		} else {
			dd.Family, dd.Path, dd.Symbol = d.Match.Family, d.Match.Path, d.Match.Symbol
		}
		in.Decisions = append(in.Decisions, dd)
	}
	in.Symbol = func(p string, line int) string {
		pr, _ := ownerOf(p)
		abs := filepath.Join(workRoot, filepath.FromSlash(p))
		return e.sources.get(abs, pr.Ecosystem, func() ([]byte, error) { return os.ReadFile(abs) }).enclosing(line)
	}
	return ai.Run(ctx, in), nil
}

func aiSummary(r *finding.AIReport) string {
	done := 0
	var failed []string
	for _, p := range r.Passes {
		if p.Status == finding.StatusComplete {
			done++
		} else {
			failed = append(failed, fmt.Sprintf("%s %s: %s", p.ID, p.Status, p.Error))
		}
	}
	s := fmt.Sprintf("%s: %d/%d discovery passes, triage %s (%s)", r.Status, done, len(r.Passes), r.Triage.Status, r.Triage.Model)
	if r.Triage.Error != "" {
		failed = append(failed, "triage: "+r.Triage.Error)
	}
	if len(failed) > 0 {
		s += "; degraded — " + strings.Join(failed, "; ")
	}
	return s + "; advisory only"
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
