// Package finding defines the versioned result models shared by every review stage.
package finding

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"example.com/pistos/internal/fsutil"
)

const (
	SchemaVersion = 1
	// KeyVersion changes whenever the identity inputs change, so old keys are never silently reinterpreted.
	KeyVersion = "k1"
)

type Severity string

const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
	SevInfo    Severity = "info"
)

func (s Severity) Rank() int {
	switch s {
	case SevError:
		return 3
	case SevWarning:
		return 2
	case SevInfo:
		return 1
	}
	return 0
}

type Lane string

const (
	LaneDeterministic Lane = "deterministic"
	LaneAI            Lane = "ai"
)

type Status string

const (
	StatusComplete       Status = "complete"
	StatusCachedComplete Status = "cached-complete"
	StatusUnsupported    Status = "unsupported"
	StatusFailed         Status = "failed"
	StatusIncomplete     Status = "incomplete"
	StatusNotRequested   Status = "not-requested"
	// StatusSkipped marks an AI call the per-review spend cap did not allow; never a clean result.
	StatusSkipped Status = "skipped"
)

func (s Status) Completed() bool { return s == StatusComplete || s == StatusCachedComplete }

type Location struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
}

// Source is one tool or AI pass that reported the finding, kept verbatim for audit.
type Source struct {
	Tool     string   `json:"tool"`
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// Anchor is the code evidence identity is built from; it never includes line numbers.
type Anchor struct {
	Symbol string `json:"symbol,omitempty"`
	// TextHash is a hash of the whitespace-normalized source line at the primary location.
	TextHash string `json:"text_hash"`
	// Ordinal separates identical anchors within the same symbol and family.
	Ordinal int `json:"ordinal"`
}

type Finding struct {
	Key      string   `json:"key"`
	Lane     Lane     `json:"lane"`
	Project  string   `json:"project"`
	Family   string   `json:"family"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Location Location `json:"location"`
	Anchor   Anchor   `json:"anchor"`
	Sources  []Source `json:"sources"`
	// EvidenceHash changes with the current evidence while Key stays stable.
	EvidenceHash string   `json:"evidence_hash"`
	Capability   string   `json:"capability"`
	Reproduce    []string `json:"reproduce,omitempty"`
	// Advisory findings are reported but never block the gate (e.g. metrics without a gate rule).
	Advisory bool `json:"advisory,omitempty"`
	// Sensitive findings point at a possible secret: reports and AI evidence withhold the flagged
	// lines. It is not part of the identity.
	Sensitive bool          `json:"sensitive,omitempty"`
	Metric    *MetricValue  `json:"metric,omitempty"`
	AI        *AIProvenance `json:"ai,omitempty"`
}

// AIProvenance records how an AI issue was found and assessed. Votes are provenance, not confidence.
type AIProvenance struct {
	Outcome         string   `json:"outcome"`
	Cluster         string   `json:"cluster"`
	Passes          []string `json:"passes"`
	Models          []string `json:"models"`
	Assumptions     []string `json:"assumptions,omitempty"`
	Impact          string   `json:"impact,omitempty"`
	FailingScenario string   `json:"failing_scenario,omitempty"`
	TriageReason    string   `json:"triage_reason,omitempty"`
	// Carried explains why an issue is present without being rediscovered in this run.
	Carried string `json:"carried,omitempty"`
}

// MetricValue records a measured value so baseline comparison can detect worsening, not just presence.
type MetricValue struct {
	Name      string `json:"name"`
	Value     int    `json:"value"`
	Threshold int    `json:"threshold"`
	Base      *int   `json:"base,omitempty"`
	Algorithm string `json:"algorithm"`
}

var spaceRun = regexp.MustCompile(`\s+`)

// NormalizeAnchorText makes anchors robust to indentation and spacing-only reformatting.
func NormalizeAnchorText(line string) string {
	return strings.TrimSpace(spaceRun.ReplaceAllString(line, " "))
}

func AnchorHash(line string) string {
	return fsutil.HashStrings("anchor", NormalizeAnchorText(line))[:16]
}

func ComputeKey(project, family, path, symbol, textHash string, ordinal int) string {
	h := fsutil.HashStrings(KeyVersion, project, family, path, symbol, textHash, fmt.Sprint(ordinal))
	return KeyVersion + "-" + h[:20]
}

// AssignIdentity sets ordinals and keys for findings that share project, family, path, symbol and anchor text.
// Ordinals follow source position so identity stays independent of tool output order.
func AssignIdentity(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool { return lessPosition(fs[i], fs[j]) })
	groups := map[string]int{}
	for i := range fs {
		f := &fs[i]
		g := strings.Join([]string{f.Project, f.Family, f.Location.Path, f.Anchor.Symbol, f.Anchor.TextHash}, "\x00")
		f.Anchor.Ordinal = groups[g]
		groups[g]++
		f.Key = ComputeKey(f.Project, f.Family, f.Location.Path, f.Anchor.Symbol, f.Anchor.TextHash, f.Anchor.Ordinal)
	}
}

func (f *Finding) RefreshEvidenceHash() {
	parts := []string{f.Message, string(f.Severity), fmt.Sprint(f.Location.Line, ":", f.Location.Column)}
	for _, s := range f.Sources {
		parts = append(parts, s.Tool, s.Rule, string(s.Severity), s.Message)
	}
	f.EvidenceHash = fsutil.HashStrings(parts...)[:20]
}

func lessPosition(a, b Finding) bool {
	if a.Project != b.Project {
		return a.Project < b.Project
	}
	if a.Location.Path != b.Location.Path {
		return a.Location.Path < b.Location.Path
	}
	if a.Location.Line != b.Location.Line {
		return a.Location.Line < b.Location.Line
	}
	if a.Location.Column != b.Location.Column {
		return a.Location.Column < b.Location.Column
	}
	if a.Family != b.Family {
		return a.Family < b.Family
	}
	return a.Message < b.Message
}

// Merge combines findings with the same key; authority ranks tools so the most trusted source leads,
// while every other source stays visible.
func Merge(fs []Finding, authority []string) []Finding {
	rank := func(tool string) int {
		for i, t := range authority {
			if t == tool {
				return i
			}
		}
		return len(authority)
	}
	byKey := map[string]int{}
	var out []Finding
	for _, f := range fs {
		i, ok := byKey[f.Key]
		if !ok {
			byKey[f.Key] = len(out)
			out = append(out, f)
			continue
		}
		cur := &out[i]
		cur.Sources = append(cur.Sources, f.Sources...)
		cur.Sensitive = cur.Sensitive || f.Sensitive
		if rank(f.Sources[0].Tool) < rank(cur.Sources[0].Tool) {
			cur.Message, cur.Capability = f.Message, f.Capability
		}
		// The gate uses the strongest claim; the weaker one stays visible in Sources.
		if f.Severity.Rank() > cur.Severity.Rank() {
			cur.Severity = f.Severity
		}
	}
	for i := range out {
		sort.SliceStable(out[i].Sources, func(a, b int) bool {
			sa, sb := out[i].Sources[a], out[i].Sources[b]
			if rank(sa.Tool) != rank(sb.Tool) {
				return rank(sa.Tool) < rank(sb.Tool)
			}
			return sa.Tool+sa.Rule+sa.Message < sb.Tool+sb.Rule+sb.Message
		})
		out[i].RefreshEvidenceHash()
	}
	Sort(out)
	return out
}

// Sort orders findings canonically: severity first, then position, then key as the final tie-break.
func Sort(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool { return CanonicalLess(fs[i], fs[j]) })
}

func CanonicalLess(a, b Finding) bool {
	if a.Severity.Rank() != b.Severity.Rank() {
		return a.Severity.Rank() > b.Severity.Rank()
	}
	if lessPosition(a, b) != lessPosition(b, a) {
		return lessPosition(a, b)
	}
	return a.Key < b.Key
}
