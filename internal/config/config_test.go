package config

import (
    "testing"

    "pipeline/internal/pipeline/types"
)

func TestValidateConfigExecutionModeMissing(t *testing.T) {
    cfg := &Config{}
    cfg.ProjectName = "test"
    cfg.DirectAccess = DirectAccess{
        Executions: []types.Execution{
            {
                Name:     "NoMode",
                Key:      "no_mode",
                Pipeline: "p.yaml",
                Hosts:    []string{},
                // Mode omitted
            },
        },
    }

    err := ValidateConfig(cfg)
    if err == nil {
        t.Fatalf("expected validation error for missing mode, got nil")
    }
}

func TestValidateConfigExecutionModeInvalid(t *testing.T) {
    cfg := &Config{}
    cfg.ProjectName = "test"
    cfg.DirectAccess = DirectAccess{
        Executions: []types.Execution{
            {
                Name:     "BadMode",
                Key:      "bad_mode",
                Pipeline: "p.yaml",
                Mode:     "danger",
                Hosts:    []string{},
            },
        },
    }

    err := ValidateConfig(cfg)
    if err == nil {
        t.Fatalf("expected validation error for invalid mode, got nil")
    }
}

func TestValidateConfigExecutionModeValid(t *testing.T) {
    cfg := &Config{}
    cfg.ProjectName = "test"
    cfg.DirectAccess = DirectAccess{
        Executions: []types.Execution{
            {
                Name:     "Sandbox",
                Key:      "sandbox",
                Pipeline: "p.yaml",
                Mode:     "sandbox",
            },
            {
                Name:     "Live",
                Key:      "live",
                Pipeline: "p.yaml",
                Mode:     "live",
            },
        },
    }

    if err := ValidateConfig(cfg); err != nil {
        t.Fatalf("unexpected validation error for valid modes: %v", err)
    }
}
