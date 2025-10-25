package executor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

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
	hasError             bool // tracks if any error occurred during execution
	forceLogging         bool // forces all logging when error occurs
	// output history per execution (in-memory ring buffer, byte-capped)
	outputHistory *RingBuffer
	historyMu     sync.Mutex
	// WriteFileFunc allows tests to inject write failures; defaults to os.WriteFile
	WriteFileFunc func(filename string, data []byte, perm os.FileMode) error
	// ExecCommand allows tests to inject a fake command runner; defaults to exec.Command
	ExecCommand func(name string, arg ...string) *exec.Cmd
	// NewSSHClient allows tests to inject a fake SSH client factory. By default
	// it uses sshclient.NewPersistentSSHClient and returns a concrete client
	// that satisfies the SSHClient interface below.
	NewSSHClient func(username, privateKeyPath, password, passphrase, host, port, proxyJump string) (SSHClient, error)
	// debugMode enables verbose debug prints for interactive diagnostics
	debugMode bool
}

// NewExecutor creates a new executor
func NewExecutor() *Executor {
	return &Executor{
		tempDir:              ".sync_temp",
		executedAsSubroutine: make(map[string]bool),
		WriteFileFunc:        os.WriteFile,
		ExecCommand:          exec.Command,
	}
}

// debugf prints debug messages when debugMode is enabled
func (e *Executor) debugf(format string, args ...interface{}) {
	if e == nil {
		return
	}
	if e.debugMode {
		fmt.Printf(format+"\n", args...)
	}
}

// ensureDefaults fills default factories that rely on external packages
func (e *Executor) ensureDefaults() {
	if e.NewSSHClient == nil {
		e.NewSSHClient = func(username, privateKeyPath, password, passphrase, host, port, proxyJump string) (SSHClient, error) {
			return sshclient.NewPersistentSSHClient(username, privateKeyPath, password, passphrase, host, port, proxyJump)
		}
	}
}

// SSHClient is the minimal interface used by executor when talking to SSH.
// Placed here to allow tests to provide mocks without changing the sshclient
// package.
type SSHClient interface {
	Connect() error
	Close() error
	UploadBytes(data []byte, remotePath string, perm os.FileMode) error
	UploadFile(localPath, remotePath string) error
	DownloadFile(localPath, remotePath string) error
	RunCommand(cmd string) error
	RunCommandWithOutput(cmd string) (string, error)
	SyncFile(localPath, remotePath string) error
	RunCommandWithStream(cmd string, usePty bool) (<-chan string, <-chan error, error)
	// ExecWithPTY executes a command on remote and requests a PTY. It returns
	// stdin (writable), stdout/stderr readers, a wait function to await
	// command completion, and an error if starting the command failed.
	ExecWithPTY(cmd string) (io.WriteCloser, io.Reader, io.Reader, func() error, func() error, error)
}

// writeLog writes a message to the log file (lazy initialization)
func (e *Executor) writeLog(message string) {
	// If log file hasn't been created yet but logging is needed, create it
	if e.logFile == nil && (e.hasError || e.shouldCreateLogFile()) {
		e.createLogFile()
	}

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

	// Ensure log file exists when flushing error evidence (force create on error)
	if e.logFile == nil {
		e.hasError = true     // Mark as error occurred
		e.forceLogging = true // Force logging
		e.createLogFile()
	}

	header := "=== ERROR EVIDENCE (buffer up to 300KB) ==="
	e.writeLog(header)
	for _, l := range lines {
		e.writeLog(l)
	}
}

// shouldLogOutput decides whether to log command output based on priority:
// Step.LogOutput > Job.LogOutput > Pipeline.LogOutput > forceLogging > default(false)
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
	// If forced logging (due to error), always log
	if e.forceLogging {
		return true
	}
	// default false
	return false
}

// shouldCreateLogFile determines if a log file should be created based on explicit log_output settings
func (e *Executor) shouldCreateLogFile() bool {
	// Check pipeline level
	if e.pipeline != nil && e.pipeline.LogOutput != nil && *e.pipeline.LogOutput {
		return true
	}
	// Check all jobs
	if e.pipeline != nil {
		for _, job := range e.pipeline.Jobs {
			if job.LogOutput != nil && *job.LogOutput {
				return true
			}
			// Check all steps in job
			for _, step := range job.Steps {
				if step.LogOutput != nil && *step.LogOutput {
					return true
				}
			}
		}
	}
	return false // Default: no log file unless error occurs
}

// saveRenderedContentToVariable loads, renders, and saves file content to a variable
func (e *Executor) saveRenderedContentToVariable(step *types.Step, vars types.Vars, entry struct{ Source, Destination, Template string }, varName string) error {
	sourcePath := entry.Source

	// Read file content
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to read source file %s: %v", sourcePath, err)
	}

	// Check if it's text and templating is enabled
	stepTemplateEnabled := (step.Template == "enabled")
	var renderedContent string

	if stepTemplateEnabled && isTextBytes(data) {
		content := string(data)
		renderedContent = e.interpolateString(content, vars)
	} else {
		renderedContent = string(data)
	}

	// Save to context variable
	if e.pipeline != nil {
		e.pipeline.ContextVariables[varName] = renderedContent
		fmt.Printf("💾 Saved rendered content to variable: %s (%d bytes)\n", varName, len(renderedContent))
	}

	return nil
}

// ensureConditionalLogging creates log file if needed before function exit
func (e *Executor) ensureConditionalLogging() {
	// Create log file if not already created and needed (for explicit logging without errors)
	if e.logFile == nil && e.shouldCreateLogFile() {
		e.createLogFile()
	}

	// Ensure log file is closed when function exits (if it was created)
	if e.logFile != nil {
		e.logFile.Close()
	}
}

// createLogFile creates the log file and writes initial header
func (e *Executor) createLogFile() {
	logsDir := ".sync_temp/logs"
	os.MkdirAll(logsDir, 0755)

	pipelineName := "unknown"
	if e.pipeline != nil && e.pipeline.Name != "" {
		pipelineName = e.pipeline.Name
	}

	timestamp := time.Now().Format("2006-01-02-15-04-05")
	logPath := filepath.Join(logsDir, fmt.Sprintf("%s-%s.log", pipelineName, timestamp))

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		e.logFile = logFile
		// Write header
		e.logFile.WriteString(fmt.Sprintf("=== Pipeline: %s ===\n", pipelineName))
		e.logFile.WriteString(fmt.Sprintf("Started: %s\n\n", timestamp))
		e.logFile.Sync()
		// Note: defer close is handled by caller
	}
}

// Execute runs a pipeline with given execution config
func (e *Executor) Execute(pipeline *types.Pipeline, execution *types.Execution, vars types.Vars, hosts []string, cfg *config.Config) error {
	e.pipeline = pipeline // Set pipeline reference for context variables

	if execution == nil {
		return fmt.Errorf("execution is nil")
	}
	if execution.Jobs == nil {
		execution.Jobs = []string{} // Initialize if nil
	}

	// Ensure conditional log creation happens before function exit
	defer e.ensureConditionalLogging()

	// Initialize subroutine tracking
	e.executedAsSubroutine = make(map[string]bool)

	// Set debug mode based on execution setting (execution.Debug can be set by CLI or pipeline config)
	if execution.Debug != nil && *execution.Debug {
		e.debugMode = true
	} else {
		e.debugMode = false
	}

	// Resolve SSH configs for hosts
	sshConfigs, err := e.resolveSSHConfigs(hosts, cfg)
	if err != nil {
		return fmt.Errorf("failed to resolve SSH configs: %v", err)
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

	// If no jobs specified in execution, this is an error
	if len(sortedJobs) == 0 {
		return fmt.Errorf("no jobs specified in execution '%s' - please specify jobs to run in the execution configuration", execution.Name)
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
			e.hasError = true
			e.forceLogging = true // Force all logging when error occurs
			// Create log file immediately when error occurs
			if e.logFile == nil && (e.hasError || e.shouldCreateLogFile()) {
				e.createLogFile()
			}
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

// AgentDownloadWithDB is a convenience exported wrapper used by tests/tools to
// invoke the download flow using a pre-downloaded indexing DB. It delegates to
// the internal performAgentDownloadWithFilter implementation.
func (e *Executor) AgentDownloadWithDB(step *types.Step, client SSHClient, localDBPath, remoteSyncTemp, osTarget string) error {
	return e.performAgentDownloadWithFilter(step, types.Vars{}, client, localDBPath, remoteSyncTemp, osTarget)
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

// saveStepOutput saves step output to context variables, parsing JSON to allow direct field access
func (e *Executor) saveStepOutput(stepOutputKey, output string) {
	if stepOutputKey == "" || e.pipeline == nil {
		return
	}

	// Try to parse as JSON
	var jsonData map[string]interface{}
	if err := json.Unmarshal([]byte(output), &jsonData); err == nil {
		// Successfully parsed as JSON, save individual fields
		for key, value := range jsonData {
			if strVal, ok := value.(string); ok {
				e.pipeline.ContextVariables[key] = strVal
			} else {
				// For non-string values, convert to string
				e.pipeline.ContextVariables[key] = fmt.Sprintf("%v", value)
			}
		}
		// Also save the full JSON for reference
		e.pipeline.ContextVariables[stepOutputKey] = output
	} else {
		// Not JSON, save as string
		e.pipeline.ContextVariables[stepOutputKey] = output
	}
}

// getEffectiveMode returns the effective execution mode for a step (step mode if set, otherwise job mode)
func getEffectiveMode(jobMode, stepMode string) string {
	if stepMode != "" {
		return stepMode
	}
	if jobMode == "" {
		return "remote"
	}
	return jobMode
}

// findJob finds a job by key (or name if key empty) in pipeline
func (e *Executor) findJob(pipeline *types.Pipeline, keyOrName string) (*types.Job, error) {
	for i, job := range pipeline.Jobs {
		// First try to match by key
		if job.Key != "" && job.Key == keyOrName {
			return &pipeline.Jobs[i], nil
		}
		// Fallback to name match
		if job.Name == keyOrName {
			return &pipeline.Jobs[i], nil
		}
	}
	return nil, fmt.Errorf("job '%s' not found", keyOrName)
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

		// For local jobs with no hosts, run steps once with empty config
		if len(configs) == 0 && getEffectiveMode(job.Mode, step.Mode) == "local" {
			stepAction, stepTarget, err := e.runStep(step, job, nil, vars)
			if err != nil {
				return err
			}

			// Handle conditional actions
			if stepAction == "goto_step" && stepTarget != "" {
				action = stepAction
				targetStep = stepTarget
			} else if stepAction == "goto_job" && stepTarget != "" {
				action = stepAction
				targetStep = stepTarget
			} else if stepAction == "drop" {
				action = stepAction
			}
		} else {
			// Normal execution for remote jobs or jobs with hosts
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
				e.hasError = true
				e.forceLogging = true // Force all logging when error occurs
				// Create log file immediately when error occurs
				if e.logFile == nil && (e.hasError || e.shouldCreateLogFile()) {
					e.createLogFile()
				}
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
	// Determine effective debug for this step using precedence:
	// CLI/Execution (e.debugMode already set in Execute) > Job.Debug > Step.Debug
	prevDebug := e.debugMode
	if !prevDebug {
		if job != nil && job.Debug != nil {
			e.debugMode = *job.Debug
		} else if step != nil && step.Debug != nil {
			e.debugMode = *step.Debug
		} else {
			e.debugMode = false
		}
	}
	// Restore previous debug mode after step completes
	defer func() {
		e.debugMode = prevDebug
	}()

	switch step.Type {
	case "file_transfer":
		err := e.runFileTransferStep(step, job, config, vars)
		return "", "", err
	case "script":
		err := e.runScriptStep(step, job, config, vars)
		return "", "", err
	case "write_file":
		err := e.runWriteFileStep(step, job, config, vars)
		return "", "", err
	default: // "command" or empty
		return e.runCommandStep(step, job, config, vars)
	}
}

// runRsyncStep executes an rsync_file step. It builds per-entry rsync args using the
// pure helper, runs rsync (local or remote via -e ssh -F .sync_temp/.ssh/config),
// captures stdout/stderr, enforces optional timeouts, and when step.SaveOutput is
// set it appends --stats, parses the output and saves structured JSON into
// e.pipeline.ContextVariables[step.SaveOutput].
