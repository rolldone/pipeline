package parser

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"pipeline/internal/pipeline/types"
)

// ParsePipeline parses a pipeline YAML file
func ParsePipeline(filePath string) (*types.Pipeline, error) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read pipeline file: %v", err)
	}

	// First, unmarshal to a map to handle the "pipeline:" wrapper
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse pipeline YAML: %v", err)
	}

	// Extract the pipeline content
	pipelineData, ok := raw["pipeline"]
	if !ok {
		// If no "pipeline" wrapper, assume the whole file is the pipeline
		pipelineData = raw
	}

	// Convert back to YAML bytes for unmarshaling to Pipeline struct
	pipelineBytes, err := yaml.Marshal(pipelineData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pipeline data: %v", err)
	}

	var pipeline types.Pipeline
	if err := yaml.Unmarshal(pipelineBytes, &pipeline); err != nil {
		return nil, fmt.Errorf("failed to parse pipeline struct: %v", err)
	}

	// Initialize context variables map
	pipeline.ContextVariables = make(map[string]string)

	return &pipeline, nil
}

// ParseVars parses a vars YAML file and returns vars for a specific key
func ParseVars(filePath, key string) (types.Vars, error) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read vars file: %v", err)
	}

	var allVars map[string]types.Vars
	if err := yaml.Unmarshal(data, &allVars); err != nil {
		return nil, fmt.Errorf("failed to parse vars YAML: %v", err)
	}

	vars, exists := allVars[key]
	if !exists {
		return nil, fmt.Errorf("vars key '%s' not found", key)
	}

	return vars, nil
}

// ParseVarsSafe parses a vars YAML file and returns vars for a specific key
// Returns empty map if file doesn't exist or key not found (safe for optional usage)
func ParseVarsSafe(filePath, key string) (types.Vars, error) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		// File doesn't exist, return empty vars
		return make(types.Vars), nil
	}

	var allVars map[string]types.Vars
	if err := yaml.Unmarshal(data, &allVars); err != nil {
		return nil, fmt.Errorf("failed to parse vars YAML: %v", err)
	}

	vars, exists := allVars[key]
	if !exists {
		// Key doesn't exist, return empty vars
		return make(types.Vars), nil
	}

	return vars, nil
}

// LoadExecutions loads executions from config (placeholder for now)
func LoadExecutions(configPath string) ([]types.Execution, error) {
	// TODO: Load from pipeline.yaml direct_access.executions (legacy: make-sync.yaml)
	return []types.Execution{}, nil
}

// ResolvePipelinePath resolves pipeline file path relative to pipeline_dir
func ResolvePipelinePath(pipelineDir, pipelineFile string) string {
	return filepath.Join(pipelineDir, pipelineFile)
}

// ResolveVarsPath resolves vars file path
func ResolveVarsPath(pipelineDir string) string {
	return filepath.Join(pipelineDir, "vars.yaml")
}
