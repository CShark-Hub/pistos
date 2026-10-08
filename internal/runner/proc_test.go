//go:build !windows

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func sh(script string) Cmd {
	return Cmd{Path: "/bin/sh", Args: []string{"-c", script}, Env: []string{"PATH=/usr/bin:/bin"}}
}

func TestRunExitCodeAndOutput(t *testing.T) {
	res, err := Run(context.Background(), sh("echo out; echo err >&2; exit 3"))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || string(res.Stdout) != "out\n" || string(res.Stderr) != "err\n" {
		t.Fatalf("%+v", res)
	}
}

func TestRunDoesNotInheritEnvironment(t *testing.T) {
	t.Setenv("PISTOS_SECRET_PROBE", "should-not-leak")
	res, err := Run(context.Background(), sh("env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(res.Stdout), "should-not-leak") {
		t.Fatal("parent environment leaked into analyzer")
	}
}

func TestRunCapsOutput(t *testing.T) {
	c := sh("head -c 100000 /dev/zero")
	c.MaxOutput = 1000
	res, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || len(res.Stdout) != 1000 {
		t.Fatalf("truncated=%v len=%d", res.Truncated, len(res.Stdout))
	}
}

func TestRunTimeoutKillsTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	c := sh("sleep 3600 & echo $! > " + pidFile + "; wait")
	c.Timeout = 300 * time.Millisecond
	res, err := Run(context.Background(), c)
	if err == nil || !res.TimedOut {
		t.Fatalf("want timeout, got %+v %v", res, err)
	}
	assertProcessGone(t, pidFile)
}

func TestRunCancelKillsTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for i := 0; i < 200; i++ {
			if b, _ := os.ReadFile(pidFile); len(b) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	res, err := Run(ctx, sh("sleep 3600 & echo $! > "+pidFile+"; wait"))
	if !errors.Is(err, context.Canceled) || !res.Cancelled {
		t.Fatalf("want cancellation, got %+v %v", res, err)
	}
	assertProcessGone(t, pidFile)
}

func TestRunReapsLingeringDescendants(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	_, err := Run(context.Background(), sh("sleep 3600 >/dev/null 2>&1 & echo $! > "+pidFile))
	if err != nil {
		t.Fatal(err)
	}
	assertProcessGone(t, pidFile)
}

func assertProcessGone(t *testing.T, pidFile string) {
	t.Helper()
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	for i := 0; i < 100; i++ {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived", pid)
}
