package report

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/pistos/internal/finding"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/agent")

func agentSource(p string) ([]byte, error) {
	if p != "internal/x/y.go" {
		return nil, errors.New("no such file")
	}
	var b strings.Builder
	for i := 1; i <= 50; i++ {
		if i == 42 {
			b.WriteString("\tfmt.Printf(\"%d\", s)\n")
			continue
		}
		fmt.Fprintf(&b, "\tline%d()\n", i)
	}
	return []byte(b.String()), nil
}

func vetIssue(key, path string, line int, blocking bool) finding.Issue {
	return finding.Issue{
		Finding: finding.Finding{
			Key: key, Lane: finding.LaneDeterministic, Project: "app", Family: "go-vet/printf", Severity: finding.SevError,
			Message: "Printf format %d has arg s of wrong type string", Location: finding.Location{Path: path, Line: line, Column: 7},
			Reproduce: []string{"go vet ./internal/x"},
		},
		Change: "new", Disposition: finding.Disposition{State: finding.DispActive}, Blocking: blocking,
	}
}

func baseReport(outcome, reason string, issues ...finding.Issue) *finding.Envelope {
	env := &finding.Envelope{
		Canonical: &finding.Report{
			Scope:   finding.Scope{Mode: "working", HeadRef: "(working tree)", BaseRef: "main", BaseCommit: "abc"},
			Verdict: finding.Verdict{Outcome: outcome, Reason: reason},
			Issues:  issues,
		},
		Numbers: map[string]int{},
	}
	for i, is := range issues {
		env.Numbers[is.Key] = 3 + i
	}
	return env
}

func TestAgentGolden(t *testing.T) {
	ai := finding.Issue{
		Finding: finding.Finding{
			Key: "k1-ai", Lane: finding.LaneAI, Project: "app", Family: "nil-dereference", Severity: finding.SevWarning,
			Message: "  user may be nil\nwhen the cache misses", Location: finding.Location{Path: "internal/a.go", Line: 88},
			AI: &finding.AIProvenance{Outcome: finding.OutcomeVerified, Passes: []string{"p1", "p2"}, FailingScenario: "Lookup returns nil, Name() dereferences it"},
		},
		Change: "new", Disposition: finding.Disposition{State: finding.DispActive},
	}
	existing := vetIssue("k1-old", "internal/x/y.go", 10, false)
	existing.Change = "existing"
	suppressed := vetIssue("k1-sup", "internal/x/y.go", 11, false)
	suppressed.Disposition = finding.Disposition{State: finding.DispSuppressed}

	blocking := baseReport("blocking", "1 active blocking issue(s) at or above warning",
		ai, vetIssue("k1-vet", "internal/x/y.go", 42, true), existing, suppressed)
	blocking.Canonical.AI = &finding.AIReport{Passes: []finding.AIPassStatus{{ID: "p1"}, {ID: "p2"}, {ID: "p3"}}}

	incomplete := baseReport("incomplete", "required checks did not complete: app/staticcheck (incomplete)")
	incomplete.Canonical.Coverage.Capabilities = []finding.CapabilityResult{
		{Project: "app", Capability: "go-vet", Side: "head", Status: finding.StatusComplete, Required: true},
		{Project: "app", Capability: "staticcheck", Side: "head", Status: finding.StatusIncomplete, Required: true, Error: "tool staticcheck not found\n(run pistos plugins install)"},
	}

	var many []finding.Issue
	for i := 0; i < 5; i++ {
		many = append(many, vetIssue(fmt.Sprintf("k1-%d", i), "internal/x/y.go", 42+i, true))
	}
	overflow := baseReport("blocking", "5 active blocking issue(s) at or above warning", many...)

	clean := baseReport("pass", "required deterministic checks completed with no active blocking issues", existing)
	clean.Canonical.Scope = finding.Scope{Mode: "staged", HeadRef: "(index)", BaseRef: "HEAD", BaseCommit: "abc"}

	for name, tc := range map[string]struct {
		env *finding.Envelope
		o   AgentOptions
	}{
		"blocking":   {blocking, AgentOptions{Source: agentSource}},
		"incomplete": {incomplete, AgentOptions{}},
		"overflow":   {overflow, AgentOptions{Source: agentSource, MaxIssues: 2, ReportPath: ".pistos/state/report.html"}},
		"clean":      {clean, AgentOptions{}},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			Agent(&buf, tc.env, tc.o)
			golden := filepath.Join("testdata", "agent", name+".txt")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/report -run TestAgentGolden -update)", err)
			}
			if buf.String() != string(want) {
				t.Fatalf("agent output differs from %s:\n--- got\n%s--- want\n%s", golden, buf.String(), want)
			}
			var again bytes.Buffer
			Agent(&again, tc.env, tc.o)
			if again.String() != buf.String() {
				t.Fatal("agent output must be deterministic")
			}
		})
	}
}
