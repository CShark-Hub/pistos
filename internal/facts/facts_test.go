package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/pistos/internal/finding"
)

func byName(fs []finding.FunctionFact) map[string]finding.FunctionFact {
	m := map[string]finding.FunctionFact{}
	for _, f := range fs {
		m[f.Symbol] = f
	}
	return m
}

func TestGoMetricsDefinitions(t *testing.T) {
	f, _, err := GoFacts("testdata/gomod", "svc")
	if err != nil {
		t.Fatal(err)
	}
	fns := byName(f.Functions)
	c := fns["complex"]
	if c.Params != 3 || c.Cyclomatic != 7 || c.Nesting != 3 || c.Statements != 11 || c.Path != "svc/internal/domain/order.go" {
		t.Fatalf("complex: %+v", c)
	}
	if d := fns["Svc.Do"]; d.Params != 3 || d.Cyclomatic != 1 || d.Statements != 1 {
		t.Fatalf("method: %+v", d)
	}
}

func TestGoMetricsIgnoreFormatting(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n"), 0o644)
	src := "package m\n\nfunc f(a int) int {\n\tif a > 1 && a < 5 {\n\t\treturn 1\n\t}\n\treturn 0\n}\n"
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o644)
	before, _, _ := GoFacts(dir, ".")
	reformatted := "package m\n\n// docs\nfunc f(\n\ta int,\n) int {\n\tif a > 1 &&\n\t\ta < 5 {\n\n\t\treturn 1\n\n\t}\n\treturn 0\n}\n"
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(reformatted), 0o644)
	after, _, _ := GoFacts(dir, ".")
	b, a := before.Functions[0], after.Functions[0]
	if b.Cyclomatic != a.Cyclomatic || b.Statements != a.Statements || b.Nesting != a.Nesting || b.Params != a.Params {
		t.Fatalf("formatting changed metrics: %+v vs %+v", b, a)
	}
}

func TestGoImportsAndArchitecture(t *testing.T) {
	f, _, err := GoFacts("testdata/gomod", ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Imports) != 2 {
		t.Fatalf("only module-internal edges are kept: %+v", f.Imports)
	}
	text := func(p string, l int) string { return p + ":" + string(rune('0'+l)) }
	rules := ArchRules{Cycles: true,
		Layers: []Layer{{Name: "domain", Paths: []string{"internal/domain/**"}}, {Name: "infra", Paths: []string{"internal/infra/**"}}},
		Rules:  []LayerRule{{From: "domain", Deny: []string{"infra"}}}}
	out, unknown := ArchFindings(f, "shop", "imports", rules, text)
	var fams []string
	for _, x := range out {
		fams = append(fams, x.Family+"@"+x.Location.Path)
		if !x.Advisory {
			t.Error("architecture findings are advisory unless gated")
		}
	}
	got := strings.Join(fams, ",")
	if unknown != 0 || got != "arch/layer@internal/domain/order.go,arch/cycle@internal/domain/order.go" {
		t.Fatalf("%s (unknown %d)", got, unknown)
	}
	if !strings.Contains(out[1].Message, "internal/domain → internal/infra → internal/domain") {
		t.Fatalf("cycle message: %s", out[1].Message)
	}
}

func TestUnknownEdgesAreCounted(t *testing.T) {
	f := &finding.Facts{Nodes: finding.NodesFile, Imports: []finding.ImportFact{{From: "a.py", Spec: "importlib(x)", Line: 1, Provenance: finding.ProvenanceUnknown}}}
	_, unknown := ArchFindings(f, "p", "imports", ArchRules{Cycles: true}, func(string, int) string { return "" })
	if unknown != 1 {
		t.Fatal("unknown edge not counted")
	}
}

func TestMetricFindingsRespectThresholdsAndGate(t *testing.T) {
	f := &finding.Facts{Algorithm: Algorithm, Functions: []finding.FunctionFact{{Path: "a.go", Symbol: "f", Line: 3, Cyclomatic: 12, Params: 2}}}
	text := func(string, int) string { return "func f()" }
	out := MetricFindings(f, "p", "metrics", MetricRules{Thresholds: map[string]int{"cyclomatic": 10, "params": 5}}, text)
	if len(out) != 1 || out[0].Family != "metrics/cyclomatic" || !out[0].Advisory || out[0].Severity != finding.SevInfo || out[0].Metric.Value != 12 {
		t.Fatalf("%+v", out)
	}
	out = MetricFindings(f, "p", "metrics", MetricRules{Gate: true, Thresholds: map[string]int{"cyclomatic": 10}}, text)
	if out[0].Advisory || out[0].Severity != finding.SevWarning {
		t.Fatal("a gate rule makes metric findings blocking-capable")
	}
}
