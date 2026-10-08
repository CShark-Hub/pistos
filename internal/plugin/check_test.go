package plugin

import (
	"context"
	"os"
	"strings"
	"testing"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/testutil/fakeworker"
)

func workerManifest(t *testing.T, mode string) *Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(`{"schema":1,"name":"fake","version":"1.0.0","languages":["fake"],
	  "tools":{"worker":{"command":"worker","lookup":["path"],"version_args":[],"version_regex":"(.*)"}},
	  "checks":{"fake-lint":{"tool":"worker","args":[],"format":"worker","scope":"file","weight":1,
	    "env":{"` + fakeworker.EnvMode + `":"` + mode + `"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func runWorkerCheck(t *testing.T, mode string) (Parsed, finding.Status, error) {
	exe, _ := os.Executable()
	return Execute(context.Background(), Invocation{
		Manifest: workerManifest(t, mode), CheckID: "fake-lint",
		Tool: ResolvedTool{Name: "worker", Path: exe}, ProjectDir: t.TempDir(), ScratchDir: t.TempDir(),
		Tree: TreeScope{ID: "tree-1", Project: "p"},
	})
}

func TestWorkerCheckComplete(t *testing.T) {
	p, status, err := runWorkerCheck(t, fakeworker.ModeOK)
	if err != nil || status != finding.StatusComplete || len(p.Diagnostics) != 1 {
		t.Fatalf("%+v %s %v", p, status, err)
	}
}

func TestWorkerCheckFailuresAreNeverClean(t *testing.T) {
	for mode, want := range map[string]finding.Status{
		fakeworker.ModeWrongScope:  finding.StatusIncomplete,
		fakeworker.ModeCrash:       finding.StatusFailed,
		fakeworker.ModeMalformed:   finding.StatusFailed,
		fakeworker.ModeBadProtocol: finding.StatusFailed,
	} {
		_, status, err := runWorkerCheck(t, mode)
		if status != want || err == nil {
			t.Errorf("%s: got %s %v, want %s", mode, status, err, want)
		}
	}
}

func TestDeclarativeCommandDropsUnsetOptions(t *testing.T) {
	m, _ := LoadManifest("go")
	cmd := Invocation{Manifest: m, CheckID: "go-vet", Tool: ResolvedTool{Name: "go", Path: "/x/go"}, Jobs: 2, Options: map[string]string{"goos": "linux"}}.Command()
	args := strings.Join(cmd.Args, " ")
	if args != "vet -p=2 -json ./..." {
		t.Fatalf("args = %q", args)
	}
	env := strings.Join(cmd.Env, " ")
	if !strings.Contains(env, "GOOS=linux") || strings.Contains(env, "GOARCH=") || !strings.Contains(env, "GOPROXY=off") {
		t.Fatalf("env = %q", env)
	}
}

func TestAnalyzerEnvironmentExcludesSecrets(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-should-not-leak")
	t.Setenv("PISTOS_REVIEWER_KEY", "also-secret")
	m, _ := LoadManifest("go")
	cmd := Invocation{Manifest: m, CheckID: "go-vet", Tool: ResolvedTool{Name: "go", Path: "/x/go"}}.Command()
	for _, kv := range cmd.Env {
		if strings.Contains(kv, "secret") || strings.Contains(kv, "sk-test") {
			t.Fatalf("secret leaked into analyzer env: %s", kv)
		}
	}
}
