//go:build !windows
// +build !windows

package main

// startParentWatcher is a no-op on Unix systems
func startParentWatcher() {
	// Unix systems don't need parent process monitoring
	// as child processes are automatically cleaned up
}
