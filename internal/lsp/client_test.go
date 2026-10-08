package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/pistos/internal/runner"
	"example.com/pistos/internal/testutil/fakelsp"
)

func TestMain(m *testing.M) {
	fakelsp.MaybeServe()
	os.Exit(m.Run())
}

func start(t *testing.T, mode string) (*Client, string, error) {
	t.Helper()
	exe, _ := os.Executable()
	dir := t.TempDir()
	file := filepath.Join(dir, "a.go")
	os.WriteFile(file, []byte("package a\n\nfunc f() {}\n"), 0o644)
	c, err := Start(context.Background(), runner.Cmd{Path: exe, Env: []string{fakelsp.EnvMode + "=" + mode}}, dir, nil, nil)
	return c, file, err
}

func TestPullDiagnosticsAreComplete(t *testing.T) {
	c, file, err := start(t, fakelsp.ModePull)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Kill()
	uri, _ := c.Open(file, "go")
	d, err := c.Diagnostics(context.Background(), uri, time.Second)
	if err != nil || !d.Complete || len(d.Diagnostics) != 1 {
		t.Fatalf("%+v %v", d, err)
	}
	locs, err := c.Locations(context.Background(), "textDocument/definition", uri, Position{0, 0})
	if err != nil || len(locs) != 2 {
		t.Fatalf("%+v %v", locs, err)
	}
	if _, err := c.Locations(context.Background(), "textDocument/implementation", uri, Position{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unadvertised capability must be unsupported, got %v", err)
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPushDiagnosticsAreNeverComplete(t *testing.T) {
	c, file, err := start(t, fakelsp.ModePushLate)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Kill()
	uri, _ := c.Open(file, "go")
	d, err := c.Diagnostics(context.Background(), uri, 50*time.Millisecond)
	if err != nil || d.Complete || len(d.Diagnostics) != 0 || !strings.Contains(d.Reason, "deadline") {
		t.Fatalf("an early deadline must not look complete: %+v %v", d, err)
	}
	d, err = c.Diagnostics(context.Background(), uri, 2*time.Second)
	if err != nil || d.Complete || len(d.Diagnostics) != 1 {
		t.Fatalf("late push diagnostics are still incomplete: %+v %v", d, err)
	}
}

func TestFailedInitializationAndCrash(t *testing.T) {
	if _, _, err := start(t, fakelsp.ModeInitFail); err == nil || !strings.Contains(err.Error(), "cannot load workspace") {
		t.Fatalf("got %v", err)
	}
	c, file, err := start(t, fakelsp.ModeCrash)
	if err != nil {
		t.Fatal(err)
	}
	uri, _ := c.Open(file, "go")
	if _, err := c.Locations(context.Background(), "textDocument/definition", uri, Position{}); !errors.Is(err, ErrServerExited) && err == nil {
		t.Fatalf("a crashed server must fail requests, got %v", err)
	}
}

func TestWaitIdleFollowsProgress(t *testing.T) {
	c, _, err := start(t, fakelsp.ModeLoadDone)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Kill()
	if err := c.WaitIdle(context.Background(), 100*time.Millisecond); err != nil {
		t.Fatalf("loading that ends must become idle: %v", err)
	}
	c2, _, err := start(t, fakelsp.ModeLoading)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Kill()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := c2.WaitIdle(ctx, 100*time.Millisecond); err == nil || !strings.Contains(err.Error(), "still loading") {
		t.Fatalf("never-ending loading must not be treated as ready: %v", err)
	}
}
