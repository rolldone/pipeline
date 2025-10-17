package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"pipeline/internal/config"
	"pipeline/internal/pipeline/executor"
	"pipeline/internal/pipeline/parser"
	"pipeline/internal/pipeline/types"
	"pipeline/internal/util"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var (
	configFile string
	rootCmd    = &cobra.Command{
		Use:   "pipeline",
		Short: "Pipeline execution tool",
		Long:  `Standalone pipeline execution tool`,
		Run: func(cmd *cobra.Command, args []string) {
			showPipelineMenu()
		},
	}
)

func init() {
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "Config file path")
	rootCmd.AddCommand(newPipelineRunCmd())
	rootCmd.AddCommand(newPipelineListCmd())
	rootCmd.AddCommand(newPipelineCreateCmd())
	rootCmd.AddCommand(newPipelineInitCmd())
	rootCmd.AddCommand(newPipelineInfoCmd())
	rootCmd.AddCommand(newPipelineMenuCmd())
}

func Execute() error {
	return rootCmd.Execute()
}

// newPipelineRunCmd creates the pipeline run subcommand
func newPipelineRunCmd() *cobra.Command {
	var varOverrides map[string]string

	cmd := &cobra.Command{
		Use:   "run [execution_key]",
		Short: "Run a pipeline execution",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			executionKey := args[0]

			// Load config
			var cfg *config.Config
			var err error
			if configFile != "" {
				cfg, err = config.LoadAndRenderConfigWithPath(configFile)
			} else {
				cfg, err = config.LoadAndRenderConfigForPipeline()
			}
			if err != nil {
				fmt.Printf("❌ Failed to load config: %v\n", err)
				os.Exit(1)
			}

			// Find execution
			var execution *types.Execution
			for i, exec := range cfg.DirectAccess.Executions {
				if exec.Key == executionKey {
					execution = &cfg.DirectAccess.Executions[i]
					break
				}
			}
			if execution == nil {
				fmt.Printf("❌ Execution '%s' not found\n", executionKey)
				os.Exit(1)
			}

			// Load pipeline
			pipelinePath := filepath.Join(cfg.DirectAccess.PipelineDir, execution.Pipeline)
			pipeline, err := parser.ParsePipeline(pipelinePath)
			if err != nil {
				fmt.Printf("❌ Failed to parse pipeline: %v\n", err)
				os.Exit(1)
			}

			// Load vars with priority system:
			// 1. Start with empty vars
			vars := make(types.Vars)

			// 2. Load from vars.yaml if execution.Var is specified (lowest priority)
			if execution.Var != "" {
				varsPath := parser.ResolveVarsPath(cfg.DirectAccess.PipelineDir)
				fileVars, err := parser.ParseVarsSafe(varsPath, execution.Var)
				if err != nil {
					fmt.Printf("❌ Failed to parse vars: %v\n", err)
					os.Exit(1)
				}
				// Merge fileVars into vars
				for k, v := range fileVars {
					vars[k] = v
				}
			}

			// 3. Merge execution.Variables (higher priority than vars.yaml)
			if execution.Variables != nil {
				for k, v := range execution.Variables {
					vars[k] = v
				}
			}

			// 4. Apply CLI overrides (highest priority)
			for k, v := range varOverrides {
				vars[k] = v
			}

			// Execute
			executor := executor.NewExecutor()
			if err := executor.Execute(pipeline, execution, vars, execution.Hosts, cfg); err != nil {
				fmt.Printf("❌ Execution failed: %v\n", err)
				os.Exit(1)
			}

			fmt.Println("✅ Pipeline executed successfully")
		},
	}

	cmd.Flags().StringToStringVar(&varOverrides, "var", nil, "Override variables (key=value)")

	return cmd
}

// newPipelineListCmd creates the pipeline list subcommand
func newPipelineListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available pipeline executions",
		Run: func(cmd *cobra.Command, args []string) {
			var cfg *config.Config
			var err error
			if configFile != "" {
				cfg, err = config.LoadAndRenderConfigWithPath(configFile)
			} else {
				cfg, err = config.LoadAndRenderConfigForPipeline()
			}
			if err != nil {
				fmt.Printf("❌ Failed to load config: %v\n", err)
				os.Exit(1)
			}

			fmt.Println("Available Executions:")
			for _, exec := range cfg.DirectAccess.Executions {
				fmt.Printf("- %s (%s): %s\n", exec.Name, exec.Key, exec.Pipeline)
			}
		},
	}

	return cmd
}

// newPipelineCreateCmd creates the pipeline create subcommand
func newPipelineCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a new pipeline template",
		Long:  `Create a new pipeline YAML file with a Docker-focused template for CI/CD workflows.`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]

			// Validate pipeline name - reject names that would conflict with system files
			reservedNames := []string{"vars", "scripts"}
			for _, reserved := range reservedNames {
				if name == reserved {
					fmt.Printf("❌ Pipeline name '%s' is not allowed as it would conflict with system files\n", name)
					os.Exit(1)
				}
			}

			filename := name + ".yaml"

			// Load config to get pipeline_dir
			var cfg *config.Config
			var err error
			if configFile != "" {
				cfg, err = config.LoadAndRenderConfigWithPath(configFile)
			} else {
				cfg, err = config.LoadAndRenderConfigForPipeline()
			}
			if err != nil {
				fmt.Printf("❌ Failed to load config: %v\n", err)
				os.Exit(1)
			}

			// Determine where to save the pipeline file
			var outputPath string
			if cfg.DirectAccess.PipelineDir != "" {
				// Use configured pipeline directory
				outputPath = filepath.Join(cfg.DirectAccess.PipelineDir, filename)
				// Ensure pipeline directory exists
				if err := os.MkdirAll(cfg.DirectAccess.PipelineDir, 0755); err != nil {
					util.Default.Printf("❌ Failed to create pipeline directory: %v\n", err)
					util.Default.ClearLine()
					os.Exit(1)
				}
			} else {
				// Fallback to current working directory
				outputPath = filename
			}

			// Check if file already exists
			if _, err := os.Stat(outputPath); err == nil {
				util.Default.Printf("❌ Pipeline file '%s' already exists\n", outputPath)
				util.Default.ClearLine()
				util.Default.Printf("💡 Use a different name or remove the existing file if you want to recreate it\n")
				util.Default.ClearLine()
				os.Exit(1)
			}

			// Get template
			template := getDockerPipelineTemplate(name)

			// Write to file
			if err := os.WriteFile(outputPath, []byte(template), 0644); err != nil {
				util.Default.Printf("❌ Failed to create pipeline file: %v\n", err)
				util.Default.ClearLine()
				os.Exit(1)
			}

			util.Default.Printf("✅ Created pipeline template: %s\n", outputPath)
			util.Default.ClearLine()
			util.Default.Println("📝 Edit the file to customize your pipeline configuration")
			util.Default.ClearLine()
		},
	}

	return cmd
}

// newPipelineInitCmd creates the pipeline init subcommand
func newPipelineInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize default pipeline configuration",
		Long: `Generate a default pipeline.yaml config file in the current directory.
This creates the default configuration file that pipeline commands will use
when no --config flag is specified.`,
		Run: func(cmd *cobra.Command, args []string) {
			runPipelineInit()
		},
	}

	return cmd
}

func runPipelineInit() {
	cwd, _ := os.Getwd()
	fmt.Printf("📂 Current directory: %s\n", cwd)

	// Check if pipeline.yaml already exists
	if _, err := os.Stat("pipeline.yaml"); err == nil {
		fmt.Printf("❌ pipeline.yaml already exists in current directory\n")
		fmt.Printf("💡 Use a different directory or remove the existing file if you want to recreate it\n")
		return
	}

	// Find pipeline-sample.yaml - check current directory first, then executable directory
	var sampleFile string
	var foundSample bool

	// Check current directory first
	if _, err := os.Stat("pipeline-sample.yaml"); err == nil {
		sampleFile = "pipeline-sample.yaml"
		foundSample = true
	} else {
		// Get executable directory and look for pipeline-sample.yaml there
		if exePath, err := os.Executable(); err == nil {
			exeDir := filepath.Dir(exePath)
			projectSample := filepath.Join(exeDir, "pipeline-sample.yaml")
			if _, err := os.Stat(projectSample); err == nil {
				sampleFile = projectSample
				foundSample = true
			}
		}
	}

	if !foundSample {
		fmt.Printf("❌ Error: pipeline-sample.yaml not found in executable directory\n")
		fmt.Printf("📝 Please ensure pipeline-sample.yaml exists alongside the pipeline binary\n")
		return
	}

	fmt.Printf("📄 Using pipeline-sample.yaml as template from: %s\n", sampleFile)

	// Read template file
	data, err := os.ReadFile(sampleFile)
	if err != nil {
		fmt.Printf("❌ Error reading pipeline-sample.yaml: %v\n", err)
		return
	}

	// Write pipeline.yaml directly from sample file (avoid parsing through Config struct which has extra fields)
	if err := os.WriteFile("pipeline.yaml", data, 0644); err != nil {
		fmt.Printf("❌ Error writing pipeline.yaml: %v\n", err)
		return
	}

	fmt.Printf("✅ Created pipeline.yaml\n")

	// Create pipelines directory
	pipelinesDir := "pipelines"
	if err := os.MkdirAll(pipelinesDir, 0755); err != nil {
		fmt.Printf("⚠️  Warning: Failed to create pipelines directory: %v\n", err)
	} else {
		fmt.Printf("✅ Created pipelines directory: %s\n", pipelinesDir)
	}

	// Show usage instructions
	fmt.Printf("\n💡 Pipeline initialized! You can now:\n")
	fmt.Printf("   - Create pipeline files in the ./pipelines/ directory\n")
	fmt.Printf("   - Use 'pipeline create <name>' to generate pipeline templates\n")
	fmt.Printf("   - Use 'pipeline list' to see available executions\n")
	fmt.Printf("   - Use 'pipeline run <key>' to execute pipelines\n")
}

// showPipelineMenu displays an interactive menu for pipeline operations
func showPipelineMenu() {
	for {
		// Load config for menu options
		cfg, err := config.LoadAndRenderConfigForPipeline()
		if err != nil {
			fmt.Printf("❌ Failed to load config: %v\n", err)
			fmt.Printf("💡 Run 'pipeline init' to create default configuration\n")
			return
		}

		// Create menu items from executions
		var items []string
		for _, exec := range cfg.DirectAccess.Executions {
			items = append(items, fmt.Sprintf("▶️  %s (%s)", exec.Name, exec.Key))
		}

		// Add SSH commands
		for _, sshCmd := range cfg.DirectAccess.SSHCommands {
			items = append(items, fmt.Sprintf("🔗 %s", sshCmd.AccessName))
		}

		// Add static menu items
		items = append(items, "📝 Create Pipeline")
		items = append(items, "📋 List Pipelines")
		items = append(items, "ℹ️  Show Info")
		items = append(items, "🔄 Reload Config")
		items = append(items, "🚪 Exit")

		prompt := promptui.Select{
			Label: "Select a pipeline option",
			Items: items,
			Size:  10,
		}

		_, result, err := prompt.Run()
		if err != nil {
			fmt.Printf("❌ Menu cancelled: %v\n", err)
			return
		}

		// Handle execution selection
		for _, exec := range cfg.DirectAccess.Executions {
			if fmt.Sprintf("▶️  %s (%s)", exec.Name, exec.Key) == result {
				fmt.Printf("🚀 Executing pipeline: %s\n", exec.Name)

				// Load pipeline
				pipelinePath := filepath.Join(cfg.DirectAccess.PipelineDir, exec.Pipeline)
				pipeline, err := parser.ParsePipeline(pipelinePath)
				if err != nil {
					fmt.Printf("❌ Failed to parse pipeline: %v\n", err)
					continue
				}

				// Load vars (simplified version)
				vars := make(types.Vars)
				if exec.Variables != nil {
					for k, v := range exec.Variables {
						vars[k] = v
					}
				}

				// Execute
				executor := executor.NewExecutor()
				if err := executor.Execute(pipeline, &exec, vars, exec.Hosts, cfg); err != nil {
					fmt.Printf("❌ Execution failed: %v\n", err)
				} else {
					fmt.Println("✅ Pipeline executed successfully")
				}

				fmt.Println("\nPress Enter to continue...")
				fmt.Scanln()
				break
			}
		}

		// Handle SSH command execution
		for _, sshCmd := range cfg.DirectAccess.SSHCommands {
			if fmt.Sprintf("🔗 %s", sshCmd.AccessName) == result {
				fmt.Printf("🔗 Executing SSH command: %s\n", sshCmd.AccessName)
				fmt.Printf("🔧 Command: %s\n", sshCmd.Command)

				// Parse SSH command to get host name
				hostName, err := parseSSHCommand(sshCmd.Command)
				if err != nil {
					fmt.Printf("❌ Error parsing SSH command: %v\n", err)
					fmt.Println("\nPress Enter to continue...")
					fmt.Scanln()
					break
				}

				fmt.Printf("🔍 SSH Host: %s\n", hostName)

				// Generate temporary SSH config
				err = generateSSHTempConfig(cfg, hostName)
				if err != nil {
					fmt.Printf("❌ Error generating SSH temp config: %v\n", err)
					fmt.Println("\nPress Enter to continue...")
					fmt.Scanln()
					break
				}

				// Execute the SSH command with custom config using -F option
				modifiedCommand := strings.Replace(sshCmd.Command, "ssh ", "ssh -F .sync_temp/.ssh/config ", 1)
				fmt.Printf("🔧 Modified command: %s\n", modifiedCommand)

				cmd := exec.Command("bash", "-c", modifiedCommand)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				cmd.Stdin = os.Stdin

				err = cmd.Run()
				if err != nil {
					fmt.Printf("❌ Error executing SSH command: %v\n", err)
				}

				fmt.Println("\nPress Enter to continue...")
				fmt.Scanln()
				break
			}
		}

		// Handle other options
		switch result {
		case "📝 Create Pipeline":
			fmt.Print("Enter pipeline name: ")
			var name string
			fmt.Scanln(&name)
			if name != "" {
				// Simulate pipeline create command
				fmt.Printf("Creating pipeline: %s\n", name)
				// Note: This would need to be implemented properly
				fmt.Printf("⚠️  Create functionality not implemented in menu yet\n")
			}
			fmt.Println("Press Enter to continue...")
			fmt.Scanln()

		case "📋 List Pipelines":
			fmt.Println("Available Executions:")
			for _, exec := range cfg.DirectAccess.Executions {
				fmt.Printf("- %s (%s): %s\n", exec.Name, exec.Key, exec.Pipeline)
			}
			fmt.Println("\nPress Enter to continue...")
			fmt.Scanln()

		case "ℹ️  Show Info":
			runPipelineInfo()
			fmt.Println("\nPress Enter to continue...")
			fmt.Scanln()

		case "🔄 Reload Config":
			fmt.Println("🔄 Reloading configuration...")
			// Config will be reloaded on next menu iteration
			fmt.Println("✅ Configuration reloaded")
			fmt.Println("Press Enter to continue...")
			fmt.Scanln()

		case "🚪 Exit":
			fmt.Println("👋 Goodbye!")
			return
		}
	}
}

// newPipelineInfoCmd creates the pipeline info subcommand
func newPipelineInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Display pipeline information for debugging",
		Long: `Display comprehensive pipeline information including:
- Current working directory
- Executable paths
- Configuration file status
- Pipeline directory and files
- Available executions

This command is useful for debugging pipeline configuration issues.`,
		Run: func(cmd *cobra.Command, args []string) {
			runPipelineInfo()
		},
	}

	return cmd
}

func runPipelineInfo() {
	fmt.Println("🔍 Pipeline Information")
	fmt.Println("=" + strings.Repeat("=", 22))
	fmt.Println()

	// Current working directory
	wd, err := os.Getwd()
	if err != nil {
		fmt.Printf("❌ Failed to get working directory: %v\n", wd)
		wd = "<unknown>"
	}
	fmt.Printf("📂 Current Working Directory: %s\n", wd)

	// Executable paths
	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("❌ Failed to get executable path: %v\n", err)
		exePath = "<unknown>"
	}
	fmt.Printf("🔧 Executable Path (original): %s\n", exePath)

	// Resolved executable path (symlinks)
	resolvedPath := exePath
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		resolvedPath = resolved
	}
	if resolvedPath != exePath {
		fmt.Printf("🔗 Executable Path (resolved): %s\n", resolvedPath)
		fmt.Printf("   └─ Symlink detected\n")
	} else {
		fmt.Printf("🔗 Executable Path (resolved): %s (no symlink)\n", resolvedPath)
	}

	// Development mode detection
	isDev := isDevelopmentMode(resolvedPath)
	fmt.Printf("🛠️  Development Mode: %v\n", isDev)

	fmt.Println()

	// Configuration info
	fmt.Println("⚙️  Configuration:")
	fmt.Println(strings.Repeat("-", 14))

	// Try to load config
	var cfg *config.Config
	var configPath string
	var configErr error

	if configFile != "" {
		configPath = configFile
		cfg, configErr = config.LoadAndRenderConfigWithPath(configFile)
	} else {
		configPath = "pipeline.yaml or make-sync.yaml (default)"
		cfg, configErr = config.LoadAndRenderConfigForPipeline()
	}

	if configErr != nil {
		fmt.Printf("❌ Failed to load config: %v\n", configErr)
		fmt.Printf("📄 Config file: %s\n", configPath)
	} else {
		fmt.Printf("✅ Config loaded successfully\n")
		fmt.Printf("📄 Config file: %s\n", configPath)

		// Pipeline directory
		if cfg.DirectAccess.PipelineDir != "" {
			fmt.Printf("📁 Pipeline Directory: %s\n", cfg.DirectAccess.PipelineDir)

			// Check if pipeline directory exists
			if _, err := os.Stat(cfg.DirectAccess.PipelineDir); err == nil {
				fmt.Printf("✅ Pipeline directory exists\n")

				// List pipeline files
				entries, err := os.ReadDir(cfg.DirectAccess.PipelineDir)
				if err == nil {
					var pipelineFiles []string
					for _, entry := range entries {
						if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
							pipelineFiles = append(pipelineFiles, entry.Name())
						}
					}
					if len(pipelineFiles) > 0 {
						fmt.Printf("📋 Pipeline files (%d found):\n", len(pipelineFiles))
						for _, file := range pipelineFiles {
							fmt.Printf("   - %s\n", file)
						}
					} else {
						fmt.Printf("📋 Pipeline files: none found\n")
					}
				}
			} else {
				fmt.Printf("❌ Pipeline directory NOT found\n")
			}
		} else {
			fmt.Printf("📁 Pipeline Directory: not configured\n")
		}

		// Available executions
		if len(cfg.DirectAccess.Executions) > 0 {
			fmt.Printf("🎯 Available Executions (%d found):\n", len(cfg.DirectAccess.Executions))
			for _, exec := range cfg.DirectAccess.Executions {
				fmt.Printf("   - %s (%s): %s\n", exec.Name, exec.Key, exec.Pipeline)
			}
		} else {
			fmt.Printf("🎯 Available Executions: none configured\n")
		}
	}

	fmt.Println()
	fmt.Println("💡 Tip: Use --config flag to specify a custom config file:")
	fmt.Printf("   ./pipeline --config custom.yaml info\n")
}

// isDevelopmentMode checks if the executable path indicates we're running via "go run"
func isDevelopmentMode(exePath string) bool {
	tempDir := os.TempDir()
	tempDir = filepath.Clean(tempDir)
	exePath = filepath.Clean(exePath)

	// Check if executable is in temp directory
	if strings.HasPrefix(exePath, tempDir) {
		return true
	}

	// Check for Go build cache
	homeDir, err := os.UserHomeDir()
	if err == nil {
		goBuildCache := filepath.Join(homeDir, ".cache", "go-build")
		goBuildCache = filepath.Clean(goBuildCache)
		if strings.HasPrefix(exePath, goBuildCache) {
			return true
		}
	}

	// Check for go-build in path
	if strings.Contains(exePath, "go-build") {
		return true
	}

	return false
}

// getDockerPipelineTemplate returns a Docker-focused pipeline template
func getDockerPipelineTemplate(name string) string {
	// Get project root using the same method as other commands
	projectRoot, err := util.GetProjectRoot()
	if err != nil {
		fmt.Printf("❌ Failed to detect project root: %v\n", err)
		os.Exit(1)
	}

	// Read template from project root
	templatePath := filepath.Join(projectRoot, "job-sample.yaml")
	templateBytes, err := os.ReadFile(templatePath)
	if err != nil {
		fmt.Printf("❌ Failed to read template file: %v\n", err)
		os.Exit(1)
	}

	template := string(templateBytes)

	// Replace placeholders
	template = strings.ReplaceAll(template, "{{PIPELINE_NAME}}", name)

	return template
}

// newPipelineMenuCmd creates the pipeline menu subcommand
func newPipelineMenuCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "menu",
		Short: "Interactive menu for pipeline operations",
		Long:  `Launch an interactive menu to select and execute pipeline operations.`,
		Run: func(cmd *cobra.Command, args []string) {
			showPipelineMenu()
		},
	}

	return cmd
}

// parseSSHCommand parses SSH command to extract host name
func parseSSHCommand(command string) (string, error) {
	// Simple parsing for "ssh [options] host [command]"
	parts := strings.Fields(command)
	if len(parts) < 2 || parts[0] != "ssh" {
		return "", fmt.Errorf("invalid SSH command format")
	}

	// Skip "ssh" and find the first non-option argument
	for i := 1; i < len(parts); i++ {
		part := parts[i]
		// Skip options (starting with -)
		if !strings.HasPrefix(part, "-") {
			return part, nil
		}
	}

	return "", fmt.Errorf("could not find host in SSH command")
}

// generateSSHTempConfig generates temporary SSH config folder (.sync_temp/.ssh/config)
func generateSSHTempConfig(cfg *config.Config, hostName string) error {
	syncTempDir := ".sync_temp"
	sshDir := filepath.Join(syncTempDir, ".ssh")
	configPath := filepath.Join(sshDir, "config")

	// Clean up old .sync_temp file if it exists (from previous version)
	if _, err := os.Stat(syncTempDir); err == nil {
		// Check if it's a file, not a directory
		if info, err := os.Stat(syncTempDir); err == nil && !info.IsDir() {
			fmt.Printf("🔄 Removing old .sync_temp file...\n")
			if err := os.Remove(syncTempDir); err != nil {
				return fmt.Errorf("error removing old .sync_temp file: %v", err)
			}
		}
	}

	// Create .sync_temp directory if it doesn't exist
	if err := os.MkdirAll(sshDir, 0755); err != nil {
		return fmt.Errorf("error creating .sync_temp directory: %v", err)
	}

	// Render template variables first so =host, =remotePath, etc. are concrete
	renderedCfg, rerr := config.RenderTemplateVariablesInMemory(cfg)
	if rerr != nil {
		return fmt.Errorf("error rendering template variables: %v", rerr)
	}

	// helper to quote values with spaces
	quoteIfNeeded := func(s string) string {
		if s == "" {
			return s
		}
		if strings.ContainsAny(s, " \t\"'") {
			// prefer double quotes; escape existing double quotes
			s = strings.ReplaceAll(s, "\"", "\\\"")
			return "\"" + s + "\""
		}
		return s
	}

	// Generate SSH config content for ALL entries (multi-host support)
	var configLines []string
	configLines = append(configLines, "# Temporary SSH config generated by pipeline")
	configLines = append(configLines, "")

	// Optionally: ensure the requested host exists, but still write all
	hasRequested := false
	for _, sc := range renderedCfg.DirectAccess.SSHConfigs {
		if host, ok := sc["Host"].(string); ok && host == hostName {
			hasRequested = true
			break
		}
	}
	if !hasRequested {
		return fmt.Errorf("no SSH config found for host: %s", hostName)
	}

	for idx, sc := range renderedCfg.DirectAccess.SSHConfigs {
		host, ok := sc["Host"].(string)
		if !ok || host == "" {
			continue
		}
		if idx > 0 {
			configLines = append(configLines, "")
		}
		configLines = append(configLines, fmt.Sprintf("Host %s", host))

		// Iterate over map and write non-empty values
		for key, val := range sc {
			// Skip Host field as it's already written above
			if key == "Host" {
				continue
			}

			if valStr := fmt.Sprintf("%v", val); valStr != "" {
				// Khusus untuk RemoteCommand: jangan quote agar tidak ada petik ganda
				if key == "RemoteCommand" {
					configLines = append(configLines, fmt.Sprintf("    %s %s", key, valStr))
				} else {
					configLines = append(configLines, fmt.Sprintf("    %s %s", key, quoteIfNeeded(valStr)))
				}
			}
		}
	}

	// Write to .sync_temp/.ssh/config file
	content := strings.Join(configLines, "\n") + "\n"
	err := os.WriteFile(configPath, []byte(content), 0644)
	if err != nil {
		return fmt.Errorf("error writing SSH temp config: %v", err)
	}

	fmt.Printf("✅ Generated SSH temp config with %d host entries: %s\n", len(renderedCfg.DirectAccess.SSHConfigs), configPath)
	return nil
}
