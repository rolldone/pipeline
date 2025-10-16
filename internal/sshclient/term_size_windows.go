//go:build windows
// +build windows

package sshclient

import (
	"golang.org/x/sys/windows"
)

// getTerminalSizeFallback returns the console window size (cols, rows) on Windows
// by querying the Win32 console via GetConsoleScreenBufferInfo.
func getTerminalSizeFallback() (cols, rows int, err error) {
	h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return 0, 0, err
	}
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(h, &info); err != nil {
		return 0, 0, err
	}
	cols = int(info.Window.Right - info.Window.Left + 1)
	rows = int(info.Window.Bottom - info.Window.Top + 1)
	return cols, rows, nil
}
