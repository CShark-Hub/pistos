package cli

import (
	"fmt"
	"strings"
	"testing"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/testutil/gitrepo"
)

// Expected values follow docs/metrics.md (metrics-v1); they were derived by hand from the fixtures.
var expectedMetrics = map[string]map[string]int{
	"Shop.total":       {"cyclomatic": 9, "nesting": 3, "params": 4, "statements": 11},
	"outer":            {"cyclomatic": 1, "nesting": 0, "params": 1, "statements": 2},
	"outer.inner":      {"cyclomatic": 1, "nesting": 0, "params": 1, "statements": 1},
	"kind":             {"cyclomatic": 3, "nesting": 1, "params": 1, "statements": 4},
	"Cart.total":       {"cyclomatic": 8, "nesting": 3, "params": 2, "statements": 11},
	"pick":             {"cyclomatic": 3, "nesting": 1, "params": 1, "statements": 4},
	"Cart.Total":       {"cyclomatic": 8, "nesting": 3, "params": 2, "statements": 11},
	"Cart.Pick":        {"cyclomatic": 3, "nesting": 0, "params": 1, "statements": 1},
	"Cart.Outer":       {"cyclomatic": 1, "nesting": 0, "params": 1, "statements": 2},
	"Cart.Outer.Inner": {"cyclomatic": 1, "nesting": 0, "params": 1, "statements": 1},
}

func TestMetricsAcrossLanguagesLive(t *testing.T) {
	requireRuntimes(t, "python3", "node", "npm", "dotnet")
	cfg := "schema = 1\nlanguages = [\"python\", \"ts\", \"csharp\"]\n\n[metrics]\ncyclomatic = 0\n" +
		"\n[[projects]]\nid = \"py\"\nroot = \"py\"\necosystem = \"python\"\nchecks = [\"metrics\"]\n" +
		"\n[[projects]]\nid = \"ts\"\nroot = \"ts\"\necosystem = \"ts\"\nchecks = [\"metrics\"]\n" +
		"\n[[projects]]\nid = \"cs\"\nroot = \"cs\"\necosystem = \"csharp\"\nchecks = [\"metrics\"]\n"
	// Thresholds of 0 are rejected, so drive every metric through threshold 1 instead.
	cfg = strings.Replace(cfg, "[metrics]\ncyclomatic = 0\n", "[metrics]\ncyclomatic = 1\nnesting = 1\nparams = 1\nstatements = 1\n", 1)
	r := setupPackWithConfig(t, "metrics-langs", cfg)

	_, env, errs := runReview(t, r.Dir, "--all")
	for _, p := range []string{"py", "ts", "cs"} {
		if c := capability(env, p, "metrics", "head"); c.Status != finding.StatusComplete {
			t.Fatalf("%s metrics: %+v %s", p, c, errs)
		}
	}
	got := map[string]map[string]int{}
	for _, is := range env.Canonical.Issues {
		if is.Metric == nil {
			continue
		}
		if got[is.Anchor.Symbol] == nil {
			got[is.Anchor.Symbol] = map[string]int{}
		}
		got[is.Anchor.Symbol][is.Metric.Name] = is.Metric.Value
	}
	for sym, want := range expectedMetrics {
		for name, v := range want {
			reported := got[sym][name]
			if v <= 1 {
				if reported != 0 {
					t.Errorf("%s %s: %d is not above threshold 1 but was reported", sym, name, reported)
				}
				continue
			}
			if reported != v {
				t.Errorf("%s %s = %d, want %d (all: %v)", sym, name, reported, v, fmt.Sprint(got[sym]))
			}
		}
	}
}

func setupPackWithConfig(t *testing.T, fixture, cfg string) *gitrepo.Repo {
	t.Helper()
	t.Setenv("PISTOS_CACHE_DIR", t.TempDir())
	r := gitrepo.New(t, fixture)
	r.Write(".gitignore", "bin/\nobj/\nnode_modules/\n")
	r.Write(".pistos/config.toml", cfg)
	if code, out, errs := run(t, r.Dir, "plugins", "install"); code != 0 {
		skipLive(t, "tool acquisition unavailable: %s %s", out, errs)
	}
	r.Commit("configure pistos")
	return r
}
