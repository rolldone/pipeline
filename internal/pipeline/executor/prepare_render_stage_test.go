package executor

import (
	"os"
	"path/filepath"
	"testing"

	"pipeline/internal/pipeline/types"
)

func TestPrepareRenderAndStage_TextRender(t *testing.T) {
	e := NewExecutor()
	// use a dedicated temp dir for staging
	temp := t.TempDir()
	e.tempDir = temp

	// create source file
	src := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(src, []byte("Hello {{name}}"), 0644); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	step := &types.Step{Template: "enabled"}
	raw := []struct{ Source, Destination, Template string }{{Source: src, Destination: "/dest", Template: ""}}
	vars := types.Vars{"name": "World"}

	entries, cleanup, warnings, err := e.prepareRenderAndStage(step, raw, vars)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepareRenderAndStage failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	staged := entries[0].Source
	data, rerr := os.ReadFile(staged)
	if rerr != nil {
		t.Fatalf("failed to read staged file: %v", rerr)
	}
	if string(data) != "Hello World" {
		t.Fatalf("render mismatch: got %q", string(data))
	}
}

func TestPrepareRenderAndStage_BinaryCopy(t *testing.T) {
	e := NewExecutor()
	e.tempDir = t.TempDir()

	// create binary source
	src := filepath.Join(t.TempDir(), "bin.dat")
	orig := []byte{0x00, 0x01, 0x02, 0x03}
	if err := os.WriteFile(src, orig, 0644); err != nil {
		t.Fatalf("failed to write binary source: %v", err)
	}

	step := &types.Step{Template: "enabled"}
	raw := []struct{ Source, Destination, Template string }{{Source: src, Destination: "/dest", Template: ""}}
	vars := types.Vars{}

	entries, cleanup, warnings, err := e.prepareRenderAndStage(step, raw, vars)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepareRenderAndStage failed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	staged := entries[0].Source
	data, rerr := os.ReadFile(staged)
	if rerr != nil {
		t.Fatalf("failed to read staged binary: %v", rerr)
	}
	if len(data) != len(orig) {
		t.Fatalf("binary length mismatch: got %d want %d", len(data), len(orig))
	}
	for i := range orig {
		if data[i] != orig[i] {
			t.Fatalf("binary byte mismatch at %d: got %x want %x", i, data[i], orig[i])
		}
	}
}

func TestPrepareRenderAndStage_RenderWriteFailureFallback(t *testing.T) {
	e := NewExecutor()
	e.tempDir = t.TempDir()

	// create source template
	src := filepath.Join(t.TempDir(), "tmpl.txt")
	if err := os.WriteFile(src, []byte("Hi {{who}}"), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// inject WriteFileFunc that fails first call then succeeds
	calls := 0
	e.WriteFileFunc = func(filename string, data []byte, perm os.FileMode) error {
		calls++
		if calls == 1 {
			return os.ErrPermission
		}
		// ensure dir exists
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			return err
		}
		return os.WriteFile(filename, data, perm)
	}

	step := &types.Step{Template: "enabled"}
	raw := []struct{ Source, Destination, Template string }{{Source: src, Destination: "/dest", Template: ""}}
	vars := types.Vars{"who": "Bob"}

	entries, cleanup, warnings, err := e.prepareRenderAndStage(step, raw, vars)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepareRenderAndStage failed: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected warnings due to first write failure, got none")
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	staged := entries[0].Source
	data, rerr := os.ReadFile(staged)
	if rerr != nil {
		t.Fatalf("failed to read staged file: %v", rerr)
	}
	// If the initial write of rendered content failed, the helper falls back to
	// writing the original bytes (unrendered). Expect the original placeholder.
	if string(data) != "Hi {{who}}" {
		t.Fatalf("unexpected staged content (expected fallback original): %q", string(data))
	}
}
