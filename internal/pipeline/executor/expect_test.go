package executor

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"

	"pipeline/internal/pipeline/types"
)

// mockSSHClient implements the SSHClient interface minimally for tests.
type ptyMockSSHClient struct {
	killCalled int32
}

func (m *ptyMockSSHClient) Connect() error                                { return nil }
func (m *ptyMockSSHClient) Close() error                                  { return nil }
func (m *ptyMockSSHClient) UploadBytes([]byte, string, os.FileMode) error { return nil }
func (m *ptyMockSSHClient) UploadFile(string, string) error               { return nil }
func (m *ptyMockSSHClient) DownloadFile(string, string) error             { return nil }
func (m *ptyMockSSHClient) RunCommand(string) error                       { return nil }
func (m *ptyMockSSHClient) RunCommandWithOutput(string) (string, error)   { return "", nil }
func (m *ptyMockSSHClient) SyncFile(string, string) error                 { return nil }
func (m *ptyMockSSHClient) RunCommandWithStream(string, bool) (<-chan string, <-chan error, error) {
	outc := make(chan string)
	errc := make(chan error)
	close(outc)
	close(errc)
	return outc, errc, nil
}

// ExecWithPTY starts a local sh process under a PTY. The provided script
// should drive prompts so the executor can match them.
func (m *ptyMockSSHClient) ExecWithPTY(cmd string) (io.WriteCloser, io.Reader, io.Reader, func() error, func() error, error) {
	c := newCommandForPTY(cmd)
	ptmx, err := pty.Start(c)
	if err != nil {
		// Some restricted environments cannot start processes with Setpgid.
		// Fall back to starting without Setpgid so tests can run in sandboxes.
		fallback := exec.Command("sh", "-lc", cmd)
		ptmx2, err2 := pty.Start(fallback)
		if err2 != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("failed to start pty: %v (fallback: %v)", err, err2)
		}
		c = fallback
		ptmx = ptmx2
	}

	waitFn := func() error {
		err := c.Wait()
		_ = ptmx.Close()
		return err
	}

	killFn := func() error {
		atomic.StoreInt32(&m.killCalled, 1)
		if c.Process != nil {
			_ = killProcessGroup(c.Process.Pid)
		}
		_ = ptmx.Close()
		return nil
	}

	// PTY merges stdout/stderr; return ptmx as stdout and an empty stderr reader.
	return ptmx, ptmx, bytes.NewReader(nil), waitFn, killFn, nil
}

func TestExpectHappyPath(t *testing.T) {
	e := NewExecutor()

	// Build a step that expects the literal PROMPT: and responds with answer
	step := &types.Step{}
	step.Expect = []types.Expect{{Prompt: `PROMPT:`, Response: `myresponse`}}

	// Command: print PROMPT:, then read a line and echo it, then exit
	script := `printf "PROMPT:"; read -r line; printf "GOT:%s\n" "$line"`

	mock := &ptyMockSSHClient{}

	out, err := e.runCommandInteractive(mock, script, step.Expect, types.Vars{}, 10, 5, false, step, nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "GOT:myresponse") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestExpectTimeoutAndKill(t *testing.T) {
	e := NewExecutor()

	// Step expects PROMPT: which will never be emitted by the script
	step := &types.Step{}
	step.Expect = []types.Expect{{Prompt: `PROMPT:`, Response: `resp`}}

	// Script: sleep long (no prompt)
	script := `sleep 10`

	mock := &ptyMockSSHClient{}

	// Set a small overall timeout so executor triggers kill
	start := time.Now()
	_, err := e.runCommandInteractive(mock, script, step.Expect, types.Vars{}, 1, 1, true, step, nil)
	took := time.Since(start)
	if err == nil {
		t.Fatalf("expected error due to timeout, got nil")
	}
	if atomic.LoadInt32(&mock.killCalled) == 0 {
		t.Fatalf("expected killFn to be called on timeout")
	}
	if took > 5*time.Second {
		t.Fatalf("timeout handling took too long: %v", took)
	}
}
