package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"example.com/pistos/internal/finding"
)

// AgentOptions controls the compact output for coding agents.
type AgentOptions struct {
	// MaxIssues caps the listed issues (default 20); the rest are counted and pointed to ReportPath.
	MaxIssues int
	// Source reads a file as it was in the reviewed tree; without it issues carry no excerpt.
	Source func(path string) ([]byte, error)
	// ReportPath is the browsable report the overflow line points to ("" when none was written).
	ReportPath string
}

// agentContext is the number of lines shown around an issue in agent output.
const (
	agentContext  = 2
	agentMaxRange = 5
	agentMaxLine  = 160
)

// Agent renders what a coding agent needs to act on: only issues the change introduced (new, unknown,
// worsened, or gating again because a decision no longer holds), blocking first, with short excerpts
// and the command that reproduces each. Output is deterministic for a given envelope.
func Agent(w io.Writer, env *finding.Envelope, o AgentOptions) {
	if o.MaxIssues <= 0 {
		o.MaxIssues = 20
	}
	r := env.Canonical
	var issues []finding.Issue
	for _, is := range r.Issues {
		if is.Disposition.State == finding.DispSuppressed || (is.Change == "existing" && !is.Blocking) {
			continue
		}
		issues = append(issues, is)
	}
	sort.SliceStable(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		if a.Blocking != b.Blocking {
			return a.Blocking
		}
		if a.Severity.Rank() != b.Severity.Rank() {
			return a.Severity.Rank() > b.Severity.Rank()
		}
		if a.Location.Path != b.Location.Path {
			return a.Location.Path < b.Location.Path
		}
		if a.Location.Line != b.Location.Line {
			return a.Location.Line < b.Location.Line
		}
		return env.Numbers[a.Key] < env.Numbers[b.Key]
	})
	blocking := 0
	for _, is := range issues {
		if is.Blocking {
			blocking++
		}
	}

	outcome := strings.ToUpper(r.Verdict.Outcome)
	if r.Verdict.Outcome == "blocking" {
		outcome = "FAIL"
	}
	scope := agentScope(r.Scope)
	if len(issues) == 0 && r.Verdict.Outcome == "pass" {
		fmt.Fprintf(w, "pistos: PASS — %s (%s)\n", r.Verdict.Reason, scope)
		return
	}
	fmt.Fprintf(w, "pistos: %s — %d blocking, %d advisory (%s)\n", outcome, blocking, len(issues)-blocking, scope)

	shown := issues
	if len(shown) > o.MaxIssues {
		shown = shown[:o.MaxIssues]
	}
	src := sourceLines(o.Source, finding.SensitiveLines(r.Issues))
	section := ""
	for _, is := range shown {
		if is.Blocking && section != "blocking" {
			section = "blocking"
			fmt.Fprintln(w, "BLOCKING — fix before finishing")
		} else if !is.Blocking && section != "advisory" {
			section = "advisory"
			heading := "ADVISORY — not blocking"
			for _, a := range shown {
				if a.AI != nil && !a.Blocking {
					heading += "; AI items are hypotheses, confirm before changing code"
					break
				}
			}
			fmt.Fprintln(w, heading)
		}
		agentIssue(w, env.Numbers[is.Key], is, r.AI, src)
	}
	if n := len(issues) - len(shown); n > 0 {
		if o.ReportPath != "" {
			fmt.Fprintf(w, "+%d more → %s\n", n, o.ReportPath)
		} else {
			fmt.Fprintf(w, "+%d more (pistos review --format text lists all)\n", n)
		}
	}
	agentIncomplete(w, r)
	if len(issues) > 0 {
		fmt.Fprintln(w, "Do not edit .pistos/ or tag issues. If you think an issue is wrong, tell the user its number.")
	}
}

func agentScope(s finding.Scope) string {
	head := s.HeadRef
	switch s.Mode {
	case "all":
		return "full review of " + s.HeadRef
	case "working":
		head = "working tree"
	case "staged":
		head = "staged changes"
	}
	base := s.BaseRef
	if s.BaseRef != "" && s.BaseRef != "HEAD" && s.BaseCommit != "" {
		base = "merge-base(" + s.BaseRef + ")"
	}
	return head + " vs " + base
}

func agentIssue(w io.Writer, n int, is finding.Issue, ai *finding.AIReport, src func(string) []string) {
	loc := is.Location.Path
	if is.Location.Line > 0 {
		loc = fmt.Sprintf("%s:%d", loc, is.Location.Line)
		if is.Location.Column > 0 {
			loc += fmt.Sprintf(":%d", is.Location.Column)
		}
	}
	var notes []string
	if is.Change == "worsened" || is.Change == "unknown" {
		notes = append(notes, is.Change)
	}
	if is.Disposition.State != finding.DispActive && is.Disposition.State != "" {
		notes = append(notes, is.Disposition.State)
	}
	if is.AI != nil {
		total := len(is.AI.Passes)
		if ai != nil && len(ai.Passes) > total {
			total = len(ai.Passes)
		}
		notes = append(notes, fmt.Sprintf("AI, %d/%d passes, triage: %s", len(is.AI.Passes), total, is.AI.Outcome))
	}
	extra := ""
	if len(notes) > 0 {
		extra = "  (" + strings.Join(notes, "; ") + ")"
	}
	fmt.Fprintf(w, "#%d %s %s  %s%s\n", n, is.Severity, is.Family, loc, extra)
	msg := is.Message
	if is.Metric != nil && is.Metric.Base != nil && *is.Metric.Base != is.Metric.Value {
		msg += fmt.Sprintf(" (was %d)", *is.Metric.Base)
	}
	fmt.Fprintf(w, "   %s\n", oneLine(msg))
	if is.AI != nil {
		if is.AI.FailingScenario != "" {
			fmt.Fprintf(w, "   failing scenario: %s\n", oneLine(is.AI.FailingScenario))
		} else if is.AI.Impact != "" {
			fmt.Fprintf(w, "   impact: %s\n", oneLine(is.AI.Impact))
		}
	}
	if lines := src(is.Location.Path); is.Location.Line > 0 && is.Location.Line <= len(lines) {
		first := is.Location.Line
		last := min(max(first, is.Location.EndLine), first+agentMaxRange-1, len(lines))
		from, to := max(1, first-agentContext), min(len(lines), last+agentContext)
		width := len(fmt.Sprint(to))
		for i := from; i <= to; i++ {
			mark := " "
			if i >= first && i <= last {
				mark = ">"
			}
			fmt.Fprintf(w, "   %s %*d | %s\n", mark, width, i, clip(lines[i-1]))
		}
	}
	for _, c := range is.Reproduce {
		fmt.Fprintf(w, "   verify: %s\n", c)
	}
}

// agentIncomplete names the required checks that did not run to completion. They are environment
// problems the agent cannot fix by editing code.
func agentIncomplete(w io.Writer, r *finding.Report) {
	var lines []string
	for _, c := range r.Coverage.Capabilities {
		if !c.Required || c.Status.Completed() {
			continue
		}
		l := fmt.Sprintf("  %s/%s (%s)", c.Project, c.Capability, c.Status)
		if c.Error != "" {
			l += ": " + clip(oneLine(c.Error))
		}
		lines = append(lines, l)
	}
	if r.AI != nil && r.AI.Required && r.AI.Status != finding.StatusComplete {
		lines = append(lines, fmt.Sprintf("  ai (required, %s)", r.AI.Status))
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, "INCOMPLETE — environment problem, not caused by your change; report it to the user:")
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}

// sourceLines reads files lazily; lines flagged by sensitive findings are withheld.
func sourceLines(read func(string) ([]byte, error), sensitive map[string]map[int]string) func(string) []string {
	files := map[string][]string{}
	return func(p string) []string {
		if l, ok := files[p]; ok || read == nil {
			return l
		}
		var l []string
		if b, err := read(p); err == nil {
			l = strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
			l = finding.WithholdLines(l, sensitive[p], func(string) string { return finding.WithheldLine })
		}
		files[p] = l
		return l
	}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string) string {
	s = strings.ReplaceAll(strings.TrimRight(s, " \t"), "\t", "    ")
	if r := []rune(s); len(r) > agentMaxLine {
		return string(r[:agentMaxLine]) + "…"
	}
	return s
}
