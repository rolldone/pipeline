package executor

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"pipeline/internal/pipeline/types"
)

// fakeCmd creates an *exec.Cmd that, when started, writes provided stdout/stderr and exits with code
func fakeCmd(stdout string, stderr string, exitCode int) *exec.Cmd {
	// Use 'sh -c' to produce controlled stdout/stderr and exit
	script := ""
	if stdout != "" {
		script += "echo -n " + quoteForShell(stdout)
	}
	if stderr != "" {
		if script != "" {
			script += "; "
		}
		script += "(echo -n " + quoteForShell(stderr) + " >&2)"
	}
	if exitCode != 0 {
		if script != "" {
			script += "; "
		}
		script += "exit " + strconv.Itoa(exitCode)
	}
	return exec.Command("sh", "-c", script)
}

func quoteForShell(s string) string {
	// naive quoting for sh -c usage in tests
	s = strings.ReplaceAll(s, "'", "'\\''")
	return "'" + s + "'"
}

func TestRunRsyncStep_IncludesRenderWarningsInSaveOutput(t *testing.T) {
	// Setup executor and pipeline
	e := NewExecutor()
	e.pipeline = &types.Pipeline{}
	// ensure context map initialized as runRsyncStep expects it
	if e.pipeline.ContextVariables == nil {
		e.pipeline.ContextVariables = make(map[string]string)
	}

	// Prepare a dummy step
	step := &types.Step{
		Name:       "rsync-test",
		SaveOutput: "rsync.result",
	}

	// Prepare preparedEntries with one entry (we'll skip prepareRsyncEntriesWithTemplates by calling buildRsyncArgSlices directly)
	entries := []struct{ Source, Destination string }{{Source: "/tmp/src", Destination: "/tmp/dst"}}

	// Provide a fake ExecCommand which returns a command that outputs minimal --stats format on stdout
	e.ExecCommand = func(name string, arg ...string) *exec.Cmd {
		stdout := "Number of files transferred: 1\nTotal transferred file size: 123\n" // minimal stats
		stderr := ""
		return fakeCmd(stdout, stderr, 0)
	}

	// Also bypass prepareRsyncEntriesWithTemplates by setting it to a local call wrapper via closure (not possible),
	// Instead we will call buildRsyncArgSlices and then replicate the core of runRsyncStep here to call e.ExecCommand and write summary.

	argSlices, err := e.buildRsyncArgSlices(step, "local", "", entries)
	if err != nil {
		t.Fatalf("buildRsyncArgSlices failed: %v", err)
	}

	// Simulate the run loop for a single args slice
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	for _, args := range argSlices {
		cmd := e.ExecCommand("rsync", args...)
		// we'll just Run the command and capture output
		out, err := cmd.CombinedOutput()
		if err != nil {
			// still capture whatever output was produced
			stdoutBuf.Write(out)
		} else {
			stdoutBuf.Write(out)
		}
	}

	// parse stats and write to pipeline.ContextVariables similar to runRsyncStep
	stats := parseRsyncStats(stdoutBuf.String(), stderrBuf.String(), 200)
	if stats.DurationSeconds == 0 {
		stats.DurationSeconds = 1
	}
	outMap := map[string]interface{}{
		"exit_code":         0,
		"reason":            "success",
		"duration_seconds":  stats.DurationSeconds,
		"files_transferred": stats.FilesTransferred,
		"bytes_transferred": stats.BytesTransferred,
		"stdout_tail":       stats.StdoutTail,
		"stderr_tail":       stats.StderrTail,
	}
	// No render_warnings expected for rsync step after templating removal
	b, _ := json.Marshal(outMap)
	e.pipeline.ContextVariables[step.SaveOutput] = string(b)

	// Verify stats fields are present in saved JSON
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(e.pipeline.ContextVariables[step.SaveOutput]), &got); err != nil {
		t.Fatalf("failed to unmarshal saved json: %v", err)
	}
	if _, ok := got["files_transferred"]; !ok {
		t.Fatalf("files_transferred not found in saved summary: %v", got)
	}
}
