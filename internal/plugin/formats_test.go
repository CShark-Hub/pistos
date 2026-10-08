package plugin

import (
	"strings"
	"testing"

	"example.com/pistos/internal/fsutil"
)

const vetMixed = `# ex.com/a/p
vet: p/p.go:3:23: cannot use "x" (untyped string constant) as int value in return statement
{
	"ex.com/a/q": {
		"printf": [
			{
				"posn": "/work/q/q.go:5:24",
				"end": "/work/q/q.go:5:26",
				"message": "fmt.Printf format %d has arg \"s\" of wrong type string"
			}
		]
	}
}
`

func TestParseGoVetMixedStreams(t *testing.T) {
	for name, out := range map[string]Output{
		"go1.27 stderr": {Stderr: []byte(vetMixed), ExitCode: 1},
		"go1.26 stdout": {Stdout: []byte(vetMixed[strings.Index(vetMixed, "{"):]), Stderr: []byte(vetMixed[:strings.Index(vetMixed, "{")]), ExitCode: 1},
	} {
		t.Run(name, func(t *testing.T) {
			p, err := Parse("go-vet", out)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Diagnostics) != 2 || p.Incomplete != "" {
				t.Fatalf("%+v", p)
			}
			var rules []string
			for _, d := range p.Diagnostics {
				rules = append(rules, d.Rule+"@"+d.Path)
			}
			got := strings.Join(rules, ",")
			if !strings.Contains(got, "typecheck@p/p.go") || !strings.Contains(got, "printf@/work/q/q.go") {
				t.Fatal(got)
			}
		})
	}
}

func TestParseGoVetLoadErrorIsIncomplete(t *testing.T) {
	p, err := Parse("go-vet", Output{Stderr: []byte("r/r.go:3:8: cannot find module providing package github.com/nope/zzz: import lookup disabled by -mod=readonly\n"), ExitCode: 1})
	if err != nil || p.Incomplete == "" || len(p.Diagnostics) != 1 || p.Diagnostics[0].Rule != "load" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestParseGoVetFailureWithoutDiagnostics(t *testing.T) {
	_, err := Parse("go-vet", Output{Stderr: []byte("go: go.mod requires go >= 1.99 (running go 1.27.2; GOTOOLCHAIN=local)\n"), ExitCode: 1})
	if err == nil || !strings.Contains(err.Error(), "go.mod requires") {
		t.Fatalf("want failure, got %v", err)
	}
}

func TestParseStaticcheck(t *testing.T) {
	out := Output{ExitCode: 1, Stdout: []byte(`{"code":"SA5009","severity":"error","location":{"file":"/w/a.go","line":6,"column":21},"end":{"file":"/w/a.go","line":6,"column":25},"message":"Printf format %s has arg #1 of wrong type int"}
{"code":"compile","severity":"error","location":{"file":"","line":0,"column":0},"end":{"file":"","line":0,"column":0},"message":"# ex.com/a/p\np/p.go:3:23: cannot use \"x\" (untyped string constant) as int value in return statement"}
`)}
	p, err := Parse("staticcheck-json", out)
	if err != nil || len(p.Diagnostics) != 2 || p.Diagnostics[1].Path != "p/p.go" || p.Incomplete != "" {
		t.Fatalf("%+v %v", p, err)
	}
}

// Captured from staticcheck 0.8.1 run against a Go 1.27.2 toolchain.
func TestParseStaticcheckIncompatibleToolchain(t *testing.T) {
	out := Output{ExitCode: 1, Stdout: []byte(`{"code":"compile","severity":"error","location":{"file":"","line":0,"column":0},"end":{"file":"","line":0,"column":0},"message":"internal error in importing \"internal/cpu\" (cannot decode \"internal/cpu\", export data version 5 is greater than maximum supported version 4); please report an issue"}
`)}
	p, err := Parse("staticcheck-json", out)
	if err != nil || !strings.Contains(p.Incomplete, "incompatible") {
		t.Fatalf("incompatible toolchain must be incomplete, got %+v %v", p, err)
	}
}

func TestParseStaticcheckMalformed(t *testing.T) {
	if _, err := Parse("staticcheck-json", Output{Stdout: []byte("{oops\n"), ExitCode: 1}); err == nil {
		t.Fatal("malformed output must fail")
	}
}

func TestBundledManifestsLoad(t *testing.T) {
	m, err := LoadManifest("go")
	if err != nil {
		t.Fatal(err)
	}
	if m.Family("staticcheck", "SA5009") != "go-vet/printf" || !m.IsTestPath("pkg/x_test.go") || m.IsTestPath("pkg/x.go") {
		t.Fatal("aliases or test globs broken")
	}
}

func TestMatchGlob(t *testing.T) {
	for _, tc := range []struct {
		pat, name string
		want      bool
	}{
		{"**/*_test.go", "a_test.go", true},
		{"**/*_test.go", "x/y/a_test.go", true},
		{"**/testdata/**", "x/testdata/f.go", true},
		{"**/testdata/**", "x/data/f.go", false},
		{"tests/**", "tests/a/b.py", true},
	} {
		if got := fsutil.MatchGlob(tc.pat, tc.name); got != tc.want {
			t.Errorf("%s %s = %v", tc.pat, tc.name, got)
		}
	}
}
