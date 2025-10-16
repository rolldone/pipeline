//go:build windows
// +build windows

package pty

import (
	"fmt"
	"os"
	"os/exec"
	"sync"

	widepty "github.com/aymanbagabas/go-pty"
	"golang.org/x/term"
)

// winConPTY wraps charmbracelet/x/conpty.ConPty to satisfy our PTY interface.
type winConPTY struct {
	c        widepty.Pty
	oldState *term.State
	child    *widepty.Cmd // bukan *exec.Cmd, tapi *pty.Cmd
	rawSet   bool
	mu       sync.Mutex // protects rawSet, oldState and paused
	paused   bool
}

func Start(cmd *exec.Cmd) (PTY, error) {
	// Create a new ConPTY with default size and spawn the command attached to it.
	p, err := widepty.New()
	if err != nil {
		return nil, fmt.Errorf("pty: failed to create PTY: %w", err)
	}

	// Build command from exec.Cmd
	var name string
	var args []string
	if len(cmd.Args) > 0 {
		name = cmd.Args[0]
		if len(cmd.Args) > 1 {
			args = cmd.Args[1:]
		}
	} else {
		name = cmd.Path
	}

	c := p.Command(name, args...)
	c.Env = cmd.Env
	c.Dir = cmd.Dir
	c.SysProcAttr = cmd.SysProcAttr

	if err := c.Start(); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("pty: failed to start command in PTY: %w", err)
	}
	win := &winConPTY{
		c:     p,
		child: c,
	}
	// If parent stdin is a terminal, attempt to put it into raw mode for
	// interactive sessions. This is best-effort: failure to set raw mode
	// should not cause Start to fail.

	// Try to enter raw mode if stdin is a terminal. Use helper so we
	// can manage idempotence and later Pause/Resume safely.
	// if term.IsTerminal(int(os.Stdin.Fd())) {
	// 	if err := win.EnterRaw(); err != nil {
	// 		// best-effort: don't fail startup if raw mode cannot be set
	// 		fmt.Fprintf(os.Stderr, "pty: EnterRaw failed: %v\n", err)
	// 	}
	// }

	// Do NOT start global io.Copy here; the caller/bridge should wire
	// stdin/stdout handling so it can intercept input and output (matchers,
	// observers, callbacks, pause/resume).

	return win, nil
}

func Open() (PTY, *os.File, error) {
	p, err := widepty.New()
	if err != nil {
		return nil, nil, fmt.Errorf("pty: failed to create PTY: %w", err)
	}
	if cp, ok := p.(interface{ OutputPipe() *os.File }); ok {
		return &winConPTY{c: p}, cp.OutputPipe(), nil
	}
	return &winConPTY{c: p}, nil, nil
}

func (w *winConPTY) Wait() error {
	if w.child != nil {
		return w.child.Wait()
	}
	return fmt.Errorf("no child process")
}

func (w *winConPTY) Read(b []byte) (int, error)  { return w.c.Read(b) }
func (w *winConPTY) Write(b []byte) (int, error) { return w.c.Write(b) }
func (w *winConPTY) Close() error {
	// Restore stdin state if we set raw mode earlier. Attempt restore
	// first, then close underlying PTY and return any errors.
	var restoreErr error
	if w != nil {
		// Ensure we leave raw mode if we previously entered it.
		// if err := w.LeaveRaw(); err != nil {
		// 	restoreErr = err
		// }
	}

	if err := w.c.Close(); err != nil {
		if restoreErr != nil {
			return fmt.Errorf("restore error: %v; close error: %v", restoreErr, err)
		}
		return err
	}
	return restoreErr
}
func (w *winConPTY) Fd() uintptr { return w.c.Fd() }
func (w *winConPTY) File() *os.File {
	if cp, ok := w.c.(interface{ OutputPipe() *os.File }); ok {
		return cp.OutputPipe()
	}
	return nil
}
func (w *winConPTY) SetSize(rows, cols int) error { return w.c.Resize(cols, rows) }

// // MakeRawStdin sets the process stdin into raw mode and returns the previous
// // terminal state which should be passed to RestoreStdin when finished.
// // This is a convenience wrapper so callers don't need to import x/term
// // directly when using the PTY implementation.
// func MakeRawStdin() (*term.State, error) {
// 	fd := int(os.Stdin.Fd())
// 	return term.MakeRaw(fd)
// }

// // RestoreStdin restores stdin to a previous terminal state returned by
// // MakeRawStdin. It is safe to call with a nil state.
// func RestoreStdin(old *term.State) error {
// 	if old == nil {
// 		return nil
// 	}
// 	return term.Restore(int(os.Stdin.Fd()), old)
// }

// EnterRaw puts stdin into raw mode if not already in raw. It's safe to call
// multiple times; repeated calls are no-ops until LeaveRaw is called.
// func (w *winConPTY) EnterRaw() error {
// 	if w == nil {
// 		return fmt.Errorf("nil winConPTY")
// 	}
// 	w.mu.Lock()
// 	defer w.mu.Unlock()
// 	if w.rawSet {
// 		return nil
// 	}
// 	if !term.IsTerminal(int(os.Stdin.Fd())) {
// 		return fmt.Errorf("stdin is not a terminal")
// 	}
// 	old, err := MakeRawStdin()
// 	if err != nil {
// 		return err
// 	}
// 	w.oldState = old
// 	w.rawSet = true
// 	return nil
// }

// // LeaveRaw restores stdin to the previous state if we previously entered
// // raw mode. It's safe to call multiple times.
// func (w *winConPTY) LeaveRaw() error {
// 	if w == nil {
// 		return nil
// 	}
// 	w.mu.Lock()
// 	defer w.mu.Unlock()
// 	if !w.rawSet {
// 		return nil
// 	}
// 	err := RestoreStdin(w.oldState)
// 	w.oldState = nil
// 	w.rawSet = false
// 	return err
// }

// Pause temporarily releases raw mode (if set) so the caller can perform
// blocking terminal operations that require the tty to be in canonical mode.
// Pause is idempotent; multiple Pause calls will have the same effect.
func (w *winConPTY) Pause() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	// if already paused, nothing to do
	if w.paused {
		w.mu.Unlock()
		return nil
	}
	// If raw was set, restore it and mark paused. We keep rawSet=false so
	// future LeaveRaw won't try to restore again; we'll re-enter raw on Resume.

	//old := w.oldState
	w.oldState = nil
	w.rawSet = false
	w.paused = true
	w.mu.Unlock()
	// perform actual restore outside lock to avoid blocking other callers
	// if err := RestoreStdin(old); err != nil {
	// 	fmt.Println("pty: Pause RestoreStdin error:", err)
	// }
	return nil
}

// Resume re-enters raw mode if we previously paused with Pause. Resume is
// idempotent.
func (w *winConPTY) Resume() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	if !w.paused {
		w.mu.Unlock()
		return nil
	}
	w.paused = false
	w.mu.Unlock()

	// If stdin is a terminal, try to re-enter raw mode. If it fails, return
	// the error to the caller.
	// if term.IsTerminal(int(os.Stdin.Fd())) {
	// 	return w.EnterRaw()
	// }
	return nil
}

// InPipe exposes the PTY input pipe (write to this to send stdin to the child).
func (w *winConPTY) InPipe() *os.File {
	if cp, ok := w.c.(interface{ InputPipe() *os.File }); ok {
		return cp.InputPipe()
	}
	return nil
}

// OutPipe exposes the PTY output pipe (read from this to get child's stdout/stderr).
func (w *winConPTY) OutPipe() *os.File {
	if cp, ok := w.c.(interface{ OutputPipe() *os.File }); ok {
		return cp.OutputPipe()
	}
	return nil
}
