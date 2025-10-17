package executor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"pipeline/internal/pipeline/types"
)

func TestRunRsyncStep_EndToEnd_RenderWarningsIncluded(t *testing.T) {
	// create temp dir for workspace
	tmp, err := os.MkdirTemp("", "rsync_e2e_")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}
	defer os.RemoveAll(tmp)

	// create a source file with template content
	src := filepath.Join(tmp, "page.html")
	if err := os.WriteFile(src, []byte("Hello {{user}}"), 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	e := NewExecutor()
	// use the temp dir for rendering
	e.tempDir = tmp
	// ensure pipeline and context map
	e.pipeline = &types.Pipeline{ContextVariables: make(map[string]string)}

	// Inject WriteFileFunc to fail on first write (simulate render write failure), then succeed
	calls := 0
	e.WriteFileFunc = func(filename string, data []byte, perm os.FileMode) error {
		calls++
		if calls == 1 {
			return os.ErrPermission
		}
		return os.WriteFile(filename, data, perm)
	}

	// Fake rsync command that prints minimal --stats on stdout
	e.ExecCommand = func(name string, arg ...string) *exec.Cmd {
		stdout := "Number of files transferred: 1\nTotal transferred file size: 11\n"
		stderr := ""
		return fakeCmd(stdout, stderr, 0)
	}

	step := &types.Step{
		Name:        "rsync-e2e",
		Source:      src,
		Destination: filepath.Join(tmp, "out") + string(os.PathSeparator),
		Template:    "enabled",
		SaveOutput:  "rsync.result",
	}
	job := &types.Job{Mode: "local"}
	config := map[string]interface{}{"Host": "localhost"}
	vars := types.Vars{"user": "Tester"}

	// Run runRsyncStep
	if err := e.runRsyncStep(step, job, config, vars); err != nil {
		t.Fatalf("runRsyncStep failed: %v", err)
	}

	// Verify saved JSON contains stats fields
	raw, ok := e.pipeline.ContextVariables[step.SaveOutput]
	if !ok {
		t.Fatalf("no save_output found at %s", step.SaveOutput)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("failed to parse saved json: %v", err)
	}
	if _, ok := m["files_transferred"]; !ok {
		t.Fatalf("files_transferred not present in saved json: %v", m)
	}
}
