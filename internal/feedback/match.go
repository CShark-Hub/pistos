package feedback

import (
	"fmt"
	"strings"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/fsutil"
)

// Environment answers questions about the reviewed snapshot that matching depends on.
type Environment struct {
	// Renamed reports whether a verified rename moved oldPath (as of the decision's origin) to newPath.
	Renamed func(d *Decision, newPath string) bool
	// IsTestPath classifies a repository-relative path within a project using its pack's test globs.
	IsTestPath func(project, path string) bool
}

type Matcher struct {
	file *File
	env  Environment
}

func NewMatcher(f *File, env Environment) *Matcher {
	if env.Renamed == nil {
		env.Renamed = func(*Decision, string) bool { return false }
	}
	if env.IsTestPath == nil {
		env.IsTestPath = func(string, string) bool { return false }
	}
	return &Matcher{file: f, env: env}
}

// Apply sets each issue's disposition. Strong identity matches apply decisions; weaker evidence only
// asks for revalidation and never hides an issue.
func (m *Matcher) Apply(issues []finding.Issue) {
	used := map[string]bool{}
	pending := map[int]bool{}
	for i := range issues {
		is := &issues[i]
		if d, via := m.strong(is); d != nil {
			used[d.ID] = true
			m.applyDecision(is, d, via)
			continue
		}
		if d := m.pattern(is); d != nil {
			used[d.ID] = true
			m.applyDecision(is, d, "pattern")
			continue
		}
		pending[i] = true
	}
	for i := range pending {
		is := &issues[i]
		if d := m.weak(is, used); d != nil {
			is.Disposition = finding.Disposition{
				State: finding.DispRevalidate, Decision: d.ID, Tags: d.Tags,
				Note: fmt.Sprintf("code changed since decision %s [%s]; the decision no longer applies automatically — re-tag if it still holds", d.ID, strings.Join(d.Tags, ",")),
			}
		}
	}
}

func (m *Matcher) strong(is *finding.Issue) (*Decision, string) {
	for i := range m.file.Decisions {
		d := &m.file.Decisions[i]
		if d.Scope != ScopeIssue {
			continue
		}
		if d.Subject == is.Key {
			return d, "identity"
		}
		mt := d.Match
		if mt.Project == is.Project && m.familyMatches(mt, is) && mt.Anchor == is.Anchor.TextHash &&
			mt.Symbol == is.Anchor.Symbol && mt.Ordinal == is.Anchor.Ordinal && mt.Path != is.Location.Path &&
			m.env.Renamed(d, is.Location.Path) {
			return d, "verified rename from " + mt.Path
		}
	}
	return nil, ""
}

func (m *Matcher) familyMatches(mt *Match, is *finding.Issue) bool {
	if mt.Family == is.Family {
		return true
	}
	for _, s := range is.Sources {
		for _, a := range mt.Aliases {
			if a == s.Tool+"/"+s.Rule {
				return true
			}
		}
	}
	return false
}

func (m *Matcher) pattern(is *finding.Issue) *Decision {
	for i := range m.file.Decisions {
		d := &m.file.Decisions[i]
		if d.Scope != ScopePattern || (d.Pattern.Project != "" && d.Pattern.Project != is.Project) {
			continue
		}
		if !PatternFamilyMatches(d.Pattern.Family, is) {
			continue
		}
		if len(d.Pattern.Paths) == 0 {
			return d
		}
		for _, g := range d.Pattern.Paths {
			if fsutil.MatchGlob(g, is.Location.Path) {
				return d
			}
		}
	}
	return nil
}

// PatternFamilyMatches accepts the canonical family or any source rule ("tool/rule").
func PatternFamilyMatches(family string, is *finding.Issue) bool {
	if family == is.Family {
		return true
	}
	for _, s := range is.Sources {
		if family == s.Tool+"/"+s.Rule {
			return true
		}
	}
	return false
}

// weak finds an unused suppressing decision for the same cause location whose code evidence changed.
func (m *Matcher) weak(is *finding.Issue, used map[string]bool) *Decision {
	for i := range m.file.Decisions {
		d := &m.file.Decisions[i]
		if d.Scope != ScopeIssue || used[d.ID] || !d.Suppresses() {
			continue
		}
		mt := d.Match
		samePlace := mt.Path == is.Location.Path || m.env.Renamed(d, is.Location.Path)
		if mt.Project == is.Project && m.familyMatches(mt, is) && samePlace && mt.Symbol == is.Anchor.Symbol && mt.Anchor != is.Anchor.TextHash {
			used[d.ID] = true
			return d
		}
	}
	return nil
}

func (m *Matcher) applyDecision(is *finding.Issue, d *Decision, via string) {
	disp := finding.Disposition{Decision: d.ID, Tags: d.Tags}
	if d.Guards != nil && d.Guards.TestPath && !m.env.IsTestPath(is.Project, is.Location.Path) {
		disp.State = finding.DispRevalidate
		disp.Note = fmt.Sprintf("decision %s assumed test-only code, but %s is no longer test code; revalidate", d.ID, is.Location.Path)
		is.Disposition = disp
		return
	}
	switch {
	case d.Suppresses():
		disp.State = finding.DispSuppressed
	case d.HasTag(TagDeferred):
		disp.State = finding.DispDeferred
	default:
		disp.State = finding.DispActive
	}
	if via != "identity" {
		disp.Note = "matched by " + via + " (" + d.ID + ")"
	}
	is.Disposition = disp
}
