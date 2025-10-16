package executor

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"pipeline/internal/config"
	"pipeline/internal/pipeline/types"
	"pipeline/internal/sshclient"
)

// Executor handles pipeline execution
type Executor struct {
	tempDir              string
	pipeline             *types.Pipeline
	executedAsSubroutine map[string]bool
	logFile              *os.File
	// output history per execution (in-memory ring buffer, byte-capped)
	outputHistory *RingBuffer
	historyMu     sync.Mutex
}

// NewExecutor creates a new executor
func NewExecutor() *Executor {
	return &Executor{
		tempDir:              ".sync_temp",
		executedAsSubroutine: make(map[string]bool),
	}
}

// writeLog writes a message to the log file (lazy initialization)
func (e *Executor) writeLog(message string) {
	if e.logFile != nil {
		// Strip ANSI escape codes for clean log file
		cleanMessage := stripAnsiCodes(message)
		timestamp := time.Now().Format("[2006-01-02 15:04:05]")
		e.logFile.WriteString(fmt.Sprintf("%s %s\n", timestamp, cleanMessage))
		e.logFile.Sync()
	}
}

// flushErrorEvidence writes the last n lines from outputHistory to the pipeline log with a header
func (e *Executor) flushErrorEvidence(n int) {
	if e.outputHistory == nil {
		return
	}
	e.historyMu.Lock()
	lines := e.outputHistory.LastN(n)
	e.historyMu.Unlock()
	if len(lines) == 0 {
		return
	}
	header := fmt.Sprintf("=== ERROR EVIDENCE (last %d lines) ===", len(lines))
	e.writeLog(header)
	for _, l := range lines {
		e.writeLog(l)
	}
}

// flushErrorEvidenceAll writes the entire output history buffer (up to cap)
// to the pipeline log as error evidence.
func (e *Executor) flushErrorEvidenceAll() {
	if e.outputHistory == nil {
		return
	}
	e.historyMu.Lock()
	lines := e.outputHistory.All()
	e.historyMu.Unlock()
	if len(lines) == 0 {
		return
	}
	header := "=== ERROR EVIDENCE (buffer up to 300KB) ==="
	e.writeLog(header)
	for _, l := range lines {
		e.writeLog(l)
	}
}

// shouldLogOutput decides whether to log command output based on priority:
// Step.LogOutput > Job.LogOutput > Pipeline.LogOutput > default(false)
func (e *Executor) shouldLogOutput(step *types.Step, job *types.Job) bool {
	// Step-level
	if step != nil && step.LogOutput != nil {
		return *step.LogOutput
	}
	// Job-level
	if job != nil && job.LogOutput != nil {
		return *job.LogOutput
	}
	// Pipeline-level
	if e.pipeline != nil {
		if e.pipeline.LogOutput != nil {
			return *e.pipeline.LogOutput
		}
	}
	// default false
	return false
}

// stripAnsiCodes removes ANSI escape sequences from string
func stripAnsiCodes(str string) string {
	// Regex pattern to match ANSI escape codes
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	return ansiRegex.ReplaceAllString(str, "")
}

// RingBuffer stores lines with a total byte cap. FIFO when cap exceeded.
type RingBuffer struct {
	lines      []string
	totalBytes int
	capBytes   int
}

func NewRingBuffer(capBytes int) *RingBuffer {
	return &RingBuffer{capBytes: capBytes}
}

// Add appends a line and evicts oldest lines while totalBytes > capBytes
func (r *RingBuffer) Add(line string) {
	if line == "" {
		return
	}
	r.lines = append(r.lines, line)
	r.totalBytes += len(line)
	// Evict oldest until under cap
	for r.totalBytes > r.capBytes && len(r.lines) > 0 {
		// remove first
		removed := r.lines[0]
		r.lines = r.lines[1:]
		r.totalBytes -= len(removed)
	}
}

// LastN returns up to n last lines (copy)
func (r *RingBuffer) LastN(n int) []string {
	if n <= 0 {
		return nil
	}
	if len(r.lines) == 0 {
		return nil
	}
	if n >= len(r.lines) {
		// return copy
		out := make([]string, len(r.lines))
		copy(out, r.lines)
		return out
	}
	start := len(r.lines) - n
	out := make([]string, n)
	copy(out, r.lines[start:])
	return out
}

// All returns a copy of all stored lines in the buffer.
func (r *RingBuffer) All() []string {
	if len(r.lines) == 0 {
		return nil
	}
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}

// Execute runs a pipeline with given execution config
func (e *Executor) Execute(pipeline *types.Pipeline, execution *types.Execution, vars types.Vars, hosts []string, cfg *config.Config) error {
	e.pipeline = pipeline // Set pipeline reference for context variables

	if execution == nil {
		return fmt.Errorf("execution is nil")
	}
	if execution.Jobs == nil {
		return fmt.Errorf("execution.Jobs is nil")
	}

	// Initialize subroutine tracking
	e.executedAsSubroutine = make(map[string]bool)

	// Resolve SSH configs for hosts
	sshConfigs, err := e.resolveSSHConfigs(hosts, cfg)
	if err != nil {
		return fmt.Errorf("failed to resolve SSH configs: %v", err)
	}

	// Create log file immediately after SSH configs resolved
	logsDir := ".sync_temp/logs"
	os.MkdirAll(logsDir, 0755)

	pipelineName := "unknown"
	if pipeline != nil && pipeline.Name != "" {
		pipelineName = pipeline.Name
	}

	timestamp := time.Now().Format("2006-01-02-15-04-05")
	logPath := filepath.Join(logsDir, fmt.Sprintf("%s-%s.log", pipelineName, timestamp))

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		e.logFile = logFile
		defer e.logFile.Close()
		// Write header
		e.logFile.WriteString(fmt.Sprintf("=== Pipeline: %s ===\n", pipelineName))
		e.logFile.WriteString(fmt.Sprintf("Started: %s\n\n", timestamp))
		e.logFile.Sync()
	}

	// initialize output history buffer (300 KB cap)
	e.historyMu.Lock()
	e.outputHistory = NewRingBuffer(307200)
	e.historyMu.Unlock()

	// Run jobs (parallel if possible)
	levels := e.groupJobsByLevel(execution.Jobs, pipeline)
	var sortedJobs []string
	for _, level := range levels {
		sortedJobs = append(sortedJobs, level...)
	}

	currentJobIndex := 0

	for currentJobIndex < len(sortedJobs) {
		jobName := sortedJobs[currentJobIndex]

		// Skip jobs that have been executed as subroutines
		if e.executedAsSubroutine[jobName] {
			currentJobIndex++
			continue
		}

		job, err := e.findJob(pipeline, jobName)
		if err != nil {
			return err
		}

		err = e.runJobFromStep(job, jobName, 0, sshConfigs, vars, pipeline, currentJobIndex == 0)
		if err != nil {
			return err
		}

		// Add visual separation between jobs
		fmt.Println()
		if e.shouldLogOutput(nil, job) {
			e.writeLog(fmt.Sprintf("=== job %s start ===", job.Name))
		}

		currentJobIndex++
	}

	return nil
}

// injectSSHKeys copies .ssh to .sync_temp/.ssh
func (e *Executor) injectSSHKeys() error {
	srcDir := ".ssh"
	destDir := filepath.Join(e.tempDir, ".ssh")

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	// Copy files (simplified, use existing copy logic from make-sync)
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(srcDir, path)
		destPath := filepath.Join(destDir, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		return copyFile(path, destPath)
	})
}

// resolveSSHConfigs gets SSH configs for hosts
func (e *Executor) resolveSSHConfigs(hosts []string, cfg *config.Config) ([]map[string]interface{}, error) {
	var configs []map[string]interface{}
	for _, host := range hosts {
		for _, sshCfg := range cfg.DirectAccess.SSHConfigs {
			if h, ok := sshCfg["Host"].(string); ok && h == host {
				configs = append(configs, sshCfg)
				break
			}
		}
	}
	if len(configs) != len(hosts) {
		return nil, fmt.Errorf("not all hosts found in SSH configs")
	}
	return configs, nil
}

// findJob finds a job by name in pipeline
func (e *Executor) findJob(pipeline *types.Pipeline, name string) (*types.Job, error) {
	for i, job := range pipeline.Jobs {
		if job.Name == name {
			return &pipeline.Jobs[i], nil
		}
	}
	return nil, fmt.Errorf("job not found")
}

// runJob runs a job on all hosts
func (e *Executor) runJob(job *types.Job, jobName string, configs []map[string]interface{}, vars types.Vars) error {
	return e.runJobFromStep(job, jobName, 0, configs, vars, nil, false)
}

// runJobFromStep runs a job starting from a specific step index
func (e *Executor) runJobFromStep(job *types.Job, jobName string, startStepIdx int, configs []map[string]interface{}, vars types.Vars, pipeline *types.Pipeline, isHead bool) error {
	if isHead {
		fmt.Printf("▶️  EXECUTING JOB: %s (HEAD)\n", jobName)
		if e.shouldLogOutput(nil, job) {
			e.writeLog(fmt.Sprintf("▶️  EXECUTING JOB: %s (HEAD)", jobName))
		}
	} else {
		fmt.Printf("▶️  EXECUTING JOB: %s\n", jobName)
		if e.shouldLogOutput(nil, job) {
			e.writeLog(fmt.Sprintf("▶️  EXECUTING JOB: %s", jobName))
		}
	}
	stepIndex := startStepIdx
	executedGotoTarget := false
	gotoTarget := ""

	for stepIndex < len(job.Steps) {
		step := &job.Steps[stepIndex]

		// Skip error handling steps unless they are the goto target
		if strings.HasSuffix(step.Name, "_handler") && step.Name != gotoTarget {
			stepIndex++
			continue
		}

		action := ""
		targetStep := ""

		for _, config := range configs {
			stepAction, stepTarget, err := e.runStep(step, job, config, vars)
			if err != nil {
				return err
			}

			// Handle conditional actions
			if stepAction == "goto_step" && stepTarget != "" {
				action = stepAction
				targetStep = stepTarget
				break // Break host loop to restart with new step
			} else if stepAction == "goto_job" && stepTarget != "" {
				action = stepAction
				targetStep = stepTarget
				break // Break host loop to switch job
			} else if stepAction == "drop" {
				action = stepAction
				break // Break host loop to stop job execution
			}
			// For "continue" or no action, continue to next host
		}

		// Handle goto_step and goto_job after processing all hosts for this step
		if action == "goto_step" && targetStep != "" {
			if e.shouldLogOutput(step, job) {
				e.writeLog(fmt.Sprintf("=== step %s start ===", step.Name))
			}
			// Find target step index
			newIndex := e.findStepIndex(job, targetStep)
			if newIndex == -1 {
				return fmt.Errorf("goto_step target '%s' not found in job", targetStep)
			}
			// Jump to target step
			stepIndex = newIndex
			executedGotoTarget = true
			gotoTarget = targetStep
			continue // Restart loop with new stepIndex
		} else if action == "goto_job" && targetStep != "" {
			// Execute target job as subroutine, then continue with next step
			targetJob, err := e.findJob(pipeline, targetStep)
			if err != nil {
				return fmt.Errorf("goto_job target '%s' not found: %v", targetStep, err)
			}
			err = e.runJobFromStep(targetJob, targetStep, 0, configs, vars, pipeline, false)
			if err != nil {
				return fmt.Errorf("subroutine job '%s' failed: %v", targetStep, err)
			}
			// Mark as executed to prevent re-execution in main flow
			e.executedAsSubroutine[targetStep] = true
			// Continue with next step in current job
			stepIndex++
			continue
		} else if action == "drop" {
			// Stop job execution without error
			fmt.Printf("🛑 Job execution dropped at step: %s\n", step.Name)
			break
		}

		// If we've executed a goto target, stop after executing it
		if executedGotoTarget && step.Name == gotoTarget {
			break
		}

		stepIndex++
	}
	fmt.Printf("✅ Completed job: %s\n", jobName)
	return nil
}

// runStep runs a step on a host
func (e *Executor) runStep(step *types.Step, job *types.Job, config map[string]interface{}, vars types.Vars) (string, string, error) {
	switch step.Type {
	case "file_transfer":
		err := e.runFileTransferStep(step, job, config, vars)
		return "", "", err
	case "script":
		err := e.runScriptStep(step, job, config, vars)
		return "", "", err
	default: // "command" or empty
		return e.runCommandStep(step, job, config, vars)
	}
}

// runCommandStep runs a command step on a host with conditional and interactive support
func (e *Executor) runCommandStep(step *types.Step, job *types.Job, config map[string]interface{}, vars types.Vars) (string, string, error) {
	fmt.Printf("📋 Executing step: %s\n", step.Name)
	// Interpolate vars in commands
	commands := e.interpolateVars(step.Commands, vars)

	// Check if job is in local mode (default to "remote" for backward compatibility)
	jobMode := job.Mode
	if jobMode == "" {
		jobMode = "remote"
	}
	if jobMode == "local" {
		return e.runCommandStepLocal(step, commands, vars)
	}

	// Remote mode - use SSH execution
	// Extract SSH params from config
	host, _ := config["HostName"].(string)
	if host == "" {
		host, _ = config["Host"].(string) // fallback
	}
	user, _ := config["User"].(string)
	port, _ := config["Port"].(string)
	if port == "" {
		port = "22"
	}
	privateKey, _ := config["IdentityFile"].(string)
	password, _ := config["Password"].(string)

	// Create SSH client
	client, err := sshclient.NewPersistentSSHClient(user, privateKey, password, host, port)
	if err != nil {
		return "", "", fmt.Errorf("failed to create SSH client: %v", err)
	}
	if err := client.Connect(); err != nil {
		return "", "", fmt.Errorf("failed to connect SSH client: %v", err)
	}
	defer client.Close()

	// Determine working directory
	workingDir := e.interpolateString(step.WorkingDir, vars)
	if workingDir == "" {
		// Use working_dir from vars if step doesn't specify one
		if wd, ok := vars["working_dir"].(string); ok {
			workingDir = wd
		}
	}

	// Determine timeout (default 0 = unlimited if not specified)
	timeout := step.Timeout
	if timeout == 0 && step.IdleTimeout == 0 {
		timeout = 100 // Legacy default only if no idle timeout
	}

	// Determine idle timeout (default 600 seconds = 10 minutes)
	idleTimeout := step.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = 600
	}

	var lastOutput string
	for _, cmd := range commands {
		// Prepend cd command if working directory is specified
		fullCmd := cmd
		if workingDir != "" {
			fullCmd = fmt.Sprintf("cd %s && %s", workingDir, cmd)
		}

		fmt.Printf("Running on %s: %s\n", host, fullCmd)

		// Run command with interactive support and timeout
		output, err := e.runCommandInteractive(client, fullCmd, step.Expect, vars, timeout, idleTimeout, step.Silent, step, job)
		if err != nil {
			// flush evidence into pipeline log before returning
			e.flushErrorEvidenceAll()
			return "", "", fmt.Errorf("command failed: %v", err)
		}

		lastOutput = output // Save for potential output saving

		// Check conditions on output
		action, targetStep, err := e.checkConditions(step, output, vars)
		if err != nil {
			return "", "", err
		}
		if action != "" {
			return action, targetStep, nil
		}
	}

	// Save output to context variable if requested
	if step.SaveOutput != "" && e.pipeline != nil {
		e.pipeline.ContextVariables[step.SaveOutput] = strings.TrimSpace(lastOutput)
	}

	return "", "", nil
}

// checkConditions checks conditions against command output
func (e *Executor) checkConditions(step *types.Step, output string, vars types.Vars) (string, string, error) {
	conditionMatched := false

	for _, condition := range step.Conditions {
		// Interpolate pattern with vars
		pattern := e.interpolateString(condition.Pattern, vars)

		matched, err := regexp.MatchString(pattern, output)
		if err != nil {
			return "", "", fmt.Errorf("invalid regex pattern '%s' in step %s: %v", pattern, step.Name, err)
		}

		if matched {
			conditionMatched = true
			switch condition.Action {
			case "continue":
				// Continue to next step (no action needed)
				continue
			case "drop":
				return "drop", "", nil // Stop job execution without error
			case "goto_step":
				if condition.Step == "" {
					return "", "", fmt.Errorf("goto_step action requires 'step' field in step %s", step.Name)
				}
				return "goto_step", condition.Step, nil
			case "goto_job":
				if condition.Job == "" {
					return "", "", fmt.Errorf("goto_job action requires 'job' field in step %s", step.Name)
				}
				return "goto_job", condition.Job, nil
			case "fail":
				return "", "", fmt.Errorf("step intentionally failed due to condition match")
			default:
				return "", "", fmt.Errorf("unknown condition action '%s' in step %s", condition.Action, step.Name)
			}
		}
	}

	// If no conditions matched and else_action is specified, use else_action
	if !conditionMatched && step.ElseAction != "" {
		switch step.ElseAction {
		case "continue":
			return "", "", nil // Continue normally
		case "drop":
			return "drop", "", nil
		case "goto_step":
			if step.ElseStep == "" {
				return "", "", fmt.Errorf("else goto_step action requires 'else_step' field in step %s", step.Name)
			}
			return "goto_step", step.ElseStep, nil
		case "goto_job":
			if step.ElseJob == "" {
				return "", "", fmt.Errorf("else goto_job action requires 'else_job' field in step %s", step.Name)
			}
			return "goto_job", step.ElseJob, nil
		case "fail":
			return "", "", fmt.Errorf("step failed due to else condition")
		default:
			return "", "", fmt.Errorf("unknown else action '%s' in step %s", step.ElseAction, step.Name)
		}
	}

	return "", "", nil
}

// runFileTransferStep uploads/downloads files to/from remote host or copies locally
func (e *Executor) runFileTransferStep(step *types.Step, job *types.Job, config map[string]interface{}, vars types.Vars) error {
	// Handle local mode - no SSH, just local file operations
	jobMode := job.Mode
	if jobMode == "" {
		jobMode = "remote"
	}
	if jobMode == "local" {
		return e.runLocalFileTransfer(step, vars)
	}

	// Remote mode (default) - use SSH
	// Build list of file transfer entries to process (source,destination,template)
	entries, err := e.buildFileTransferEntries(step, vars)
	if err != nil {
		return err
	}

	// Extract SSH params
	host, _ := config["HostName"].(string)
	if host == "" {
		host, _ = config["Host"].(string)
	}
	user, _ := config["User"].(string)
	port, _ := config["Port"].(string)
	if port == "" {
		port = "22"
	}
	privateKey, _ := config["IdentityFile"].(string)
	password, _ := config["Password"].(string)

	// Create SSH client
	client, err := sshclient.NewPersistentSSHClient(user, privateKey, password, host, port)
	if err != nil {
		return fmt.Errorf("failed to create SSH client: %v", err)
	}
	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect SSH client: %v", err)
	}
	defer client.Close()

	direction := step.Direction
	if direction == "" {
		direction = "upload" // default
	}

	// Process each entry sequentially
	for _, ent := range entries {
		src := ent.Source
		dst := ent.Destination
		tmpl := ent.Template

		if direction == "download" {
			// Download from remote source to local destination
			fmt.Printf("Downloading %s:%s to %s\n", host, src, dst)
			if err := client.DownloadFile(dst, src); err != nil {
				return fmt.Errorf("failed to download file: %v", err)
			}
			continue
		}

		// Upload
		fileTemplateEnabled := false
		if tmpl != "" {
			fileTemplateEnabled = (tmpl == "enabled")
		} else if step.Template == "enabled" {
			fileTemplateEnabled = true
		}

		if fileTemplateEnabled {
			// Render content with variables
			content, err := os.ReadFile(src)
			if err != nil {
				return fmt.Errorf("failed to read source file %s: %v", src, err)
			}
			interpolatedContent := e.interpolateString(string(content), vars)

			// Create temporary file with interpolated content
			tempFile, err := os.CreateTemp("", "pipeline-upload-*")
			if err != nil {
				return fmt.Errorf("failed to create temp file: %v", err)
			}
			defer os.Remove(tempFile.Name())
			defer tempFile.Close()

			if _, err := tempFile.WriteString(interpolatedContent); err != nil {
				return fmt.Errorf("failed to write temp file: %v", err)
			}
			tempFile.Close()

			// Upload interpolated temp file using SCP
			fmt.Printf("Uploading %s (rendered) to %s:%s\n", src, host, dst)
			if err := client.UploadFile(tempFile.Name(), dst); err != nil {
				return fmt.Errorf("failed to upload file: %v", err)
			}
		} else {
			// Upload file as-is without rendering
			fmt.Printf("Uploading %s (as-is) to %s:%s\n", src, host, dst)
			if err := client.UploadFile(src, dst); err != nil {
				return fmt.Errorf("failed to upload file: %v", err)
			}
		}
	}
	return nil
}

// buildFileTransferEntries builds an expanded list of file entries from step.Files, step.Sources or step.Source
// It expands local glob patterns and resolves per-file destinations. Returns entries with interpolated strings.
func (e *Executor) buildFileTransferEntries(step *types.Step, vars types.Vars) ([]struct {
	Source      string
	Destination string
	Template    string
}, error) {
	type entry struct {
		Source      string
		Destination string
		Template    string
	}
	var results []entry

	// Helper to append resolved sources (expanding globs)
	appendResolved := func(srcPattern, dstTemplate, tmpl string) error {
		// Interpolate both
		srcInterp := e.interpolateString(srcPattern, vars)
		dstInterp := e.interpolateString(dstTemplate, vars)

		// If pattern contains glob characters, expand
		if strings.ContainsAny(srcInterp, "*?[") {
			matches, err := filepath.Glob(srcInterp)
			if err != nil {
				return fmt.Errorf("invalid glob pattern %s: %v", srcInterp, err)
			}
			if len(matches) == 0 {
				return fmt.Errorf("no files match pattern %s", srcInterp)
			}
			for _, m := range matches {
				// Determine dest path: if dstInterp ends with / treat as dir and preserve relative path
				if strings.HasSuffix(dstInterp, string(os.PathSeparator)) || strings.HasSuffix(dstInterp, "/") {
					rel, err := filepath.Rel(filepath.Dir(srcInterp), m)
					if err != nil {
						rel = filepath.Base(m)
					}
					dst := filepath.Join(dstInterp, rel)
					results = append(results, entry{Source: m, Destination: dst, Template: tmpl})
				} else {
					// Destination is a directory if multiple matches; place files preserving base name
					dst := filepath.Join(dstInterp, filepath.Base(m))
					results = append(results, entry{Source: m, Destination: dst, Template: tmpl})
				}
			}
			return nil
		}

		// No glob - single file
		results = append(results, entry{Source: srcInterp, Destination: dstInterp, Template: tmpl})
		return nil
	}

	// Priority 1: step.Files
	if len(step.Files) > 0 {
		for _, f := range step.Files {
			// Destination resolution: per-file destination required or fallback to step.Destination
			dst := f.Destination
			if dst == "" {
				dst = step.Destination
			}
			if dst == "" {
				return nil, fmt.Errorf("file entry %s missing destination and step.destination not set", f.Source)
			}
			if err := appendResolved(f.Source, dst, f.Template); err != nil {
				return nil, err
			}
		}
		// Convert and return
		out := make([]struct{ Source, Destination, Template string }, len(results))
		for i, r := range results {
			out[i] = struct{ Source, Destination, Template string }{r.Source, r.Destination, r.Template}
		}
		return out, nil
	}

	// Priority 2: step.Sources
	if len(step.Sources) > 0 {
		if step.Destination == "" {
			return nil, fmt.Errorf("step 'sources' provided but step.destination is not set")
		}
		for _, s := range step.Sources {
			if err := appendResolved(s, step.Destination, ""); err != nil {
				return nil, err
			}
		}
		out := make([]struct{ Source, Destination, Template string }, len(results))
		for i, r := range results {
			out[i] = struct{ Source, Destination, Template string }{r.Source, r.Destination, r.Template}
		}
		return out, nil
	}

	// Fallback: single source
	if step.Source == "" {
		return nil, fmt.Errorf("no source(s) specified for file_transfer step %s", step.Name)
	}
	dst := step.Destination
	if dst == "" {
		return nil, fmt.Errorf("destination not specified for single source in step %s", step.Name)
	}
	if err := appendResolved(step.Source, dst, ""); err != nil {
		return nil, err
	}
	out := make([]struct{ Source, Destination, Template string }, len(results))
	for i, r := range results {
		out[i] = struct{ Source, Destination, Template string }{r.Source, r.Destination, r.Template}
	}
	return out, nil
}

// runScriptStep loads and executes a script file
func (e *Executor) runScriptStep(step *types.Step, job *types.Job, config map[string]interface{}, vars types.Vars) error {
	// Interpolate vars in file path
	scriptFile := e.interpolateString(step.File, vars)

	// Load script file
	scriptPath := filepath.Join(".sync_pipelines", "scripts", scriptFile)
	scriptContent, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read script file %s: %v", scriptPath, err)
	}

	// Interpolate vars in script
	script := e.interpolateString(string(scriptContent), vars)

	// Execute as command, preserving step-level settings
	tempStep := &types.Step{
		Name:       step.Name,
		Type:       "command",
		Commands:   []string{script},
		Conditions: step.Conditions,
		Expect:     step.Expect,
		WorkingDir: step.WorkingDir,
		Timeout:    step.Timeout,
	}
	action, _, err := e.runCommandStep(tempStep, job, config, vars)
	if err != nil {
		return err
	}
	if action != "" {
		return fmt.Errorf("conditional action '%s' not supported in script steps", action)
	}
	return nil
}

// findStepIndex finds the index of a step by name in a job
func (e *Executor) findStepIndex(job *types.Job, stepName string) int {
	for i, step := range job.Steps {
		if step.Name == stepName {
			return i
		}
	}
	return -1
}

// runJobsParallel runs jobs in parallel respecting dependencies
func (e *Executor) runJobsParallel(jobNames []string, pipeline *types.Pipeline, sshConfigs []map[string]interface{}, vars types.Vars) error {
	// Group jobs by dependency level
	levels := e.groupJobsByLevel(jobNames, pipeline)

	for _, level := range levels {
		if len(level) == 1 {
			// Single job, run sequential
			job, err := e.findJob(pipeline, level[0])
			if err != nil {
				return err
			}
			if err := e.runJob(job, level[0], sshConfigs, vars); err != nil {
				return err
			}
		} else {
			// Multiple jobs, run parallel
			errChan := make(chan error, len(level))
			for _, jobName := range level {
				go func(name string) {
					job, err := e.findJob(pipeline, name)
					if err != nil {
						errChan <- err
						return
					}
					errChan <- e.runJob(job, name, sshConfigs, vars)
				}(jobName)
			}

			// Wait for all
			for range level {
				if err := <-errChan; err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// groupJobsByLevel groups jobs by dependency levels
func (e *Executor) groupJobsByLevel(jobNames []string, pipeline *types.Pipeline) [][]string {
	if pipeline == nil {
		return nil
	}
	if pipeline.Jobs == nil {
		return nil
	}
	var levels [][]string
	remaining := make(map[string]bool)
	for _, name := range jobNames {
		remaining[name] = true
	}

	for len(remaining) > 0 {
		var level []string
		for name := range remaining {
			job, err := e.findJob(pipeline, name)
			if err != nil {
				// Skip jobs that don't exist
				continue
			}
			canRun := true
			for _, dep := range job.DependsOn {
				if remaining[dep] {
					canRun = false
					break
				}
			}
			if canRun {
				level = append(level, name)
			}
		}

		if len(level) == 0 {
			// Circular dependency or error
			break
		}

		levels = append(levels, level)
		for _, name := range level {
			delete(remaining, name)
		}
	}

	return levels
}

// copyFile copies a file (utility)
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// interpolateVars replaces {{var}} with values in commands
func (e *Executor) interpolateVars(commands []string, vars types.Vars) []string {
	result := make([]string, len(commands))
	for i, cmd := range commands {
		result[i] = e.interpolateString(cmd, vars)
	}
	return result
}

// runCommandInteractive runs a command with interactive prompt support and timeout
func (e *Executor) runCommandInteractive(client *sshclient.SSHClient, cmd string, expects []types.Expect, vars types.Vars, timeoutSeconds int, idleTimeoutSeconds int, silent bool, step *types.Step, job *types.Job) (string, error) {
	if len(expects) == 0 {
		// No expects, run normally with timeout
		return e.runCommandWithTimeout(client, cmd, timeoutSeconds, idleTimeoutSeconds, silent, step, job)
	}

	// For interactive commands, try to pipe responses
	// This is a basic implementation - full expect needs streaming I/O
	fmt.Printf("Note: Basic expect support - piping %d responses to command.\n", len(expects))

	// Build command with piped input for multiple responses
	var responses []string
	for _, expect := range expects {
		response := e.interpolateString(expect.Response, vars)
		responses = append(responses, response)
	}

	// Create a command that echoes all responses line by line
	echoCmd := "echo '" + strings.Join(responses, "\n") + "'"
	fullCmd := echoCmd + " | " + cmd

	// Use RunCommandWithOutput to capture and display output with timeout
	return e.runCommandWithTimeout(client, fullCmd, timeoutSeconds, idleTimeoutSeconds, silent, step, job)
}

// runCommandWithTimeout runs a command with timeout support and real-time output
func (e *Executor) runCommandWithTimeout(client *sshclient.SSHClient, cmd string, timeoutSeconds int, idleTimeoutSeconds int, silent bool, step *types.Step, job *types.Job) (string, error) {
	type result struct {
		output string
		err    error
	}

	resultChan := make(chan result, 1)
	outputChan := make(chan string, 100) // Channel for monitoring output

	// Run command in goroutine with output streaming
	go func() {
		output, err := e.runCommandWithStreamingAndChannel(client, cmd, silent, outputChan, step, job)
		resultChan <- result{output: output, err: err}
	}()

	// Setup timers
	totalTimeout := time.Duration(timeoutSeconds) * time.Second
	if timeoutSeconds == 0 {
		totalTimeout = 365 * 24 * time.Hour // ~1 year for unlimited
	}
	idleTimeout := time.Duration(idleTimeoutSeconds) * time.Second

	totalTimer := time.NewTimer(totalTimeout)
	idleTimer := time.NewTimer(idleTimeout)
	defer totalTimer.Stop()
	defer idleTimer.Stop()

	for {
		select {
		case <-outputChan:
			// Reset idle timer on any output
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(idleTimeout)

		case res := <-resultChan:
			// Command completed successfully
			if res.err != nil {
				return "", res.err
			}
			return res.output, nil

		case <-idleTimer.C:
			// Idle timeout - command has been silent too long
			e.flushErrorEvidenceAll()
			return "", fmt.Errorf("command idle timeout: no output for %d seconds", idleTimeoutSeconds)

		case <-totalTimer.C:
			// Total timeout
			if timeoutSeconds == 0 {
				continue // Unlimited, ignore total timeout
			}
			e.flushErrorEvidenceAll()
			return "", fmt.Errorf("command timed out after %d seconds", timeoutSeconds)
		}
	}
}

// runCommandWithStreaming runs a command and streams output in real-time
func (e *Executor) runCommandWithStreaming(client *sshclient.SSHClient, cmd string, silent bool, step *types.Step, job *types.Job) (string, error) {
	// Create a new session like RunCommandWithOutput does
	session, err := client.CreateSession()
	if err != nil {
		e.flushErrorEvidenceAll()
		return "", fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	// Get stdout and stderr pipes for real-time streaming
	stdout, err := session.StdoutPipe()
	if err != nil {
		e.flushErrorEvidenceAll()
		return "", fmt.Errorf("failed to get stdout pipe: %v", err)
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		e.flushErrorEvidenceAll()
		return "", fmt.Errorf("failed to get stderr pipe: %v", err)
	}

	// Start the command
	if err := session.Start(cmd); err != nil {
		e.flushErrorEvidenceAll()
		return "", fmt.Errorf("failed to start command: %v", err)
	}

	// Read output in real-time
	var outputBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)

	// Read stdout
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if !silent {
				fmt.Printf("Command output: %s\n", line)
			}
			outputBuf.WriteString(line + "\n")
			// record into history unconditionally
			e.historyMu.Lock()
			if e.outputHistory != nil {
				e.outputHistory.Add(line)
			}
			e.historyMu.Unlock()
			if e.shouldLogOutput(step, job) {
				e.writeLog(line)
			}
		}
	}()

	// Read stderr
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if !silent {
				fmt.Printf("Command output: %s\n", line)
			}
			outputBuf.WriteString(line + "\n")
			e.historyMu.Lock()
			if e.outputHistory != nil {
				e.outputHistory.Add(line)
			}
			e.historyMu.Unlock()
			if e.shouldLogOutput(step, job) {
				e.writeLog(line)
			}
		}
	}()

	// Wait for command to complete
	wg.Wait()
	if err := session.Wait(); err != nil {
		return outputBuf.String(), fmt.Errorf("command failed: %v", err)
	}

	return outputBuf.String(), nil
}

func (e *Executor) runCommandWithStreamingAndChannel(client *sshclient.SSHClient, cmd string, silent bool, outputChan chan<- string, step *types.Step, job *types.Job) (string, error) {
	// Create a new session like RunCommandWithOutput does
	session, err := client.CreateSession()
	if err != nil {
		e.flushErrorEvidenceAll()
		return "", fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	// Get stdout and stderr pipes for real-time streaming
	stdout, err := session.StdoutPipe()
	if err != nil {
		e.flushErrorEvidenceAll()
		return "", fmt.Errorf("failed to get stdout pipe: %v", err)
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		e.flushErrorEvidenceAll()
		return "", fmt.Errorf("failed to get stderr pipe: %v", err)
	}

	// Start the command
	if err := session.Start(cmd); err != nil {
		e.flushErrorEvidenceAll()
		return "", fmt.Errorf("failed to start command: %v", err)
	}

	// Read output in real-time
	var outputBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)

	// Read stdout
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if !silent {
				fmt.Printf("Command output: %s\n", line)
			}
			outputBuf.WriteString(line + "\n")
			// record into history unconditionally
			e.historyMu.Lock()
			if e.outputHistory != nil {
				e.outputHistory.Add(line)
			}
			e.historyMu.Unlock()
			if e.shouldLogOutput(step, job) {
				e.writeLog(line)
			}
			// Send line to channel for idle timeout monitoring
			select {
			case outputChan <- line:
			default:
				// Channel is full, skip to avoid blocking
			}
		}
	}()

	// Read stderr
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if !silent {
				fmt.Printf("Command output: %s\n", line)
			}
			outputBuf.WriteString(line + "\n")
			// record into history unconditionally
			e.historyMu.Lock()
			if e.outputHistory != nil {
				e.outputHistory.Add(line)
			}
			e.historyMu.Unlock()
			if e.shouldLogOutput(step, job) {
				e.writeLog(line)
			}
			// Send line to channel for idle timeout monitoring
			select {
			case outputChan <- line:
			default:
				// Channel is full, skip to avoid blocking
			}
		}
	}()

	// Wait for command to complete
	wg.Wait()
	if err := session.Wait(); err != nil {
		return outputBuf.String(), fmt.Errorf("command failed: %v", err)
	}

	return outputBuf.String(), nil
}

// interpolateString replaces {{var}} with values in a string
func (e *Executor) interpolateString(s string, vars types.Vars) string {
	result := s

	// First, replace with regular vars (higher priority)
	for key, value := range vars {
		placeholder := fmt.Sprintf("{{%s}}", key)
		if strVal, ok := value.(string); ok {
			result = strings.ReplaceAll(result, placeholder, strVal)
		}
	}

	// Then, replace with context variables (lower priority)
	if e.pipeline != nil {
		for key, value := range e.pipeline.ContextVariables {
			placeholder := fmt.Sprintf("{{%s}}", key)
			// Only replace if not already replaced by regular vars
			if strings.Contains(result, placeholder) {
				result = strings.ReplaceAll(result, placeholder, value)
			}
		}
	}

	// Handle undefined variables based on strict mode
	if e.pipeline != nil && e.pipeline.StrictVariables {
		// Check for any remaining {{variable}} patterns
		re := regexp.MustCompile(`\{\{([^}]+)\}\}`)
		matches := re.FindAllStringSubmatch(result, -1)
		for _, match := range matches {
			if len(match) > 1 {
				varName := match[1]
				// Check if it's in vars or context variables
				if _, exists := vars[varName]; !exists {
					if _, exists := e.pipeline.ContextVariables[varName]; !exists {
						// Variable not found in strict mode - this should be an error
						// For now, we'll leave the placeholder as-is and let caller handle
					}
				}
			}
		}
	}

	return result
}

// runCommandStepLocal runs commands locally for testing
func (e *Executor) runCommandStepLocal(step *types.Step, commands []string, vars types.Vars) (string, string, error) {
	// Determine working directory
	workingDir := e.interpolateString(step.WorkingDir, vars)
	if workingDir == "" {
		// Use working_dir from vars if step doesn't specify one
		if wd, ok := vars["working_dir"].(string); ok {
			workingDir = wd
		}
	}

	// debug prints removed
	var lastOutput string
	// For local mode, implement idle and total timeout behavior similar to remote execution
	for _, cmd := range commands {
		// Prepend cd command if working directory is specified
		fullCmd := cmd
		if workingDir != "" {
			fullCmd = fmt.Sprintf("cd %s && %s", workingDir, cmd)
		}

		fmt.Printf("Running locally: %s\n", fullCmd)

		// Prepare command
		execCmd := exec.Command("bash", "-c", fullCmd)
		stdout, err := execCmd.StdoutPipe()
		if err != nil {
			e.flushErrorEvidenceAll()
			return "", "", fmt.Errorf("failed to get stdout pipe: %v", err)
		}
		stderr, err := execCmd.StderrPipe()
		if err != nil {
			e.flushErrorEvidenceAll()
			return "", "", fmt.Errorf("failed to get stderr pipe: %v", err)
		}

		if err := execCmd.Start(); err != nil {
			e.flushErrorEvidenceAll()
			return "", "", fmt.Errorf("failed to start local command: %v", err)
		}

		// Stream output and capture
		var outputBuf bytes.Buffer
		var wg sync.WaitGroup
		wg.Add(2)

		// Channels for idle detection
		outChan := make(chan string, 100)

		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := scanner.Text()
				fmt.Printf("Command output: %s\n", line)
				outputBuf.WriteString(line + "\n")
				// record history
				e.historyMu.Lock()
				if e.outputHistory != nil {
					e.outputHistory.Add(line)
				}
				e.historyMu.Unlock()
				// send to idle monitor
				select {
				case outChan <- line:
				default:
				}
			}
		}()

		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				line := scanner.Text()
				fmt.Printf("Command output: %s\n", line)
				outputBuf.WriteString(line + "\n")
				e.historyMu.Lock()
				if e.outputHistory != nil {
					e.outputHistory.Add(line)
				}
				e.historyMu.Unlock()
				select {
				case outChan <- line:
				default:
				}
			}
		}()

		// Timers
		totalTimeout := time.Duration(step.Timeout) * time.Second
		if step.Timeout == 0 && step.IdleTimeout == 0 {
			totalTimeout = 365 * 24 * time.Hour
		}
		if step.Timeout == 0 && step.IdleTimeout != 0 {
			totalTimeout = 365 * 24 * time.Hour
		}
		if step.Timeout != 0 {
			totalTimeout = time.Duration(step.Timeout) * time.Second
		}
		idleTimeout := time.Duration(step.IdleTimeout) * time.Second
		if idleTimeout == 0 {
			idleTimeout = 600 * time.Second
		}

		totalTimer := time.NewTimer(totalTimeout)
		idleTimer := time.NewTimer(idleTimeout)
		done := make(chan error, 1)

		go func() {
			wg.Wait()
			done <- execCmd.Wait()
		}()

	loop:
		for {
			select {
			case <-outChan:
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(idleTimeout)
			case err := <-done:
				if err != nil {
					e.flushErrorEvidenceAll()
					return "", "", fmt.Errorf("command failed: %v", err)
				}
				break loop
			case <-idleTimer.C:
				// idle timeout
				_ = execCmd.Process.Kill()
				e.flushErrorEvidenceAll()
				return "", "", fmt.Errorf("command idle timeout: no output for %d seconds", step.IdleTimeout)
			case <-totalTimer.C:
				if step.Timeout == 0 {
					// unlimited
					continue
				}
				_ = execCmd.Process.Kill()
				e.flushErrorEvidenceAll()
				return "", "", fmt.Errorf("command timed out after %d seconds", step.Timeout)
			}
		}

		// Completed
		wg.Wait()
		outStr := outputBuf.String()
		if strings.TrimSpace(outStr) != "" {
			fmt.Printf("Output: %s\n", strings.TrimSpace(outStr))
		} else {
			fmt.Printf("Output: (empty)\n")
		}
		lastOutput = outStr

		// Check conditions on output
		action, targetStep, err := e.checkConditions(step, outStr, vars)
		if err != nil {
			return "", "", err
		}
		if action != "" {
			return action, targetStep, nil
		}
	}

	// Save output to context variable if requested
	if step.SaveOutput != "" && e.pipeline != nil {
		e.pipeline.ContextVariables[step.SaveOutput] = strings.TrimSpace(lastOutput)
	}

	return "", "", nil
}

// runCommandLocal runs a command locally
func (e *Executor) runCommandLocal(cmd string) (string, error) {
	// Use os/exec to run command locally
	output, err := exec.Command("bash", "-c", cmd).CombinedOutput()
	return string(output), err
}

// runLocalFileTransfer copies files locally (no SSH)
func (e *Executor) runLocalFileTransfer(step *types.Step, vars types.Vars) error {
	// Interpolate vars in paths
	source := e.interpolateString(step.Source, vars)
	destination := e.interpolateString(step.Destination, vars)

	fmt.Printf("📁 Local file transfer: %s → %s\n", source, destination)

	// Check if template rendering is enabled
	if step.Template == "enabled" {
		return e.runLocalFileTransferWithTemplate(source, destination, vars)
	}

	// Regular file copy without template rendering
	return e.copyLocalPath(source, destination)
}

// runLocalFileTransferWithTemplate handles file transfer with template rendering
func (e *Executor) runLocalFileTransferWithTemplate(source, destination string, vars types.Vars) error {
	// Read source file
	content, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("failed to read source file %s: %v", source, err)
	}

	// Render template with variables
	renderedContent := e.interpolateString(string(content), vars)

	// Ensure destination directory exists
	destDir := filepath.Dir(destination)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %v", destDir, err)
	}

	// Write rendered content to destination
	if err := os.WriteFile(destination, []byte(renderedContent), 0644); err != nil {
		return fmt.Errorf("failed to write rendered file %s: %v", destination, err)
	}

	fmt.Printf("✅ Template rendered and copied: %s → %s\n", source, destination)
	return nil
}

// copyLocalPath copies a file or directory from source to destination
func (e *Executor) copyLocalPath(source, destination string) error {
	// Check if source is a glob pattern
	if strings.Contains(source, "*") {
		return e.copyGlobPattern(source, destination)
	}

	// Get source info
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %v", source, err)
	}

	if info.IsDir() {
		return e.copyDirectory(source, destination)
	} else {
		return e.copyFile(source, destination)
	}
}

// copyFile copies a single file
func (e *Executor) copyFile(source, destination string) error {
	// Ensure destination directory exists
	destDir := filepath.Dir(destination)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %v", destDir, err)
	}

	// Copy file
	srcFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %v", source, err)
	}
	defer srcFile.Close()

	destFile, err := os.Create(destination)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %v", destination, err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		return fmt.Errorf("failed to copy file %s to %s: %v", source, destination, err)
	}

	fmt.Printf("✅ File copied: %s → %s\n", source, destination)
	return nil
}

// copyDirectory copies a directory recursively
func (e *Executor) copyDirectory(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate relative path from source
		relPath, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}

		// Construct destination path
		destPath := filepath.Join(destination, relPath)

		if info.IsDir() {
			// Create directory
			return os.MkdirAll(destPath, info.Mode())
		} else {
			// Copy file
			return e.copyFile(path, destPath)
		}
	})
}

// copyGlobPattern handles glob patterns like src/**/*.js
func (e *Executor) copyGlobPattern(pattern, destination string) error {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("invalid glob pattern %s: %v", pattern, err)
	}

	if len(matches) == 0 {
		return fmt.Errorf("no files match pattern %s", pattern)
	}

	for _, match := range matches {
		// Calculate relative path for destination
		relPath, err := filepath.Rel(filepath.Dir(pattern), match)
		if err != nil {
			relPath = filepath.Base(match)
		}

		destPath := filepath.Join(destination, relPath)
		if err := e.copyFile(match, destPath); err != nil {
			return err
		}
	}

	fmt.Printf("✅ Glob pattern copied: %s → %s (%d files)\n", pattern, destination, len(matches))
	return nil
}
