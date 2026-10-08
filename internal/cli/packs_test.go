package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/testutil/gitrepo"
)

// skipLive skips a live test, unless PISTOS_REQUIRE_LIVE=1 (release CI) turns the skip into a failure.
func skipLive(t *testing.T, format string, a ...any) {
	t.Helper()
	if os.Getenv("PISTOS_REQUIRE_LIVE") == "1" {
		t.Fatalf("live coverage required: "+format, a...)
	}
	t.Skipf(format, a...)
}

func requireRuntimes(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			skipLive(t, "%s not on PATH; live %s coverage skipped", n, t.Name())
		}
	}
}

// setupPack initializes pistos in a fixture repository and runs `plugins install`, skipping the
// test when acquisition needs a network that is unavailable.
func setupPack(t *testing.T, fixture, gitignore string, edit func(r *gitrepo.Repo)) *gitrepo.Repo {
	t.Helper()
	t.Setenv("PISTOS_CACHE_DIR", t.TempDir())
	r := gitrepo.New(t, fixture)
	r.Write(".gitignore", gitignore)
	if edit != nil {
		edit(r)
	}
	if code, out, errs := run(t, r.Dir, "init"); code != 0 {
		t.Fatalf("init: %s %s", out, errs)
	}
	if code, out, errs := run(t, r.Dir, "plugins", "install"); code != 0 {
		if strings.Contains(out+errs, "acquir") || strings.Contains(out+errs, "npm") || strings.Contains(out+errs, "pip") {
			skipLive(t, "tool acquisition unavailable: %s %s", out, errs)
		}
		t.Fatalf("plugins install: %s %s", out, errs)
	}
	r.Commit("configure pistos")
	return r
}

func issueFamilies(env *finding.Envelope) map[string]finding.Issue {
	out := map[string]finding.Issue{}
	for _, is := range env.Canonical.Issues {
		out[is.Family] = is
	}
	return out
}

func requireStatus(t *testing.T, env *finding.Envelope, project, capName string, want finding.Status) {
	t.Helper()
	if c := capability(env, project, capName, "head"); c.Status != want {
		t.Fatalf("%s/%s: status %s (%s), want %s", project, capName, c.Status, c.Error, want)
	}
}

func TestPythonPackLive(t *testing.T) {
	requireRuntimes(t, "python3", "node", "npm")
	r := setupPack(t, "python-app", ".venv/\n", nil)

	r.Write("pkg/calc.py", "import os\n\n\ndef total(values: list[int]) -> int:\n    x: int = \"s\"\n    return sum(values)\n\n\ndef scaled(values: list[int], factor: int = 2, seen=[]) -> list[int]:\n    return [v * factor for v in values]\n")
	code, env, errs := runReview(t, r.Dir)
	if code != ExitBlocking {
		t.Fatalf("code %d %s", code, errs)
	}
	requireStatus(t, env, "python", "ruff", finding.StatusComplete)
	requireStatus(t, env, "python", "pyright", finding.StatusComplete)
	fams := issueFamilies(env)
	for _, want := range []string{"python/unused-import", "pyright/reportAssignmentType", "ruff/B006"} {
		if _, ok := fams[want]; !ok {
			t.Errorf("missing %s (ruff config extend-select must be honoured): %v", want, keys(fams))
		}
	}
	if is := fams["pyright/reportAssignmentType"]; is.Anchor.Symbol != "total" || is.Location.Line != 5 {
		t.Errorf("pyright location/symbol: %+v", is.Location)
	}

	r.Write("pkg/calc.py", strings.Replace(r.Read("pkg/calc.py"), "import os\n", "", 1))
	r.Write("pkg/calc.py", strings.Replace(r.Read("pkg/calc.py"), "    x: int = \"s\"\n", "", 1))
	r.Write("pkg/calc.py", strings.Replace(r.Read("pkg/calc.py"), ", seen=[]", "", 1))
	r.Write("pkg/extra.py", "def double(v: int) -> int:\n    return v * 2\n")
	if code, env, _ := runReview(t, r.Dir); code != ExitOK || len(env.Canonical.Issues) != 0 {
		t.Fatalf("clean change must pass: %d %+v", code, env.Canonical.Issues)
	}
}

func TestPythonUnsupportedBackendIsExplicit(t *testing.T) {
	requireRuntimes(t, "python3")
	r := setupPack(t, "python-app", "", func(r *gitrepo.Repo) {
		r.Write("pyproject.toml", strings.Replace(r.Read("pyproject.toml"), "[tool.pyright]\ntypeCheckingMode = \"standard\"\n", "[tool.ty]\n", 1))
	})
	if cfg := r.Read(".pistos/config.toml"); !strings.Contains(cfg, `checks = ["ruff", "ty", "metrics"]`) {
		t.Fatalf("init must keep the repository's ty selection, not substitute pyright:\n%s", cfg)
	}
	r.Write("pkg/extra.py", "X = 1\n")
	code, env, _ := runReview(t, r.Dir)
	c := capability(env, "python", "ty", "head")
	if code != ExitError || c.Status != finding.StatusUnsupported || !strings.Contains(c.Error, "not integrated") {
		t.Fatalf("unsupported backend must be explicit: %d %+v", code, c)
	}
}

var (
	jsDepsOnce sync.Once
	jsDepsDir  string
	jsDepsErr  error
)

// jsDependencies installs the fixture's devDependencies once and shares them between tests.
func jsDependencies(t *testing.T) string {
	t.Helper()
	jsDepsOnce.Do(func() {
		dir := filepath.Join(os.Getenv("PISTOS_TOOLS_DIR"), "jsts-fixture-deps")
		if _, err := os.Stat(filepath.Join(dir, "node_modules", "typescript")); err == nil {
			jsDepsDir = dir
			return
		}
		os.MkdirAll(dir, 0o755)
		b, _ := os.ReadFile(filepath.Join(gitrepo.FixturePath("jsts-app"), "package.json"))
		os.WriteFile(filepath.Join(dir, "package.json"), b, 0o644)
		cmd := exec.Command("npm", "install", "--no-audit", "--no-fund", "--ignore-scripts")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			jsDepsErr = fmt.Errorf("npm install: %v: %s", err, out)
			return
		}
		jsDepsDir = dir
	})
	if jsDepsErr != nil {
		skipLive(t, "JS fixture dependencies unavailable: %v", jsDepsErr)
	}
	return filepath.Join(jsDepsDir, "node_modules")
}

func TestJSTSPackLive(t *testing.T) {
	requireRuntimes(t, "node", "npm")
	deps := jsDependencies(t)
	r := setupPack(t, "jsts-app", "node_modules\n", func(r *gitrepo.Repo) {
		if err := os.Symlink(deps, filepath.Join(r.Dir, "node_modules")); err != nil {
			t.Fatal(err)
		}
	})
	if cfg := r.Read(".pistos/config.toml"); !strings.Contains(cfg, `checks = ["tsc", "eslint", "metrics"]`) {
		t.Fatalf("init must keep the repository's ESLint rather than substitute another linter:\n%s", cfg)
	}
	r.Write(".pistos/config.toml", strings.Replace(r.Read(".pistos/config.toml"), `checks = ["tsc", "eslint", "metrics"]`, `checks = ["tsc", "eslint", "oxlint", "metrics"]`, 1))
	if code, out, errs := run(t, r.Dir, "plugins", "install"); code != 0 {
		t.Fatalf("%s %s", out, errs)
	}
	r.Commit("add oxlint")

	r.Write("src/math.ts", "export function add(a: number, b: number): number {\n  return a + b + \"x\";\n}\nexport function loose(x) { return x; }\n")
	r.Write("src/util.js", "export function same(a, b) {\n  const tmp = 2;\n  return a == b;\n}\n")
	code, env, errs := runReview(t, r.Dir)
	if code != ExitBlocking {
		t.Fatalf("code %d %s %+v", code, errs, env.Canonical.Coverage.Capabilities)
	}
	for _, c := range []string{"tsc", "eslint", "oxlint"} {
		requireStatus(t, env, "jsts", c, finding.StatusComplete)
	}
	fams := issueFamilies(env)
	for _, want := range []string{"tsc/TS2322", "tsc/TS7006", "eslint/eqeqeq", "js/unused-variable"} {
		if _, ok := fams[want]; !ok {
			t.Errorf("missing %s: %v", want, keys(fams))
		}
	}
	if len(fams["js/unused-variable"].Sources) != 2 {
		t.Errorf("eslint and oxlint must merge on the same cause: %+v", fams["js/unused-variable"].Sources)
	}

	r.Write("tsconfig.json", strings.Replace(r.Read("tsconfig.json"), `"strict": true`, `"strict": false`, 1))
	_, env, _ = runReview(t, r.Dir)
	if _, ok := issueFamilies(env)["tsc/TS7006"]; ok {
		t.Error("the repository's tsconfig (strict: false) must be honoured")
	}
}

func TestJSTSMissingRepositoryCompilerIsExplicit(t *testing.T) {
	requireRuntimes(t, "node")
	t.Setenv("PISTOS_CACHE_DIR", t.TempDir())
	r := gitrepo.New(t, "jsts-app")
	run(t, r.Dir, "init")
	code, out, _ := run(t, r.Dir, "plugins", "install")
	if code != ExitError || !strings.Contains(out, "tsc") || !strings.Contains(out, "not found") {
		t.Fatalf("missing repository TypeScript must be reported, not replaced: %d\n%s", code, out)
	}
}

func TestCSharpPackLive(t *testing.T) {
	requireRuntimes(t, "dotnet")
	r := setupPack(t, "csharp-app", "bin/\nobj/\n", nil)
	r.Write("Lib/Greeter.cs", "namespace Lib;\n\npublic class Greeter\n{\n    public string Greet(string name)\n    {\n        int unused;\n        string? maybe = name.Length > 3 ? null : name;\n        return $\"hello, {maybe.Length}\";\n    }\n}\n")
	r.Write("App/Program.cs", strings.Replace(r.Read("App/Program.cs"), "Lib.Greeter.Greet(name)", "new Lib.Greeter().Greet(name)", 1))

	code, env, errs := runReview(t, r.Dir)
	if code != ExitBlocking {
		t.Fatalf("code %d %s", code, errs)
	}
	requireStatus(t, env, "csharp-app", "dotnet-build", finding.StatusComplete)
	requireStatus(t, env, "csharp-lib", "dotnet-build", finding.StatusComplete)
	fams := issueFamilies(env)
	for _, want := range []string{"dotnet-build/CS0168", "dotnet-build/CS8602", "dotnet-build/CA1822"} {
		is, ok := fams[want]
		if !ok {
			t.Errorf("missing %s (CA1822 comes from .editorconfig): %v", want, keys(fams))
			continue
		}
		if is.Project != "csharp-lib" {
			t.Errorf("%s attributed to %s; diagnostics belong to the project that owns the file", want, is.Project)
		}
	}
	if len(env.Canonical.Issues) != 3 {
		t.Errorf("referenced-project diagnostics must not be duplicated: %d issues", len(env.Canonical.Issues))
	}
	for _, is := range env.Canonical.Issues {
		if strings.Contains(is.Location.Path, "obj/") {
			t.Errorf("generated sources must not surface as findings: %s", is.Location.Path)
		}
	}

	// A rebuild of an unchanged tree after cache loss must reanalyze, not report a silent clean.
	os.RemoveAll(filepath.Join(r.Dir, ".pistos", "cache", "results"))
	code, again, _ := runReview(t, r.Dir)
	if code != ExitBlocking || len(again.Canonical.Issues) != 3 {
		t.Fatalf("no-op rebuild lost diagnostics: %d %d", code, len(again.Canonical.Issues))
	}
}

func TestCSharpMultiTargetLive(t *testing.T) {
	requireRuntimes(t, "dotnet")
	nuget := filepath.Join(os.Getenv("PISTOS_TOOLS_DIR"), "nuget")
	t.Setenv("NUGET_PACKAGES", nuget)
	r := setupPack(t, "csharp-app", "bin/\nobj/\n", func(r *gitrepo.Repo) {
		r.Write("Lib/Lib.csproj", strings.Replace(r.Read("Lib/Lib.csproj"), "<TargetFramework>net10.0</TargetFramework>", "<TargetFrameworks>net10.0;net8.0</TargetFrameworks>", 1))
	})
	// Explicit dependency acquisition, as a developer would run before reviewing.
	restore := exec.Command("dotnet", "restore", "Lib/Lib.csproj")
	restore.Dir = r.Dir
	if out, err := restore.CombinedOutput(); err != nil {
		skipLive(t, "net8.0 reference pack unavailable (offline?): %v %s", err, out)
	}
	r.Write("Lib/Greeter.cs", "namespace Lib;\n\npublic static class Greeter\n{\n    public static string Greet(string name)\n    {\n#if NET10_0\n        int onlyOnNet10;\n#endif\n        int everywhere;\n        return $\"hello, {name}\";\n    }\n}\n")
	code, env, errs := runReview(t, r.Dir)
	if code != ExitBlocking {
		t.Fatalf("code %d %s", code, errs)
	}
	var lines []int
	for _, is := range env.Canonical.Issues {
		if is.Project == "csharp-lib" && is.Family == "dotnet-build/CS0168" {
			lines = append(lines, is.Location.Line)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("want the shared diagnostic once and the net10.0-only one once, got lines %v", lines)
	}
}

func TestMixedRepositoryStartsOnlyAffectedPacks(t *testing.T) {
	requireRuntimes(t, "python3")
	useGoToolchain(t)
	r := gitrepo.New(t, "mixed")
	run(t, r.Dir, "init")
	if code, out, errs := run(t, r.Dir, "plugins", "install"); code != 0 {
		skipLive(t, "tool acquisition unavailable: %s %s", out, errs)
	}
	r.Commit("configure")

	r.Write("svc/main.go", r.Read("svc/main.go")+"\n// go-only change\n")
	_, env, _ := runReview(t, r.Dir)
	for _, task := range env.Telemetry.Tasks {
		if !strings.HasPrefix(task.Task, "go-svc/") {
			t.Fatalf("a Go-only change started %s", task.Task)
		}
	}
	if len(env.Telemetry.Tasks) == 0 {
		t.Fatal("the Go project must be analyzed")
	}
	r.Commit("go change")

	r.Write("tools/report.py", r.Read("tools/report.py")+"\n\ndef extra() -> int:\n    return 1\n")
	_, env, _ = runReview(t, r.Dir)
	for _, task := range env.Telemetry.Tasks {
		if !strings.HasPrefix(task.Task, "python-tools/") {
			t.Fatalf("a Python-only change started %s", task.Task)
		}
	}
}

func keys(m map[string]finding.Issue) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
