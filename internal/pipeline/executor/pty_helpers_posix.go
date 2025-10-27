//go:build !windows
// +build !windows

package executor

import (
	"os/exec"
	"syscall"
	"time"
)

// newCommandForPTY returns an *exec.Cmd configured to start in its own
// process group so we can signal the whole group. POSIX-only.
func newCommandForPTY(cmdStr string) *exec.Cmd {
	c := exec.Command("sh", "-lc", cmdStr)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return c
}

// killProcessGroup sends SIGTERM then SIGKILL to the process group of pid.
func killProcessGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	time.Sleep(250 * time.Millisecond)
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	return nil
}
