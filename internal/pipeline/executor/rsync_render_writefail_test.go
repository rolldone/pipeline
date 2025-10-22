package executor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"pipeline/internal/pipeline/types"
)

func TestPrepareRsyncEntriesWithTemplates_RenderWriteFailureFallback(t *testing.T) {
	// setup temp dir
	tmp, err := os.MkdirTemp("", "rsync_test_")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}
	defer os.RemoveAll(tmp)

	src := filepath.Join(tmp, "bad.txt")
	orig := []byte("Original content with {{var}} inside")
	if err := os.WriteFile(src, orig, 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	e := NewExecutor()
	e.tempDir = tmp

	// Inject WriteFileFunc that fails on first attempt to write rendered content,
	// then succeeds on subsequent writes (to simulate fallback behavior).
	calls := 0
	e.WriteFileFunc = func(filename string, data []byte, perm os.FileMode) error {
		calls++
		if calls == 1 {
			// simulate failure when writing rendered file
			return errors.New("simulated write failure")
		}
		// on fallback, succeed
		return os.WriteFile(filename, data, perm)
	}

	step := &types.Step{Template: "enabled"}
	raw := []struct {
		Source      string
		Destination string
		Template    string
	}{
		{Source: src, Destination: "/dest", Template: ""},
	}

	vars := types.Vars{"var": "X"}

	entries, cleanup, warnings, err := e.prepareRenderAndStage(step, raw, vars)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepareRsyncEntriesWithTemplates failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	data, rerr := os.ReadFile(entries[0].Source)
	if rerr != nil {
		t.Fatalf("failed to read fallback file: %v", rerr)
	}
	got := string(data)
	// Since first write failed, fallback should have written original bytes (no interpolation)
	if got != string(orig) {
		t.Fatalf("expected fallback to original bytes, got %q", got)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected at least one render warning, got none")
	}
}
