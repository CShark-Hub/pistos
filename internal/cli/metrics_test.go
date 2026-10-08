package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/pistos/internal/finding"
)

const metricsProject = "\n[metrics]\ncyclomatic = 3\n\n[[projects]]\nid = \"app\"\nroot = \".\"\necosystem = \"go\"\nchecks = [\"metrics\"]\n"

const branchy = `package main

func classify(n int) string {
	if n < 0 {
		return "negative"
	}
	if n == 0 {
		return "zero"
	}
	return "positive"
}
`

func metricIssues(env *finding.Envelope) []finding.Issue {
	var out []finding.Issue
	for _, is := range env.Canonical.Issues {
		if strings.HasPrefix(is.Family, "metrics/") {
			out = append(out, is)
		}
	}
	return out
}

func TestComplexityRegressionAndDeltas(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(metricsProject))
	r.Write("classify.go", branchy)
	code, env, _ := runReview(t, r.Dir)
	m := metricIssues(env)
	if code != ExitOK || len(m) != 0 {
		t.Fatalf("cyclomatic 3 is within the threshold: %d %+v", code, m)
	}
	r.Commit("classify")

	// New debt: the function crosses the threshold.
	r.Write("classify.go", strings.Replace(branchy, "\treturn \"positive\"", "\tif n > 100 {\n\t\treturn \"large\"\n\t}\n\treturn \"positive\"", 1))
	_, env, _ = runReview(t, r.Dir)
	m = metricIssues(env)
	if len(m) != 1 || m[0].Change != "new" || m[0].Metric.Value != 4 || !m[0].Advisory || m[0].Blocking {
		t.Fatalf("complexity regression must be reported as new advisory debt: %+v", m)
	}
	r.Commit("over threshold")

	// Worsened debt: an existing over-threshold function gets more complex.
	r.Write("classify.go", strings.Replace(r.Read("classify.go"), "\treturn \"positive\"", "\tif n > 10 {\n\t\treturn \"medium\"\n\t}\n\treturn \"positive\"", 1))
	_, env, _ = runReview(t, r.Dir)
	m = metricIssues(env)
	if len(m) != 1 || m[0].Change != "worsened" || m[0].Metric.Value != 5 || m[0].Metric.Base == nil || *m[0].Metric.Base != 4 {
		t.Fatalf("worsened debt must carry its baseline value: %+v", m)
	}
	r.Commit("worse")

	// Formatting, a rename and moving the function to another file are not new debt.
	r.Write("classify.go", strings.ReplaceAll(strings.ReplaceAll(r.Read("classify.go"), "\t", "    "), "n < 0", "n<0"))
	_, env, _ = runReview(t, r.Dir)
	if m = metricIssues(env); len(m) != 1 || m[0].Change != "existing" {
		t.Fatalf("reformatting must not create debt: %+v", m)
	}
	r.Git("checkout", "--", "classify.go")
	r.Git("mv", "classify.go", "rules.go")
	_, env, _ = runReview(t, r.Dir)
	if m = metricIssues(env); len(m) != 1 || m[0].Change != "existing" || m[0].Location.Path != "rules.go" {
		t.Fatalf("a renamed file must not create debt: %+v", m)
	}
	r.Git("mv", "rules.go", "classify.go")
	body := r.Read("classify.go")
	os.Remove(filepath.Join(r.Dir, "classify.go"))
	r.Write("main.go", r.Read("main.go")+strings.TrimPrefix(body, "package main\n"))
	_, env, _ = runReview(t, r.Dir)
	if m = metricIssues(env); len(m) != 1 || m[0].Change != "existing" || m[0].Location.Path != "main.go" {
		t.Fatalf("a relocated function must not create debt: %+v", m)
	}
}

func TestMetricsGateIsOptIn(t *testing.T) {
	cfg := strings.Replace(metricsProject, "[metrics]\n", "[metrics]\ngate = true\n", 1)
	r := setupGo(t, "go-clean", configFor(cfg))
	r.Write("classify.go", strings.Replace(branchy, "\treturn \"positive\"", "\tif n > 100 {\n\t\treturn \"large\"\n\t}\n\treturn \"positive\"", 1))
	code, env, _ := runReview(t, r.Dir)
	if m := metricIssues(env); code != ExitBlocking || len(m) != 1 || m[0].Advisory {
		t.Fatalf("an explicit gate rule makes new debt blocking: %d %+v", code, m)
	}
}

const archProject = `
[architecture]
[[architecture.layers]]
name = "domain"
paths = ["internal/domain/**"]
[[architecture.layers]]
name = "infra"
paths = ["internal/infra/**"]
[[architecture.rules]]
from = "domain"
deny = ["infra"]

[[projects]]
id = "app"
root = "."
ecosystem = "go"
checks = ["imports"]
`

func TestArchitectureViolationsAndCoverage(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(archProject))
	r.Write("go.mod", "module example.com/clean\n\ngo 1.24\n")
	r.Write("internal/domain/order.go", "package domain\n\nfunc Total() int { return 1 }\n")
	r.Write("internal/infra/db.go", "package infra\n\nimport \"example.com/clean/internal/domain\"\n\nfunc Save() int { return domain.Total() }\n")
	code, env, _ := runReview(t, r.Dir)
	if code != ExitOK || len(env.Canonical.Issues) != 0 {
		t.Fatalf("infra → domain is allowed: %d %+v", code, env.Canonical.Issues)
	}
	r.Commit("layers")

	r.Write("internal/domain/order.go", "package domain\n\nimport \"example.com/clean/internal/infra\"\n\nfunc Total() int { return 1 }\n\nvar _ = infra.Save\n")
	_, env, _ = runReview(t, r.Dir)
	fams := map[string]finding.Issue{}
	for _, is := range env.Canonical.Issues {
		fams[is.Family] = is
	}
	layer, cycle := fams["arch/layer"], fams["arch/cycle"]
	if layer.Change != "new" || layer.Location.Path != "internal/domain/order.go" || layer.Location.Line != 3 {
		t.Fatalf("layer violation: %+v", layer)
	}
	if cycle.Change != "new" || !strings.Contains(cycle.Message, "internal/domain → internal/infra → internal/domain") {
		t.Fatalf("dependency cycle: %+v", cycle)
	}

	r.Write("internal/domain/order.go", "package domain\n\nimport _ \"example.com/clean/internal/missing\"\n\nfunc Total() int { return 1 }\n")
	_, env, _ = runReview(t, r.Dir)
	c := capability(env, "app", "imports", "head")
	if !strings.Contains(strings.Join(c.Limitations, ";"), "not proven") {
		t.Fatalf("unresolved imports must qualify the result: %+v", c)
	}
	r.Write(".pistos/config.toml", strings.Replace(configFor(archProject), "[architecture]\n", "[architecture]\nrequire_complete = true\n", 1))
	code, env, _ = runReview(t, r.Dir)
	if c := capability(env, "app", "imports", "head"); code != ExitError || c.Status != finding.StatusIncomplete {
		t.Fatalf("a strict architecture gate needs complete coverage: %d %+v", code, c)
	}
}
