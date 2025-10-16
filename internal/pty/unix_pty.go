//go:build !windows
// +build !windows

package pty

import (
	"fmt"
	"os"
	"os/exec"
	"sync"

	creackpty "github.com/creack/pty"
	"golang.org/x/term"
)

// unixPTY wraps *os.File returned by creack/pty
type unixPTY struct {
	f        *os.File
	cmd      *exec.Cmd
	mu       sync.Mutex
	rawMu    sync.Mutex
	oldState *term.State
	rawSet   bool
	paused   bool
}

func Start(cmd *exec.Cmd) (PTY, error) {
	f, err := creackpty.Start(cmd)
	if err != nil {
		return nil, err
	}
	p := &unixPTY{f: f, cmd: cmd}
	// If parent stdin is a terminal, attempt to put it into raw mode for
	// interactive sessions. This is best-effort: failure to set raw mode
	// should not cause Start to fail.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if err := p.EnterRaw(); err != nil {
			// best-effort: don't fail startup if raw mode cannot be set
			fmt.Fprintf(os.Stderr, "pty: EnterRaw failed: %v\n", err)
		}
	}
	return p, nil
}

// ...existing code...
func Open() (PTY, *os.File, error) {
	// creackpty.Open returns (master, slave, err)
	master, slave, err := creackpty.Open()
	if err != nil {
		return nil, nil, err
	}
	// we only keep the master side in our wrapper; close the slave to avoid fd leak
	if slave != nil {
		_ = slave.Close()
	}
	return &unixPTY{
			f:   master,
			cmd: nil,
		},
		master, nil
}

// ...existing code...

func (p *unixPTY) Wait() error {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Wait()
	}
	return nil
}
func (p *unixPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *unixPTY) Pause() error {
	// Pause the PTY: leave raw mode and mark paused. We don't kill the
	// underlying process here; callers decide how to handle the process.
	if err := p.LeaveRaw(); err != nil {
		return err
	}
	p.rawMu.Lock()
	p.paused = true
	p.rawMu.Unlock()
	return nil
}
func (p *unixPTY) Close() error {
	// Restore stdin state if we set raw mode earlier.
	_ = p.LeaveRaw()
	err := p.f.Close()
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return err
}
func (p *unixPTY) Fd() uintptr    { return p.f.Fd() }
func (p *unixPTY) File() *os.File { return p.f }

// InPipe returns the master file used to write to the PTY.
func (p *unixPTY) InPipe() *os.File { return p.f }

// OutPipe returns the master file used to read from the PTY.
func (p *unixPTY) OutPipe() *os.File { return p.f }
func (p *unixPTY) SetSize(rows, cols int) error {
	return creackpty.Setsize(p.f, &creackpty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// MakeRawStdin sets the process stdin into raw mode and returns the previous
// terminal state which should be passed to RestoreStdin when finished.
// This is provided for non-Windows builds so callers can reuse the same helpers
// that exist in the Windows PTY implementation.
func MakeRawStdin() (*term.State, error) {
	fd := int(os.Stdin.Fd())
	return term.MakeRaw(fd)
}

// RestoreStdin restores stdin to a previous terminal state returned by
// MakeRawStdin. It is safe to call with a nil state.
func RestoreStdin(old *term.State) error {
	if old == nil {
		return nil
	}
	return term.Restore(int(os.Stdin.Fd()), old)
}

// EnterRaw puts stdin into raw mode if not already set. It is idempotent and
// safe to call from multiple goroutines.
func (p *unixPTY) EnterRaw() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.rawSet {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("stdin is not a terminal")
	}
	old, err := MakeRawStdin()
	if err != nil {
		return err
	}
	p.oldState = old
	p.rawSet = true
	return nil
}

// LeaveRaw restores stdin to the previous state if EnterRaw set it.
// It is idempotent.
func (p *unixPTY) LeaveRaw() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.rawSet {
		return nil
	}
	err := RestoreStdin(p.oldState)
	p.oldState = nil
	p.rawSet = false
	return err
}

// Resume is a small convenience that re-enters raw mode (alias to EnterRaw).
func (p *unixPTY) Resume() error { return p.EnterRaw() }
