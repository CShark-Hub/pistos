//go:build !windows

package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"example.com/pistos/internal/testutil/fakeworker"
)

func TestWorkerCancellationKillsProcessTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	ctx, cancel := context.WithCancel(context.Background())
	w, err := StartWorker(ctx, workerCmd(t, fakeworker.ModeHang, "PATH="+os.Getenv("PATH"), fakeworker.EnvPidFile+"="+pidFile), "fake", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(pidFile); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	}()
	_, err = w.Analyze(ctx, AnalyzeParams{Tree: "t"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation, got %v", err)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(b))
	assertGone(t, w.Pid())
	assertGone(t, pid)
}

func assertGone(t *testing.T, pid int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d still running", pid)
}
