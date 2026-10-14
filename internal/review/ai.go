package review

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"example.com/pistos/internal/ai"
	"example.com/pistos/internal/config"
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
			// The repository-wide secret scan owns no code: files only it covers (.env, CI
			// configuration) are not sent as evidence.
			if pr.Ecosystem == config.Secrets {
				continue
			}
			if projectFilter(pr).Contains(p) && (!ok || len(pr.Root) > len(best.Root)) {
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
	withheld := finding.SensitiveLines(issues)
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
		// Lines flagged as possible secrets never leave the machine, whatever the issue's state.
		lines := finding.WithholdLines(strings.Split(strings.TrimSuffix(string(b), "\n"), "\n"), withheld[p], func(family string) string {
			return "[line withheld by pistos: possible secret (" + family + ")]"
		})
		in.Files = append(in.Files, ai.File{Path: p, Project: pr.ID, Lines: lines})
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
	in.Guidelines, in.Notes = e.loadGuidelines(ctx, s, in.Files)
	in.Symbol = func(p string, line int) string {
		pr, _ := ownerOf(p)
		abs := filepath.Join(workRoot, filepath.FromSlash(p))
		return e.sources.get(abs, pr.Ecosystem, func() ([]byte, error) { return os.ReadFile(abs) }).enclosing(line)
	}
	return ai.Run(ctx, in), nil
}

// loadGuidelines reads the review guidelines that apply to this evidence from git: from the reviewed
// head tree, or from the baseline under --feedback-from-base, so an untrusted change cannot rewrite
// its own review instructions. Without [[ai.instructions]], .pistos/review.md is used if present.
// The total is capped at ai.MaxGuidelineChars, truncating in configuration order.
func (e *engine) loadGuidelines(ctx context.Context, s finding.Scope, files []ai.File) ([]ai.Guideline, []string) {
	tree, side := s.HeadTree, sideHead
	if e.opts.FeedbackFromBase {
		tree, side = s.BaseTree, sideBase
	}
	entries := e.cfg.AI.Instructions
	explicit := len(entries) > 0
	if !explicit {
		entries = []config.AIInstruction{{File: config.DefaultInstructions}}
	}
	var out []ai.Guideline
	var notes []string
	used := map[string]bool{}
	left := ai.MaxGuidelineChars
	for _, in := range entries {
		if used[in.File] || !guidelineApplies(in, files) {
			continue
		}
		blob := e.git.ObjectID(ctx, tree, in.File)
		if blob == "" {
			if explicit {
				notes = append(notes, fmt.Sprintf("review guideline %s is not in the %s tree; not used", in.File, side))
			}
			continue
		}
		b, err := e.git.Show(ctx, tree, in.File)
		if err != nil || bytes.IndexByte(b, 0) >= 0 {
			notes = append(notes, fmt.Sprintf("review guideline %s is not a text file in the %s tree; not used", in.File, side))
			continue
		}
		used[in.File] = true
		g := ai.Guideline{Record: finding.AIGuideline{File: in.File, Side: side, Tree: tree, Blob: blob, Bytes: len(b)}, Text: string(b)}
		if len(g.Text) > left {
			g.Text = truncateUTF8(g.Text, left) + "\n[truncated by pistos]"
			g.Record.Truncated = true
			notes = append(notes, fmt.Sprintf("review guidelines are capped at %d characters in total: %s was truncated to %d of %d", ai.MaxGuidelineChars, in.File, left, len(b)))
			left = 0
		} else {
			left -= len(g.Text)
		}
		out = append(out, g)
	}
	return out, notes
}

// guidelineApplies: an entry without paths always applies; otherwise one of its globs (relative to
// the repository root) must match an evidence file.
func guidelineApplies(in config.AIInstruction, files []ai.File) bool {
	if len(in.Paths) == 0 {
		return true
	}
	filter := config.PathFilter{Root: ".", Include: in.Paths}
	for _, f := range files {
		if filter.Contains(f.Path) {
			return true
		}
	}
	return false
}

// truncateUTF8 cuts s to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// ownedByAny keeps AI findings to files some analyzed project still claims after its path filters.
func ownedByAny(projects []finding.Project, p string) bool {
	for _, pr := range projects {
		if projectFilter(pr).Contains(p) {
			return true
		}
	}
	return false
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
