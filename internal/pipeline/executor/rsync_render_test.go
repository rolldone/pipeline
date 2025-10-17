package executor

import (
	"os"
	"path/filepath"
	"testing"

	"pipeline/internal/pipeline/types"
)

func TestPrepareRsyncEntriesWithTemplates_TextRender(t *testing.T) {
	// setup temp dir
	tmp, err := os.MkdirTemp("", "rsync_test_")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}
	defer os.RemoveAll(tmp)

	src := filepath.Join(tmp, "hello.txt")
	if err := os.WriteFile(src, []byte("Hello {{name}}!"), 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	e := NewExecutor()
	e.tempDir = tmp

	step := &types.Step{Template: "enabled"}
	raw := []struct {
		Source      string
		Destination string
		Template    string
	}{
		{Source: src, Destination: "/dest", Template: ""},
	}

	vars := types.Vars{"name": "World"}

	entries, cleanup, warnings, err := e.prepareRsyncEntriesWithTemplates(step, raw, vars)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepareRsyncEntriesWithTemplates failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	data, rerr := os.ReadFile(entries[0].Source)
	if rerr != nil {
		t.Fatalf("failed to read rendered file: %v", rerr)
	}
	got := string(data)
	if got != "Hello World!" {
		t.Fatalf("render mismatch: got %q", got)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestPrepareRsyncEntriesWithTemplates_BinaryCopy(t *testing.T) {
	// setup temp dir
	tmp, err := os.MkdirTemp("", "rsync_test_")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}
	defer os.RemoveAll(tmp)

	src := filepath.Join(tmp, "bin.dat")
	orig := []byte{0x00, 0x01, 0x02, 0x03}
	if err := os.WriteFile(src, orig, 0644); err != nil {
		t.Fatalf("failed to write binary source file: %v", err)
	}

	e := NewExecutor()
	e.tempDir = tmp

	step := &types.Step{Template: "enabled"}
	raw := []struct {
		Source      string
		Destination string
		Template    string
	}{
		{Source: src, Destination: "/dest", Template: ""},
	}

	vars := types.Vars{"unused": "x"}

	entries, cleanup, warnings, err := e.prepareRsyncEntriesWithTemplates(step, raw, vars)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepareRsyncEntriesWithTemplates failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	data, rerr := os.ReadFile(entries[0].Source)
	if rerr != nil {
		t.Fatalf("failed to read copied binary file: %v", rerr)
	}
	if len(data) != len(orig) {
		t.Fatalf("binary length mismatch: got %d, want %d", len(data), len(orig))
	}
	for i := range orig {
		if data[i] != orig[i] {
			t.Fatalf("binary byte mismatch at %d: got %x want %x", i, data[i], orig[i])
		}
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for binary copy, got %v", warnings)
	}
}
