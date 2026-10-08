package review

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"example.com/pistos/internal/config"
	"example.com/pistos/internal/feedback"
	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/snapshot"
	"example.com/pistos/internal/store"
)

// Ref is a resolved issue reference: the issue as last published plus the tree it was seen in.
type Ref struct {
	Number int
	Issue  finding.Issue
	Tree   string
	Commit string
}

// ResolveRef resolves a short number against the current report, or a canonical key against the
// current report and then the ledger. Missing or ambiguous references are errors, never guesses.
func ResolveRef(repo, ref string) (*Ref, error) {
	st := store.Store{Repo: repo}
	env, curErr := st.LoadCurrent()
	if n, err := strconv.Atoi(strings.TrimPrefix(ref, "#")); err == nil {
		if curErr != nil {
			return nil, curErr
		}
		var hits []finding.Issue
		for _, is := range env.Canonical.Issues {
			if env.Numbers[is.Key] == n {
				hits = append(hits, is)
			}
		}
		switch len(hits) {
		case 0:
			return nil, fmt.Errorf("#%d is not in the current report; run `pistos review` or use a canonical key", n)
		case 1:
			return &Ref{Number: n, Issue: hits[0], Tree: env.Canonical.Scope.HeadTree, Commit: headCommitOf(env)}, nil
		default:
			return nil, fmt.Errorf("#%d is ambiguous in the current report", n)
		}
	}
	if !strings.HasPrefix(ref, finding.KeyVersion+"-") {
		return nil, fmt.Errorf("%q is neither an issue number nor a canonical key (%s-…)", ref, finding.KeyVersion)
	}
	if curErr == nil {
		for _, is := range env.Canonical.Issues {
			if is.Key == ref {
				return &Ref{Number: env.Numbers[is.Key], Issue: is, Tree: env.Canonical.Scope.HeadTree, Commit: headCommitOf(env)}, nil
			}
		}
	}
	l := st.LoadLedger()
	if e, ok := l.Seen[ref]; ok {
		return &Ref{Number: e.Number, Issue: finding.Issue{Finding: e.Finding}, Tree: e.Tree}, nil
	}
	return nil, fmt.Errorf("unknown issue key %s", ref)
}

// headCommitOf names a reachable commit near the reviewed tree, used when the tree object itself has
// been garbage-collected.
func headCommitOf(env *finding.Envelope) string {
	s := env.Canonical.Scope
	if s.HeadCommit != "" {
		return s.HeadCommit
	}
	if s.BaseRef == "HEAD" {
		return s.BaseCommit
	}
	return ""
}

type TagOptions struct {
	Tags   []string
	Reason string
	// PatternPaths, when non-nil, widens the decision to every finding of the family under these globs.
	PatternPaths []string
	Pattern      bool
	// Notices receives explanations of automatic choices made while tagging.
	Notices func(string)
}

// Tag records a decision for the referenced issue in feedback.yaml and refilters the current report.
func Tag(ctx context.Context, repo, ref string, o TagOptions) (*feedback.Decision, *finding.Envelope, error) {
	if len(o.Tags) == 0 {
		return nil, nil, errors.New("at least one tag is required")
	}
	for _, t := range o.Tags {
		if !feedback.ValidTag(t) {
			return nil, nil, fmt.Errorf("invalid tag %q: use lowercase letters, digits and dashes", t)
		}
	}
	notice := o.Notices
	if notice == nil {
		notice = func(string) {}
	}
	r, err := ResolveRef(repo, ref)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := config.Load(repo)
	if err != nil {
		return nil, nil, err
	}
	g := snapshot.Git{Repo: repo}
	env := MatchEnvironment(ctx, g, cfg, r.Tree)

	var result feedback.Decision
	_, err = feedback.Update(ctx, repo, func(f *feedback.File) error {
		d := existingDecision(f, r.Issue, o.Pattern)
		if d == nil {
			nd := newDecision(ctx, g, r, o)
			f.Decisions = append(f.Decisions, nd)
			d = &f.Decisions[len(f.Decisions)-1]
		} else {
			d.Version++
		}
		d.AddTags(o.Tags...)
		if o.Reason != "" {
			d.Reason = o.Reason
		}
		if d.Scope == feedback.ScopeIssue && d.HasTag(feedback.TagTestOnly) && (d.Guards == nil || !d.Guards.TestPath) {
			if env.IsTestPath(r.Issue.Project, r.Issue.Location.Path) {
				d.Guards = &feedback.Guards{TestPath: true}
			} else {
				notice(fmt.Sprintf("%s is not recognized as test code by the %s pack; test-only recorded as a label without a path guard", r.Issue.Location.Path, r.Issue.Project))
			}
		}
		result = *d
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	refreshed, err := Refilter(ctx, repo)
	if errors.Is(err, store.ErrNoCurrentReport) {
		return &result, nil, nil
	}
	return &result, refreshed, err
}

func existingDecision(f *feedback.File, is finding.Issue, pattern bool) *feedback.Decision {
	for i := range f.Decisions {
		d := &f.Decisions[i]
		if pattern {
			continue
		}
		if d.Scope == feedback.ScopeIssue && (d.Subject == is.Key || d.ID == is.Disposition.Decision) {
			return d
		}
	}
	return nil
}

func newDecision(ctx context.Context, g snapshot.Git, r *Ref, o TagOptions) feedback.Decision {
	is := r.Issue
	if o.Pattern {
		p := &feedback.Pattern{Project: is.Project, Family: is.Family, Paths: o.PatternPaths}
		return feedback.Decision{
			ID: feedback.NewID("pattern\x00" + p.Project + "\x00" + p.Family + "\x00" + strings.Join(p.Paths, "\x00")), Scope: feedback.ScopePattern, Pattern: p, Version: 1,
		}
	}
	var aliases []string
	for _, s := range is.Sources {
		if a := s.Tool + "/" + s.Rule; a != is.Family {
			aliases = append(aliases, a)
		}
	}
	sort.Strings(aliases)
	return feedback.Decision{
		ID: feedback.NewID(is.Key), Scope: feedback.ScopeIssue, Subject: is.Key, Version: 1,
		Match: &feedback.Match{
			Project: is.Project, Path: is.Location.Path, Symbol: is.Anchor.Symbol, Family: is.Family,
			Anchor: is.Anchor.TextHash, Ordinal: is.Anchor.Ordinal, Aliases: aliases,
			FileBlob: g.ObjectID(ctx, r.Tree, is.Location.Path),
		},
		Origin: &feedback.Origin{Tree: r.Tree, Commit: r.Commit},
	}
}

// Reopen removes the suppressing and deferral tags of the decision applied to an issue.
func Reopen(ctx context.Context, repo, ref string) (*finding.Envelope, error) {
	r, err := ResolveRef(repo, ref)
	if err != nil {
		return nil, err
	}
	_, err = feedback.Update(ctx, repo, func(f *feedback.File) error {
		i, d := -1, (*feedback.Decision)(nil)
		for j := range f.Decisions {
			c := &f.Decisions[j]
			if c.ID == r.Issue.Disposition.Decision || (c.Scope == feedback.ScopeIssue && c.Subject == r.Issue.Key) {
				i, d = j, c
				break
			}
		}
		if d == nil {
			return fmt.Errorf("no decision applies to %s", ref)
		}
		if d.Scope == feedback.ScopePattern {
			return fmt.Errorf("%s is covered by pattern decision %s (%s); edit it with `pistos feedback`", ref, d.ID, d.Describe())
		}
		d.RemoveTags(func(t string) bool { return feedback.Suppressing(t) || t == feedback.TagDeferred })
		d.Version++
		if len(d.Tags) == 0 {
			f.Decisions = append(f.Decisions[:i], f.Decisions[i+1:]...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return Refilter(ctx, repo)
}

// PatternPreview lists issues in the current report that a pattern decision would cover.
func PatternPreview(repo string, p feedback.Pattern) ([]finding.Issue, map[string]int, error) {
	env, err := store.Store{Repo: repo}.LoadCurrent()
	if err != nil {
		return nil, nil, err
	}
	f := &feedback.File{Schema: feedback.SchemaVersion, Decisions: []feedback.Decision{{
		ID: "d-preview00", Tags: []string{feedback.TagIrrelevant}, Scope: feedback.ScopePattern, Pattern: &p, Version: 1,
	}}}
	issues := append([]finding.Issue(nil), env.Canonical.Issues...)
	feedback.NewMatcher(f, feedback.Environment{}).Apply(issues)
	var out []finding.Issue
	for _, is := range issues {
		if is.Disposition.Decision == "d-preview00" {
			out = append(out, is)
		}
	}
	return out, env.Numbers, nil
}
