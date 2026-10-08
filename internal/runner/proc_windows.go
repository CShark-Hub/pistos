//go:build windows

package runner

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// killTree uses taskkill /T because Windows has no process-group signal; descendants of the
// leader are terminated by parent-child relationship.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	_ = kill.Run()
	_ = cmd.Process.Kill()
}

// reapGroup is a no-op after a normal exit: the leader's PID may already be reused, so taskkill /T
// could hit an unrelated tree. Job objects would close this gap.
func reapGroup(*exec.Cmd) {}
