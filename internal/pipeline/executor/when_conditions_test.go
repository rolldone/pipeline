package executor

import (
	"pipeline/internal/pipeline/types"
	"testing"
)

func TestCheckConditions_WhenContainsGotoStep(t *testing.T) {
	e := &Executor{}
	step := &types.Step{
		Name: "s1",
		When: []types.WhenEntry{
			{Contains: "hello", Action: "goto_step", Step: "next"},
		},
	}

	action, target, err := e.checkConditions(step, "hello world", types.Vars{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != "goto_step" || target != "next" {
		t.Fatalf("expected goto_step next, got action=%q target=%q", action, target)
	}
}

func TestCheckConditions_WhenAllAnyNested(t *testing.T) {
	e := &Executor{}
	step := &types.Step{
		Name: "s1",
		When: []types.WhenEntry{
			{
				All: []types.WhenEntry{
					{Contains: "alpha"},
					{
						Any: []types.WhenEntry{
							{Contains: "missing"},
							{Regex: "beta:[0-9]+"},
						},
					},
				},
				Action: "goto_step",
				Step:   "s2",
			},
		},
	}

	action, target, err := e.checkConditions(step, "alpha beta:42", types.Vars{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != "goto_step" || target != "s2" {
		t.Fatalf("expected goto_step s2, got action=%q target=%q", action, target)
	}
}

func TestCheckConditions_WhenNoMatchUsesElse(t *testing.T) {
	e := &Executor{}
	step := &types.Step{
		Name:       "s1",
		ElseAction: "goto_step",
		ElseStep:   "fallback",
		When: []types.WhenEntry{
			{Contains: "needle", Action: "goto_step", Step: "match"},
		},
	}

	action, target, err := e.checkConditions(step, "haystack", types.Vars{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != "goto_step" || target != "fallback" {
		t.Fatalf("expected else goto_step fallback, got action=%q target=%q", action, target)
	}
}

func TestCheckConditions_LegacyConditionsTakePrecedence(t *testing.T) {
	e := &Executor{}
	step := &types.Step{
		Name: "s1",
		Conditions: []types.Condition{
			{Pattern: "alpha", Action: "goto_step", Step: "legacy"},
		},
		When: []types.WhenEntry{
			{Contains: "alpha", Action: "goto_step", Step: "when"},
		},
	}

	action, target, err := e.checkConditions(step, "alpha", types.Vars{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != "goto_step" || target != "legacy" {
		t.Fatalf("expected legacy goto_step legacy, got action=%q target=%q", action, target)
	}
}
