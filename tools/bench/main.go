// Command bench measures pistos latency and memory on fixture repositories and on pistos itself.
// Run from the repository root: go run ./tools/bench -pistos <binary> [-out docs/benchmarks]
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type scenario struct {
	Name    string
	Fixture string
	Config  string
	Edit    func(dir string, i int) error
	Prepare func(dir string) error
}

type measure struct {
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	Runs    int     `json:"runs"`
	PeakRSS int64   `json:"peak_total_rss_kb"`
}

type result struct {
	Scenario      string  `json:"scenario"`
	Files         int     `json:"files"`
	Lines         int     `json:"lines"`
	InstallMS     float64 `json:"install_ms"`
	NoOp          measure `json:"noop_review"`
	Cold          measure `json:"cold_review"`
	Warm          measure `json:"warm_function_edit"`
	Deterministic bool    `json:"three_run_canonical_identical"`
	Error         string  `json:"error,omitempty"`
}

var pistos string

func main() {
	flag.StringVar(&pistos, "pistos", "", "pistos binary to measure")
	out := flag.String("out", "", "directory for results.json and results.md")
	only := flag.String("only", "", "comma-separated scenario names")
	flag.Parse()
	if pistos == "" {
		fmt.Fprintln(os.Stderr, "-pistos is required")
		os.Exit(2)
	}
	pistos, _ = filepath.Abs(pistos)
	root, _ := os.Getwd()
	cache, _ := os.MkdirTemp("", "pistos-bench-cache-")
	defer os.RemoveAll(cache)
	os.Setenv("PISTOS_CACHE_DIR", cache)
	os.Setenv("PISTOS_TOOLS_DIR", filepath.Join(cache, "tools"))

	startup := timeRuns(25, func() error { _, err := runPistos("", "--version"); return err }, nil)
	var results []result
	for _, s := range scenarios(root) {
		if *only != "" && !strings.Contains(","+*only+",", ","+s.Name+",") {
			continue
		}
		fmt.Fprintf(os.Stderr, "== %s\n", s.Name)
		results = append(results, runScenario(root, s))
	}
	env := environment()
	report := map[string]any{"environment": env, "startup": startup, "scenarios": results}
	b, _ := json.MarshalIndent(report, "", "  ")
	if *out == "" {
		os.Stdout.Write(b)
		return
	}
	os.MkdirAll(*out, 0o755)
	os.WriteFile(filepath.Join(*out, "results.json"), append(b, '\n'), 0o644)
	os.WriteFile(filepath.Join(*out, "results.md"), []byte(markdown(env, startup, results)), 0o644)
}

func scenarios(root string) []scenario {
	appendLine := func(file, line string) func(dir string, i int) error {
		return func(dir string, i int) error {
			p := filepath.Join(dir, file)
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(p, append(b, []byte(fmt.Sprintf(line, i))...), 0o644)
		}
	}
	linkDeps := func(dir string) error {
		deps := filepath.Join(os.Getenv("PISTOS_TOOLS_DIR"), "..", "jsdeps")
		if _, err := os.Stat(filepath.Join(deps, "node_modules")); err != nil {
			os.MkdirAll(deps, 0o755)
			b, _ := os.ReadFile(filepath.Join(dir, "package.json"))
			os.WriteFile(filepath.Join(deps, "package.json"), b, 0o644)
			cmd := exec.Command("npm", "install", "--no-audit", "--no-fund", "--ignore-scripts")
			cmd.Dir = deps
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("npm install: %v %s", err, out)
			}
		}
		return os.Symlink(filepath.Join(deps, "node_modules"), filepath.Join(dir, "node_modules"))
	}
	return []scenario{
		{Name: "go-small", Fixture: "testdata/fixtures/go-clean",
			Config: project("go", ".", `"go-vet", "staticcheck", "metrics"`),
			Edit:   appendLine("main.go", "\nfunc edit%d(n int) int { return n + 1 }\n")},
		{Name: "go-medium (pistos itself)", Fixture: ".",
			Config: project("go", ".", `"go-vet", "staticcheck", "metrics", "imports"`),
			Edit:   appendLine("internal/fsutil/fsutil.go", "\nfunc benchEdit%d(n int) int { return n + 1 }\n")},
		{Name: "python-small", Fixture: "testdata/fixtures/python-app",
			Config: project("python", ".", `"ruff", "pyright", "metrics"`),
			Edit:   appendLine("pkg/calc.py", "\n\ndef edit_%d(n: int) -> int:\n    return n + 1\n")},
		{Name: "jsts-small", Fixture: "testdata/fixtures/jsts-app", Prepare: linkDeps,
			Config: project("ts", ".", `"tsc", "eslint", "metrics"`),
			Edit:   appendLine("src/math.ts", "\nexport function edit%d(n: number): number { return n + 1; }\n")},
		{Name: "csharp-small", Fixture: "testdata/fixtures/csharp-app",
			Config: project("csharp", "Lib", `"dotnet-build", "metrics"`),
			Edit:   appendLine("Lib/Greeter.cs", "\npublic static class Edit%d { public static int F(int n) => n + 1; }\n")},
	}
}

func project(eco, root, checks string) string {
	return fmt.Sprintf("schema = 1\nlanguages = [%q]\n\n[execution]\njobs = 4\n\n[[projects]]\nid = \"p\"\nroot = %q\necosystem = %q\nchecks = [%s]\n", eco, root, eco, checks)
}

func runScenario(root string, s scenario) result {
	r := result{Scenario: s.Name}
	dir, _ := os.MkdirTemp("", "pistos-bench-repo-")
	defer os.RemoveAll(dir)
	fail := func(err error) result { r.Error = err.Error(); return r }
	if err := copyTree(filepath.Join(root, s.Fixture), dir); err != nil {
		return fail(err)
	}
	if s.Prepare != nil {
		if err := s.Prepare(dir); err != nil {
			return fail(err)
		}
	}
	os.MkdirAll(filepath.Join(dir, ".pistos"), 0o755)
	os.WriteFile(filepath.Join(dir, ".pistos", "config.toml"), []byte(s.Config), 0o644)
	os.WriteFile(filepath.Join(dir, ".pistos", ".gitignore"), []byte("cache/\nstate/\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules\nbin/\nobj/\n"), 0o644)
	r.Files, r.Lines = sourceSize(dir)
	if err := git(dir, "init", "-q", "-b", "main"); err != nil {
		return fail(err)
	}
	git(dir, "add", "-A")
	git(dir, "-c", "user.name=b", "-c", "user.email=b@b", "commit", "-qm", "base")

	start := time.Now()
	if out, err := runPistos(dir, "plugins", "install"); err != nil {
		return fail(fmt.Errorf("install: %v %s", err, out))
	}
	r.InstallMS = ms(time.Since(start))
	git(dir, "add", "-A")
	git(dir, "-c", "user.name=b", "-c", "user.email=b@b", "commit", "-qm", "lock")

	r.NoOp = timeRuns(10, func() error { _, err := runPistos(dir, "review"); return err }, nil)
	clear := func() {
		os.RemoveAll(filepath.Join(dir, ".pistos", "cache"))
		os.RemoveAll(filepath.Join(os.Getenv("PISTOS_CACHE_DIR"), "trees"))
	}
	if err := s.Edit(dir, 0); err != nil {
		return fail(err)
	}
	r.Cold = timeRuns(3, func() error { _, err := runPistos(dir, "review", "--all"); return err }, clear)
	i := 1
	r.Warm = timeRuns(5, func() error { _, err := runPistos(dir, "review"); return err }, func() { s.Edit(dir, i); i++ })

	var first []byte
	r.Deterministic = true
	for k := 0; k < 3; k++ {
		clear()
		out, _ := runPistos(dir, "review", "--all", "--format", "canonical-json")
		if k == 0 {
			first = out
		} else if !bytes.Equal(first, out) {
			r.Deterministic = false
		}
	}
	return r
}

// runPistos tolerates exit codes 1 and 2 (findings, incomplete): only the timing matters here.
func runPistos(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command(pistos, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok && (ee.ExitCode() == 1 || ee.ExitCode() == 2) {
		err = nil
	}
	return out.Bytes(), err
}

func timeRuns(n int, fn func() error, before func()) measure {
	var ds []float64
	var peak int64
	for i := 0; i < n; i++ {
		if before != nil {
			before()
		}
		stop := make(chan struct{})
		peakCh := make(chan int64, 1)
		go func() { peakCh <- samplePeak(stop) }()
		start := time.Now()
		if err := fn(); err != nil {
			fmt.Fprintln(os.Stderr, "run failed:", err)
		}
		ds = append(ds, ms(time.Since(start)))
		close(stop)
		if p := <-peakCh; p > peak {
			peak = p
		}
	}
	sort.Float64s(ds)
	return measure{P50MS: pct(ds, 0.5), P95MS: pct(ds, 0.95), Runs: n, PeakRSS: peak}
}

// samplePeak sums the RSS of every descendant of this process every 10 ms (Linux /proc only).
func samplePeak(stop chan struct{}) int64 {
	var peak int64
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if total := treeRSS(os.Getpid()); total > peak {
			peak = total
		}
		select {
		case <-stop:
			return peak
		case <-tick.C:
		}
	}
}

func treeRSS(root int) int64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	entries, _ := os.ReadDir("/proc")
	parent := map[int]int{}
	rss := map[int]int64{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/status")
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) >= 2 && f[0] == "PPid:" {
				parent[pid], _ = strconv.Atoi(f[1])
			}
			if len(f) >= 2 && f[0] == "VmRSS:" {
				rss[pid], _ = strconv.ParseInt(f[1], 10, 64)
			}
		}
	}
	var total int64
	for pid, kb := range rss {
		for p := parent[pid]; p != 0; p = parent[p] {
			if p == root {
				total += kb
				break
			}
		}
	}
	return total
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1) * p)
	return sorted[i]
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() && rel != "." && (d.Name() == ".git" || d.Name() == "dist" || d.Name() == "node_modules" || strings.HasPrefix(rel, ".pistos")) {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

func sourceSize(dir string) (files, lines int) {
	exts := map[string]bool{".go": true, ".py": true, ".ts": true, ".js": true, ".cs": true}
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && (d.Name() == "node_modules" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if err == nil && !d.IsDir() && exts[filepath.Ext(p)] {
			files++
			b, _ := os.ReadFile(p)
			lines += bytes.Count(b, []byte("\n"))
		}
		return nil
	})
	return
}

func environment() map[string]string {
	env := map[string]string{"os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": strconv.Itoa(runtime.NumCPU()), "date": time.Now().UTC().Format("2006-01-02")}
	probe := func(key string, argv ...string) {
		if out, err := exec.Command(argv[0], argv[1:]...).Output(); err == nil {
			env[key] = strings.TrimSpace(strings.Split(string(out), "\n")[0])
		}
	}
	probe("pistos", pistos, "--version")
	probe("go", "go", "version")
	probe("node", "node", "--version")
	probe("python", "python3", "--version")
	probe("dotnet", "dotnet", "--version")
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "model name") || strings.HasPrefix(l, "CPU part") {
				env["cpu"] = strings.TrimSpace(strings.SplitN(l, ":", 2)[1])
				break
			}
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		env["memory"] = strings.Join(strings.Fields(strings.Split(string(b), "\n")[0])[1:], " ")
	}
	return env
}

func markdown(env map[string]string, startup measure, rs []result) string {
	var b strings.Builder
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.WriteString("| Environment | |\n|---|---|\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "| %s | %s |\n", k, env[k])
	}
	fmt.Fprintf(&b, "\nStartup (`pistos --version`): p50 %.1f ms, p95 %.1f ms (%d runs)\n\n", startup.P50MS, startup.P95MS, startup.Runs)
	b.WriteString("| Scenario | Source files / lines | Install | No-op review p50/p95 | Cold review p50/p95 | Warm function edit p50/p95 | Peak RSS (all processes) | 3-run canonical identical |\n|---|---|---|---|---|---|---|---|\n")
	for _, r := range rs {
		if r.Error != "" {
			fmt.Fprintf(&b, "| %s | — | — | error: %s | | | | |\n", r.Scenario, r.Error)
			continue
		}
		peak := max(r.NoOp.PeakRSS, r.Cold.PeakRSS, r.Warm.PeakRSS)
		fmt.Fprintf(&b, "| %s | %d / %d | %.0f ms | %.0f / %.0f ms | %.0f / %.0f ms | %.0f / %.0f ms | %d MB | %v |\n",
			r.Scenario, r.Files, r.Lines, r.InstallMS, r.NoOp.P50MS, r.NoOp.P95MS, r.Cold.P50MS, r.Cold.P95MS, r.Warm.P50MS, r.Warm.P95MS, peak/1024, r.Deterministic)
	}
	return b.String()
}
