package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/testutil/gitrepo"
)

const defectLine = "\treturn fmt.Sprintf(\"hello, %d\", name)"

// setupTagged prepares a repo where main.go gained a vet finding, reviews it and tags it irrelevant.
func setupTagged(t *testing.T) (*gitrepo.Repo, string) {
	t.Helper()
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Write("main.go", strings.Replace(r.Read("main.go"), "hello, %s", "hello, %d", 1))
	code, env, _ := runReview(t, r.Dir)
	if code != ExitBlocking || len(env.Canonical.Issues) != 1 {
		t.Fatalf("setup review: %d %+v", code, env.Canonical.Issues)
	}
	key := env.Canonical.Issues[0].Key
	n := env.Numbers[key]
	code, out, errs := run(t, r.Dir, "tag", strconv.Itoa(n), "irrelevant")
	if code != 0 || !strings.Contains(out, "PASS") {
		t.Fatalf("tag: code=%d %s %s", code, out, errs)
	}
	return r, key
}

func assertSuppressed(t *testing.T, r *gitrepo.Repo, step string, args ...string) *finding.Envelope {
	t.Helper()
	code, env, errs := runReview(t, r.Dir, args...)
	var states []string
	for _, is := range env.Canonical.Issues {
		states = append(states, is.Change+"/"+is.Disposition.State)
	}
	if code != ExitOK || len(env.Canonical.Issues) == 0 {
		t.Fatalf("%s: want suppressed issue and exit 0, got %d %v %s", step, code, states, errs)
	}
	for _, is := range env.Canonical.Issues {
		if is.Change != "existing" && is.Disposition.State != finding.DispSuppressed {
			t.Fatalf("%s: issue not suppressed: %v", step, states)
		}
	}
	return env
}

func TestTagPersistsAcrossDiffsRefsMovesAndCacheDeletion(t *testing.T) {
	r, key := setupTagged(t)
	fb := r.Read(".pistos/feedback.yaml")
	if !strings.Contains(fb, key) || !strings.Contains(fb, "irrelevant") || strings.Contains(fb, "Sprintf") {
		t.Fatalf("feedback must hold selectors but no source bodies:\n%s", fb)
	}

	r.Write("other.go", "package main\n\nfunc other() int { return 2 }\n")
	assertSuppressed(t, r, "unrelated file change")

	r.Write("main.go", "package main\n\n// a comment that shifts every line\n// down by a few lines\n"+strings.TrimPrefix(r.Read("main.go"), "package main\n"))
	assertSuppressed(t, r, "line shift")

	r.Write("main.go", strings.Replace(r.Read("main.go"), defectLine, "\treturn   fmt.Sprintf(\"hello, %d\",   name)", 1))
	assertSuppressed(t, r, "whitespace reformat")

	os.RemoveAll(filepath.Join(r.Dir, ".pistos", "cache"))
	os.RemoveAll(filepath.Join(r.Dir, ".pistos", "state"))
	assertSuppressed(t, r, "cache and state deleted")

	r.Git("checkout", "-q", "-b", "feature")
	r.Commit("feature with tagged issue")
	r.Write("unrelated.txt", "x")
	assertSuppressed(t, r, "different base/head", "--base", "main", "--head", "feature")
	assertSuppressed(t, r, "full review", "--all")
}

func TestVerifiedRenameKeepsDecision(t *testing.T) {
	r, _ := setupTagged(t)
	r.Commit("defect committed")
	r.Git("mv", "main.go", "app.go")
	env := assertSuppressed(t, r, "rename")
	if n := env.Canonical.Issues[0].Disposition.Note; !strings.Contains(n, "verified rename from main.go") {
		t.Fatalf("rename provenance missing: %q", n)
	}
}

func TestChangedCauseAndNearbyDefectStayVisible(t *testing.T) {
	r, _ := setupTagged(t)
	src := strings.Replace(r.Read("main.go"), defectLine, "\treturn fmt.Sprintf(\"hi there, %d\", name)", 1)
	src += "\nfunc second(n string) string { return fmt.Sprintf(\"%x %d\", n, n) }\n"
	r.Write("main.go", src)
	code, env, _ := runReview(t, r.Dir)
	if code != ExitBlocking {
		t.Fatalf("changed cause must gate again: %d", code)
	}
	var revalidate, active int
	for _, is := range env.Canonical.Issues {
		switch is.Disposition.State {
		case finding.DispRevalidate:
			revalidate++
		case finding.DispActive:
			active++
		case finding.DispSuppressed:
			t.Fatalf("nothing may be suppressed: %+v", is)
		}
	}
	if revalidate != 1 || active < 1 {
		t.Fatalf("want one revalidation request and the new defect active: %+v", env.Canonical.Issues)
	}
}

func TestReopenRestoresVisibilityAndKeepsOtherDecisions(t *testing.T) {
	r, key := setupTagged(t)
	fb := r.Read(".pistos/feedback.yaml")
	r.Write(".pistos/feedback.yaml", fb+"  # unrelated decision for code outside this diff\n  - id: d-elsewhere01\n    tags: [false-positive]\n    scope: pattern\n    pattern: {family: go-vet/shadow, paths: [\"legacy/**\"]}\n    version: 1\n")
	if code, out, errs := run(t, r.Dir, "reopen", key); code != 0 || !strings.Contains(out, "BLOCKING") {
		t.Fatalf("reopen: %d %s %s", code, out, errs)
	}
	after := r.Read(".pistos/feedback.yaml")
	if strings.Contains(after, key) || !strings.Contains(after, "d-elsewhere01") || !strings.Contains(after, "# unrelated decision") {
		t.Fatalf("reopen must drop only its own decision:\n%s", after)
	}
	if code, _, _ := runReview(t, r.Dir); code != ExitBlocking {
		t.Fatal("reopened issue must block again")
	}
}

func TestOrdinaryLabelDoesNotChangeVerdictAndDeferredStillGates(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Write("main.go", strings.Replace(r.Read("main.go"), "hello, %s", "hello, %d", 1))
	runReview(t, r.Dir)
	for _, tag := range []string{"security", "deferred"} {
		if code, out, errs := run(t, r.Dir, "tag", "1", tag); code != 0 || !strings.Contains(out, "BLOCKING") {
			t.Fatalf("%s: %d %s %s", tag, code, out, errs)
		}
	}
	code, env, _ := runReview(t, r.Dir)
	if code != ExitBlocking || env.Canonical.Issues[0].Disposition.State != finding.DispDeferred {
		t.Fatalf("deferred must stay tracked and gating: %d %+v", code, env.Canonical.Issues[0].Disposition)
	}
}

func TestTestOnlyGuardInvalidatedByMovingToProduction(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Write("fixture_test.go", "package main\n\nimport \"fmt\"\n\nfunc vulnerableFixture(n string) string { return fmt.Sprintf(\"%d\", n) }\n")
	runReview(t, r.Dir)
	if code, out, errs := run(t, r.Dir, "tag", "1", "test-only", "accepted-risk"); code != 0 {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	if !strings.Contains(r.Read(".pistos/feedback.yaml"), "test_path: true") {
		t.Fatal("test-only must record a test-path guard")
	}
	r.Commit("fixture")
	r.Write("other.go", "package main\n")
	assertSuppressed(t, r, "test fixture across diffs")

	r.Git("mv", "fixture_test.go", "fixture.go")
	code, env, _ := runReview(t, r.Dir)
	if code != ExitBlocking || env.Canonical.Issues[0].Disposition.State != finding.DispRevalidate {
		t.Fatalf("production-reachable fixture must be revalidated: %d %+v", code, env.Canonical.Issues)
	}
}

func TestPatternScopeNeedsConfirmation(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Write("gen/a.go", "package gen\n\nimport \"fmt\"\n\nfunc A(n string) string { return fmt.Sprintf(\"%d\", n) }\n")
	r.Write("gen/b.go", "package gen\n\nimport \"fmt\"\n\nfunc B(n string) string { return fmt.Sprintf(\"%x %d\", n, n) }\n")
	r.Write("main.go", strings.Replace(r.Read("main.go"), "hello, %s", "hello, %d", 1))
	_, env, _ := runReview(t, r.Dir)
	var genRef string
	for _, is := range env.Canonical.Issues {
		if strings.HasPrefix(is.Location.Path, "gen/") {
			genRef = strconv.Itoa(env.Numbers[is.Key])
		}
	}
	code, out, _ := run(t, r.Dir, "tag", genRef, "irrelevant", "--scope", "pattern", "--paths", "gen/**")
	if code != ExitError || !strings.Contains(out, "currently matches 2 issue(s)") {
		t.Fatalf("pattern without --yes must preview and refuse: %d %s", code, out)
	}
	if code, out, errs := run(t, r.Dir, "tag", genRef, "irrelevant", "--scope", "pattern", "--paths", "gen/**", "--yes"); code != 0 {
		t.Fatalf("%s %s", out, errs)
	}
	code, env, _ = runReview(t, r.Dir)
	for _, is := range env.Canonical.Issues {
		wantSuppressed := strings.HasPrefix(is.Location.Path, "gen/")
		if (is.Disposition.State == finding.DispSuppressed) != wantSuppressed {
			t.Fatalf("pattern must cover exactly gen/**: %s %s", is.Location.Path, is.Disposition.State)
		}
	}
	if code != ExitBlocking {
		t.Fatal("main.go issue outside the pattern must still block")
	}
}

func TestInvalidManualFeedbackIsRejected(t *testing.T) {
	r, _ := setupTagged(t)
	r.Write(".pistos/feedback.yaml", strings.Replace(r.Read(".pistos/feedback.yaml"), "scope: issue", "scope: everything", 1))
	code, _, errs := run(t, r.Dir, "review")
	if code != ExitError || !strings.Contains(errs, "scope must be") {
		t.Fatalf("want config error, got %d %s", code, errs)
	}
}

func TestFeedbackFromBaseIgnoresSelfExemption(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Git("checkout", "-q", "-b", "pr")
	r.Write("main.go", strings.Replace(r.Read("main.go"), "hello, %s", "hello, %d", 1))
	runReview(t, r.Dir)
	run(t, r.Dir, "tag", "1", "irrelevant")
	r.Commit("defect plus self-exemption")

	if code, _, _ := runReview(t, r.Dir, "--base", "main", "--head", "pr"); code != ExitOK {
		t.Fatal("trusted mode applies the working feedback")
	}
	code, env, _ := runReview(t, r.Dir, "--base", "main", "--head", "pr", "--feedback-from-base")
	if code != ExitBlocking {
		t.Fatalf("baseline policy must not include the PR's own exemption: %d", code)
	}
	found := false
	for _, c := range env.Canonical.Coverage.Capabilities {
		for _, l := range c.Limitations {
			found = found || strings.Contains(l, "NOT applied")
		}
	}
	if !found {
		t.Fatal("report must flag the unapplied feedback change")
	}
}

func TestReferencesResolveStrictly(t *testing.T) {
	r, key := setupTagged(t)
	for ref, want := range map[string]string{"99": "not in the current report", "k1-deadbeef": "unknown issue key", "main": "neither"} {
		if code, _, errs := run(t, r.Dir, "tag", ref, "irrelevant"); code != ExitError || !strings.Contains(errs, want) {
			t.Errorf("%s: %d %s", ref, code, errs)
		}
	}
	code, out, _ := run(t, r.Dir, "reproduce", key)
	if code != 0 || !strings.Contains(out, "go vet") || !strings.Contains(out, "decision") {
		t.Fatalf("reproduce: %s", out)
	}
}

func TestInteractiveFeedbackView(t *testing.T) {
	r, _ := setupTagged(t)
	var out strings.Builder
	code := Run(t.Context(), []string{"-C", r.Dir, "feedback"}, strings.NewReader("show 1\nreason 1 generated upstream\ntag 1 security\nquit\n"), &out, &out)
	if code != 0 || !strings.Contains(out.String(), "issue: go-vet/printf at main.go in greet") {
		t.Fatalf("%d %s", code, out.String())
	}
	fb := r.Read(".pistos/feedback.yaml")
	if !strings.Contains(fb, "reason: generated upstream") || !strings.Contains(fb, "security") || !strings.Contains(fb, "version: 3") {
		t.Fatalf("interactive edits not persisted:\n%s", fb)
	}
}
