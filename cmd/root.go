package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"pipeline/internal/config"
	"pipeline/internal/pipeline/executor"
	"pipeline/internal/pipeline/parser"
	"pipeline/internal/pipeline/types"
	"pipeline/internal/util"
)

var (
	configFile string
	rootCmd    = &cobra.Command{
		Use:   "pipeline",
		Short: "Pipeline execution tool",
		Long:  `Standalone pipeline execution tool`,
	}
)

func init() {
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "Config file path")
	rootCmd.AddCommand(newPipelineRunCmd())
	rootCmd.AddCommand(newPipelineListCmd())
	rootCmd.AddCommand(newPipelineCreateCmd())
	rootCmd.AddCommand(newPipelineInitCmd())
	rootCmd.AddCommand(newPipelineInfoCmd())
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

	// Find pipeline-sample.yaml in executable directory
	var sampleFile string
	var foundSample bool

	// Get executable directory and look for pipeline-sample.yaml there
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		projectSample := filepath.Join(exeDir, "pipeline-sample.yaml")
		if _, err := os.Stat(projectSample); err == nil {
			sampleFile = projectSample
			foundSample = true
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

	// Parse and re-marshal to ensure clean YAML
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("❌ Error parsing pipeline-sample.yaml: %v\n", err)
		return
	}

	// Write pipeline.yaml
	outputData, err := yaml.Marshal(&cfg)
	if err != nil {
		fmt.Printf("❌ Error generating config: %v\n", err)
		return
	}

	if err := os.WriteFile("pipeline.yaml", outputData, 0644); err != nil {
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
