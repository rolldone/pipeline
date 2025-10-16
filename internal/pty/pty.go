package pty

import (
	"os"
)

// PTY is a small, cross-platform abstraction over a pseudo-terminal.
type PTY interface {
	Pause() error
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
	Wait() error
	Fd() uintptr
	File() *os.File
	// InPipe returns a file suitable for writing to the PTY's stdin.
	InPipe() *os.File
	// OutPipe returns a file suitable for reading from the PTY's stdout/stderr.
	OutPipe() *os.File
	SetSize(rows, cols int) error
}
