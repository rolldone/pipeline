package executor

import (
	"pipeline/internal/pipeline/types"
	"strings"
	"testing"
)

func TestBuildRsyncArgs_SoftVsForce(t *testing.T) {
	e := NewExecutor()
	step := &types.Step{
		Name:         "test",
		Includes:     []string{"*/", "*.js"},
		Excludes:     []string{"*"},
		Options:      []string{"--progress"},
		DeletePolicy: "soft",
	}
	entries := []struct{ Source, Destination string }{{Source: "dist/", Destination: "/var/www/"}}

	args, err := e.buildRsyncArgSlices(step, "remote", "web-1", entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(args) != 1 {
		t.Fatalf("expected 1 arg slice, got %d", len(args))
	}
	joined := ""
	for _, a := range args[0] {
		joined += a + " "
	}
	// ensure --delete not present
	for _, a := range args[0] {
		if a == "--delete" {
			t.Fatalf("--delete should not be present for soft policy")
		}
	}

	// Now test force
	step.DeletePolicy = "force"
	step.ConfirmForce = true
	args2, err := e.buildRsyncArgSlices(step, "remote", "web-1", entries)
	if err != nil {
		t.Fatalf("unexpected error for force: %v", err)
	}
	found := false
	for _, a := range args2[0] {
		if a == "--delete" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("--delete should be present for force policy")
	}
}

func TestBuildRsyncArgs_IncludeExcludeOrdering(t *testing.T) {
	e := NewExecutor()
	step := &types.Step{
		Name:     "inc-exc",
		Includes: []string{"*/", "*.css", "*.js"},
		Excludes: []string{"*"},
	}
	entries := []struct{ Source, Destination string }{{Source: "assets/", Destination: "/var/www/assets/"}}
	args, err := e.buildRsyncArgSlices(step, "remote", "web-1", entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// verify includes appear before excludes
	idxInclude := -1
	idxExclude := -1
	for i, a := range args[0] {
		if idxInclude == -1 && strings.HasPrefix(a, "--include=") {
			idxInclude = i
		}
		if idxExclude == -1 && strings.HasPrefix(a, "--exclude=") {
			idxExclude = i
		}
	}
	if idxInclude == -1 || idxExclude == -1 {
		t.Fatalf("includes or excludes not found in args: %v", args[0])
	}
	if idxInclude > idxExclude {
		t.Fatalf("includes should come before excludes")
	}
}
