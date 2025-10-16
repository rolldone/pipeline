//go:build !windows
// +build !windows

package sshclient

import (
	"os"

	"golang.org/x/term"
)

// getTerminalSizeFallback returns the terminal size (cols, rows) on unix-like
// systems using term.GetSize. Returns an error if not available.
func getTerminalSizeFallback() (cols, rows int, err error) {
	w, h, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return 0, 0, err
	}
	return w, h, nil
}
