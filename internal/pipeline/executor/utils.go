package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"strconv"

	"github.com/cespare/xxhash/v2"

	gitignore "github.com/sabhiram/go-gitignore"

	"pipeline/internal/config"
	"pipeline/internal/logging"
	"pipeline/internal/pipeline/types"
)

func (e *Executor) interpolateString(template string, vars types.Vars) string {
	if template == "" {
		return template
	}

	result := template

	// Simple variable interpolation: {{VAR_NAME}}
	for key, value := range vars {
		strValue := fmt.Sprintf("%v", value)
		// Support multiple common placeholder forms used across the codebase/tests:
		// - {{key}}
		// - {{ key }}
		// - {{.key}}
		// - {{ .key }}
		// - with or without surrounding spaces
		variants := []string{
			"{{" + key + "}}",
			"{{ " + key + " }}",
			"{{." + key + "}}",
			"{{ ." + key + " }}",
			"{{ ." + key + "}}",
			"{{." + key + " }}",
		}
		for _, ph := range variants {
			result = strings.ReplaceAll(result, ph, strValue)
		}
	}

	return result
}

// expandResponse replaces ${n} placeholders in response templates using
// capture groups from regex matches. groups[0] is the full match, groups[1]
// is the first capture group, etc.
func expandResponse(tmpl string, groups []string) string {
	if tmpl == "" {
		return ""
	}
	// pattern to find ${N}
	repl := regexp.MustCompile(`\$\{(\d+)\}`)
	return repl.ReplaceAllStringFunc(tmpl, func(m string) string {
		sub := repl.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		idx, err := strconv.Atoi(sub[1])
		if err != nil {
			return ""
		}
		if idx < 0 || idx >= len(groups) {
			return ""
		}
		return groups[idx]
	})
}

// invokeKillLogger logs and invokes a kill function with contextual metadata.
func (e *Executor) invokeKillLogger(killFn func() error, reason, cmd string, step *types.Step, job *types.Job) {
	if killFn == nil {
		return
	}
	stepName := ""
	jobName := ""
	if step != nil {
		stepName = step.Name
	}
	if job != nil {
		jobName = job.Name
	}
	l := logging.WithFields(map[string]interface{}{"event": "executor.kill", "reason": reason, "job": jobName, "step": stepName, "cmd": cmd})
	l.Info("kill: start", nil)
	if err := killFn(); err != nil {
		l.Error("kill: result", map[string]interface{}{"result": "error", "error": err.Error()})
	} else {
		l.Info("kill: result", map[string]interface{}{"result": "success"})
	}
}

// processExpectBuffer looks for the earliest regex match among compiled expects
// within the buffer. If a match is found it expands the response using
// capture groups, writes it to stdin, and consumes the buffer up to the end
// of the match. Returns true if a match was processed.
func processExpectBuffer(b *bytes.Buffer, compiled []struct {
	re   *regexp.Regexp
	resp string
}, stdin io.WriteCloser) bool {
	if b.Len() == 0 || len(compiled) == 0 {
		return false
	}
	data := b.String()
	earliestStart := -1
	earliestEnd := -1
	chosen := -1
	var chosenMatches []string
	for i, ce := range compiled {
		if ce.re == nil {
			continue
		}
		if loc := ce.re.FindStringIndex(data); loc != nil {
			start := loc[0]
			end := loc[1]
			if earliestStart == -1 || start < earliestStart {
				// store matches for replacement
				matches := ce.re.FindStringSubmatch(data)
				earliestStart = start
				earliestEnd = end
				chosen = i
				chosenMatches = matches
			}
		}
	}
	if chosen == -1 {
		// no match
		// keep buffer capped to last 16KB to avoid unbounded growth
		const maxBuf = 16 * 1024
		if b.Len() > maxBuf {
			// trim front
			d := b.Bytes()
			tail := d[b.Len()-maxBuf:]
			b.Reset()
			b.Write(tail)
		}
		return false
	}

	// expand response and write to stdin
	resp := expandResponse(compiled[chosen].resp, chosenMatches)
	if stdin != nil {
		io.WriteString(stdin, resp+"\n")
	}

	// consume buffer up to earliestEnd
	remainder := data[earliestEnd:]
	b.Reset()
	b.WriteString(remainder)
	return true
}

// matchesIncludeExclude checks if a path matches include/exclude patterns
func (e *Executor) matchesIncludeExclude(relPath string, include, exclude []string) bool {
	// If no patterns specified, include everything
	if len(include) == 0 && len(exclude) == 0 {
		return true
	}

	// Check exclude patterns first (exclude takes precedence)
	for _, pattern := range exclude {
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return false
		}
		// Also check if path starts with pattern (for directory matches)
		if strings.HasPrefix(relPath, pattern) {
			return false
		}
	}

	// If include patterns specified, must match at least one
	if len(include) > 0 {
		for _, pattern := range include {
			if matched, _ := filepath.Match(pattern, relPath); matched {
				return true
			}
			// Also check if path starts with pattern (for directory matches)
			if strings.HasPrefix(relPath, pattern) {
				return true
			}
		}
		return false
	}

	// No include patterns and not excluded = include
	return true
}

// copyLocalPath copies a file from source to destination
func (e *Executor) copyLocalPath(src, dst string) error {
	// Ensure destination directory exists
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %v", err)
	}

	// Open source file
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %v", src, err)
	}
	defer srcFile.Close()

	// Create destination file
	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %v", dst, err)
	}
	defer dstFile.Close()

	// Copy file contents
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return fmt.Errorf("failed to copy file contents: %v", err)
	}

	// Copy file permissions
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("failed to stat source file: %v", err)
	}
	return os.Chmod(dst, srcInfo.Mode())
}

// computeFileHash computes SHA256 hash of a file
func computeFileHash(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// computeXXHash computes the xxHash of a file and returns hex string
func computeXXHash(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := xxhash.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// shellQuote quotes a POSIX string safely using single quotes
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// isWindowsTarget returns true if the osTarget string indicates a Windows target.
// Centralizes windows detection logic so it can be changed in one place.
func isWindowsTarget(osTarget string) bool {
	return strings.Contains(strings.ToLower(osTarget), "win")
}

// stripAnsiCodes removes ANSI escape sequences from string
func stripAnsiCodes(str string) string {
	// Regex pattern to match ANSI escape codes
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	return ansiRegex.ReplaceAllString(str, "")
}

// isTextBytes performs a heuristic check whether data bytes represent text.
// It returns true if data likely contains UTF-8 text and no NUL bytes.
func isTextBytes(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	// quick NUL check
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	// check UTF-8 validity on sample (whole data or prefix)
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	if !utf8.Valid(sample) {
		// If not valid UTF-8, treat as binary by default
		return false
	}
	// compute proportion of non-printable runes; allow a small fraction (e.g., < 10%)
	printable := 0
	total := 0
	for len(sample) > 0 {
		r, size := utf8.DecodeRune(sample)
		sample = sample[size:]
		total++
		if r == utf8.RuneError {
			continue
		}
		if r >= 32 || r == '\n' || r == '\r' || r == '\t' {
			printable++
		}
	}
	if total == 0 {
		return true
	}
	return float64(printable)/float64(total) >= 0.9
}

// RingBuffer stores lines with a total byte cap. FIFO when cap exceeded.
type RingBuffer struct {
	lines      []string
	totalBytes int
	capBytes   int
}

func NewRingBuffer(capBytes int) *RingBuffer {
	return &RingBuffer{capBytes: capBytes}
}

// Add appends a line and evicts oldest lines while totalBytes > capBytes
func (r *RingBuffer) Add(line string) {
	if line == "" {
		return
	}
	r.lines = append(r.lines, line)
	r.totalBytes += len(line)
	// Evict oldest until under cap
	for r.totalBytes > r.capBytes && len(r.lines) > 0 {
		// remove first
		removed := r.lines[0]
		r.lines = r.lines[1:]
		r.totalBytes -= len(removed)
	}
}

// LastN returns up to n last lines (copy)
func (r *RingBuffer) LastN(n int) []string {
	if n <= 0 {
		return nil
	}
	if len(r.lines) == 0 {
		return nil
	}
	if n >= len(r.lines) {
		// return copy
		out := make([]string, len(r.lines))
		copy(out, r.lines)
		return out
	}
	start := len(r.lines) - n
	out := make([]string, n)
	copy(out, r.lines[start:])
	return out
}

// All returns a copy of all stored lines in the buffer.
func (r *RingBuffer) All() []string {
	if len(r.lines) == 0 {
		return nil
	}
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}

// groupJobsByLevel groups jobs by their dependency level (for parallel execution)
func (e *Executor) groupJobsByLevel(jobNames []string, pipeline *types.Pipeline) [][]string {
	// Simple implementation: return all jobs in one level for now
	// TODO: Implement proper dependency analysis
	return [][]string{jobNames}
}

// copyFile copies a file from src to dst (global function)
func copyFile(src, dst string) error {
	// Ensure destination directory exists
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %v", err)
	}

	// Open source file
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %v", src, err)
	}
	defer srcFile.Close()

	// Create destination file
	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %v", dst, err)
	}
	defer dstFile.Close()

	// Copy file contents
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return fmt.Errorf("failed to copy file contents: %v", err)
	}

	// Copy file permissions
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("failed to stat source file: %v", err)
	}
	return os.Chmod(dst, srcInfo.Mode())
}

// findStepIndex finds the index of a step by name in a job
func (e *Executor) findStepIndex(job *types.Job, stepName string) int {
	for i, step := range job.Steps {
		if step.Name == stepName {
			return i
		}
	}
	return -1
}

// runScriptStep executes a script step
func (e *Executor) runScriptStep(step *types.Step, job *types.Job, config map[string]interface{}, vars types.Vars) error {
	// Basic implementation - treat as command step for now
	_, _, err := e.runCommandStep(step, job, config, vars)
	return err
}

// buildFileTransferEntries builds a list of file transfer entries from step configuration
func (e *Executor) buildFileTransferEntries(step *types.Step, vars types.Vars) ([]struct{ Source, Destination, Template string }, error) {
	var entries []struct{ Source, Destination, Template string }

	// Handle single source/destination
	if step.Source != "" && step.Destination != "" {
		entries = append(entries, struct{ Source, Destination, Template string }{
			Source:      e.interpolateString(step.Source, vars),
			Destination: e.interpolateString(step.Destination, vars),
			Template:    step.Template,
		})
	}

	// Handle multiple files if specified
	for _, file := range step.Files {
		src := e.interpolateString(file.Source, vars)
		dst := e.interpolateString(file.Destination, vars)
		entries = append(entries, struct{ Source, Destination, Template string }{
			Source:      src,
			Destination: dst,
			Template:    step.Template, // Use step-level template
		})
	}

	return entries, nil
}

// prepareRenderAndStage prepares and stages files for rendering
func (e *Executor) prepareRenderAndStage(step *types.Step, rawEntries []struct{ Source, Destination, Template string }, vars types.Vars) ([]struct{ Source, Destination string }, func(), []map[string]string, error) {
	var prepared []struct{ Source, Destination string }
	var cleanupPaths []string
	var renderWarnings []map[string]string

	// Ensure temp dir exists before creating temp files. Some tests run in
	// a workspace where `.sync_temp` may not exist yet; CreateTemp requires
	// the directory to exist when a non-empty dir is provided.
	if err := os.MkdirAll(e.tempDir, 0755); err != nil {
		return nil, func() {}, nil, fmt.Errorf("failed to create temp dir: %v", err)
	}

	for _, entry := range rawEntries {
		src := entry.Source
		dst := entry.Destination
		tmpl := entry.Template

		// Determine if templating is enabled for this entry
		fileTemplateEnabled := false
		if tmpl != "" {
			fileTemplateEnabled = (tmpl == "enabled")
		} else if step.Template == "enabled" {
			fileTemplateEnabled = true
		}

		if fileTemplateEnabled {
			// Read source file
			data, err := os.ReadFile(src)
			if err != nil {
				return nil, func() {}, nil, fmt.Errorf("failed to read source file %s: %v", src, err)
			}

			// Check if it's text
			if isTextBytes(data) {
				// Render template
				content := string(data)
				rendered := e.interpolateString(content, vars)

				// Create temp file for rendered content
				tempFile, err := os.CreateTemp(e.tempDir, "rendered-*")
				if err != nil {
					return nil, func() {}, nil, fmt.Errorf("failed to create temp file: %v", err)
				}

				// Try to write rendered content
				if err := e.WriteFileFunc(tempFile.Name(), []byte(rendered), 0644); err != nil {
					tempFile.Close()
					os.Remove(tempFile.Name())
					// Fallback: write original content
					tempFile, err = os.CreateTemp(e.tempDir, "rendered-*")
					if err != nil {
						return nil, func() {}, nil, fmt.Errorf("failed to create fallback temp file: %v", err)
					}
					if err := e.WriteFileFunc(tempFile.Name(), data, 0644); err != nil {
						tempFile.Close()
						os.Remove(tempFile.Name())
						return nil, func() {}, nil, fmt.Errorf("failed to write fallback content: %v", err)
					}
					renderWarnings = append(renderWarnings, map[string]string{
						"file":    src,
						"warning": fmt.Sprintf("failed to write rendered content: %v, using original", err),
					})
				}
				tempFile.Close()
				cleanupPaths = append(cleanupPaths, tempFile.Name())
				prepared = append(prepared, struct{ Source, Destination string }{Source: tempFile.Name(), Destination: dst})
			} else {
				// Binary file - copy as-is
				tempFile, err := os.CreateTemp(e.tempDir, "binary-*")
				if err != nil {
					return nil, func() {}, nil, fmt.Errorf("failed to create temp file for binary: %v", err)
				}
				if _, err := tempFile.Write(data); err != nil {
					tempFile.Close()
					os.Remove(tempFile.Name())
					return nil, func() {}, nil, fmt.Errorf("failed to write binary content: %v", err)
				}
				tempFile.Close()
				cleanupPaths = append(cleanupPaths, tempFile.Name())
				prepared = append(prepared, struct{ Source, Destination string }{Source: tempFile.Name(), Destination: dst})
			}
		} else {
			// No templating - use source directly
			prepared = append(prepared, struct{ Source, Destination string }{Source: src, Destination: dst})
		}
	}

	// Cleanup function
	cleanup := func() {
		for _, path := range cleanupPaths {
			os.Remove(path)
		}
	}

	return prepared, cleanup, renderWarnings, nil
}

// getPermForEntry gets the permission string for a file entry
func (e *Executor) getPermForEntry(step *types.Step, src, dst string) string {
	// Check if this matches a specific file entry
	for _, file := range step.Files {
		if file.Source == src && (file.Destination == dst || file.Destination == "") {
			if file.Perm != "" {
				return file.Perm
			}
		}
	}

	// Default permission
	return "0755"
}

// getStepCommands gets the commands from a step (with priority: commands > command)
func (e *Executor) getStepCommands(step *types.Step, vars types.Vars) []string {
	if len(step.Commands) > 0 {
		commands := make([]string, len(step.Commands))
		for i, cmd := range step.Commands {
			commands[i] = e.interpolateString(cmd, vars)
		}
		return commands
	}
	if step.Command != "" {
		return []string{e.interpolateString(step.Command, vars)}
	}
	return []string{}
}

// runCommandStepLocal runs a command step in local mode
func (e *Executor) runCommandStepLocal(step *types.Step, commands []string, vars types.Vars) (string, string, error) {
	// Execute each command locally using the shell with streaming output and
	// optional "expect" style prompt/response handling.
	workingDir := e.interpolateString(step.WorkingDir, vars)
	if workingDir == "" {
		if wd, ok := vars["working_dir"].(string); ok {
			workingDir = wd
		}
	}

	// Determine overall timeout and idle timeout. A value of 0 means
	// unlimited for that timer. We intentionally do not apply a legacy
	// default here so callers that expect '0' to mean unlimited are
	// respected.
	overallTimeout := step.Timeout
	idleTimeout := step.IdleTimeout

	var combinedOut bytes.Buffer

	for _, c := range commands {
		fullCmd := c
		if workingDir != "" {
			fullCmd = fmt.Sprintf("cd %s && %s", workingDir, c)
		}

		// Respect step.Silent: when true avoid printing the full command line
		if !step.Silent {
			// If command is multiline or very long, show a truncated preview to
			// avoid dumping huge saved outputs into the console when variables
			// contain large text. Show first 200 chars or up to first newline.
			preview := fullCmd
			if idx := strings.IndexByte(preview, '\n'); idx != -1 {
				preview = preview[:idx]
			}
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			fmt.Printf("Running locally: %s\n", preview)
		}

		// compile Expect regexes once per step (fail fast on invalid regex)
		type compiledExpect struct {
			re   *regexp.Regexp
			resp string
		}
		var compiled []compiledExpect
		for _, ex := range step.Expect {
			if ex.Prompt == "" {
				continue
			}
			r, err := regexp.Compile(ex.Prompt)
			if err != nil {
				return "", "", fmt.Errorf("invalid expect regex %q: %v", ex.Prompt, err)
			}
			compiled = append(compiled, compiledExpect{re: r, resp: ex.Response})
		}

		// Create command using ExecCommand (testable injection point)
		cmd := e.ExecCommand("/bin/sh", "-lc", fullCmd)

		stdin, _ := cmd.StdinPipe()
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()

		if err := cmd.Start(); err != nil {
			e.flushErrorEvidenceAll()
			return "", "", fmt.Errorf("failed to start command: %v", err)
		}

		// If there are no expect handlers for this step, close stdin so the
		// shell/command sees EOF and cannot wait for input indefinitely.
		if stdin != nil && len(compiled) == 0 {
			// best effort close
			_ = stdin.Close()
		}

		// trackers and synchronization
		var wg sync.WaitGroup
		doneCh := make(chan struct{})
		lastOutputAt := time.Now()

		pushOutput := func(s string) {
			// append to ring buffer for global history
			e.historyMu.Lock()
			if e.outputHistory == nil {
				e.outputHistory = NewRingBuffer(307200)
			}
			e.outputHistory.Add(s)
			e.historyMu.Unlock()

			// also write to combined buffer
			combinedOut.WriteString(s)
			// print to stdout unless silent
			if !step.Silent {
				fmt.Print(s)
			}
		}

		// stdout reader (chunked) - supports prompts that may be split across reads
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 4096)
			streamBuf := bytes.NewBuffer(nil)
			for {
				n, rerr := stdout.Read(buf)
				if n > 0 {
					chunk := string(buf[:n])
					lastOutputAt = time.Now()
					pushOutput(chunk)
					// append to stream buffer and try to process expects
					streamBuf.Write(buf[:n])
					// build compiled list once (reuse same struct shape)
					var comp []struct {
						re   *regexp.Regexp
						resp string
					}
					for _, ce := range compiled {
						comp = append(comp, struct {
							re   *regexp.Regexp
							resp string
						}{re: ce.re, resp: ce.resp})
					}
					// process as many matches as present
					for processExpectBuffer(streamBuf, comp, stdin) {
					}
				}
				if rerr != nil {
					if rerr != io.EOF {
						pushOutput(fmt.Sprintf("[error reading stdout]: %v\n", rerr))
					}
					return
				}
			}
		}()

		// stderr reader (chunked)
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 4096)
			streamBuf := bytes.NewBuffer(nil)
			for {
				n, rerr := stderr.Read(buf)
				if n > 0 {
					chunk := string(buf[:n])
					lastOutputAt = time.Now()
					pushOutput(chunk)
					streamBuf.Write(buf[:n])
					var comp []struct {
						re   *regexp.Regexp
						resp string
					}
					for _, ce := range compiled {
						comp = append(comp, struct {
							re   *regexp.Regexp
							resp string
						}{re: ce.re, resp: ce.resp})
					}
					for processExpectBuffer(streamBuf, comp, stdin) {
					}
				}
				if rerr != nil {
					if rerr != io.EOF {
						pushOutput(fmt.Sprintf("[error reading stderr]: %v\n", rerr))
					}
					return
				}
			}
		}()

		// timeout/idle watchers
		var overallTimer *time.Timer
		var idleTimer *time.Timer
		if overallTimeout > 0 {
			overallTimer = time.NewTimer(time.Duration(overallTimeout) * time.Second)
		}
		if idleTimeout > 0 {
			idleTimer = time.NewTimer(time.Duration(idleTimeout) * time.Second)
		}

		// monitor goroutine (not part of readers waitgroup). It watches
		// timers and will kill the process on timeout. It listens on
		// doneCh to know when readers have finished and it should exit.
		go func() {
			for {
				select {
				case <-doneCh:
					return
				default:
				}
				// check timers
				if overallTimer != nil {
					select {
					case <-overallTimer.C:
						pushOutput("[error]: overall timeout reached\n")
						// best-effort kill
						_ = cmd.Process.Kill()
						return
					default:
					}
				}
				if idleTimer != nil {
					if time.Since(lastOutputAt) > time.Duration(idleTimeout)*time.Second {
						pushOutput("[error]: idle timeout reached\n")
						_ = cmd.Process.Kill()
						return
					}
				}
				time.Sleep(200 * time.Millisecond)
			}
		}()

		// wait for readers to finish, then signal monitor to stop
		wg.Wait()
		close(doneCh)

		// ensure process exit
		if err := cmd.Wait(); err != nil {
			e.flushErrorEvidenceAll()
			outStr := combinedOut.String()
			if step.SaveOutput != "" && e.pipeline != nil {
				e.saveStepOutput(step.SaveOutput, strings.TrimSpace(outStr))
			}
			// check conditions
			action, target, cerr := e.checkConditions(step, outStr, vars)
			if cerr != nil {
				return "", "", cerr
			}
			if action != "" {
				return action, target, nil
			}
			return "", "", fmt.Errorf("command failed: %v", err)
		}

		// normal completion
		outStr := combinedOut.String()
		if step.SaveOutput != "" && e.pipeline != nil {
			e.saveStepOutput(step.SaveOutput, strings.TrimSpace(outStr))
		}

		// check conditions
		action, target, cerr := e.checkConditions(step, outStr, vars)
		if cerr != nil {
			return "", "", cerr
		}
		if action != "" {
			return action, target, nil
		}
	}

	return "", "", nil
}

// runCommandInteractive runs a command interactively via SSH
func (e *Executor) runCommandInteractive(client interface{}, cmd string, expect []types.Expect, vars types.Vars, timeout, idleTimeout int, silent bool, step *types.Step, job *types.Job) (string, error) {
	// Respect silent flag: only print interactive header when not silent
	if !silent {
		// Truncate preview to avoid printing huge multiline commands
		preview := cmd
		if idx := strings.IndexByte(preview, '\n'); idx != -1 {
			preview = preview[:idx]
		}
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		fmt.Printf("Running interactively: %s\n", preview)
	}

	// If client implements SSHClient interface, try ExecWithPTY which provides
	// stdin/stdout/stderr and a wait function. This avoids concrete type
	// assertions and makes the code testable with mocks.
	if c, ok := client.(SSHClient); ok && c != nil {
		stdin, stdout, stderr, waitFn, killFn, err := c.ExecWithPTY(cmd)
		if err != nil {
			return "", fmt.Errorf("failed to start remote PTY command: %v", err)
		}

		// compile expect regexes for remote handling
		type compiledExpect struct {
			re   *regexp.Regexp
			resp string
		}
		var compiled []compiledExpect
		for _, ex := range step.Expect {
			if ex.Prompt == "" {
				continue
			}
			r, err := regexp.Compile(ex.Prompt)
			if err != nil {
				// best effort: kill session if possible then return, but log the attempt
				if killFn != nil {
					e.invokeKillLogger(killFn, "invalid-expect", cmd, step, job)
				} else if stdin != nil {
					stdin.Close()
				}
				return "", fmt.Errorf("invalid expect regex %q: %v", ex.Prompt, err)
			}
			compiled = append(compiled, compiledExpect{re: r, resp: ex.Response})
		}

		var outBuf bytes.Buffer
		lastOutputAt := time.Now()
		done := make(chan struct{})

		// helper to push output
		pushOut := func(s string) {
			lastOutputAt = time.Now()
			outBuf.WriteString(s)
			if !silent {
				fmt.Print(s)
			}
		}

		// stdout reader
		go func() {
			buf := make([]byte, 4096)
			streamBuf := bytes.NewBuffer(nil)
			for {
				n, rerr := stdout.Read(buf)
				if n > 0 {
					s := string(buf[:n])
					pushOut(s)
					streamBuf.Write(buf[:n])
					var comp []struct {
						re   *regexp.Regexp
						resp string
					}
					for _, ce := range compiled {
						comp = append(comp, struct {
							re   *regexp.Regexp
							resp string
						}{re: ce.re, resp: ce.resp})
					}
					for processExpectBuffer(streamBuf, comp, stdin) {
					}
				}
				if rerr != nil {
					if rerr != io.EOF {
						pushOut(fmt.Sprintf("[stdout err]: %v\n", rerr))
					}
					return
				}
			}
		}()

		// stderr reader
		go func() {
			buf := make([]byte, 4096)
			streamBuf := bytes.NewBuffer(nil)
			for {
				n, rerr := stderr.Read(buf)
				if n > 0 {
					s := string(buf[:n])
					pushOut(s)
					streamBuf.Write(buf[:n])
					var comp []struct {
						re   *regexp.Regexp
						resp string
					}
					for _, ce := range compiled {
						comp = append(comp, struct {
							re   *regexp.Regexp
							resp string
						}{re: ce.re, resp: ce.resp})
					}
					for processExpectBuffer(streamBuf, comp, stdin) {
					}
				}
				if rerr != nil {
					if rerr != io.EOF {
						pushOut(fmt.Sprintf("[stderr err]: %v\n", rerr))
					}
					return
				}
			}
		}()

		// monitor timeouts
		// A timeout value of 0 means unlimited; do not coerce to a legacy
		// default here.
		var overallTimer *time.Timer
		if timeout > 0 {
			overallTimer = time.NewTimer(time.Duration(timeout) * time.Second)
		}

		go func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				if overallTimer != nil {
					select {
					case <-overallTimer.C:
						pushOut("[error]: overall timeout reached\n")
						// best-effort: kill the session if available (log the attempt)
						if killFn != nil {
							e.invokeKillLogger(killFn, "overall-timeout", cmd, step, job)
						} else if stdin != nil {
							stdin.Close()
						}
						return
					default:
					}
				}
				if idleTimeout > 0 {
					if time.Since(lastOutputAt) > time.Duration(idleTimeout)*time.Second {
						pushOut("[error]: idle timeout reached\n")
						if killFn != nil {
							e.invokeKillLogger(killFn, "idle-timeout", cmd, step, job)
						} else if stdin != nil {
							stdin.Close()
						}
						return
					}
				}
				time.Sleep(200 * time.Millisecond)
			}
		}()

		// wait for command to finish
		if err := waitFn(); err != nil {
			close(done)
			outStr := outBuf.String()
			if step.SaveOutput != "" && e.pipeline != nil {
				e.saveStepOutput(step.SaveOutput, strings.TrimSpace(outStr))
			}
			return outStr, fmt.Errorf("remote command failed: %v", err)
		}

		close(done)
		outStr := outBuf.String()
		if step.SaveOutput != "" && e.pipeline != nil {
			e.saveStepOutput(step.SaveOutput, strings.TrimSpace(outStr))
		}
		return outStr, nil
	}

	// Fallback: if client implements RunCommandWithStream, use that (no stdin)
	if c2, ok := client.(SSHClient); ok {
		outCh, errCh, err := c2.RunCommandWithStream(cmd, true)
		if err != nil {
			return "", fmt.Errorf("failed to start remote streaming command: %v", err)
		}
		var outBuf bytes.Buffer
		for {
			select {
			case s, ok := <-outCh:
				if !ok {
					outStr := outBuf.String()
					if step.SaveOutput != "" && e.pipeline != nil {
						e.saveStepOutput(step.SaveOutput, strings.TrimSpace(outStr))
					}
					return outStr, nil
				}
				outBuf.WriteString(s)
				if !silent {
					fmt.Print(s)
				}
			case eerr, ok := <-errCh:
				if ok && eerr != nil {
					outStr := outBuf.String()
					if step.SaveOutput != "" && e.pipeline != nil {
						e.saveStepOutput(step.SaveOutput, strings.TrimSpace(outStr))
					}
					return outStr, fmt.Errorf("remote command error: %v", eerr)
				}
			}
		}
	}

	return "", fmt.Errorf("ssh client does not support interactive execution")
}

// runLocalFileTransfer handles local file transfer operations
func (e *Executor) runLocalFileTransfer(step *types.Step, vars types.Vars) error {
	// Basic implementation - delegate to runLocalFileTransferWithFiltering
	return e.runLocalFileTransferWithFiltering(step, vars)
}

// BuildOptions contains parameters that control agent building behavior
type BuildOptions struct {
	// SourceDir is the directory containing the agent source code
	SourceDir string
	// OutputDir is the directory where the built binary should be placed
	OutputDir string
	// TargetOS specifies the target operating system (linux, windows, darwin)
	TargetOS string
	// SSHClient for remote architecture detection (optional)
	SSHClient SSHClient
	// ProjectRoot for determining default paths
	ProjectRoot string
	// Config for unique agent naming
	Config *config.Config
}

// DeployOptions contains parameters that control deployment behavior
type DeployOptions struct {
	// Timeout for remote operations
	Timeout time.Duration
	// Overwrite if remote binary exists
	Overwrite bool
	// OSTarget indicates remote OS ("windows", "linux", etc.)
	OSTarget string
}

// BuildAgentForTarget builds an agent binary for the target OS
func (e *Executor) BuildAgentForTarget(opts BuildOptions) (string, error) {
	// Set defaults
	if opts.TargetOS == "" {
		opts.TargetOS = "linux"
	}
	if opts.SourceDir == "" && opts.ProjectRoot != "" {
		opts.SourceDir = filepath.Join(opts.ProjectRoot, "sub_app", "agent")
	}
	if opts.OutputDir == "" && opts.ProjectRoot != "" {
		opts.OutputDir = opts.ProjectRoot
	}

	// Validate source directory. If not provided or missing, try to locate
	// `sub_app/agent` under a few likely roots: ProjectRoot, current working
	// directory, and executable directory (searching upward). This helps when
	// the `pipeline` CLI is invoked from another folder or an installed binary.
	if opts.SourceDir == "" {
		// prepare candidate roots
		var candidates []string
		if opts.ProjectRoot != "" {
			candidates = append(candidates, filepath.Join(opts.ProjectRoot, "sub_app", "agent"))
		}
		// current working directory
		if cwd, err := os.Getwd(); err == nil {
			candidates = append(candidates, filepath.Join(cwd, "sub_app", "agent"))
		}
		// executable directory and its parents
		if exePath, err := os.Executable(); err == nil {
			exeDir := filepath.Dir(exePath)
			// walk upward from exeDir up to filesystem root looking for sub_app/agent
			cur := exeDir
			for {
				candidates = append(candidates, filepath.Join(cur, "sub_app", "agent"))
				parent := filepath.Dir(cur)
				if parent == cur {
					break
				}
				cur = parent
			}
		}

		// check candidates
		found := ""
		for _, c := range candidates {
			if c == "" {
				continue
			}
			if _, err := os.Stat(c); err == nil {
				found = c
				break
			}
		}
		if found != "" {
			opts.SourceDir = found
		}
	}

	if opts.SourceDir == "" {
		return "", fmt.Errorf("source directory required")
	}
	if _, err := os.Stat(opts.SourceDir); os.IsNotExist(err) {
		return "", fmt.Errorf("source directory does not exist: %s", opts.SourceDir)
	}

	// Determine GOOS and binary name
	var goos string
	var binaryName string

	// Generate unique agent binary name based on target OS
	switch strings.ToLower(opts.TargetOS) {
	case "linux":
		goos = "linux"
		binaryName = "pipeline-agent"
	case "windows", "win":
		goos = "windows"
		binaryName = "pipeline-agent.exe"
	case "darwin", "macos":
		goos = "darwin"
		binaryName = "pipeline-agent"
	default:
		goos = "linux" // default fallback
		binaryName = "pipeline-agent"
	}

	// Build output path (make absolute so go build -o creates it at a known location
	// even when cmd.Dir is set to the agent source directory)
	outputPath := filepath.Join(opts.OutputDir, binaryName)
	if !filepath.IsAbs(outputPath) {
		if abs, err := filepath.Abs(outputPath); err == nil {
			outputPath = abs
		}
	}

	// Prepare build command with static linking flags for maximum compatibility
	cmd := exec.Command("go", "build",
		"-ldflags", "-w -s -extldflags '-static'", // Strip symbols and create static binary
		"-o", outputPath, ".")
	cmd.Dir = opts.SourceDir

	// Prepare environment variables for cross-compilation
	env := []string{}
	for _, e := range os.Environ() {
		// Skip existing GOOS/GOARCH/GOARM/CGO_ENABLED to avoid conflicts
		if strings.HasPrefix(e, "GOOS=") ||
			strings.HasPrefix(e, "GOARCH=") ||
			strings.HasPrefix(e, "GOARM=") ||
			strings.HasPrefix(e, "CGO_ENABLED=") {
			continue
		}
		env = append(env, e)
	}

	// Set target OS and disable CGO for maximum compatibility
	env = append(env, "GOOS="+goos)
	env = append(env, "CGO_ENABLED=0")

	// Detect remote architecture if SSH client provided
	if opts.SSHClient != nil {
		if output, err := opts.SSHClient.RunCommandWithOutput("uname -m"); err == nil {
			arch := strings.TrimSpace(output)

			// Map uname -m output to Go architecture
			switch arch {
			case "x86_64", "amd64":
				env = append(env, "GOARCH=amd64")
			case "aarch64", "arm64":
				env = append(env, "GOARCH=arm64")
			case "armv7l", "armv7":
				env = append(env, "GOARCH=arm")
				env = append(env, "GOARM=7")
			case "armv6l", "armv6":
				env = append(env, "GOARCH=arm")
				env = append(env, "GOARM=6")
			default:
				fmt.Printf("⚠️  Unknown architecture '%s', using Go defaults\n", arch)
			}
			fmt.Printf("ℹ️  Detected remote architecture: %s\n", arch)
		} else {
			fmt.Printf("⚠️  Could not detect remote architecture: %v\n", err)
		}
	}

	cmd.Env = env

	// Execute build
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build failed: %v\nOutput: %s", err, string(output))
	}

	// Verify the output file exists
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		return "", fmt.Errorf("build output file does not exist: %s", outputPath)
	}

	return outputPath, nil
}

// BuildHashCheckerForTarget builds a hash checker utility for the target OS
func (e *Executor) BuildHashCheckerForTarget(opts BuildOptions) (string, error) {
	// Hash checking functionality is built into the agent itself
	// No separate hash checker utility is needed
	return "", nil
}

// DeployAgent deploys an agent to a remote host
func (e *Executor) DeployAgent(ctx context.Context, cancel context.CancelFunc, client interface{}, agentPath, remotePath string, opts DeployOptions) error {
	// Try to type assert to SSHClient; if nil or not provided, perform local deploy (copy)
	var sshClient SSHClient
	if client != nil {
		if c, ok := client.(SSHClient); ok {
			sshClient = c
		}
	}

	// Basic validation
	if agentPath == "" {
		return fmt.Errorf("agent path is required")
	}
	if _, err := os.Stat(agentPath); os.IsNotExist(err) {
		return fmt.Errorf("agent file does not exist: %s", agentPath)
	}

	// Ensure remote directory exists. If SSH client is present, use it; otherwise create locally.
	if sshClient != nil {
		var mkdirCmd string
		if strings.Contains(strings.ToLower(opts.OSTarget), "win") {
			mkdirCmd = fmt.Sprintf("cmd.exe /C if not exist \"%s\" mkdir \"%s\"", remotePath, remotePath)
		} else {
			mkdirCmd = fmt.Sprintf("mkdir -p '%s'", strings.ReplaceAll(remotePath, "'", "'\\''"))
		}
		if err := sshClient.RunCommand(mkdirCmd); err != nil {
			return fmt.Errorf("failed to create remote directory: %v", err)
		}
	} else {
		if err := os.MkdirAll(remotePath, 0755); err != nil {
			return fmt.Errorf("failed to create local remotePath: %v", err)
		}
	}

	// Prepare remote agent path
	agentName := "pipeline-agent"
	if strings.Contains(strings.ToLower(opts.OSTarget), "win") {
		agentName = "pipeline-agent.exe"
	}
	remoteAgentPath := filepath.Join(remotePath, agentName)
	if strings.Contains(strings.ToLower(opts.OSTarget), "win") {
		remoteAgentPath = strings.ReplaceAll(remoteAgentPath, "/", "\\")
	} else {
		remoteAgentPath = filepath.ToSlash(remoteAgentPath)
	}

	// Compute local agent identity using xxHash
	localHash, err := computeXXHash(agentPath)
	if err != nil {
		return fmt.Errorf("failed to compute local agent hash: %v", err)
	}

	// Check remote agent existence and identity (skip upload when identical and not Overwrite)
	if !opts.Overwrite {
		if sshClient != nil {
			// Check remote via SSH
			var statCmd string
			if strings.Contains(strings.ToLower(opts.OSTarget), "win") {
				statCmd = fmt.Sprintf("if exist \"%s\" (echo exists) else (echo no)", remoteAgentPath)
			} else {
				statCmd = fmt.Sprintf("[ -f '%s' ] && echo exists || echo no", strings.ReplaceAll(remoteAgentPath, "'", "'\\''"))
			}
			out, _ := sshClient.RunCommandWithOutput(statCmd)
			if strings.Contains(strings.ToLower(out), "exists") {
				// run remote identity command to get its hash
				var idCmd string
				if strings.Contains(strings.ToLower(opts.OSTarget), "win") {
					idCmd = fmt.Sprintf("cmd.exe /C \"%s identity\"", remoteAgentPath)
				} else {
					dir := filepath.Dir(remoteAgentPath)
					idCmd = fmt.Sprintf("cd %s && %s identity", shellQuote(dir), shellQuote(remoteAgentPath))
				}
				remoteOut, rerr := sshClient.RunCommandWithOutput(idCmd)
				if rerr == nil {
					remoteHash := strings.TrimSpace(remoteOut)
					if remoteHash != "" && remoteHash == localHash {
						fmt.Printf("ℹ️  Remote agent matches local hash (%s) - skipping upload\n", localHash)
						return nil
					}
				}
			}
		} else {
			// Local filesystem check
			if _, err := os.Stat(remoteAgentPath); err == nil {
				if h, herr := computeXXHash(remoteAgentPath); herr == nil {
					if h == localHash {
						fmt.Printf("ℹ️  Local remote agent matches local hash (%s) - skipping copy\n", localHash)
						return nil
					}
				}
			}
		}
	}

	// Upload / copy agent
	if sshClient != nil {
		if err := sshClient.UploadFile(agentPath, remoteAgentPath); err != nil {
			return fmt.Errorf("failed to upload agent: %v", err)
		}
		if !strings.Contains(strings.ToLower(opts.OSTarget), "win") {
			chmodCmd := fmt.Sprintf("chmod +x '%s'", strings.ReplaceAll(remoteAgentPath, "'", "'\\''"))
			if err := sshClient.RunCommand(chmodCmd); err != nil {
				return fmt.Errorf("failed to set execute permission: %v", err)
			}
		}
	} else {
		// Local copy
		if err := e.copyLocalPath(agentPath, remoteAgentPath); err != nil {
			return fmt.Errorf("failed to copy agent locally: %v", err)
		}
		if !strings.Contains(strings.ToLower(opts.OSTarget), "win") {
			if err := os.Chmod(remoteAgentPath, 0755); err != nil {
				return fmt.Errorf("failed to chmod local agent: %v", err)
			}
		}
	}

	return nil
}

// matchesIgnore evaluates ignore-list semantics where patterns in 'ignores'
// exclude files, and patterns prefixed with '!' are negations (force-include).
// Patterns are applied in the provided order and later patterns override earlier
// ones (last-match-wins). Returns true when the relPath should be included
// (not ignored).
func (e *Executor) matchesIgnore(relPath string, ignores []string) bool {
	// If no patterns specified, include everything
	if len(ignores) == 0 {
		return true
	}

	// Normalize path to forward slashes for gitignore matching
	rp := filepath.ToSlash(relPath)

	// Compile ignore patterns using go-gitignore which supports negation (!)
	gi := gitignore.CompileIgnoreLines(ignores...)

	// MatchesPath returns true when the path is ignored. We want to return
	// true when it should be included (i.e., not ignored).
	return !gi.MatchesPath(rp)
}

// uploadConfigToRemote uploads agent configuration to remote host. It accepts an
// explicit ignores slice which will be written into the remote config JSON. It
// writes config to both <remote>/.sync_temp/config.json and <remote>/.sync_config/config.json
// when possible to support both agent expectations.
func (e *Executor) uploadConfigToRemote(client interface{}, remoteSyncTemp, osTarget, configWorkingDir string, ignores []string) error {
	// Try SSH client; if not provided, fall back to local file copy into remoteSyncTemp
	var sshClient SSHClient
	if client != nil {
		if c, ok := client.(SSHClient); ok {
			sshClient = c
		}
	}

	// Normalize ignores and ensure .sync_temp present
	norm := make([]string, 0, len(ignores)+1)
	hasSyncTemp := false
	for _, p := range ignores {
		// preserve provided order for now
		norm = append(norm, p)
		if p == ".sync_temp" || p == ".sync_temp/" {
			hasSyncTemp = true
		}
	}
	if !hasSyncTemp {
		norm = append(norm, ".sync_temp")
	}

	// Preserve the provided order of ignore patterns. The agent uses
	// go-gitignore which implements gitignore semantics (negations and
	// later pattern precedence). Reordering here would change meaning.
	// Normalize working_dir so it's sensible relative to how we execute the agent.
	wd := configWorkingDir
	wdClean := filepath.ToSlash(filepath.Clean(wd))
	// If remoteSyncTemp is a nested path like 'src/.sync_temp', the executor
	// will 'cd' into the parent (src) before running the agent. In that
	// situation a relative working_dir that begins with the parent basename
	// (e.g. 'src/' or 'src/sub') would cause the agent to chdir into
	// 'src/src' or 'src/src/sub'. To avoid double-parenting, strip the
	// leading parent basename when appropriate.
	if strings.HasSuffix(remoteSyncTemp, ".sync_temp") && wdClean != "" && !filepath.IsAbs(wdClean) {
		cdDir := strings.TrimSuffix(remoteSyncTemp, ".sync_temp")
		if cdDir == "" {
			cdDir = remoteSyncTemp
		}
		base := filepath.Base(cdDir)
		if wdClean == base || wdClean == base+"/" {
			wd = "."
		} else if strings.HasPrefix(wdClean, base+"/") {
			stripped := strings.TrimPrefix(wdClean, base+"/")
			if stripped == "" {
				wd = "."
			} else {
				wd = stripped
			}
		}
	}

	config := map[string]interface{}{
		"pipeline": map[string]interface{}{
			"working_dir": wd,
			"ignores":     norm,
		},
	}

	// Convert to JSON
	configJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	// Create basic config for the agent using top-level `pipeline` key
	// Create temporary local file
	tempFile, err := os.CreateTemp("", "remote-config-*.json")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Write config to temp file
	if _, err := tempFile.Write(configJSON); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}
	tempFile.Close()

	// Build primary remote config path (.sync_temp/config.json)
	var remoteConfigPath string
	if strings.Contains(strings.ToLower(osTarget), "win") {
		base := strings.ReplaceAll(remoteSyncTemp, "/", "\\")
		if strings.HasSuffix(base, ".sync_temp") {
			remoteConfigPath = base + "\\config.json"
		} else {
			remoteConfigPath = filepath.Join(base, ".sync_temp", "config.json")
		}
	} else {
		base := remoteSyncTemp
		if strings.HasSuffix(base, ".sync_temp") {
			remoteConfigPath = filepath.Join(base, "config.json")
		} else {
			remoteConfigPath = filepath.Join(base, ".sync_temp", "config.json")
		}
	}

	// Upload to remote .sync_temp/config.json only
	data, rerr := os.ReadFile(tempFile.Name())
	if rerr != nil {
		return fmt.Errorf("failed to read temp config file: %v", rerr)
	}

	if sshClient != nil {
		if err := sshClient.SyncFile(tempFile.Name(), remoteConfigPath); err != nil {
			return fmt.Errorf("failed to upload config to %s: %v", remoteConfigPath, err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(remoteConfigPath), 0755); err != nil {
			return fmt.Errorf("failed to create remote config dir: %v", err)
		}
		if werr := os.WriteFile(remoteConfigPath, data, 0644); werr != nil {
			return fmt.Errorf("failed to write config locally: %v", werr)
		}
	}

	return nil
}

// RemoteRunAgentIndexing runs agent indexing on remote host
func (e *Executor) RemoteRunAgentIndexing(client interface{}, remoteSyncTemp, osTarget string, bypassIgnore bool, prefixes []string) (string, error) {
	// Try SSH client; if not provided, run the agent locally from remoteSyncTemp
	var sshClient SSHClient
	if client != nil {
		if c, ok := client.(SSHClient); ok {
			sshClient = c
		}
	}

	// Build the binary path
	binaryName := "pipeline-agent"
	if strings.Contains(strings.ToLower(osTarget), "win") {
		binaryName = "pipeline-agent.exe"
	}

	var cmd string
	if strings.Contains(strings.ToLower(osTarget), "win") {
		var agentPath, cdDir string
		if strings.HasSuffix(remoteSyncTemp, ".sync_temp") {
			agentPath = remoteSyncTemp + "\\" + binaryName
			cdDir = remoteSyncTemp[:len(remoteSyncTemp)-len(".sync_temp")]
			if cdDir == "" {
				cdDir = remoteSyncTemp
			}
		} else {
			agentPath = remoteSyncTemp + "\\.sync_temp\\" + binaryName
			cdDir = remoteSyncTemp
		}
		indexingCmd := "indexing"
		if bypassIgnore {
			indexingCmd = "indexing --bypass-ignore"
		}
		if len(prefixes) > 0 {
			joined := strings.Join(prefixes, ",")
			indexingCmd = fmt.Sprintf("%s --manual-transfer %s", indexingCmd, joined)
		}
		cmd = fmt.Sprintf("cmd.exe /C cd /d \"%s\" && \"%s\" %s", cdDir, agentPath, indexingCmd)
	} else {
		var cdDir string
		// Execute agent relative to the working directory so that paths do not
		// get duplicated when remoteSyncTemp is a nested path like 'src/.sync_temp'.
		if strings.HasSuffix(remoteSyncTemp, ".sync_temp") {
			cdDir = strings.TrimSuffix(remoteSyncTemp, ".sync_temp")
			if cdDir == "" {
				cdDir = remoteSyncTemp
			}
		} else {
			cdDir = remoteSyncTemp
		}
		// Agent executable path relative to cdDir (use ./ prefix)
		agentExec := filepath.ToSlash(filepath.Join(".sync_temp", binaryName))
		if !strings.HasPrefix(agentExec, "./") {
			agentExec = "./" + agentExec
		}
		cdDir = filepath.ToSlash(cdDir)
		indexingCmd := "indexing"
		if bypassIgnore {
			indexingCmd = "indexing --bypass-ignore"
		}
		if len(prefixes) > 0 {
			joined := strings.Join(prefixes, ",")
			indexingCmd = fmt.Sprintf("%s --manual-transfer %s", indexingCmd, joined)
		}
		cmd = fmt.Sprintf("cd %s && %s %s", shellQuote(cdDir), shellQuote(agentExec), indexingCmd)
	}

	if sshClient != nil {
		// Run via SSH and include the remote output in the error for easier debugging
		output, err := sshClient.RunCommandWithOutput(cmd)
		if err != nil {
			// Include remote stdout/stderr (captured in output) to help debugging
			trimmed := strings.TrimSpace(output)
			if trimmed == "" {
				return output, fmt.Errorf("remote indexing failed: %v", err)
			}
			return output, fmt.Errorf("remote indexing failed: %v; remote output:\n%s", err, trimmed)
		}
		return output, nil
	}

	// Local run: execute the command via os/exec (run in shell so cd && binary works)
	outBytes, err := exec.Command("/bin/sh", "-lc", cmd).CombinedOutput()
	return string(outBytes), err
}
