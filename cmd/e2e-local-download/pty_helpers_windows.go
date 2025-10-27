//go:build windows
// +build windows

package main

import (
	"os"
	"os/exec"
)

// Windows fallback: do not set process group attributes.
func newCommandForPTY(cmdStr string) *exec.Cmd {
	return exec.Command("sh", "-lc", cmdStr)
}

// killProcessGroup on Windows just finds and kills the single process.
func killProcessGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
