package feedback

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"example.com/pistos/internal/finding"
)

func repoWith(t *testing.T, content string) string {
	t.Helper()
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".pistos"), 0o755)
	if content != "" {
		os.WriteFile(Path(repo), []byte(content), 0o644)
	}
	return repo
}

func issueDecision(id, key string, tags ...string) Decision {
	return Decision{ID: id, Tags: tags, Scope: ScopeIssue, Subject: key, Version: 1,
		Match: &Match{Project: "app", Path: "main.go", Symbol: "F", Family: "go-vet/printf", Anchor: "aaaa"}}
}

func TestParseRejectsInvalidEdits(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"unknown field": {"schema: 1\ndecisions:\n  - id: d-abcdef12\n    tags: [irrelevant]\n    scope: issue\n    subjekt: k1-x\n    version: 1\n", "field subjekt not found"},
		"bad tag":       {"schema: 1\ndecisions:\n  - id: d-abcdef12\n    tags: [Not OK]\n    scope: pattern\n    pattern: {family: x}\n    version: 1\n", `invalid tag "Not OK"`},
		"issue fields":  {"schema: 1\ndecisions:\n  - id: d-abcdef12\n    tags: [irrelevant]\n    scope: issue\n    version: 1\n", "issue scope requires"},
		"schema":        {"schema: 7\ndecisions: []\n", "schema 7 is not supported"},
		"escape":        {"schema: 1\ndecisions:\n  - id: d-abcdef12\n    tags: [irrelevant]\n    scope: pattern\n    pattern: {family: x, paths: [\"../**\"]}\n    version: 1\n", "inside the repository"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestMigratesUnversionedFile(t *testing.T) {
	f, err := Parse([]byte("decisions:\n  - id: d-abcdef12\n    tags: [irrelevant]\n    scope: pattern\n    pattern: {family: x}\n"))
	if err != nil || f.Schema != SchemaVersion || f.Decisions[0].Version != 1 {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestUpdatePreservesCommentsAndManualEntries(t *testing.T) {
	manual := `# Team decisions — keep this header.
schema: 1
decisions:
  # Generated code is reviewed upstream.
  - id: d-manual0001
    tags: [irrelevant]
    reason: generated
    scope: pattern
    pattern:
      family: go-vet/printf
      paths: ["gen/**"]
    version: 1
`
	repo := repoWith(t, manual)
	_, err := Update(context.Background(), repo, func(f *File) error {
		f.Decisions = append(f.Decisions, issueDecision("d-new0000001", "k1-abc", TagIrrelevant))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path(repo))
	out := string(b)
	for _, want := range []string{"# Team decisions — keep this header.", "# Generated code is reviewed upstream.", "reason: generated", "d-new0000001"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q after update:\n%s", want, out)
		}
	}
	if _, err := Parse(b); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRefusesInvalidFile(t *testing.T) {
	repo := repoWith(t, "schema: 1\ndecisions:\n  - nonsense: true\n")
	_, err := Update(context.Background(), repo, func(*File) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "refusing to modify invalid feedback") {
		t.Fatalf("got %v", err)
	}
}

func TestConcurrentUpdatesKeepEveryEntry(t *testing.T) {
	repo := repoWith(t, "# keep me\nschema: 1\ndecisions: []\n")
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := Update(context.Background(), repo, func(f *File) error {
				f.Decisions = append(f.Decisions, issueDecision(fmt.Sprintf("d-conc%06d", i), fmt.Sprintf("k1-%d", i), TagIrrelevant))
				return nil
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	f, err := Load(repo)
	if err != nil || len(f.Decisions) != 16 {
		t.Fatalf("want 16 decisions, got %d (%v)", len(f.Decisions), err)
	}
	if b, _ := os.ReadFile(Path(repo)); !strings.HasPrefix(string(b), "# keep me") {
		t.Fatal("manual comment lost")
	}
}

func aiFinding(line int, msg, pass, anchorText, family string) finding.Issue {
	f := finding.Finding{
		Lane: finding.LaneAI, Project: "app", Family: family, Severity: finding.SevWarning, Message: msg,
		Location: finding.Location{Path: "svc/handler.go", Line: line},
		Anchor:   finding.Anchor{Symbol: "Handle", TextHash: finding.AnchorHash(anchorText)},
		Sources:  []finding.Source{{Tool: "ai:" + pass, Rule: family, Severity: finding.SevWarning, Message: msg}},
	}
	fs := []finding.Finding{f}
	finding.AssignIdentity(fs)
	return finding.Issue{Finding: fs[0], Change: "new"}
}

func decisionFor(is finding.Issue, tags ...string) Decision {
	return Decision{ID: NewID(is.Key), Tags: tags, Scope: ScopeIssue, Subject: is.Key, Version: 1,
		Match: &Match{Project: is.Project, Path: is.Location.Path, Symbol: is.Anchor.Symbol, Family: is.Family, Anchor: is.Anchor.TextHash, Ordinal: is.Anchor.Ordinal}}
}

func TestAIShapedCandidatesKeepTheirDecision(t *testing.T) {
	const line = "user := users[id]"
	tagged := aiFinding(40, "possible nil map access", "pass-1", line, "ai/nil-deref")
	f := &File{Schema: 1, Decisions: []Decision{decisionFor(tagged, TagFalsePositive)}}
	m := NewMatcher(f, Environment{})

	issues := []finding.Issue{
		aiFinding(52, "The map lookup may yield nil and be dereferenced (reworded by another model)", "pass-3", "  user :=   users[id]", "ai/nil-deref"),
		aiFinding(53, "different defect nearby", "pass-2", "return user.Name", "ai/nil-deref"),
		aiFinding(54, "unrelated family in the same function", "pass-2", line, "ai/race"),
	}
	m.Apply(issues)
	if issues[0].Disposition.State != finding.DispSuppressed {
		t.Fatalf("rewording/model/line change must keep the decision: %+v", issues[0].Disposition)
	}
	if issues[1].Disposition.State == finding.DispSuppressed || issues[2].Disposition.State == finding.DispSuppressed {
		t.Fatalf("other defects must stay visible: %+v / %+v", issues[1].Disposition, issues[2].Disposition)
	}
}

func TestChangedAnchorRequestsRevalidation(t *testing.T) {
	tagged := aiFinding(40, "m", "pass-1", "user := users[id]", "ai/nil-deref")
	f := &File{Schema: 1, Decisions: []Decision{decisionFor(tagged, TagIrrelevant)}}
	changed := []finding.Issue{aiFinding(40, "m", "pass-1", "user := lookup(users, id)", "ai/nil-deref")}
	NewMatcher(f, Environment{}).Apply(changed)
	if changed[0].Disposition.State != finding.DispRevalidate || !strings.Contains(changed[0].Disposition.Note, "re-tag") {
		t.Fatalf("%+v", changed[0].Disposition)
	}
}

func TestPatternScopeAndLabels(t *testing.T) {
	gen := aiFinding(1, "m", "p", "a", "go-vet/printf")
	gen.Location.Path = "gen/x.go"
	src := aiFinding(1, "m", "p", "a", "go-vet/printf")
	other := aiFinding(1, "m", "p", "b", "go-vet/shadow")
	other.Location.Path = "gen/y.go"
	labelled := aiFinding(2, "m", "p", "c", "go-vet/printf")
	f := &File{Schema: 1, Decisions: []Decision{
		{ID: "d-pattern001", Tags: []string{TagIrrelevant}, Scope: ScopePattern, Pattern: &Pattern{Family: "go-vet/printf", Paths: []string{"gen/**"}}, Version: 1},
		decisionFor(labelled, "security"),
	}}
	issues := []finding.Issue{gen, src, other, labelled}
	NewMatcher(f, Environment{}).Apply(issues)
	want := []string{finding.DispSuppressed, "", "", finding.DispActive}
	for i, w := range want {
		if issues[i].Disposition.State != w {
			t.Errorf("issue %d: state %q, want %q", i, issues[i].Disposition.State, w)
		}
	}
	if len(issues[3].Disposition.Tags) != 1 {
		t.Error("labels must remain visible on the issue")
	}
}

func TestTestOnlyGuard(t *testing.T) {
	is := aiFinding(1, "m", "p", "a", "go-vet/printf")
	d := decisionFor(is, TagIrrelevant, TagTestOnly)
	d.Guards = &Guards{TestPath: true}
	f := &File{Schema: 1, Decisions: []Decision{d}}
	for _, testCode := range []bool{true, false} {
		issues := []finding.Issue{is}
		NewMatcher(f, Environment{IsTestPath: func(string, string) bool { return testCode }}).Apply(issues)
		want := finding.DispSuppressed
		if !testCode {
			want = finding.DispRevalidate
		}
		if issues[0].Disposition.State != want {
			t.Errorf("testCode=%v: %+v", testCode, issues[0].Disposition)
		}
	}
}

func TestSampleFeedbackValidates(t *testing.T) {
	b, err := os.ReadFile("../../docs/schemas/feedback.sample.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(b)
	if err != nil || len(f.Decisions) != 3 {
		t.Fatalf("%v %+v", err, f)
	}
}
