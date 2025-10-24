//go:build windows
// +build windows

package main

import (
	"fmt"
	"syscall"
	"time"
)

// Windows API constants (not always present in syscall)
const (
	PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	STILL_ACTIVE                      = 259
)

// startParentWatcher launches a goroutine that will exit the process if the parent process dies.
func startParentWatcher() {
	ppid := syscall.Getppid()
	go func() {
		for {
			if !isProcessAlive(ppid) {
				fmt.Println("❌ Parent process exited, terminating agent.")
				gracefulShutdown()
				return
			}
			time.Sleep(2 * time.Second)
		}
	}()
}

// isProcessAlive checks if a process with given pid exists.
func isProcessAlive(pid int) bool {
	h, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var exitCode uint32
	err = syscall.GetExitCodeProcess(h, &exitCode)
	if err != nil {
		return false
	}
	return exitCode == STILL_ACTIVE
}
