package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/review"
	"example.com/pistos/internal/testutil/gitrepo"
)

func TestImportGraphsAcrossLanguagesLive(t *testing.T) {
	requireRuntimes(t, "python3", "node", "npm", "dotnet")
	cfg := "schema = 1\nlanguages = [\"python\", \"ts\", \"csharp\"]\n" +
		"\n[[projects]]\nid = \"py\"\nroot = \"py\"\necosystem = \"python\"\nchecks = [\"imports\"]\n" +
		"\n[[projects]]\nid = \"ts\"\nroot = \"ts\"\necosystem = \"ts\"\nchecks = [\"imports\"]\n" +
		"\n[[projects]]\nid = \"cs\"\nroot = \"cs\"\necosystem = \"csharp\"\nchecks = [\"imports\"]\n"
	r := setupPackWithConfig(t, "metrics-langs", cfg)
	r.Write("py/pkg/__init__.py", "")
	r.Write("py/pkg/a.py", "from pkg import b\n\nVALUE = 1\n")
	r.Write("py/pkg/b.py", "import importlib\n\nfrom pkg import a\n\nplugin = importlib.import_module(a.NAME)\n")
	r.Write("ts/a.ts", "import { b } from \"./b.js\";\nexport const a = 1 + b;\n")
	r.Write("ts/b.ts", "import { a } from \"./a\";\nexport const b = 2;\nexport const lazy = (n: string) => import(n);\nvoid a;\n")
	r.Write("cs/A/Alpha.cs", "using Shop.B;\n\nnamespace Shop.A;\n\npublic class Alpha { public Beta? Next; }\n")
	r.Write("cs/B/Beta.cs", "using Shop.A;\n\nnamespace Shop.B;\n\npublic class Beta { public Alpha? Prev; }\n")

	_, env, errs := runReview(t, r.Dir)
	cycles := map[string]finding.Issue{}
	for _, is := range env.Canonical.Issues {
		if is.Family == "arch/cycle" {
			cycles[is.Project] = is
		}
	}
	want := map[string]string{
		"py": "py/pkg/a.py → py/pkg/b.py → py/pkg/a.py",
		"ts": "ts/a.ts → ts/b.ts → ts/a.ts",
		"cs": "cs/A → cs/B → cs/A",
	}
	for p, path := range want {
		if !strings.Contains(cycles[p].Message, path) {
			t.Errorf("%s cycle: %q (want %s) %s", p, cycles[p].Message, path, errs)
		}
	}
	for _, p := range []string{"py", "ts"} {
		c := capability(env, p, "imports", "head")
		if !strings.Contains(strings.Join(c.Limitations, ";"), "1 unresolved") {
			t.Errorf("%s: the dynamic import must be counted as unresolved: %v", p, c.Limitations)
		}
	}
}

func factsQuery(t *testing.T, dir string, args ...string) *review.FactsResult {
	t.Helper()
	code, out, errs := run(t, dir, append([]string{"facts"}, append(args, "--format", "json")...)...)
	var res review.FactsResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("facts %v: code %d %s %s", args, code, out, errs)
	}
	return &res
}

func locationsOf(r *review.FactsResult) string {
	var out []string
	for _, l := range r.Locations {
		out = append(out, l.Path+":"+strconv.Itoa(l.Line))
	}
	return strings.Join(out, ",")
}

func installLSP(t *testing.T, r *gitrepo.Repo) {
	t.Helper()
	if code, out, errs := run(t, r.Dir, "plugins", "install", "--lsp"); code != 0 {
		skipLive(t, "language server acquisition unavailable: %s %s", out, errs)
	}
}

func TestGoFactsLive(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	installLSP(t, r)
	refs := factsQuery(t, r.Dir, "references", "main.go:5:6")
	if refs.Status != finding.StatusComplete || refs.Backend != "gopls" || locationsOf(refs) != "main.go:5,main.go:10" {
		t.Fatalf("references: %+v", refs)
	}
	def := factsQuery(t, r.Dir, "definition", "main.go:10:15")
	if locationsOf(def) != "main.go:5" {
		t.Fatalf("definition: %+v", def)
	}
	if d := factsQuery(t, r.Dir, "diagnostics", "main.go"); d.Status != finding.StatusIncomplete || !strings.Contains(d.Reason, "pushes") {
		t.Fatalf("gopls only pushes diagnostics, so they cannot be complete: %+v", d)
	}
}

func TestPythonFactsLive(t *testing.T) {
	requireRuntimes(t, "python3", "node", "npm")
	r := setupPack(t, "python-app", "", nil)
	installLSP(t, r)
	refs := factsQuery(t, r.Dir, "references", "pkg/calc.py:1:5")
	if refs.Status != finding.StatusComplete || !strings.Contains(locationsOf(refs), "tests/test_calc.py:5") || !strings.Contains(locationsOf(refs), "pkg/calc.py:1") {
		t.Fatalf("references must map to original paths: %+v", refs)
	}
}

func TestTypeScriptFactsLive(t *testing.T) {
	requireRuntimes(t, "node", "npm")
	deps := jsDependencies(t)
	r := setupPack(t, "jsts-app", "node_modules\n", func(r *gitrepo.Repo) {
		os.Symlink(deps, filepath.Join(r.Dir, "node_modules"))
		r.Write("src/use.ts", "import { add } from \"./math.js\";\nexport const three = add(1, 2);\n")
	})
	installLSP(t, r)
	refs := factsQuery(t, r.Dir, "references", "src/math.ts:1:17")
	if refs.Status != finding.StatusComplete || locationsOf(refs) != "src/math.ts:1,src/use.ts:1,src/use.ts:2" {
		t.Fatalf("references: %+v", refs)
	}
	r.Write("src/math.ts", "export function add(a: number, b: number): number {\n  return a + b + \"x\";\n}\n")
	d := factsQuery(t, r.Dir, "diagnostics", "src/math.ts")
	if d.Status != finding.StatusComplete || len(d.Diagnostics) != 1 || d.Diagnostics[0].Code != "2322" || d.Diagnostics[0].Line != 2 {
		t.Fatalf("TypeScript supports pull diagnostics: %+v", d)
	}
}

func TestCSharpFactsLive(t *testing.T) {
	requireRuntimes(t, "dotnet")
	r := setupPack(t, "csharp-app", "bin/\nobj/\n", nil)
	installLSP(t, r)
	def := factsQuery(t, r.Dir, "definition", "App/Program.cs:11:39")
	if def.Status != finding.StatusComplete || locationsOf(def) != "Lib/Greeter.cs:5" {
		t.Fatalf("cross-project definition must map to the original path: %+v", def)
	}
}

func TestReviewNeverStartsLanguageServers(t *testing.T) {
	r := setupGo(t, "go-clean", configFor(goProject))
	r.Write("main.go", r.Read("main.go")+"\n// edit\n")
	if code, _, errs := runReview(t, r.Dir); code != ExitOK {
		t.Fatalf("review needs no language server: %s", errs)
	}
	res := factsQuery(t, r.Dir, "references", "main.go:5:6")
	if res.Status != finding.StatusIncomplete || !strings.Contains(res.Reason, "gopls") || !strings.Contains(res.Reason, "--lsp") {
		t.Fatalf("an unlocked server is never started implicitly: %+v", res)
	}
}
