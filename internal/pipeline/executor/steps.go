package executor

import (
	"context"

	"database/sql"
	"encoding/json"
	"fmt"

	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pipeline/internal/pipeline/types"
	"pipeline/internal/util"

	_ "github.com/mattn/go-sqlite3"
)

// (rsync_file implementation removed - use `file_transfer` instead)

// runWriteFileStep implements the `write_file` step: render files (tolerant), upload them (upload-from-memory for rendered content),
// apply default perms when unspecified (0755), and save a simple summary to pipeline.ContextVariables[step.SaveOutput] when requested.
func (e *Executor) runWriteFileStep(step *types.Step, job *types.Job, config map[string]interface{}, vars types.Vars) error {
	// Expand file entries
	rawEntries, err := e.buildFileTransferEntries(step, vars)
	if err != nil {
		return err
	}

	var prepared []struct{ Source, Destination string }
	var cleanup func()
	var renderWarnings []map[string]string

	// For single file: handle directly without temporary staging
	if len(rawEntries) == 1 {
		// Use original source directly - no staging needed
		prepared = []struct{ Source, Destination string }{
			{Source: rawEntries[0].Source, Destination: rawEntries[0].Destination},
		}
		cleanup = func() {} // No cleanup needed
		renderWarnings = []map[string]string{}
	} else {
		// For multiple files: use staging as before
		prepared, cleanup, renderWarnings, err = e.prepareRenderAndStage(step, rawEntries, vars)
		if err != nil {
			return err
		}
	}

	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()

	// Setup SSH client for remote uploads (or use local mode)
	effectiveMode := getEffectiveMode(job.Mode, step.Mode)

	filesWritten := []string{}
	start := time.Now()

	if effectiveMode == "local" {
		// Local mode: copy staged/prepared files to destination
		for i, p := range prepared {
			// prepared.Source points to staged temp file if rendering occurred; otherwise original source
			src := p.Source
			dst := p.Destination
			// Ensure dest dir exists
			if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				return fmt.Errorf("failed to create destination dir: %v", err)
			}
			data, rerr := os.ReadFile(src)
			if rerr != nil {
				return fmt.Errorf("failed to read prepared file %s: %v", src, rerr)
			}

			// For single file: apply rendering if needed
			if len(rawEntries) == 1 && step.Template == "enabled" && isTextBytes(data) {
				content := string(data)
				data = []byte(e.interpolateString(content, vars))
			}

			// Determine perm from step.Files if present (use original source path)
			origSrc := rawEntries[i].Source
			permStr := e.getPermForEntry(step, origSrc, dst)
			perm := os.FileMode(0755)
			if permStr != "" {
				if v, perr := strconv.ParseUint(permStr, 8, 32); perr == nil {
					perm = os.FileMode(v)
				}
			}
			if werr := e.WriteFileFunc(dst, data, perm); werr != nil {
				return fmt.Errorf("failed to write local destination %s: %v", dst, werr)
			}
			filesWritten = append(filesWritten, dst)
		}
	} else {
		// Remote mode: use SSH client
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
		if password == "" {
			password, _ = config["_Password"].(string)
		}
		passphrase, _ := config["_Passphrase"].(string)
		proxyJump, _ := config["ProxyJump"].(string)

		e.ensureDefaults()
		client, err := e.NewSSHClient(user, privateKey, password, passphrase, host, port, proxyJump)
		if err != nil {
			return fmt.Errorf("failed to create SSH client: %v", err)
		}
		if err := client.Connect(); err != nil {
			return fmt.Errorf("failed to connect SSH client: %v", err)
		}
		defer client.Close()

		for i, p := range prepared {
			src := p.Source
			dst := p.Destination
			// If prepared.Source points to a temp staged file, read bytes and UploadBytes; else if not rendered, UploadFile
			data, rerr := os.ReadFile(src)
			if rerr != nil {
				return fmt.Errorf("failed to read file %s: %v", src, rerr)
			}

			// For single file: apply rendering if needed
			if len(rawEntries) == 1 && step.Template == "enabled" && isTextBytes(data) {
				content := string(data)
				data = []byte(e.interpolateString(content, vars))
			}

			// Determine perm (use original source path)
			origSrc := rawEntries[i].Source
			permStr := e.getPermForEntry(step, origSrc, dst)
			perm := os.FileMode(0755)
			if permStr != "" {
				if v, perr := strconv.ParseUint(permStr, 8, 32); perr == nil {
					perm = os.FileMode(v)
				}
			}

			if err := client.UploadBytes(data, dst, perm); err != nil {
				return fmt.Errorf("failed to upload %s: %v", dst, err)
			}
			filesWritten = append(filesWritten, dst)
		}
	}

	duration := int(time.Since(start).Seconds())

	// For single file: always save rendered content to variable (auto-generate variable name if not specified)
	if len(rawEntries) == 1 && e.pipeline != nil {
		var varName string
		if step.SaveOutput != "" {
			varName = step.SaveOutput
		} else {
			// Auto-generate variable name from step name
			varName = strings.ReplaceAll(step.Name, "-", "_")
			varName = strings.ReplaceAll(varName, " ", "_")
		}

		if err := e.saveRenderedContentToVariable(step, vars, rawEntries[0], varName); err != nil {
			return fmt.Errorf("failed to save rendered content to variable: %v", err)
		}

		// If SaveOutput is specified, also save via saveStepOutput for consistency
		if step.SaveOutput != "" {
			if content, exists := e.pipeline.ContextVariables[varName]; exists {
				e.saveStepOutput(step.SaveOutput, content)
			}
		}

		// If auto-generated, also save with step name as key
		if step.SaveOutput == "" {
			if content, exists := e.pipeline.ContextVariables[varName]; exists {
				e.pipeline.ContextVariables[step.Name] = content
			}
		}
	}

	// Save output summary if requested (only for multiple files)
	if step.SaveOutput != "" && e.pipeline != nil && len(rawEntries) > 1 {
		outMap := map[string]interface{}{
			"exit_code":        0,
			"reason":           "success",
			"duration_seconds": duration,
			"files_written":    filesWritten,
		}
		if len(renderWarnings) > 0 {
			outMap["render_warnings"] = renderWarnings
		}
		if b, jerr := json.Marshal(outMap); jerr == nil {
			e.saveStepOutput(step.SaveOutput, string(b))
		}
	}

	return nil
}

// runCommandStep runs a command step on a host with conditional and interactive support
func (e *Executor) runCommandStep(step *types.Step, job *types.Job, config map[string]interface{}, vars types.Vars) (string, string, error) {
	fmt.Printf("📋 Executing step: %s\n", step.Name)
	// Get commands with priority: commands > command
	commands := e.getStepCommands(step, vars)

	// Check if step is in local mode (default to job mode, then "remote" for backward compatibility)
	effectiveMode := getEffectiveMode(job.Mode, step.Mode)
	if effectiveMode == "local" {
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
	if password == "" {
		password, _ = config["_Password"].(string)
	}
	passphrase, _ := config["_Passphrase"].(string)
	proxyJump, _ := config["ProxyJump"].(string)

	// Create SSH client
	e.ensureDefaults()
	client, err := e.NewSSHClient(user, privateKey, password, passphrase, host, port, proxyJump)
	if err != nil {
		return "", "", fmt.Errorf("failed to create SSH client: %v", err)
	}
	if err := client.Connect(); err != nil {
		return "", "", fmt.Errorf("failed to connect SSH client: %v", err)
	}
	defer client.Close()

	// runCommandInteractive requires an SSHClient that supports PTY execution.
	// The factory returns an SSHClient interface; ensure it's non-nil.
	if client == nil {
		return "", "", fmt.Errorf("ssh client does not support interactive commands")
	}

	// Determine working directory
	workingDir := e.interpolateString(step.WorkingDir, vars)
	if workingDir == "" {
		// Use working_dir from vars if step doesn't specify one
		if wd, ok := vars["working_dir"].(string); ok {
			workingDir = wd
		}
	}

	// Determine timeouts. A value of 0 means unlimited for that timer.
	// We do NOT coerce to legacy defaults here so callers and step authors
	// can explicitly request unlimited behavior by setting 0.
	timeout := step.Timeout
	idleTimeout := step.IdleTimeout

	var lastOutput string
	for _, cmd := range commands {
		// Prepend cd command if working directory is specified
		fullCmd := cmd
		if workingDir != "" {
			fullCmd = fmt.Sprintf("cd %s && %s", workingDir, cmd)
		}

		// Respect step.Silent: avoid echoing the full command when the step is silent
		if !step.Silent {
			preview := fullCmd
			if idx := strings.IndexByte(preview, '\n'); idx != -1 {
				preview = preview[:idx]
			}
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			// If the SSH client supports an interactive PTY execution path,
			// the interactive runner will already print a header like
			// "Running interactively: ...". Avoid duplicating that header by
			// only printing the host header when the client does NOT implement
			// the SSHClient interface (i.e., non-interactive path).
			if client == nil {
				fmt.Printf("Running on %s: %s\n", host, preview)
			} else {
				if _, ok := client.(SSHClient); !ok {
					fmt.Printf("Running on %s: %s\n", host, preview)
				}
			}
		}

		// Run command with interactive support and timeout
		output, err := e.runCommandInteractive(client, fullCmd, step.Expect, vars, timeout, idleTimeout, step.Silent, step, job)
		if err != nil {
			// flush evidence into pipeline log before returning
			e.flushErrorEvidenceAll()
			return "", "", fmt.Errorf("command failed: %v", err)
		}

		lastOutput = output // Save for potential output saving

		// Save output to context variable if requested (BEFORE condition check)
		if step.SaveOutput != "" && e.pipeline != nil {
			e.saveStepOutput(step.SaveOutput, strings.TrimSpace(lastOutput))
		}

		// Check conditions on output
		action, targetStep, err := e.checkConditions(step, output, vars)
		if err != nil {
			return "", "", err
		}
		if action != "" {
			return action, targetStep, nil
		}
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
	effectiveMode := getEffectiveMode(job.Mode, step.Mode)
	if effectiveMode == "local" {
		// Interpolate properties for local single-transfer (props rendering only)
		step.Source = e.interpolateString(step.Source, vars)
		step.Destination = e.interpolateString(step.Destination, vars)
		// Delegate to local transfer which will handle filtering/delete_policy
		return e.runLocalFileTransfer(step, vars)
	}

	// Check if advanced features are requested (include/exclude/delete_policy)
	// Treat plural `Ignores` as an advanced feature trigger as well to match agent config
	hasAdvancedFeatures := len(step.Include) > 0 || len(step.Exclude) > 0 || len(step.Ignores) > 0 || step.DeletePolicy != ""

	if hasAdvancedFeatures {
		return e.runFileTransferWithAgent(step, job, config, vars)
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
	if password == "" {
		password, _ = config["_Password"].(string)
	}
	passphrase, _ := config["_Passphrase"].(string)
	proxyJump, _ := config["ProxyJump"].(string)

	// Create SSH client
	e.ensureDefaults()
	client, err := e.NewSSHClient(user, privateKey, password, passphrase, host, port, proxyJump)
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

		if direction == "download" {
			// Download from remote source to local destination
			fmt.Printf("Downloading %s:%s to %s\n", host, src, dst)
			if err := client.DownloadFile(dst, src); err != nil {
				return fmt.Errorf("failed to download file: %v", err)
			}
			continue
		}

		// Upload
		// Single-entry upload: properties (source/destination) already interpolated by buildFileTransferEntries
		// Content templating of file contents is intentionally disabled; only property interpolation is supported.
		fmt.Printf("Uploading %s to %s:%s\n", src, host, dst)
		if err := client.UploadFile(src, dst); err != nil {
			return fmt.Errorf("failed to upload file %s: %v", dst, err)
		}
	}
	return nil
}

// runFileTransferWithAgent handles file transfer with agent-based sync for advanced features
func (e *Executor) runFileTransferWithAgent(step *types.Step, job *types.Job, config map[string]interface{}, vars types.Vars) error {
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
	if password == "" {
		password, _ = config["_Password"].(string)
	}
	passphrase, _ := config["_Passphrase"].(string)
	proxyJump, _ := config["ProxyJump"].(string)

	// Create SSH client
	e.ensureDefaults()
	client, err := e.NewSSHClient(user, privateKey, password, passphrase, host, port, proxyJump)
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

	// Determine working directory for agent
	workingDir := e.interpolateString(step.WorkingDir, vars)
	if workingDir == "" {
		// Get user's home directory from SSH session
		homeDir, err := client.RunCommandWithOutput("echo $HOME")
		if err != nil {
			return fmt.Errorf("failed to get remote home directory: %v", err)
		}
		workingDir = strings.TrimSpace(homeDir)
		if workingDir == "" {
			workingDir = "~" // fallback
		}
	}

	// Determine remote OS (simplified - assume Linux unless specified)
	osTarget := "linux"

	// Try to find an existing local agent binary in common locations. We'll
	// still prefer to build from `sub_app/agent` when present (developer flow).
	localAgentCandidates := []string{
		"pipeline-agent",
		"./pipeline-agent",
		"../pipeline-agent",
		"../../pipeline-agent",
		filepath.Join("..", "..", "pipeline-agent"),
	}
	var localAgentBinary string
	for _, candidate := range localAgentCandidates {
		if _, err := os.Stat(candidate); err == nil {
			localAgentBinary = candidate
			break
		}
	}
	// If sub_app/agent exists under project root, force a build from source.
	projRoot := "."
	if pr, perr := util.GetProjectRoot(); perr == nil {
		projRoot = pr
	}
	agentSrc := filepath.Join(projRoot, "sub_app", "agent")
	var forcedBuiltAgent string
	var forcedBuiltNeedsCleanup bool
	if info, err := os.Stat(agentSrc); err == nil && info.IsDir() {
		// Attempt to build the agent from source. If build fails, exit so we can debug.
		buildOptsInner := BuildOptions{
			ProjectRoot: projRoot,
			TargetOS:    osTarget,
			SSHClient:   client,
		}
		bp, berr := e.BuildAgentForTarget(buildOptsInner)
		if berr != nil {
			fmt.Printf("❌ Agent build failed (from %s): %v\n", agentSrc, berr)
			fmt.Println("Exiting with status 1 for debugging as requested.")
			os.Exit(1)
		}
		forcedBuiltAgent = bp
		forcedBuiltNeedsCleanup = true
		fmt.Printf("✅ Agent built from %s: %s\n", agentSrc, forcedBuiltAgent)
	}

	// Determine remote sync temp directory
	var remoteSyncTemp string
	if strings.Contains(strings.ToLower(osTarget), "win") {
		remoteSyncTemp = "%TEMP%\\.sync_temp"
	} else {
		// For upload operations, use destination directory as base for .sync_temp to avoid conflicts
		if direction == "upload" {
			destDir := e.interpolateString(step.Destination, vars)
			if destDir != "" {
				remoteSyncTemp = filepath.Join(destDir, ".sync_temp")
			} else {
				remoteSyncTemp = filepath.Join(workingDir, ".sync_temp")
			}
		} else {
			// For download operations, use source directory as base for .sync_temp
			sourceDir := e.interpolateString(step.Source, vars)
			if sourceDir != "" {
				remoteSyncTemp = filepath.Join(sourceDir, ".sync_temp")
			} else {
				remoteSyncTemp = filepath.Join(workingDir, ".sync_temp")
			}
		}
	}

	fmt.Printf("🔧 Deploying agent for advanced file transfer (include/exclude/delete_policy)\n")

	// Build or reuse agent for target OS with cross-compilation
	// Prefer compiling from the detected project root so sub_app/agent is used when present
	buildOpts := BuildOptions{
		TargetOS:    osTarget,
		ProjectRoot: projRoot,
		SSHClient:   client, // Use the SSHClient interface directly
	}

	var builtAgentPath string
	var builtAgentNeedsCleanup bool

	// If we already built the agent from source above, use that result
	if forcedBuiltAgent != "" {
		builtAgentPath = forcedBuiltAgent
		builtAgentNeedsCleanup = forcedBuiltNeedsCleanup
		fmt.Printf("ℹ️  Using agent built from source: %s\n", builtAgentPath)
	} else if localAgentBinary != "" {
		builtAgentPath = localAgentBinary
		fmt.Printf("ℹ️  Using existing local agent binary: %s\n", builtAgentPath)
	} else {
		var err error
		builtAgentPath, err = e.BuildAgentForTarget(buildOpts)
		if err != nil {
			return fmt.Errorf("failed to build agent for target OS %s: %v", osTarget, err)
		}
		fmt.Printf("✅ Agent built successfully at: %s\n", builtAgentPath)
		builtAgentNeedsCleanup = true
	}

	// Build hash checker utility for target OS (optional)
	builtHashCheckerPath, err := e.BuildHashCheckerForTarget(buildOpts)
	if err != nil {
		return fmt.Errorf("failed to build hash checker for target OS %s: %v", osTarget, err)
	}
	fmt.Printf("✅ Hash checker built successfully at: %s\n", builtHashCheckerPath)

	// Deploy the built agent
	deployOpts := DeployOptions{
		OSTarget:  osTarget,
		Timeout:   30 * time.Second,
		Overwrite: false, // Default: skip upload if remote agent identity matches local
	}
	if err := e.DeployAgent(context.Background(), nil, client, builtAgentPath, remoteSyncTemp, deployOpts); err != nil {
		return fmt.Errorf("failed to deploy agent: %v", err)
	}

	// Clean up local built agent only if we created it here
	if builtAgentNeedsCleanup {
		defer os.Remove(builtAgentPath)
	}

	// Upload config
	// For upload operations, set working directory to destination directory (same as remoteSyncTemp base)
	// For download operations, set working directory to source directory
	configWorkingDir := workingDir
	if direction == "upload" {
		destDir := e.interpolateString(step.Destination, vars)
		if destDir != "" {
			configWorkingDir = destDir
		}
	} else {
		sourceDir := e.interpolateString(step.Source, vars)
		if sourceDir != "" {
			configWorkingDir = sourceDir
		}
	}
	// Use step-level ignores (preferred) or exclude patterns as ignores sent to agent.
	// This keeps ignore handling simple and driven from the pipeline step configuration.
	ignores := step.Ignores
	if len(ignores) == 0 {
		ignores = step.Exclude
	}
	if err := e.uploadConfigToRemote(client, remoteSyncTemp, osTarget, configWorkingDir, ignores); err != nil {
		return fmt.Errorf("failed to upload agent config: %v", err)
	}

	// Diagnostic: list remote .sync_temp and show config/agent presence to help debug indexing failures
	fmt.Printf("🔎 Debug: verifying remote .sync_temp contents and config/agent presence\n")
	// Build a small POSIX-friendly shell snippet that checks common locations for config.json and the agent binary
	diagCmd := fmt.Sprintf("for base in %s %s; do if [ -d \"$base\" ]; then echo '--- LIST:' $base; ls -la \"$base\" || true; fi; done; "+
		"for p in %s %s; do if [ -f \"$p\" ]; then echo '--- CAT:' $p; cat \"$p\"; fi; done",
		shellQuote(remoteSyncTemp), shellQuote(filepath.Join(remoteSyncTemp, ".sync_temp")),
		shellQuote(filepath.Join(remoteSyncTemp, "config.json")),
		shellQuote(filepath.Join(remoteSyncTemp, ".sync_temp", "config.json")))
	out, derr := client.RunCommandWithOutput(diagCmd)
	if derr != nil {
		fmt.Printf("⚠️  Diagnostic command failed: %v\n", derr)
	} else {
		fmt.Printf("🔎 Remote diagnostic output:\n%s\n", out)
	}

	// Run remote indexing
	// Do NOT bypass ignore here so the agent will respect ignores while
	// indexing and avoid storing ignored paths in the DB.
	bypassIgnore := false
	var prefixes []string
	if direction == "upload" {
		// For upload, we need to index the destination directory (remote)
		// to know what files already exist on remote
		destDir := e.interpolateString(step.Destination, vars)
		if destDir == "" {
			destDir = workingDir
		}
		prefixes = []string{destDir}
	} else {
		// For download, index the source directory (remote)
		sourceDir := e.interpolateString(step.Source, vars)
		if sourceDir == "" {
			sourceDir = workingDir
		}
		prefixes = []string{sourceDir}
	}

	if _, err := e.RemoteRunAgentIndexing(client, remoteSyncTemp, osTarget, bypassIgnore, prefixes); err != nil {
		return fmt.Errorf("failed to run remote indexing: %v", err)
	}

	// Download the indexing DB
	localDBPath, err := e.downloadRemoteIndexDB(client, remoteSyncTemp, osTarget)
	if err != nil {
		return fmt.Errorf("failed to download remote index DB: %v", err)
	}
	defer os.Remove(localDBPath) // cleanup

	fmt.Printf("✅ Agent deployed and indexing complete\n")

	// Now perform the filtered transfer based on include/exclude patterns
	if direction == "upload" {
		return e.performAgentUploadWithFilter(step, vars, client, localDBPath, remoteSyncTemp, osTarget)
	} else {
		return e.performAgentDownloadWithFilter(step, vars, client, localDBPath, remoteSyncTemp, osTarget)
	}
}

// downloadRemoteIndexDB downloads the indexing_files.db from remote .sync_temp
func (e *Executor) downloadRemoteIndexDB(client interface{}, remoteSyncTemp, osTarget string) (string, error) {
	// Determine remote DB path
	var remoteDBPath string
	if strings.Contains(strings.ToLower(osTarget), "win") {
		base := strings.ReplaceAll(remoteSyncTemp, "/", "\\")
		if strings.HasSuffix(base, ".sync_temp") {
			remoteDBPath = base + "\\indexing_files.db"
		} else {
			remoteDBPath = base + "\\.sync_temp\\indexing_files.db"
		}
	} else {
		base := remoteSyncTemp
		if strings.HasSuffix(base, ".sync_temp") {
			remoteDBPath = filepath.Join(base, "indexing_files.db")
		} else {
			remoteDBPath = filepath.Join(base, ".sync_temp", "indexing_files.db")
		}
	}

	// Create local temp file
	tempFile, err := os.CreateTemp("", "remote-index-*.db")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %v", err)
	}
	tempFile.Close()

	// Download DB: via SSH client or local copy
	if client != nil {
		if sshCli, ok := client.(SSHClient); ok {
			if err := sshCli.DownloadFile(tempFile.Name(), remoteDBPath); err != nil {
				os.Remove(tempFile.Name())
				return "", fmt.Errorf("failed to download remote DB: %v", err)
			}
		} else {
			// attempt local copy
			data, rerr := os.ReadFile(remoteDBPath)
			if rerr != nil {
				os.Remove(tempFile.Name())
				return "", fmt.Errorf("failed to read local remote DB: %v", rerr)
			}
			if werr := os.WriteFile(tempFile.Name(), data, 0644); werr != nil {
				os.Remove(tempFile.Name())
				return "", fmt.Errorf("failed to write local DB temp file: %v", werr)
			}
		}
	} else {
		// client nil -> local copy
		data, rerr := os.ReadFile(remoteDBPath)
		if rerr != nil {
			os.Remove(tempFile.Name())
			return "", fmt.Errorf("failed to read local remote DB: %v", rerr)
		}
		if werr := os.WriteFile(tempFile.Name(), data, 0644); werr != nil {
			os.Remove(tempFile.Name())
			return "", fmt.Errorf("failed to write local DB temp file: %v", werr)
		}
	}

	return tempFile.Name(), nil
}

// performAgentUploadWithFilter performs upload with include/exclude filtering
func (e *Executor) performAgentUploadWithFilter(step *types.Step, vars types.Vars, client SSHClient, localDBPath, remoteSyncTemp, osTarget string) error {
	sourceDir := e.interpolateString(step.Source, vars)
	if sourceDir == "" {
		return fmt.Errorf("source directory required for upload")
	}

	destDir := e.interpolateString(step.Destination, vars)
	if destDir == "" {
		return fmt.Errorf("destination directory required for upload")
	}

	fmt.Printf("⬆️  Agent-based upload: %s -> %s (with filtering)\n", sourceDir, destDir)

	// Load remote index DB
	remoteByRel := make(map[string]struct {
		Path  string
		Rel   string
		Size  int64
		Mod   int64
		Hash  string
		IsDir bool
	})

	db, err := sql.Open("sqlite", localDBPath)
	if err != nil {
		return fmt.Errorf("failed to open remote DB: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT path, rel, size, mod_time, hash, is_dir FROM files`)
	if err != nil {
		return fmt.Errorf("failed to query remote DB: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var pathStr, relStr, hashStr string
		var sizeInt, modNano int64
		var isDirInt int
		if err := rows.Scan(&pathStr, &relStr, &sizeInt, &modNano, &hashStr, &isDirInt); err != nil {
			continue
		}
		key := filepath.ToSlash(relStr)
		remoteByRel[key] = struct {
			Path  string
			Rel   string
			Size  int64
			Mod   int64
			Hash  string
			IsDir bool
		}{Path: pathStr, Rel: relStr, Size: sizeInt, Mod: modNano, Hash: hashStr, IsDir: isDirInt != 0}
	}

	// Check if source is a single file or directory
	sourceInfo, err := os.Stat(sourceDir)
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %v", sourceDir, err)
	}

	var filesToProcess []string
	if sourceInfo.IsDir() {
		// Walk directory and collect files
		err = filepath.WalkDir(sourceDir, func(localPath string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if localPath == sourceDir {
				return nil
			}
			if d.IsDir() {
				return nil // Skip directories for now
			}
			filesToProcess = append(filesToProcess, localPath)
			return nil
		})
		if err != nil {
			return fmt.Errorf("walk error: %v", err)
		}
	} else {
		// Single file
		filesToProcess = []string{sourceDir}
	}

	// Process files with filtering
	var uploaded []string
	var examined, skippedExcluded, skippedUpToDate int

	for _, localPath := range filesToProcess {
		// Get relative path from source
		var rel string
		if sourceInfo.IsDir() {
			rel, err = filepath.Rel(sourceDir, localPath)
			if err != nil {
				continue
			}
		} else {
			// For single file, use just the basename
			rel = filepath.Base(localPath)
		}
		rel = filepath.ToSlash(rel)

		// Apply include/exclude / ignore filtering
		// Prefer step.Ignores (gitignore-style) when present; otherwise
		// fall back to legacy include/exclude matching.
		ignores := step.Ignores
		if len(ignores) == 0 {
			ignores = step.Exclude
		}
		if len(ignores) > 0 {
			// matchesIgnore returns true when the path should be INCLUDED
			if !e.matchesIgnore(rel, ignores) {
				skippedExcluded++
				continue
			}
			// If include patterns also exist, still enforce them
			if len(step.Include) > 0 {
				if !e.matchesIncludeExclude(rel, step.Include, nil) {
					skippedExcluded++
					continue
				}
			}
		} else {
			// No ignores provided; use legacy include/exclude behavior
			if !e.matchesIncludeExclude(rel, step.Include, step.Exclude) {
				skippedExcluded++
				continue
			}
		}

		examined++

		// Compute local hash using xxHash to match agent identity
		localHash := ""
		if h, herr := computeXXHash(localPath); herr == nil {
			localHash = h
		}

		// Build remote path
		remotePath := filepath.Join(destDir, rel)
		if strings.Contains(strings.ToLower(osTarget), "win") {
			remotePath = strings.ReplaceAll(remotePath, "/", "\\")
		} else {
			remotePath = filepath.ToSlash(remotePath)
		}

		// Check if remote file exists and is up to date
		rm, exists := remoteByRel[rel]
		needUpload := false
		if !exists {
			needUpload = true
		} else if strings.TrimSpace(rm.Hash) == "" || rm.Hash != localHash {
			needUpload = true
		}

		if needUpload {
			fmt.Printf("⬆️  Uploading %s -> %s\n", localPath, remotePath)
			if err := client.SyncFile(localPath, remotePath); err != nil {
				return fmt.Errorf("failed to upload %s: %v", localPath, err)
			}
			uploaded = append(uploaded, localPath)
		} else {
			skippedUpToDate++
		}
	}

	fmt.Printf("📊 Upload complete: examined %d, uploaded %d, skipped(excluded) %d, skipped(up-to-date) %d\n",
		examined, len(uploaded), skippedExcluded, skippedUpToDate)

	// Handle delete policy if specified
	if step.DeletePolicy != "" {
		if err := e.handleDeletePolicy(client, step, vars, remoteByRel, destDir, osTarget); err != nil {
			return fmt.Errorf("failed to handle delete policy: %v", err)
		}
	}

	return nil
}

// performAgentDownloadWithFilter performs download with include/exclude filtering
func (e *Executor) performAgentDownloadWithFilter(step *types.Step, vars types.Vars, client SSHClient, localDBPath, remoteSyncTemp, osTarget string) error {
	sourceDir := e.interpolateString(step.Source, vars)
	if sourceDir == "" {
		return fmt.Errorf("source directory required for download")
	}

	destDir := e.interpolateString(step.Destination, vars)
	if destDir == "" {
		return fmt.Errorf("destination directory required for download")
	}

	fmt.Printf("⬇️  Agent-based download: %s -> %s (with filtering)\n", sourceDir, destDir)

	// Load remote index DB
	remoteByRel := make(map[string]struct {
		Path  string
		Rel   string
		Size  int64
		Mod   int64
		Hash  string
		IsDir bool
	})

	db, err := sql.Open("sqlite", localDBPath)
	if err != nil {
		return fmt.Errorf("failed to open remote DB: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT path, rel, size, mod_time, hash, is_dir FROM files`)
	if err != nil {
		return fmt.Errorf("failed to query remote DB: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var pathStr, relStr, hashStr string
		var sizeInt, modNano int64
		var isDirInt int
		if err := rows.Scan(&pathStr, &relStr, &sizeInt, &modNano, &hashStr, &isDirInt); err != nil {
			continue
		}
		key := filepath.ToSlash(relStr)
		remoteByRel[key] = struct {
			Path  string
			Rel   string
			Size  int64
			Mod   int64
			Hash  string
			IsDir bool
		}{Path: pathStr, Rel: relStr, Size: sizeInt, Mod: modNano, Hash: hashStr, IsDir: isDirInt != 0}
	}

	// Download files with filtering
	var downloaded []string
	var examined, skippedExcluded int

	for rel, rm := range remoteByRel {
		// For download, adjust rel path to be relative to source directory
		sourceDir := e.interpolateString(step.Source, vars)
		adjustedRel, err := filepath.Rel(sourceDir, rm.Path)
		if err != nil {
			fmt.Printf("⚠️  Failed to adjust rel path for %s: %v\n", rm.Path, err)
			adjustedRel = rel // fallback to original
		} else {
			adjustedRel = filepath.ToSlash(adjustedRel)
		}

		// Apply include/exclude / ignore filtering using adjusted rel path.
		// Prefer step.Ignores (gitignore-style) when present; otherwise
		// fall back to legacy include/exclude matching.
		ignores := step.Ignores
		if len(ignores) == 0 {
			ignores = step.Exclude
		}
		if len(ignores) > 0 {
			// matchesIgnore returns true when the path should be INCLUDED
			if !e.matchesIgnore(adjustedRel, ignores) {
				skippedExcluded++
				continue
			}
			// If include patterns also exist, still enforce them
			if len(step.Include) > 0 {
				if !e.matchesIncludeExclude(adjustedRel, step.Include, nil) {
					skippedExcluded++
					continue
				}
			}
		} else {
			// No ignores provided; use legacy include/exclude behavior
			if !e.matchesIncludeExclude(adjustedRel, step.Include, step.Exclude) {
				skippedExcluded++
				continue
			}
		}

		examined++

		// Build local path using adjusted rel path
		localPath := filepath.Join(destDir, adjustedRel)

		// If this entry is a directory, ensure local directory exists and skip attempting to SCP it
		if rm.IsDir {
			if err := os.MkdirAll(localPath, 0755); err != nil {
				return fmt.Errorf("failed to create local directory: %v", err)
			}
			// skip downloading directories directly
			continue
		}

		// Check if local file exists and is up to date (use xxHash)
		localHash := ""
		if h, herr := computeXXHash(localPath); herr == nil {
			localHash = h
		}

		needDownload := false
		if localHash == "" {
			needDownload = true
		} else if localHash != rm.Hash {
			needDownload = true
		}

		if needDownload {
			// Ensure local directory exists
			if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
				return fmt.Errorf("failed to create local directory: %v", err)
			}

			fmt.Printf("⬇️  Downloading %s -> %s\n", rm.Path, localPath)
			if err := client.DownloadFile(localPath, rm.Path); err != nil {
				return fmt.Errorf("failed to download %s: %v", rm.Path, err)
			}
			downloaded = append(downloaded, localPath)
		}
	}

	fmt.Printf("📊 Download complete: examined %d, downloaded %d, skipped(excluded) %d\n",
		examined, len(downloaded), skippedExcluded)

	// Handle delete policy for downloads: when force, remove local files that are
	// present in destination but not present in remote index (remoteByRel).
	if step.DeletePolicy == "force" {
		fmt.Printf("🗑️  Force delete policy (download): deleting local extra files in %s\n", destDir)
		// Walk local destination and remove files not in remoteByRel
		var deleted int
		_ = filepath.WalkDir(destDir, func(localPath string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if localPath == destDir {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(destDir, localPath)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			// skip .sync_temp
			if strings.HasPrefix(rel, ".sync_temp/") || rel == ".sync_temp" {
				return nil
			}
			// If this file is not present in remoteByRel, delete it
			if _, exists := remoteByRel[rel]; !exists {
				if err := os.Remove(localPath); err != nil {
					fmt.Printf("⚠️  Failed to delete local %s: %v\n", localPath, err)
				} else {
					deleted++
				}
			}
			return nil
		})
		fmt.Printf("🗑️  Deleted %d extra local files\n", deleted)
	}

	return nil
}

// AgentUploadTest is a small exported helper used by E2E to trigger an agent-based upload test.
func (e *Executor) AgentUploadTest(client SSHClient, localDBPath, remoteSyncTemp, osTarget, sourceDir, destDir, deletePolicy string) error {
	step := &types.Step{
		Source:       sourceDir,
		Destination:  destDir,
		Direction:    "upload",
		DeletePolicy: deletePolicy,
	}
	vars := types.Vars{}
	return e.performAgentUploadWithFilter(step, vars, client, localDBPath, remoteSyncTemp, osTarget)
}

// AgentDownloadTest is a small exported helper used by E2E to trigger an agent-based download test.
func (e *Executor) AgentDownloadTest(client SSHClient, localDBPath, remoteSyncTemp, osTarget, sourceDir, destDir string) error {
	step := &types.Step{
		Source:      sourceDir,
		Destination: destDir,
		Direction:   "download",
	}
	vars := types.Vars{}
	return e.performAgentDownloadWithFilter(step, vars, client, localDBPath, remoteSyncTemp, osTarget)
}

// AgentLocalTransferTest performs a purely local transfer from source to destination
// using the same include/exclude/delete_policy semantics as remote transfers.
func (e *Executor) AgentLocalTransferTest(sourceDir, destDir, deletePolicy string) error {
	step := &types.Step{
		Source:       sourceDir,
		Destination:  destDir,
		Direction:    "upload",
		DeletePolicy: deletePolicy,
	}
	vars := types.Vars{}
	// runLocalFileTransferWithFiltering handles local transfers
	return e.runLocalFileTransferWithFiltering(step, vars)
}

// handleDeletePolicy handles file deletion based on delete_policy
func (e *Executor) handleDeletePolicy(client SSHClient, step *types.Step, vars types.Vars, remoteByRel map[string]struct {
	Path  string
	Rel   string
	Size  int64
	Mod   int64
	Hash  string
	IsDir bool
}, destDir, osTarget string) error {

	if step.DeletePolicy == "soft" {
		fmt.Printf("🗑️  Soft delete policy: moving extra files to trash\n")
		// For soft delete, we would need to implement trash functionality
		// For now, just log that soft delete is not implemented
		fmt.Printf("⚠️  Soft delete not yet implemented, skipping\n")
		return nil
	} else if step.DeletePolicy == "force" {
		fmt.Printf("🗑️  Force delete policy: deleting extra files\n")

		// Get source directory info
		sourceDir := e.interpolateString(step.Source, vars)
		sourceInfo, err := os.Stat(sourceDir)
		if err != nil {
			return fmt.Errorf("failed to stat source %s: %v", sourceDir, err)
		}

		// Collect files from local source. Respect step.Ignores (gitignore-style)
		localSourceFiles := make(map[string]bool)
		if sourceInfo.IsDir() {
			err = filepath.WalkDir(sourceDir, func(localPath string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return nil
				}
				if localPath == sourceDir {
					return nil
				}
				if d.IsDir() {
					return nil // Skip directories for now
				}
				rel, rerr := filepath.Rel(sourceDir, localPath)
				if rerr != nil {
					return nil
				}
				rel = filepath.ToSlash(rel)

				// Determine inclusion using ignores (preferred) or legacy include/exclude
				if len(step.Ignores) > 0 {
					// matchesIgnore returns true when the path should be INCLUDED
					if !e.matchesIgnore(rel, step.Ignores) {
						return nil
					}
					// If include patterns also exist, still enforce them
					if len(step.Include) > 0 {
						if !e.matchesIncludeExclude(rel, step.Include, nil) {
							return nil
						}
					}
				} else {
					if !e.matchesIncludeExclude(rel, step.Include, step.Exclude) {
						return nil
					}
				}

				localSourceFiles[rel] = true
				return nil
			})
		} else {
			// Single file
			rel := filepath.Base(sourceDir)
			rel = filepath.ToSlash(rel)
			included := false
			if len(step.Ignores) > 0 {
				if e.matchesIgnore(rel, step.Ignores) {
					if len(step.Include) > 0 {
						if e.matchesIncludeExclude(rel, step.Include, nil) {
							included = true
						}
					} else {
						included = true
					}
				}
			} else {
				if e.matchesIncludeExclude(rel, step.Include, step.Exclude) {
					included = true
				}
			}
			if included {
				localSourceFiles[rel] = true
			}
		}

		if err != nil {
			return fmt.Errorf("failed to walk local source directory: %v", err)
		}

		// Find files to delete (in remote but not in local source)
		var toDelete []string
		for rel := range remoteByRel {
			// Always exclude .sync_temp directory by default
			if strings.HasPrefix(rel, ".sync_temp/") || rel == ".sync_temp" {
				continue
			}

			// Determine whether this rel would be included by the step filters
			included := false
			if len(step.Ignores) > 0 {
				if e.matchesIgnore(rel, step.Ignores) {
					if len(step.Include) > 0 {
						if e.matchesIncludeExclude(rel, step.Include, nil) {
							included = true
						}
					} else {
						included = true
					}
				}
			} else {
				if e.matchesIncludeExclude(rel, step.Include, step.Exclude) {
					included = true
				}
			}
			if !included {
				continue // skip files that wouldn't be included anyway
			}

			if _, exists := localSourceFiles[rel]; !exists {
				toDelete = append(toDelete, rel)
			}
		}

		// Delete extra files from remote
		for _, rel := range toDelete {
			remotePath := filepath.Join(destDir, rel)
			if strings.Contains(strings.ToLower(osTarget), "win") {
				remotePath = strings.ReplaceAll(remotePath, "/", "\\")
			} else {
				remotePath = filepath.ToSlash(remotePath)
			}
			fmt.Printf("🗑️  Deleting %s\n", remotePath)
			// Use SSH to delete the file
			var deleteCmd string
			if strings.Contains(strings.ToLower(osTarget), "win") {
				deleteCmd = fmt.Sprintf("del /Q \"%s\"", remotePath)
			} else {
				deleteCmd = fmt.Sprintf("rm -f '%s'", strings.ReplaceAll(remotePath, "'", "'\\''"))
			}
			if err := client.RunCommand(deleteCmd); err != nil {
				fmt.Printf("⚠️  Failed to delete %s: %v\n", remotePath, err)
			}
		}

		fmt.Printf("🗑️  Deleted %d extra files\n", len(toDelete))
	}

	return nil
}

// runLocalFileTransferWithFiltering handles local file transfer with include/exclude/delete_policy support
func (e *Executor) runLocalFileTransferWithFiltering(step *types.Step, vars types.Vars) error {
	source := step.Source
	if source == "" {
		return fmt.Errorf("source directory required for local file transfer")
	}

	destination := step.Destination
	if destination == "" {
		return fmt.Errorf("destination directory required for local file transfer")
	}

	direction := step.Direction
	if direction == "" {
		direction = "upload" // default for local transfers
	}

	fmt.Printf("📁 Local file transfer: %s -> %s (direction: %s)\n", source, destination, direction)

	// Get source directory info
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %v", source, err)
	}

	// Handle delete policy first (for force delete).
	// For local transfers, force delete should remove extra files in the destination
	// that are not present in the source, regardless of direction (upload/download).
	if step.DeletePolicy == "force" {
		if err := e.handleLocalDeletePolicy(step, vars, source, destination); err != nil {
			return fmt.Errorf("failed to handle local delete policy: %v", err)
		}
	}

	// Now perform the actual file transfer with filtering
	if sourceInfo.IsDir() {
		// Directory transfer
		return e.transferLocalDirectoryWithFiltering(source, destination, step)
	} else {
		// Single file transfer
		return e.transferLocalFileWithFiltering(source, destination, step)
	}
}

// handleLocalDeletePolicy handles file deletion for local transfers
func (e *Executor) handleLocalDeletePolicy(step *types.Step, vars types.Vars, sourceDir, destDir string) error {
	fmt.Printf("🗑️  Force delete policy: deleting extra files in %s\n", destDir)

	// Collect files from local source
	localSourceFiles := make(map[string]bool)
	sourceInfo, err := os.Stat(sourceDir)
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %v", sourceDir, err)
	}

	if sourceInfo.IsDir() {
		err = filepath.WalkDir(sourceDir, func(localPath string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if localPath == sourceDir {
				return nil
			}
			if d.IsDir() {
				return nil // Skip directories for now
			}
			rel, rerr := filepath.Rel(sourceDir, localPath)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			// Apply include/exclude filtering
			if !e.matchesIncludeExclude(rel, step.Include, step.Exclude) {
				return nil
			}
			localSourceFiles[rel] = true
			return nil
		})
	} else {
		// Single file
		rel := filepath.Base(sourceDir)
		rel = filepath.ToSlash(rel)
		// Apply include/exclude filtering
		if e.matchesIncludeExclude(rel, step.Include, step.Exclude) {
			localSourceFiles[rel] = true
		}
	}

	if err != nil {
		return fmt.Errorf("failed to walk local source directory: %v", err)
	}

	// Collect files from destination
	var toDelete []string
	err = filepath.WalkDir(destDir, func(destPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if destPath == destDir {
			return nil
		}
		if d.IsDir() {
			return nil // Skip directories for now
		}
		rel, rerr := filepath.Rel(destDir, destPath)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		// Apply include/exclude filtering
		if !e.matchesIncludeExclude(rel, step.Include, step.Exclude) {
			return nil
		}
		if _, exists := localSourceFiles[rel]; !exists {
			toDelete = append(toDelete, destPath)
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk destination directory: %v", err)
	}

	// Delete extra files from destination
	for _, filePath := range toDelete {
		fmt.Printf("🗑️  Deleting %s\n", filePath)
		if err := os.Remove(filePath); err != nil {
			fmt.Printf("⚠️  Failed to delete %s: %v\n", filePath, err)
		}
	}

	fmt.Printf("🗑️  Deleted %d extra files\n", len(toDelete))
	return nil
}

// transferLocalDirectoryWithFiltering transfers a directory with include/exclude filtering
func (e *Executor) transferLocalDirectoryWithFiltering(sourceDir, destDir string, step *types.Step) error {
	return filepath.WalkDir(sourceDir, func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip the root directory
		if srcPath == sourceDir {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(sourceDir, srcPath)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		// Apply include/exclude filtering
		if !e.matchesIncludeExclude(relPath, step.Include, step.Exclude) {
			return nil
		}

		// Build destination path
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			// Create directory
			return os.MkdirAll(destPath, 0755)
		} else {
			// Copy file
			return e.copyLocalPath(srcPath, destPath)
		}
	})
}

// transferLocalFileWithFiltering transfers a single file with include/exclude filtering
func (e *Executor) transferLocalFileWithFiltering(sourceFile, destDir string, step *types.Step) error {
	fileName := filepath.Base(sourceFile)
	fileName = filepath.ToSlash(fileName)

	// Apply include/exclude filtering
	if !e.matchesIncludeExclude(fileName, step.Include, step.Exclude) {
		fmt.Printf("⏭️  File %s excluded by filters\n", fileName)
		return nil
	}

	destPath := filepath.Join(destDir, fileName)
	return e.copyLocalPath(sourceFile, destPath)
}
