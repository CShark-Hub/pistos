package plugin

import (
	"strings"
	"testing"
)

// Captured from the snap-packaged ruff 0.16.10, which cannot read files outside its sandbox.
func TestRuffSandboxedSilentCleanIsIncomplete(t *testing.T) {
	p, err := Parse("ruff-json", Output{Stdout: []byte("[]"), Stderr: []byte("warning: Encountered error: Permission denied (os error 13)\n")})
	if err != nil || p.Incomplete == "" {
		t.Fatalf("an unreadable tree must not look clean: %+v %v", p, err)
	}
}

func TestRuffDiagnostics(t *testing.T) {
	out := Output{ExitCode: 1, Stdout: []byte(`[{"cell":null,"code":"F401","end_location":{"column":10,"row":1},"filename":"/w/pkg/mod.py","fix":null,"location":{"column":8,"row":1},"message":"` + "`os`" + ` imported but unused","name":"unused-import","noqa_row":1,"severity":"error","url":""},
{"cell":null,"code":null,"end_location":{"column":1,"row":2},"filename":"/w/pkg/bad.py","fix":null,"location":{"column":12,"row":1},"message":"Expected a parameter","name":"invalid-syntax","noqa_row":null,"severity":"error","url":null}]`)}
	p, err := Parse("ruff-json", out)
	if err != nil || len(p.Diagnostics) != 2 || p.Diagnostics[0].Rule != "F401" || p.Diagnostics[1].Rule != "invalid-syntax" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestPyrightZeroBasedRanges(t *testing.T) {
	out := Output{ExitCode: 1, Stdout: []byte(`{"version":"1.1.414","generalDiagnostics":[{"file":"/w/pkg/mod.py","severity":"error","message":"Type mismatch\n  detail","range":{"start":{"line":4,"character":13},"end":{"line":4,"character":16}},"rule":"reportAssignmentType"},{"file":"/w/pkg/mod.py","severity":"information","message":"note","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}],"summary":{"filesAnalyzed":1,"errorCount":1}}`)}
	p, err := Parse("pyright-json", out)
	if err != nil || len(p.Diagnostics) != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	d := p.Diagnostics[0]
	if d.Line != 5 || d.Column != 14 || d.Message != "Type mismatch" || p.Diagnostics[1].Severity != "info" || p.Diagnostics[1].Rule != "general" {
		t.Fatalf("%+v", p.Diagnostics)
	}
	p, _ = Parse("pyright-json", Output{Stdout: []byte(`{"generalDiagnostics":[],"summary":{"filesAnalyzed":0}}`)})
	if p.Incomplete == "" {
		t.Fatal("zero analyzed files must be incomplete")
	}
}

func TestTscText(t *testing.T) {
	p, err := Parse("tsc-text", Output{ExitCode: 1, Stdout: []byte("src/a.ts(1,7): error TS2322: Type 'string' is not assignable to type 'number'.\n  The expected type comes from here.\nsrc/b.ts(2,1): warning TS6133: 'x' is declared but its value is never read.\n")})
	if err != nil || len(p.Diagnostics) != 2 || p.Diagnostics[0].Rule != "TS2322" || p.Diagnostics[1].Severity != "warning" {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = Parse("tsc-text", Output{ExitCode: 1, Stdout: []byte("error TS5058: The specified path does not exist: 'tsconfig.json'.\n")})
	if err != nil || !strings.Contains(p.Incomplete, "TS5058") {
		t.Fatalf("project-level errors must be incomplete: %+v %v", p, err)
	}
	if _, err := Parse("tsc-text", Output{ExitCode: 1, Stdout: []byte("Segmentation fault\n")}); err == nil {
		t.Fatal("unrecognized failure must fail")
	}
}

func TestESLintAndOxlint(t *testing.T) {
	p, err := Parse("eslint-json", Output{ExitCode: 1, Stdout: []byte(`[{"filePath":"/w/src/b.js","messages":[{"ruleId":"no-unused-vars","severity":2,"message":"unused","line":2,"column":10,"endLine":2,"endColumn":16},{"ruleId":null,"fatal":true,"severity":2,"message":"Parsing error: Unexpected token","line":1,"column":5}]}]`)})
	if err != nil || len(p.Diagnostics) != 2 || p.Diagnostics[1].Rule != "parse-error" || p.Diagnostics[0].Severity != "error" {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = Parse("oxlint-json", Output{Stdout: []byte(`{ "diagnostics": [{"message": "unused","code": "eslint(no-unused-vars)","severity": "warning","filename": "src/b.js","labels": [{"span": {"offset": 30,"length": 6,"line": 2,"column": 10}}]}], "number_of_files": 2, "number_of_rules": 96}`)})
	if err != nil || len(p.Diagnostics) != 1 || p.Diagnostics[0].Rule != "eslint/no-unused-vars" || p.Diagnostics[0].Line != 2 {
		t.Fatalf("%+v %v", p, err)
	}
}

const sarifOne = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"Microsoft (R) Visual C# Compiler","version":"5.9.0"}},"results":[
{"ruleId":"CS0168","level":"warning","message":{"text":"The variable 'unused' is declared but never used"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"file:///w/Lib/Greeter.cs"},"region":{"startLine":7,"startColumn":13,"endLine":7,"endColumn":19}}}]},
{"ruleId":"CS0219","level":"warning","message":{"text":"suppressed"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"file:///w/Lib/Greeter.cs"},"region":{"startLine":8,"startColumn":1}}}],"suppressions":[{"kind":"inSource"}]},
{"ruleId":"CA1016","level":"note","message":{"text":"assembly-level"}}]}]}`

func TestSARIFArtifacts(t *testing.T) {
	p, err := Parse("sarif-artifacts", Output{Artifacts: [][]byte{[]byte(sarifOne), []byte(sarifOne)}})
	if err != nil || len(p.Diagnostics) != 2 || p.Diagnostics[0].Path != "/w/Lib/Greeter.cs" || len(p.Limitations) != 1 {
		t.Fatalf("suppressed results dropped, unanchored counted, one entry per target framework: %+v %v", p, err)
	}
	if p, _ := Parse("sarif-artifacts", Output{}); p.Incomplete == "" {
		t.Fatal("a build without a diagnostics log must not look clean")
	}
	if _, err := Parse("sarif-artifacts", Output{ExitCode: 1, Stdout: []byte("error NU1101: Unable to find package Foo\n")}); err == nil || !strings.Contains(err.Error(), "NU1101") {
		t.Fatalf("restore failure must fail with its message: %v", err)
	}
	p, _ = Parse("sarif-artifacts", Output{ExitCode: 1, Artifacts: [][]byte{[]byte(sarifOne)}})
	if p.Incomplete == "" {
		t.Fatal("a failed build without compiler errors is incomplete")
	}
}
