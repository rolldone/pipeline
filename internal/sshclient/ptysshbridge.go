package sshclient

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"pipeline/internal/pty"
	"pipeline/internal/util"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// PTYSSHBridge represents a bridge between PTY and SSH session for interactive sessions
type PTYSSHBridge struct {
	localPTY   pty.PTY
	sshClient  *SSHClient
	sshSession *ssh.Session
	// ioCancel   chan bool
	ioOnce sync.Once

	initialCommand string
	postCommand    string

	StdinMatcher   func([]byte) bool
	StdinCallback  func([]byte)
	StdinObserver  func([]byte)
	outputDisabled bool
	outputMu       sync.Mutex
	outputCache    []byte
	cacheMu        sync.Mutex

	localTTY *os.File

	// stdin control
	// stopStdinCh chan struct{}

	stdinPipe io.WriteCloser

	mu    sync.RWMutex // protect concurrent access to shared fields below
	stdin io.WriteCloser
	// exit listener called when the interactive session exits
	exitListener         func()
	inputListener        func([]byte)
	inputHitCodeListener func(string)
	exitMu               sync.Mutex
	// inputBuf is a small bounded channel used to queue stdin fragments
	// delivered to this bridge. The drainer goroutine writes queued data
	// into the bridge's stdin writer and notifies the inputListener.
	inputBuf chan []byte

	// separate cancel funcs for input and output lifecycles
	inputCancel  context.CancelFunc
	outputCancel context.CancelFunc

	oldState   *term.State
	stdoutPipe io.Reader
	stderrPipe io.Reader
	// output tap receives stdout/stderr bytes regardless of outputDisabled
	outputTap func([]byte, bool)
	// reconnect state
	reconnectMu    sync.Mutex
	reconnecting   bool
	ReconnectLimit time.Duration // max time to attempt reconnect
	ReconnectBase  time.Duration // base backoff

	// exiting indicates the bridge is in the process of normal exit/close
	exiting   bool
	exitingMu sync.Mutex
}

// cacheOutput adds output data to the cache with FIFO strategy (removes oldest data when full)
func (bridge *PTYSSHBridge) cacheOutput(data []byte) {
	const maxCacheSize = 1024 * 1024 // 1MB
	bridge.cacheMu.Lock()
	defer bridge.cacheMu.Unlock()

	dataLen := len(data)
	if dataLen == 0 {
		return
	}

	currentLen := len(bridge.outputCache)

	// If we have space, just append
	if currentLen+dataLen <= maxCacheSize {
		bridge.outputCache = append(bridge.outputCache, data...)
		return
	}

	// Need to make room using FIFO: remove oldest data
	spaceNeeded := dataLen
	spaceAvailable := maxCacheSize - currentLen

	if spaceAvailable < 0 {
		// Cache is already over limit, clear it entirely
		bridge.outputCache = make([]byte, 0, maxCacheSize)
		bridge.outputCache = append(bridge.outputCache, data...)
		return
	}

	// Remove enough from the beginning to make room
	bytesToRemove := spaceNeeded - spaceAvailable
	if bytesToRemove > currentLen {
		// Need to remove more than we have, clear and add new data
		bridge.outputCache = make([]byte, 0, maxCacheSize)
		bridge.outputCache = append(bridge.outputCache, data...)
	} else {
		// Remove oldest bytes and append new data
		bridge.outputCache = bridge.outputCache[bytesToRemove:]
		bridge.outputCache = append(bridge.outputCache, data...)
	}
}

func NewPTYSSHBridge(sshClient *SSHClient) (*PTYSSHBridge, error) {
	ptWrapper, ptFile, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to create PTY: %v", err)
	}
	sshSession, err := sshClient.CreatePTYSession()
	if err != nil {
		if ptWrapper != nil {
			ptWrapper.Close()
		}
		return nil, fmt.Errorf("failed to create SSH session: %v", err)
	}

	return &PTYSSHBridge{
		localPTY:   ptWrapper,
		localTTY:   ptFile,
		sshClient:  sshClient,
		sshSession: sshSession,
		// ioCancel:   make(chan bool),
		ioOnce: sync.Once{},
	}, nil
}

func NewPTYSSHBridgeWithCommand(sshClient *SSHClient, initialCommand string) (*PTYSSHBridge, error) {
	// keep behavior consistent with NewPTYSSHBridge: open a PTY master
	ptWrapper, ptFile, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to create PTY: %v", err)
	}
	sshSession, err := sshClient.CreatePTYSession()
	if err != nil {
		if ptWrapper != nil {
			_ = ptWrapper.Close()
		}
		if ptFile != nil {
			_ = ptFile.Close()
		}
		return nil, fmt.Errorf("failed to create SSH session: %v", err)
	}

	return &PTYSSHBridge{
		localPTY:   ptWrapper,
		localTTY:   ptFile,
		sshClient:  sshClient,
		sshSession: sshSession,
		// ioCancel:       make(chan bool),
		ioOnce:         sync.Once{},
		initialCommand: initialCommand,
	}, nil
}

// NewPTYSSHBridgeWithCommandAndPost creates a bridge with both initial shell command and post command
func NewPTYSSHBridgeWithCommandAndPost(sshClient *SSHClient, initialCommand string, postCommand string) (*PTYSSHBridge, error) {
	bridge, err := NewPTYSSHBridgeWithCommand(sshClient, initialCommand)
	if err != nil {
		return nil, err
	}
	bridge.postCommand = postCommand
	return bridge, nil
}

// StartInteractiveShell starts an interactive shell session
func (bridge *PTYSSHBridge) StartInteractiveShell() error {

	// Best-effort: set stdin into raw mode for interactive sessions and keep
	// the restore function so Pause/Resume/Close can restore it.
	oldstate, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to snapshot terminal state: %v", err)
	}
	bridge.oldState = oldstate

	cols, rows := 80, 24

	if err := bridge.sshSession.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{}); err != nil {
		if bridge.localPTY != nil {
			_ = bridge.localPTY.Close()
		}
		bridge.sshSession.Close()
		return fmt.Errorf("failed to request PTY: %v", err)
	}

	stdoutPipe, err := bridge.sshSession.StdoutPipe()
	if err != nil {
		fmt.Print("failed to get stdout pipe: ", err)
		os.Exit(1)
		return err
	}
	bridge.stdoutPipe = stdoutPipe
	stderrPipe, err := bridge.sshSession.StderrPipe()
	if err != nil {
		fmt.Print("failed to get stdout pipe: ", err)
		os.Exit(1)
		return err
	}
	bridge.stderrPipe = stderrPipe

	// Create separate contexts: output stays alive until session end; input can be paused
	outCtx, outCancel := context.WithCancel(context.Background())
	inCtx, inCancel := context.WithCancel(context.Background())
	bridge.outputCancel = outCancel
	bridge.inputCancel = inCancel
	log.Println("StartInteractiveShell : Started interactive shell session")
	// start readers: output readers long-lived, input reader can be canceled on Pause
	bridge.ProcessPTYReadOutput(outCtx)
	bridge.ProcessPTYReadInput(inCtx)

	// Start a small resize watcher that polls the terminal size and applies
	// WindowChange on the remote session when it changes. This is a simple
	// fallback for environments that don't send SIGWINCH or when the TUI
	// framework doesn't propagate resize events.
	go func(ctx context.Context) {
		prevW, prevH := rows, cols
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
				if bridge.sshSession == nil {
					continue
				}
				if w, h, err := getTerminalSizeFallback(); err == nil {
					if w != prevW || h != prevH {
						_ = bridge.sshSession.WindowChange(h, w)
						log.Printf("DEBUG: Resize watcher WindowChange applied height=%d width=%d\n", h, w)
						prevW, prevH = w, h
					}
				}
			}
		}
	}(outCtx)

	stdinPipe, err := bridge.sshSession.StdinPipe()
	if err != nil {
		return err
	}
	bridge.stdinPipe = stdinPipe
	// expose stdin writer so PTYManager can forward stdin bytes into the session
	bridge.SetStdinWriter(stdinPipe)

	bridge.sshSession.Setenv("TERM", "xterm-256color")

	log.Println("DEBUG: Starting interactive shell with initialCommand:", bridge.initialCommand)

	// Smart detection: if initialCommand contains both shell + user command,
	// split them to avoid double execution. Look for patterns like:
	// "cmd.exe /K cd /d \"path\" & ping google" -> shell="cmd.exe /K cd /d \"path\"", cmd="ping google"
	ic := strings.TrimSpace(bridge.initialCommand)
	var shellCmd, userCmd string

	if strings.Contains(ic, "cmd.exe /K") && strings.Contains(ic, " & ") {
		// Windows pattern: "cmd.exe /K cd /d \"path\" & command"
		parts := strings.Split(ic, " & ")
		if len(parts) >= 2 {
			shellCmd = strings.TrimSpace(parts[0])                      // "cmd.exe /K cd /d \"path\""
			userCmd = strings.TrimSpace(strings.Join(parts[1:], " & ")) // "command"
			log.Printf("DEBUG: Detected Windows shell+cmd: shell=%q cmd=%q\n", shellCmd, userCmd)
		} else {
			shellCmd = ic
		}
	} else if strings.Contains(ic, "bash -c") && strings.Contains(ic, " ; exec bash") {
		// Unix pattern: "mkdir -p /path && cd /path && bash -c 'command' ; exec bash"
		if idx := strings.Index(ic, "bash -c "); idx != -1 {
			beforeBash := ic[:idx] + "bash -l" // "mkdir -p /path && cd /path && bash -l"
			shellCmd = beforeBash
			// Extract command from bash -c '...'
			remaining := ic[idx+8:] // after "bash -c "
			if idx2 := strings.Index(remaining, " ; exec bash"); idx2 != -1 {
				cmdPart := remaining[:idx2]
				cmdPart = strings.Trim(cmdPart, "'\"") // remove quotes
				userCmd = strings.TrimSpace(cmdPart)
				log.Printf("DEBUG: Detected Unix shell+cmd: shell=%q cmd=%q\n", shellCmd, userCmd)
			}
		}
		if shellCmd == "" {
			shellCmd = ic
		}
	} else {
		shellCmd = ic
	}

	// If we have both shell and user command, use two-step approach
	if userCmd != "" {
		// Two-step: start shell, then send user command
		log.Printf("DEBUG: Two-step execution: starting shell=%q\n", shellCmd)
		if err := bridge.sshSession.Start(shellCmd); err != nil {
			return err
		}
		if bridge.stdinPipe != nil {
			go func(cmd string, w io.WriteCloser) {
				// longer delay for shell to fully initialize
				time.Sleep(300 * time.Millisecond)
				// util.Default.ClearLine()
				// util.Default.Printf("DEBUG: Sending user command=%q\n", cmd)
				// util.Default.ClearLine()
				_, _ = w.Write([]byte(cmd + "\r\n"))
			}(userCmd, bridge.stdinPipe)
		}
	} else {
		// Single command: determine if it's a shell starter or regular command
		startsShell := false
		if ic != "" {
			low := strings.ToLower(ic)
			if strings.HasPrefix(low, "cmd.exe") || strings.Contains(low, "/k") || strings.Contains(low, "-noexit") || strings.Contains(low, "-c") {
				startsShell = true
			}
		}

		if ic != "" && startsShell {
			// Start shell directly
			util.Default.Printf("DEBUG: Starting shell directly: %q\n", ic)
			if err := bridge.sshSession.Start(bridge.initialCommand); err != nil {
				return err
			}
		} else {
			// Legacy mode: start shell and write command to stdin
			util.Default.Printf("DEBUG: Legacy mode: Shell() + stdin write: %q\n", ic)
			if err := bridge.sshSession.Shell(); err != nil {
				return err
			}
			if bridge.initialCommand != "" && bridge.stdinPipe != nil {
				go func(cmd string, w io.WriteCloser) {
					time.Sleep(150 * time.Millisecond)
					_, _ = w.Write([]byte(cmd + "\r\n"))
				}(bridge.initialCommand, bridge.stdinPipe)
			}
		}
	}

	// Note: the bridge no longer starts a stdin-reading goroutine. The PTYManager
	// is responsible for reading os.Stdin and forwarding bytes into the bridge's
	// stdin writer. This avoids multiple readers on os.Stdin and centralizes
	// shortcut handling.
	util.Default.ClearLine()
	util.Default.Print("2982394 : Started interactive shell session")
	util.Default.ClearLine()
	err = bridge.sshSession.Wait()
	if err != nil {
		if err == io.EOF {
			// normal exit
		} else {
			util.Default.ClearLine()
			util.Default.Print("ssh session wait exited with err:", err)
			util.Default.ClearLine()
		}
	}

	// Before notifying exit listener, cancel reader goroutines and disable output
	// so they won't trigger reconnect logic on normal session exit.
	if bridge.inputCancel != nil {
		bridge.inputCancel()
	}
	if bridge.outputCancel != nil {
		bridge.outputCancel()
	}
	bridge.outputMu.Lock()
	bridge.outputDisabled = true
	bridge.outputMu.Unlock()

	// mark exiting so reconnect logic knows this was an intentional close
	bridge.exitingMu.Lock()
	bridge.exiting = true
	bridge.exitingMu.Unlock()

	// Notify registered exit listener (if any). Protect invocation with mutex
	util.Default.Print("PTYSSHBridge: invoking exit listener")
	util.Default.ClearLine()
	bridge.exitListener()
	log.Println("PTYSSHBridge : SetOnExitListener exit listener done")
	return nil
}

// ProcessPTYReadInput starts stdin reader controlled by input context
func (bridge *PTYSSHBridge) ProcessPTYReadInput(ctx context.Context) error {
	// stdin reader
	go func(ctx context.Context) {
		buf := make([]byte, 256)
		throttledMyFunc := util.ThrottledFunction(300 * time.Millisecond)
		util.Default.ClearLine()
		for {
			select {
			case <-ctx.Done():
				// fmt.Println("PTYSSHBridge stdin reader: context done, exiting")
				log.Println("PTYSSHBridge stdin reader: context done, exiting")
				return
			default:
				n, rerr := os.Stdin.Read(buf)
				if n > 0 {

					// call inputListener asynchronously
					bridge.mu.RLock()
					il := bridge.inputListener
					ih := bridge.inputHitCodeListener
					bridge.mu.RUnlock()

					if il != nil {
						data := make([]byte, n)
						copy(data, buf[:n])
						il(data)
					}
					isHit := false
					// Scan for ESC + digit (Alt+1..Alt+9 and Alt+0)
					if ih != nil {
						for i := 0; i < n-1; i++ {
							if buf[i] == 0x1b { // ESC
								c := buf[i+1]
								if (c >= '1' && c <= '9') || c == '0' {
									digit := string([]byte{c})
									throttledMyFunc(func() {
										ih("alt+" + digit)
									})
									isHit = true
									i++
								}
							}
						}
					}
					if isHit {
						continue
					}
					w := bridge.GetStdinWriter()
					if w != nil {
						if _, werr := w.Write(buf[:n]); werr != nil {
							return
						}
					}
				}

				if rerr != nil {
					return
				}
			}
		}
	}(ctx)

	return nil
}

// ProcessPTYReadOutput starts stdout/stderr readers controlled by output context
func (bridge *PTYSSHBridge) ProcessPTYReadOutput(ctx context.Context) error {
	stdoutPipe := bridge.stdoutPipe
	stderrPipe := bridge.stderrPipe
	// stdout reader
	go func(ctx context.Context) {
		buf := make([]byte, 4096)
		util.Default.ClearLine()
		// Set pending output delay to allow terminal to initialize
		time.Sleep(2 * time.Second)
		for {
			select {
			case <-ctx.Done():
				// fmt.Println("PTYSSHBridge stdout reader: context done, exiting")
				log.Println("PTYSSHBridge stdout reader: context done, exiting")
				return
			default:
				n, err := stdoutPipe.Read(buf)
				if n > 0 {
					bridge.outputMu.Lock()
					disabled := bridge.outputDisabled
					bridge.outputMu.Unlock()
					if !disabled {
						_, _ = os.Stdout.Write(buf[:n])
					}
					// Always cache output as history buffer
					bridge.cacheOutput(buf[:n])
					// always pass to tap regardless of disabled
					if bridge.outputTap != nil {
						data := make([]byte, n)
						copy(data, buf[:n])
						bridge.outputTap(data, false)
					}
				}
				if err != nil {
					util.Default.ClearLine()
					util.Default.Print("PTYSSHBridge stdout reader error:", err)
					util.Default.ClearLine()
					// If EOF (clean/expected exit), do not attempt reconnect.
					if err == io.EOF {
						return
					}
					// Trigger reconnect handling (best-effort)
					go bridge.handleDisconnect(err)
					return
				}
			}
		}
	}(ctx)

	// stderr reader
	go func(ctx context.Context) {
		buf := make([]byte, 4096)
		// Set pending output delay to allow terminal to initialize
		time.Sleep(2 * time.Second)
		for {
			select {
			case <-ctx.Done():
				// fmt.Println("PTYSSHBridge stderr reader: context done, exiting")
				log.Println("PTYSSHBridge stderr reader: context done, exiting")
				return
			default:
				n, err := stderrPipe.Read(buf)
				if n > 0 {
					bridge.outputMu.Lock()
					disabled := bridge.outputDisabled
					bridge.outputMu.Unlock()
					if !disabled {
						_, _ = os.Stderr.Write(buf[:n])
					}
					// Always cache output as history buffer
					bridge.cacheOutput(buf[:n])
					if bridge.outputTap != nil {
						data := make([]byte, n)
						copy(data, buf[:n])
						bridge.outputTap(data, true)
					}
				}
				if err != nil {
					util.Default.ClearLine()
					util.Default.Print("PTYSSHBridge stderr reader error:", err)
					// If EOF (clean/expected exit), do not attempt reconnect.
					if err == io.EOF {
						return
					}
					// bridge.ioOnce.Do(func() { close(bridge.ioCancel) })
					// For non-EOF errors we don't trigger disconnect here because
					// stderr errors are less actionable; primary reconnect is
					// handled by stdout reader. Still, return to exit goroutine.
					return
				}
			}
		}
	}(ctx)

	return nil
}

// Pause stops stdin/output
func (bridge *PTYSSHBridge) Pause() error {

	bridge.localPTY.Write([]byte("\x1b")) // ensure prompt ends cleanly
	bridge.localPTY.Write([]byte("\x08")) // send backspace to clear any partial input

	bridge.outputMu.Lock()
	bridge.outputDisabled = true
	bridge.outputMu.Unlock()
	if bridge.inputCancel != nil {
		bridge.inputCancel()
	}

	term.Restore(int(os.Stdin.Fd()), bridge.oldState)

	fmt.Print("\033c")

	return nil
}

// Resume restarts stdin/output
func (bridge *PTYSSHBridge) Resume() error {

	// load cached output first
	bridge.cacheMu.Lock()
	if len(bridge.outputCache) > 0 {
		fmt.Print(string(bridge.outputCache))
	}
	bridge.cacheMu.Unlock()

	bridge.outputMu.Lock()
	bridge.outputDisabled = false
	bridge.outputMu.Unlock()

	oldstate, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to snapshot terminal state: %v", err)
	}
	bridge.oldState = oldstate

	ctx, cancel := context.WithCancel(context.Background())
	bridge.inputCancel = cancel
	bridge.ProcessPTYReadInput(ctx)

	bridge.localPTY.Write([]byte("\x1b")) // ensure prompt ends cleanly
	bridge.localPTY.Write([]byte("\x08")) // send backspace to clear any partial input

	return nil
}

// Close closes bridge
func (bridge *PTYSSHBridge) Close() error {
	bridge.outputDisabled = true
	log.Println("PTYSSHBridge : Close called, output disabled")

	bridge.cacheMu.Lock()
	bridge.outputCache = nil
	bridge.cacheMu.Unlock()
	log.Println("PTYSSHBridge : cache cleared")

	bridge.exitingMu.Lock()
	bridge.exiting = true
	bridge.exitingMu.Unlock()

	log.Println("PTYSSHBridge : SetOnExitListener exit listener called")
	if bridge.localTTY != nil {
		bridge.localTTY.Close()
		log.Println("PTYSSHBridge : localTTY closed")
	}

	if bridge.sshSession != nil {
		bridge.sshSession.Close()
		log.Println("PTYSSHBridge : sshSession closed")
	}
	if bridge.localPTY != nil {
		_ = bridge.localPTY.Close()
		log.Println("PTYSSHBridge : localPTY closed")
		bridge.localPTY = nil
	}

	if bridge.inputCancel != nil {
		bridge.inputCancel()
	}
	if bridge.outputCancel != nil {
		bridge.outputCancel()
	}
	log.Println("PTYSSHBridge : context cancelled")

	// We dont need to clear these, PTYManager will discard the bridge reference
	// bridge.SetOnExitListener(nil)
	log.Println("PTYSSHBridge : Exit listener cleared")
	// bridge.SetOnInputHitCodeListener(nil)
	log.Println("PTYSSHBridge : InputHitCodeListener cleared")
	/// bridge.SetOnInputListener(nil)
	log.Println("PTYSSHBridge : InputListener cleared")
	log.Println("PTYSSHBridge : Bridge closed")

	return nil
}

// SetOnExitListener registers a listener function invoked when the bridge exits
func (bridge *PTYSSHBridge) SetOnExitListener(fn func()) {
	bridge.exitMu.Lock()
	defer bridge.exitMu.Unlock()
	bridge.exitListener = fn
}

// SetOnInputListener registers a listener for input bytes
func (bridge *PTYSSHBridge) SetOnInputListener(fn func([]byte)) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.inputListener = fn
}

// SetOnInputHitCodeListener registers a listener for special hit codes
func (bridge *PTYSSHBridge) SetOnInputHitCodeListener(fn func(string)) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.inputHitCodeListener = fn
}

// SetStdinWriter sets the stdin writer for the bridge
func (bridge *PTYSSHBridge) SetStdinWriter(w io.WriteCloser) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.stdin = w
}

// GetStdinWriter returns the stdin writer
func (bridge *PTYSSHBridge) GetStdinWriter() io.WriteCloser {
	bridge.mu.RLock()
	defer bridge.mu.RUnlock()
	return bridge.stdin
}

// Thread-safe setters/getters for stdin hooks and writer

func (b *PTYSSHBridge) SetStdinMatcher(m func([]byte) bool) {
	b.mu.Lock()
	b.StdinMatcher = m
	b.mu.Unlock()
}

func (b *PTYSSHBridge) GetStdinMatcher() func([]byte) bool {
	b.mu.RLock()
	m := b.StdinMatcher
	b.mu.RUnlock()
	return m
}

func (b *PTYSSHBridge) SetStdinCallback(cb func([]byte)) {
	b.mu.Lock()
	b.StdinCallback = cb
	b.mu.Unlock()
}

func (b *PTYSSHBridge) GetStdinCallback() func([]byte) {
	b.mu.RLock()
	cb := b.StdinCallback
	b.mu.RUnlock()
	return cb
}

func (b *PTYSSHBridge) SetStdinObserver(o func([]byte)) {
	b.mu.Lock()
	b.StdinObserver = o
	b.mu.Unlock()
}

// SetOutputTap registers a tap receiving stdout/stderr bytes (err=true for stderr).
// The tap is invoked regardless of outputDisabled so logging can continue while UI is paused.
func (bridge *PTYSSHBridge) SetOutputTap(fn func([]byte, bool)) {
	bridge.outputMu.Lock()
	bridge.outputTap = fn
	bridge.outputMu.Unlock()
}

func (b *PTYSSHBridge) GetStdinObserver() func([]byte) {
	b.mu.RLock()
	o := b.StdinObserver
	b.mu.RUnlock()
	return o
}

// handleDisconnect attempts to reconnect the SSH session with exponential backoff.
// It will Pause the bridge, try reconnecting up to ReconnectLimit, and on success
// restart the interactive shell. If reconnect fails within the limit, it closes
// the bridge.
func (b *PTYSSHBridge) handleDisconnect(err error) {
	b.reconnectMu.Lock()
	if b.reconnecting {
		b.reconnectMu.Unlock()
		return
	}
	b.reconnecting = true
	b.reconnectMu.Unlock()

	defer func() {
		b.reconnectMu.Lock()
		b.reconnecting = false
		b.reconnectMu.Unlock()
	}()

	// If we are intentionally exiting/closing, skip reconnect attempts.
	b.exitingMu.Lock()
	isExiting := b.exiting
	b.exitingMu.Unlock()
	if isExiting {
		util.Default.Print("Connection lost during intentional exit; not reconnecting")
		b.reconnectMu.Lock()
		b.reconnecting = false
		b.reconnectMu.Unlock()
		return
	}

	util.Default.Print("Connection lost, attempting reconnect...")

	// pause IO and restore terminal
	_ = b.Pause()

	base := b.ReconnectBase
	if base == 0 {
		base = 1 * time.Second
	}
	limit := b.ReconnectLimit
	if limit == 0 {
		limit = 2 * time.Minute
	}

	deadline := time.Now().Add(limit)
	backoff := base

	for time.Now().Before(deadline) {
		util.Default.Print("Attempting reconnect...")
		if b.sshClient != nil {
			if cerr := b.sshClient.Connect(); cerr == nil {
				sess, serr := b.sshClient.CreatePTYSession()
				if serr == nil {
					// Close any previous session reference
					if b.sshSession != nil {
						_ = b.sshSession.Close()
					}
					b.sshSession = sess
					// try to restart interactive shell in background
					go func() {
						if err := b.StartInteractiveShell(); err != nil {
							util.Default.Print("reconnected but failed to restart shell:", err)
						}
					}()
					util.Default.Print("Reconnected")
					return
				}
				util.Default.Print("create session failed:", serr)
			} else {
				util.Default.Print("reconnect client failed:", cerr)
			}
		}

		time.Sleep(backoff)
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}

	util.Default.Print("Failed to reconnect within limit, closing bridge")
	_ = b.Close()
}
