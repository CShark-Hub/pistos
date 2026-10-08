//go:build windows

package runner

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRunTimeoutKillsTreeWindows starts a grandchild through cmd.exe and checks taskkill /T removed it.
func TestRunTimeoutKillsTreeWindows(t *testing.T) {
	marker := "pistos-tree-probe"
	c := Cmd{Path: "cmd.exe", Args: []string{"/c", "start", "/b", "ping", "-n", "60", "127.0.0.1", "&", "title", marker, "&", "ping", "-n", "60", "127.0.0.1"},
		Env: []string{"SystemRoot=C:\\Windows", "PATH=C:\\Windows\\System32"}, Timeout: 2 * time.Second}
	res, err := Run(context.Background(), c)
	if err == nil || !res.TimedOut {
		t.Fatalf("want timeout, got %+v %v", res, err)
	}
	time.Sleep(time.Second)
	out, _ := exec.Command("tasklist", "/FI", "IMAGENAME eq PING.EXE", "/NH").Output()
	if strings.Contains(strings.ToUpper(string(out)), "PING.EXE") {
		t.Fatalf("ping survived the tree kill:\n%s", out)
	}
}
