package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"pipeline/internal/pipeline/types"
)

// helper to create a minimal pipeline object for save_output capture
func makePipeline() *types.Pipeline {
	return &types.Pipeline{
		ContextVariables: make(map[string]string),
	}
}

func TestRunWriteFileStep_LocalRenderAndDefaultPerm(t *testing.T) {
	e := NewExecutor()
	// intercept write calls
	written := make(map[string]struct {
		Data []byte
		Perm uint32
	})
	e.WriteFileFunc = func(filename string, data []byte, perm os.FileMode) error {
		written[filename] = struct {
			Data []byte
			Perm uint32
		}{Data: data, Perm: uint32(perm)}
		// ensure the file actually exists on disk for the executor to read later
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			return err
		}
		return os.WriteFile(filename, data, perm)
	}

	// prepare step with a small template file in the repo relative path
	step := &types.Step{
		Name: "write test",
		Files: []types.FileEntry{
			{Source: "testdata/template.txt", Destination: "out1.txt", Perm: ""},
		},
		Template:   "enabled",
		SaveOutput: "wf_out",
	}

	// create pipeline and attach to executor for save_output
	p := makePipeline()
	e.pipeline = p

	job := &types.Job{Mode: "local"}
	vars := types.Vars{"name": "alice"}

	// run
	if err := e.runWriteFileStep(step, job, map[string]interface{}{}, vars); err != nil {
		t.Fatalf("runWriteFileStep failed: %v", err)
	}

	// Expect out1.txt was written (there may be other intermediate writes)
	var found bool
	for k, v := range written {
		if filepath.Base(k) == "out1.txt" {
			found = true
			if v.Perm != uint32(0755) {
				t.Fatalf("expected perm 0755 for out1.txt, got %o", v.Perm)
			}
		}
	}
	if !found {
		t.Fatalf("expected out1.txt to be written, keys: %v", written)
	}

	// check save_output present and contains files_written
	raw := p.ContextVariables[step.SaveOutput]
	if raw == "" {
		t.Fatalf("expected save_output variable set")
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("invalid json in save_output: %v", err)
	}
	fw, ok := out["files_written"].([]interface{})
	if !ok || len(fw) != 1 {
		t.Fatalf("expected files_written array with 1 element, got %#v", out["files_written"])
	}
}

func TestRunWriteFileStep_LocalPerFilePermAndRenderWarnings(t *testing.T) {
	e := NewExecutor()
	written := make(map[string]struct {
		Data []byte
		Perm uint32
	})
	e.WriteFileFunc = func(filename string, data []byte, perm os.FileMode) error {
		written[filename] = struct {
			Data []byte
			Perm uint32
		}{Data: data, Perm: uint32(perm)}
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			return err
		}
		return os.WriteFile(filename, data, perm)
	}

	step := &types.Step{
		Name: "write test 2",
		Files: []types.FileEntry{
			{Source: "testdata/template.txt", Destination: "a.txt", Perm: "0700"},
			{Source: "testdata/binary.bin", Destination: "b.bin", Perm: ""},
		},
		Template:   "enabled",
		SaveOutput: "wf2_out",
	}
	p := makePipeline()
	e.pipeline = p
	job := &types.Job{Mode: "local"}
	vars := types.Vars{"name": "bob"}

	if err := e.runWriteFileStep(step, job, map[string]interface{}{}, vars); err != nil {
		t.Fatalf("runWriteFileStep failed: %v", err)
	}

	// check perms for a.txt -> 0700 and b.bin -> 0755
	var foundA, foundB bool
	for k, v := range written {
		if filepath.Base(k) == "a.txt" {
			foundA = true
			if v.Perm != uint32(0700) {
				t.Fatalf("expected a.txt perm 0700, got %o", v.Perm)
			}
		}
		if filepath.Base(k) == "b.bin" {
			foundB = true
			if v.Perm != uint32(0755) {
				t.Fatalf("expected b.bin default perm 0755, got %o", v.Perm)
			}
		}
	}
	if !foundA {
		t.Fatalf("a.txt not written, keys: %v", written)
	}
	if !foundB {
		t.Fatalf("b.bin not written, keys: %v", written)
	}

	// if any render_warnings exist, they should be present in save_output
	raw := p.ContextVariables[step.SaveOutput]
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("invalid json in save_output: %v", err)
	}
	if rw, ok := out["render_warnings"]; ok {
		if rw == nil {
			t.Fatalf("expected render_warnings to be non-nil when present")
		}
	}
}
