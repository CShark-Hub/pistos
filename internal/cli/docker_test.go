package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/pistos/internal/finding"
	"example.com/pistos/internal/plugin"
	"example.com/pistos/internal/runner"
	"example.com/pistos/internal/testutil/gitrepo"
)

func requireDocker(t *testing.T) *plugin.ContainerRuntime {
	t.Helper()
	rt, err := plugin.NewContainerRuntime()
	if err != nil {
		skipLive(t, "docker unavailable: %v", err)
	}
	if out, err := exec.Command(rt.Docker, "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		skipLive(t, "docker daemon unavailable: %v %s", err, out)
	}
	return rt
}

// comparable keeps the canonical deterministic content that must not depend on where tools run.
func comparable(env *finding.Envelope) string {
	var b strings.Builder
	b.WriteString(env.Canonical.Verdict.Outcome + "\n")
	for _, is := range env.Canonical.Issues {
		b.WriteString(strings.Join([]string{is.Key, is.Family, is.Location.Path, fmtInt(is.Location.Line), string(is.Severity), is.Change}, " ") + "\n")
	}
	return b.String()
}

func fmtInt(n int) string { return strconv.Itoa(n) }

func dockerConfig(cfg string) string {
	if strings.Contains(cfg, "mode = \"native\"") {
		return strings.Replace(cfg, "mode = \"native\"", "mode = \"docker\"", 1)
	}
	return strings.Replace(cfg, "[execution]\n", "[execution]\nmode = \"docker\"\n", 1)
}

// reviewBothWays runs the same change natively and in the pack container.
func reviewBothWays(t *testing.T, r *gitrepo.Repo, nativeCfg string, edit func()) (*finding.Envelope, *finding.Envelope) {
	t.Helper()
	edit()
	_, native, errs := runReview(t, r.Dir, "--all")
	r.Write(".pistos/config.toml", dockerConfig(nativeCfg))
	if code, out, errs := run(t, r.Dir, "plugins", "install"); code != 0 {
		skipLive(t, "image build unavailable: %s %s", out, errs)
	}
	_, container, cerrs := runReview(t, r.Dir, "--all")
	for _, c := range container.Canonical.Coverage.Capabilities {
		if c.Required && !c.Status.Completed() {
			t.Fatalf("container check %s/%s: %s %s %s", c.Project, c.Capability, c.Status, c.Error, cerrs+errs)
		}
	}
	return native, container
}

func TestNativeAndContainerAgreeGo(t *testing.T) {
	requireDocker(t)
	cfg := configFor(strings.Replace(goProject, `checks = ["go-vet"]`, `checks = ["go-vet", "metrics"]`, 1)) + "\n[metrics]\ncyclomatic = 1\n"
	r := setupGo(t, "go-defect", cfg)
	native, container := reviewBothWays(t, r, cfg, func() {})
	if comparable(native) != comparable(container) || len(native.Canonical.Issues) < 1 {
		t.Fatalf("native and container results differ:\n%q\n---\n%q\n%+v", comparable(native), comparable(container), container.Canonical.Coverage.Capabilities)
	}
}

func TestNativeAndContainerAgreePython(t *testing.T) {
	requireDocker(t)
	requireRuntimes(t, "python3", "node", "npm")
	r := setupPack(t, "python-app", "", nil)
	cfg := r.Read(".pistos/config.toml")
	native, container := reviewBothWays(t, r, cfg, func() {
		r.Write("pkg/calc.py", "import os\n\n\ndef total(values: list[int]) -> int:\n    x: int = \"s\"\n    return sum(values)\n")
	})
	if comparable(native) != comparable(container) || len(native.Canonical.Issues) < 3 {
		t.Fatalf("native and container results differ:\n%s---\n%s", comparable(native), comparable(container))
	}
}

func TestPackImagesExcludeUnrelatedRuntimes(t *testing.T) {
	rt := requireDocker(t)
	for _, tc := range []struct {
		pack   string
		absent []string
	}{
		{"go", []string{"node", "python3", "dotnet"}},
		{"python", []string{"go", "dotnet"}},
		{"jsts", []string{"go", "python3", "dotnet"}},
		{"csharp", []string{"go", "node", "python3"}},
		{"secrets", []string{"go", "node", "python3", "dotnet"}},
	} {
		m, _ := plugin.LoadManifest(tc.pack)
		if rt.ImageID(context.Background(), m) == "" {
			if _, err := rt.Build(context.Background(), m); err != nil {
				skipLive(t, "cannot build %s image: %v", tc.pack, err)
			}
		}
		script := ""
		for _, b := range tc.absent {
			script += "command -v " + b + " && exit 1; "
		}
		out, err := exec.Command(rt.Docker, "run", "--rm", "--network", "none", plugin.ImageTag(m), "sh", "-c", script+"exit 0").CombinedOutput()
		if err != nil {
			t.Errorf("%s image contains an unrelated runtime: %s", tc.pack, out)
		}
	}
}

func TestRestrictedProfileIsEnforced(t *testing.T) {
	rt := requireDocker(t)
	m, _ := plugin.LoadManifest("go")
	if rt.ImageID(context.Background(), m) == "" {
		if _, err := rt.Build(context.Background(), m); err != nil {
			skipLive(t, "cannot build go image: %v", err)
		}
	}
	src := t.TempDir()
	os.WriteFile(src+"/f", []byte("x"), 0o644)
	spec := plugin.ContainerSpec{Runtime: rt, Image: plugin.ImageTag(m), Restricted: true, MemoryMB: 256, CPUs: 1, Mounts: []plugin.Mount{{Path: src, ReadOnly: true}}}
	probe := "id -u; touch /etc/x 2>/dev/null && echo rootfs-writable; touch " + src + "/g 2>/dev/null && echo source-writable; " +
		"(getent hosts proxy.golang.org >/dev/null || wget -q -T 3 -O /dev/null http://1.1.1.1 2>/dev/null) && echo network; env"
	res, err := runner.Run(context.Background(), spec.Wrap(runner.Cmd{Path: "sh", Args: []string{"-c", probe}, Env: []string{"HOME=/tmp/home"}}))
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Stdout)
	if !strings.HasPrefix(out, fmtInt(os.Getuid())) || strings.Contains(out, "writable") || strings.Contains(out, "network") {
		t.Fatalf("restricted container must be non-root, read-only and offline:\n%s%s", out, res.Stderr)
	}
	t.Setenv("PISTOS_SECRET_PROBE", "leak")
	if strings.Contains(out, "PISTOS_SECRET") || strings.Contains(out, "OPENAI") {
		t.Fatal("host environment leaked into the container")
	}
}

func TestContainerCancellationRemovesContainer(t *testing.T) {
	rt := requireDocker(t)
	m, _ := plugin.LoadManifest("go")
	if rt.ImageID(context.Background(), m) == "" {
		skipLive(t, "go image not built")
	}
	spec := plugin.ContainerSpec{Runtime: rt, Image: plugin.ImageTag(m), Restricted: true}
	cmd := spec.Wrap(runner.Cmd{Path: "sleep", Args: []string{"300"}})
	name := cmd.Args[5]
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for i := 0; i < 100; i++ {
			if out, _ := exec.Command(rt.Docker, "ps", "-q", "--filter", "name="+name).Output(); len(strings.TrimSpace(string(out))) > 0 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		cancel()
	}()
	if _, err := runner.Run(ctx, cmd); err == nil {
		t.Fatal("want cancellation")
	}
	out, _ := exec.Command(rt.Docker, "ps", "-aq", "--filter", "name="+name).Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("container %s survived cancellation", name)
	}
}

func TestNativeAndContainerAgreeJSTS(t *testing.T) {
	requireDocker(t)
	requireRuntimes(t, "node", "npm")
	deps := jsDependencies(t)
	r := setupPack(t, "jsts-app", "node_modules\n", func(r *gitrepo.Repo) {
		os.Symlink(deps, r.Dir+"/node_modules")
	})
	cfg := r.Read(".pistos/config.toml")
	native, container := reviewBothWays(t, r, cfg, func() {
		r.Write("src/math.ts", "export function add(a: number, b: number): number {\n  return a + b + \"x\";\n}\n")
		r.Write("src/util.js", "export function same(a, b) {\n  const tmp = 2;\n  return a == b;\n}\n")
	})
	if comparable(native) != comparable(container) || len(native.Canonical.Issues) < 3 {
		t.Fatalf("native and container results differ:\n%q\n---\n%q", comparable(native), comparable(container))
	}
}

func TestNativeAndContainerAgreeCSharp(t *testing.T) {
	requireDocker(t)
	requireRuntimes(t, "dotnet")
	r := setupPack(t, "csharp-app", "bin/\nobj/\n", nil)
	cfg := r.Read(".pistos/config.toml")
	native, container := reviewBothWays(t, r, cfg, func() {
		r.Write("Lib/Greeter.cs", "namespace Lib;\n\npublic static class Greeter\n{\n    public static string Greet(string name)\n    {\n        int unused;\n        return $\"hello, {name}\";\n    }\n}\n")
	})
	if comparable(native) != comparable(container) || len(native.Canonical.Issues) < 1 {
		t.Fatalf("native and container results differ:\n%q\n---\n%q", comparable(native), comparable(container))
	}
}

func TestNativeAndContainerAgreeSecrets(t *testing.T) {
	requireDocker(t)
	cfg := secretsConfig(`["secrets"]`, "")
	r := setupSecrets(t, cfg, nil)
	native, container := reviewBothWays(t, r, cfg, func() {
		r.Write("main.go", r.Read("main.go")+"\nconst token = \""+fakeGitHubToken+"\"\n")
		r.Write(".gitleaks.toml", "[extend]\nuseDefault = true\n\n[[rules]]\nid = \"pistos-canary\"\ndescription = \"Pistos canary value\"\nregex = '''PISTOS_CANARY_[0-9]{6}'''\n")
		r.Write("notes.md", "value: PISTOS_CANARY_123456\n")
	})
	if comparable(native) != comparable(container) || len(native.Canonical.Issues) != 2 {
		t.Fatalf("native and container results differ:\n%q\n---\n%q", comparable(native), comparable(container))
	}
	if c := capability(container, "secrets", "gitleaks", "head"); c.ToolVersion != capability(native, "secrets", "gitleaks", "head").ToolVersion {
		t.Fatalf("the image must carry the pinned gitleaks: %s", c.ToolVersion)
	}
}

// TestCompleteImageRunsCoreDirectly builds the complete Go image (pack image + pistos + git) and runs
// the core inside it against a mounted repository: no Docker-in-Docker and no Docker socket.
func TestCompleteImageRunsCoreDirectly(t *testing.T) {
	rt := requireDocker(t)
	m, _ := plugin.LoadManifest("go")
	if rt.ImageID(context.Background(), m) == "" {
		if _, err := rt.Build(context.Background(), m); err != nil {
			skipLive(t, "cannot build go image: %v", err)
		}
	}
	ctxDir := t.TempDir()
	build := exec.Command("go", "build", "-o", ctxDir+"/pistos", "../../cmd/pistos")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	tag := "pistos-go-test:latest"
	if out, err := exec.Command(rt.Docker, "build", "-q", "-f", "../../docker/full.Dockerfile", "--build-arg", "PACK=go", "-t", tag, ctxDir).CombinedOutput(); err != nil {
		skipLive(t, "cannot build complete image: %v %s", err, out)
	}
	cfg := configFor(goProject)
	r := setupGo(t, "go-defect", cfg)
	_, native, _ := runReview(t, r.Dir, "--all")

	inImage := func(args ...string) []byte {
		base := []string{"run", "--rm", "--network", "none", "--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
			"-e", "HOME=/tmp", "-v", r.Dir + ":" + r.Dir, "-w", r.Dir, tag}
		out, _ := exec.Command(rt.Docker, append(base, args...)...).Output()
		return out
	}
	inImage("plugins", "install")
	out := inImage("review", "--all", "--format", "json")
	var env finding.Envelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("in-image review: %v %s", err, out)
	}
	if comparable(native) != comparable(&env) {
		t.Fatalf("complete image disagrees with native:\n%q\n---\n%q", comparable(native), comparable(&env))
	}
}
