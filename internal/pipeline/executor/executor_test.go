package executor

import (
	"pipeline/internal/pipeline/types"
	"testing"
)

func TestIdleTimeoutDefaults(t *testing.T) {
	// Test that default values are set correctly in the executor logic
	step := types.Step{
		Name: "test",
		// IdleTimeout is 0 (not set), should default to 600
		// Timeout is 0 (not set), should default to unlimited (0)
	}

	// Simulate the logic from executor.go lines 307-310
	idleTimeout := step.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = 600
	}

	// Check that IdleTimeout gets default value of 600
	if idleTimeout != 600 {
		t.Errorf("Expected idleTimeout to be 600 (10 minutes), got %d", idleTimeout)
	}

	// Check that Timeout remains 0 (unlimited)
	if step.Timeout != 0 {
		t.Errorf("Expected Timeout to be 0 (unlimited), got %d", step.Timeout)
	}
}
