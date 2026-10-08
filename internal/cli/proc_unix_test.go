//go:build !windows

package cli

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func waitForFile(t *testing.T, p string) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if b, _ := os.ReadFile(p); len(b) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", p)
}

func assertPidGone(t *testing.T, pidFile string) {
	t.Helper()
	b, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	for i := 0; i < 200; i++ {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child %d survived cancellation", pid)
}
