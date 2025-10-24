package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/rjeczalik/notify"

	"runtime"
	"sync-agent/internal/indexer"
	"sync-agent/internal/util"
)

// AgentConfig represents the agent configuration
type AgentConfig struct {
	Devsync struct {
		SizeLimit   int      `json:"size_limit"`
		AgentWatchs []string `json:"agent_watchs"`
		WorkingDir  string   `json:"working_dir"`
	} `json:"devsync"`
	Pipeline struct {
		Ignores     []string `json:"ignores,omitempty"`
		WorkingDir  string   `json:"working_dir,omitempty"`
		AgentWatchs []string `json:"agent_watchs,omitempty"`
		SizeLimit   int      `json:"size_limit,omitempty"`
		// manual_transfer kept as interface{} in other places; parse when needed
	} `json:"pipeline"`
}

// Global context for coordinating graceful shutdown
var (
	mainCtx      context.Context
	mainCancel   context.CancelFunc
	shutdownMu   sync.Mutex
	globalConfig *AgentConfig
)

// loadConfig loads configuration from .sync_temp/config.json in current dir or executable dir
func loadConfig() (*AgentConfig, error) {
	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		exeBase := filepath.Base(exeDir)
		var configPaths []string
		if exeBase == ".sync_temp" {
			// If agent is in .sync_temp, look for config.json in same dir
			configPaths = append(configPaths, filepath.Join(exeDir, "config.json"))
		} else {
			// Otherwise, look for .sync_temp/config.json in exeDir
			configPaths = append(configPaths, filepath.Join(exeDir, ".sync_temp", "config.json"))
		}
		// Also try current working directory for legacy support
		configPaths = append(configPaths, ".sync_temp/config.json")

		for _, configPath := range configPaths {
			util.Default.ClearLine()
			util.Default.Printf("🔍 Trying config: %s\n", configPath)
			if _, err := os.Stat(configPath); err == nil {
				data, err := os.ReadFile(configPath)
				if err != nil {
					return nil, fmt.Errorf("failed to read config file: %v", err)
				}
				var config AgentConfig
				if err := json.Unmarshal(data, &config); err != nil {
					return nil, fmt.Errorf("failed to parse config file: %v", err)
				}
				util.Default.ClearLine()
				util.Default.Printf("✅ Loaded config from: %s\n", configPath)
				return &config, nil
			}
		}
	}

	return nil, fmt.Errorf("config file .sync_temp/config.json not found in .sync_temp, executable dir, or current dir")
}

// loadConfigAndChangeDir loads config and changes working directory if specified
func loadConfigAndChangeDir() (*AgentConfig, error) {
	// Load configuration - REQUIRED, no fallback
	config, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("config is required for indexing: %v", err)
	}

	// Working directory is required from config. Prefer pipeline.working_dir, fallback to devsync.working_dir
	workingDir := ""
	if config.Pipeline.WorkingDir != "" {
		workingDir = config.Pipeline.WorkingDir
	} else {
		workingDir = config.Devsync.WorkingDir
	}
	if workingDir == "" {
		return nil, fmt.Errorf("working_dir must be specified in config for indexing")
	}
	util.Default.ClearLine()
	util.Default.Printf("🔧 Using working directory from config: %s\n", workingDir)
	util.Default.ClearLine()
	util.Default.Printf("🔧 DEBUG: workingDir = '%s'\n", workingDir)

	if err := os.Chdir(workingDir); err != nil {
		return nil, fmt.Errorf("failed to change to working directory '%s': %v", workingDir, err)
	}

	// Print actual working directory after chdir
	cwd, err := os.Getwd()
	if err == nil {
		util.Default.ClearLine()
		util.Default.Printf("📍 Current working directory: %s\n", cwd)
	}
	util.Default.ClearLine()
	util.Default.Printf("✅ Successfully changed to working directory: %s\n", workingDir)
	return config, nil
}

func displayConfig() {
	// Load configuration. If not present, don't exit — fall back to polling mode
	config, err := loadConfig()
	if err != nil {
		util.Default.ClearLine()
		util.Default.Printf("⚠️  Failed to load config: %v\n", err)
		fmt.Println("🔄 Falling back to polling for config in .sync_temp/config.json (agent will stay running)")
		// Initialize empty config so later logic will poll and wait for config to appear
		config = &AgentConfig{}
	}

	// Display configuration in JSON format
	fmt.Println("📋 Current Agent Configuration:")
	// Prefer pipeline values but show both for clarity
	watchPaths := config.Devsync.AgentWatchs
	if len(config.Pipeline.AgentWatchs) > 0 {
		watchPaths = config.Pipeline.AgentWatchs
	}
	util.Default.ClearLine()
	util.Default.Printf("👀 Watch Paths: %v\n", watchPaths)
	// determine working dir
	workingDir := config.Devsync.WorkingDir
	if config.Pipeline.WorkingDir != "" {
		workingDir = config.Pipeline.WorkingDir
	}
	util.Default.ClearLine()
	util.Default.Printf("� Working Directory: %s\n", workingDir)

	// Display current working directory
	if cwd, err := os.Getwd(); err == nil {
		util.Default.ClearLine()
		util.Default.Printf("📍 Current Working Directory: %s\n", cwd)
	}

	// Display config file location
	util.Default.ClearLine()
	util.Default.Printf("📄 Config File: .sync_temp/config.json\n")

	// Display raw config file content
	fmt.Println("\n📄 Raw Config Content:")
	data, err := os.ReadFile(".sync_temp/config.json")
	if err != nil {
		util.Default.ClearLine()
		util.Default.Printf("❌ Failed to read config file: %v\n", err)
	} else {
		fmt.Println(string(data))
	}
}

func main() {
	// Setup context for coordinated shutdown
	mainCtx, mainCancel = context.WithCancel(context.Background())
	defer mainCancel()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		util.Default.ClearLine()
		util.Default.Printf("🔔 Received signal: %v, initiating shutdown...\n", sig)
		gracefulShutdown()
	}()

	// On Windows, start parent watcher to auto-exit if parent dies
	if runtime.GOOS == "windows" {
		startParentWatcher()
	}

	// Check for command line arguments
	if len(os.Args) > 1 {
		command := os.Args[1]

		// Parse flags after command
		bypassOverride := false
		// manual-transfer parsing: support --manual-transfer [value]
		var manualPrefixes []string
		manualFlagPresent := false
		args := os.Args[2:]
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if arg == "--bypass-ignore" {
				bypassOverride = true
				continue
			}
			if arg == "--manual-transfer" || arg == "--manual_transfer" {
				manualFlagPresent = true
				// try to read a following value if present and not another flag
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					// support comma separated list
					vals := strings.Split(args[i+1], ",")
					for _, v := range vals {
						v = strings.TrimSpace(v)
						if v != "" {
							manualPrefixes = append(manualPrefixes, v)
						}
					}
					i++ // skip value
				}
				continue
			}
		}

		switch command {
		case "identity":
			printIdentity()
			return
		case "version":
			fmt.Println("Sync Agent v1.0.0")
			return
		case "config":
			displayConfig()
			return
		case "watch":
			startWatching()
			return
		case "indexing":
			// perform indexing now and write .sync_temp/indexing_files.db
			// Load config and change working directory first, then perform indexing
			config, err := loadConfigAndChangeDir()
			if err != nil {
				util.Default.ClearLine()
				util.Default.Printf("⚠️  Config setup failed: %v\n", err)
				fmt.Println("❌ Cannot proceed with indexing without proper config. Exiting.")
				os.Exit(1)
			}
			// Apply bypass override if specified
			if bypassOverride {
				util.Default.ClearLine()
				util.Default.Printf("🔄 Bypass ignore mode enabled via --bypass-ignore flag\n")
			}
			performIndexing(config, bypassOverride, manualFlagPresent, manualPrefixes)
			return
		case "help":
			fmt.Println("Sync Agent v1.0.0")
			fmt.Println("")
			fmt.Println("Commands:")
			fmt.Println("  identity     - Print agent identity information")
			fmt.Println("  version      - Print agent version")
			fmt.Println("  config       - Display current configuration")
			fmt.Println("  watch        - Start file watching mode")
			fmt.Println("  indexing     - Perform one-time indexing and exit")
			fmt.Println("  help         - Show this help message")
			fmt.Println("")
			fmt.Println("Flags:")
			fmt.Println("  --bypass-ignore  Bypass .sync_ignore patterns during indexing (temporary override)")
			fmt.Println("")
			fmt.Println("Examples:")
			fmt.Println("  ./pipeline-agent indexing                    # Index respecting ignore patterns")
			fmt.Println("  ./pipeline-agent indexing --bypass-ignore   # Index bypassing ignore patterns")
			return
		default:
			fmt.Printf("❌ Unknown command: %s\n", command)
			fmt.Println("Run './pipeline-agent help' for available commands.")
			os.Exit(1)
		}
	}
	util.Default.ClearLine()
	util.Default.Printf("📅 Started at: %s\n", time.Now().Format("2006-01-02 15:04:05"))

	startWatching()
}

// gracefulShutdown initiates coordinated shutdown
func gracefulShutdown() {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()

	fmt.Println("🔄 Initiating graceful shutdown...")
	mainCancel() // Cancel main context to signal all goroutines

	// Give goroutines a moment to clean up
	time.Sleep(100 * time.Millisecond)

	fmt.Println("✅ Agent shutdown complete")
	os.Exit(0)
}

func startWatching() {
	// Print PID immediately when starting watch mode
	fmt.Printf("AGENT_PID:%d\n", os.Getpid())

	// Load config and change working directory
	if _, err := loadConfigAndChangeDir(); err != nil {
		util.Default.ClearLine()
		util.Default.Printf("⚠️  Config setup failed: %v\n", err)
		os.Exit(1)
	}

	// Load configuration again for watch paths (after chdir)
	config, err := loadConfig()
	if err != nil {
		util.Default.ClearLine()
		util.Default.Printf("⚠️  Failed to load config: %v\n", err)
		fmt.Println("🔄 Falling back to watching current directory and polling for .sync_temp/config.json")
		// Initialize an empty config so we continue and poll for configuration
		config = &AgentConfig{}
	}

	// Store config globally for use in handleFileEvent
	globalConfig = config

	// Get current working directory after config loading
	workingDir, err := os.Getwd()
	if err != nil {
		util.Default.ClearLine()
		util.Default.Printf("⚠️  Failed to get current working directory: %v\n", err)
		workingDir = ""
	}

	// Resolve watch paths relative to working directory
	util.Default.ClearLine()
	util.Default.Printf("🔧 Working dir: '%s'\n", workingDir)
	// Resolve watch paths (prefer pipeline.agent_watchs)
	rawWatch := config.Devsync.AgentWatchs
	if len(config.Pipeline.AgentWatchs) > 0 {
		rawWatch = config.Pipeline.AgentWatchs
	}
	util.Default.ClearLine()
	util.Default.Printf("🔧 Raw watch paths: %v\n", rawWatch)

	watchPaths := make([]string, len(rawWatch))
	for i, watchPath := range rawWatch {
		util.Default.ClearLine()
		util.Default.Printf("🔍 Processing watch path: '%s'\n", watchPath)
		if workingDir != "" && !filepath.IsAbs(watchPath) {
			// Combine working directory with relative watch path
			resolvedPath := filepath.Join(workingDir, watchPath)
			watchPaths[i] = resolvedPath
			util.Default.ClearLine()
			util.Default.Printf("🔗 Resolved watch path: %s -> %s\n", watchPath, resolvedPath)
		} else {
			watchPaths[i] = watchPath
			util.Default.ClearLine()
			util.Default.Printf("📁 Using watch path as-is: %s\n", watchPath)
		}
	}

	util.Default.ClearLine()
	util.Default.Printf("📋 Final watch paths: %v\n", watchPaths)

	if len(watchPaths) == 0 {
		fmt.Println("⚠️  No agent_watchs configured — agent will remain running and poll for config changes")
		// Keep agent running and poll .sync_temp/config.json until watch paths are provided
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-mainCtx.Done():
					fmt.Println("🔄 Config polling stopped (context cancelled)")
					return
				case <-ticker.C:
					cfg, err := loadConfig()
					if err != nil {
						// still no config, continue polling
						util.Default.ClearLine()
						util.Default.Printf("🔍 Polling for config: %v\n", err)
						continue
					}
					// prefer pipeline.agent_watchs but fallback to devsync.agent_watchs
					var candidate []string
					if cfg != nil {
						candidate = cfg.Devsync.AgentWatchs
						if len(cfg.Pipeline.AgentWatchs) > 0 {
							candidate = cfg.Pipeline.AgentWatchs
						}
					}
					if cfg != nil && len(candidate) > 0 {
						// Resolve newly discovered watch paths relative to workingDir
						newPaths := make([]string, len(cfg.Devsync.AgentWatchs))
						for i, wp := range candidate {
							if workingDir != "" && !filepath.IsAbs(wp) {
								newPaths[i] = filepath.Join(workingDir, wp)
							} else {
								newPaths[i] = wp
							}
						}
						util.Default.ClearLine()
						util.Default.Printf("✅ Detected new watch paths: %v — starting watcher\n", newPaths)
						setupWatcher(newPaths)
						return
					}
				}
			}
		}()

		// Block main goroutine but watch for context cancellation
		<-mainCtx.Done()
		fmt.Println("⏹️  Agent shutting down (no watch paths configured)")
		return
	}

	util.Default.ClearLine()
	util.Default.Printf("📋 Loaded config with %d watch paths\n", len(watchPaths))
	setupWatcher(watchPaths)
}

func performIndexing(config *AgentConfig, bypassIgnore bool, manualFlagPresent bool, manualPrefixes []string) {
	// Use current working dir as root for indexing
	root, err := os.Getwd()
	if err != nil {
		util.Default.ClearLine()
		util.Default.Printf("❌ Failed to get working dir: %v\n", err)
		os.Exit(1)
	}
	util.Default.ClearLine()
	util.Default.Printf("🔍 Building index for: %s\n", root)

	// Decide where to place .sync_temp/indexing_files.db.
	// If the agent executable itself is located inside a directory named ".sync_temp",
	// prefer that directory (this supports running as `.sync_temp/pipeline-agent indexing`).
	exePath, err := os.Executable()
	exeDir := ""
	if err == nil {
		if ed, err2 := filepath.Abs(filepath.Dir(exePath)); err2 == nil {
			exeDir = ed
		}
	}

	// default .sync_temp inside cwd
	absSyncTemp := filepath.Join(root, ".sync_temp")
	// if exeDir ends with .sync_temp, prefer exeDir
	if exeDir != "" && filepath.Base(exeDir) == ".sync_temp" {
		absSyncTemp = exeDir
		util.Default.ClearLine()
		util.Default.Printf("ℹ️  Detected agent executable in .sync_temp, using %s for DB storage\n", absSyncTemp)
	} else {
		util.Default.ClearLine()
		util.Default.Printf("ℹ️  Using %s for DB storage\n", absSyncTemp)
	}

	if err := os.MkdirAll(absSyncTemp, 0755); err != nil {
		util.Default.ClearLine()
		util.Default.Printf("❌ Failed to create %s directory: %v\n", absSyncTemp, err)
		os.Exit(1)
	}

	var idx indexer.IndexMap
	var ierr error

	// If manual-transfer flag present: either use provided prefixes or read from config
	if manualFlagPresent {
		prefixes := manualPrefixes
		if len(prefixes) == 0 {
			// use config manual_transfer (if any) — load raw config file
			// try to read agent config file under .sync_temp/config.json
			data, rerr := os.ReadFile(filepath.Join(root, ".sync_temp", "config.json"))
			if rerr == nil {
				var ac AgentConfig
				if jerr := json.Unmarshal(data, &ac); jerr == nil {
					// generic parse: unmarshal into map to extract manual_transfer
					var m map[string]interface{}
					if merr := json.Unmarshal(data, &m); merr == nil {
						if devRaw, ok := m["devsync"]; ok {
							if devMap, ok2 := devRaw.(map[string]interface{}); ok2 {
								if mtRaw, ok3 := devMap["manual_transfer"]; ok3 && mtRaw != nil {
									if mtSlice, ok4 := mtRaw.([]interface{}); ok4 {
										for _, v := range mtSlice {
											if s, sok := v.(string); sok {
												prefixes = append(prefixes, s)
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}

		// If still empty -> warn and fallback to full index
		if len(prefixes) == 0 {
			util.Default.ClearLine()
			util.Default.Printf("⚠️  manual-transfer flag present but no prefixes found in flag or config — falling back to full index\n")
			idx, ierr = indexer.BuildIndex(root, bypassIgnore)
		} else {
			// perform per-prefix indexing and merge
			idx = indexer.IndexMap{}
			visited := map[string]struct{}{}
			for _, pr := range prefixes {
				// normalize prefix: if absolute, use as-is; if relative, join with root
				var start string
				if filepath.IsAbs(pr) {
					start = pr
				} else {
					p := strings.TrimPrefix(pr, "/")
					start = filepath.Join(root, filepath.FromSlash(p))
				}
				util.Default.ClearLine()
				util.Default.Printf("🔍 Indexing manual-transfer prefix: %s -> %s\n", pr, start)
				subIdx, subErr := indexer.BuildIndexSubtree(root, start, bypassIgnore)
				if subErr != nil {
					util.Default.ClearLine()
					util.Default.Printf("⚠️  failed to index prefix %s: %v\n", pr, subErr)
					continue
				}
				// merge: avoid overwriting previously visited absolute paths
				for k, v := range subIdx {
					if _, ok := visited[k]; ok {
						continue
					}
					visited[k] = struct{}{}
					idx[k] = v
				}
			}
			ierr = nil
		}
	} else {
		idx, ierr = indexer.BuildIndex(root, bypassIgnore)
	}
	if ierr != nil {
		util.Default.ClearLine()
		util.Default.Printf("❌ Indexing failed: %v\n", ierr)
		os.Exit(1)
	}
	dbPath := filepath.Join(absSyncTemp, "indexing_files.db")
	// Remove existing DB to avoid schema mismatches from older agent versions
	if _, serr := os.Stat(dbPath); serr == nil {
		util.Default.ClearLine()
		util.Default.Printf("🧹 Removing existing index DB to avoid schema mismatches: %s\n", dbPath)
		if rerr := os.Remove(dbPath); rerr != nil {
			util.Default.ClearLine()
			util.Default.Printf("⚠️  Failed to remove old DB (will still attempt to write): %v\n", rerr)
		}
	}
	if err := indexer.SaveIndexDB(dbPath, idx); err != nil {
		util.Default.ClearLine()
		util.Default.Printf("❌ Failed to save index DB: %v\n", err)
		os.Exit(1)
	}

	// Print a brief summary
	added, modified, removed := indexer.CompareIndices(nil, idx)
	util.Default.ClearLine()
	util.Default.Printf("✅ Index saved: %s (entries=%d)\n", dbPath, len(idx))
	util.Default.ClearLine()
	util.Default.Printf("Summary: added=%d modified=%d removed=%d\n", len(added), len(modified), len(removed))
}

func setupWatcher(watchPaths []string) {
	// Create .sync_temp directory if it doesn't exist
	syncTempDir := ".sync_temp"
	if err := os.MkdirAll(syncTempDir, 0755); err != nil {
		util.Default.ClearLine()
		util.Default.Printf("❌ Failed to create .sync_temp directory: %v\n", err)
		os.Exit(1)
	}

	util.Default.ClearLine()
	util.Default.Printf("👀 Watching directories: %v\n", watchPaths)
	util.Default.ClearLine()
	util.Default.Printf("📁 Agent location: %s\n", syncTempDir)

	// Try notify-based watching first
	if tryNotifyWatcher(watchPaths) {
		return // Success with notify
	}

	// Fallback to polling-based watching
	fmt.Println("🔄 Falling back to polling-based file watching...")
	// pollingWatcher(watchPaths)
}

func tryNotifyWatcher(watchPaths []string) bool {
	fmt.Println("🔍 Starting notify-based file watching (async, will retry missing paths)...")

	// Channel to receive events from notify
	c := make(chan notify.EventInfo, 100)

	// Start event handler goroutine immediately (it will block until events arrive)
	go func() {
		for e := range c {
			if e == nil {
				continue
			}
			handleFileEvent(e)
		}
	}()

	// Registration goroutine: try to register each path independently
	go func() {
		// Track which paths have been successfully registered
		registered := make([]bool, len(watchPaths))
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-mainCtx.Done():
				fmt.Println("🔄 Watch registration stopped (context cancelled)")
				return
			case <-ticker.C:
				allRegistered := true

				for i, p := range watchPaths {
					if registered[i] {
						continue // already registered
					}

					// Check path existence; if missing, we'll retry later but continue
					if _, err := os.Stat(p); os.IsNotExist(err) {
						util.Default.ClearLine()
						util.Default.Printf("⚠️  Watch path does not exist yet: %s\n", p)
						allRegistered = false
						continue
					}

					// Attempt to register this individual path
					pattern := filepath.Join(p, "...")
					util.Default.ClearLine()
					util.Default.Printf("📋 Registering watch: %s\n", pattern)
					if err := notify.Watch(pattern, c, notify.All); err != nil {
						// Log error and try again later for this path; do not stop other registrations
						util.Default.ClearLine()
						util.Default.Printf("❌ Failed to register watch for %s: %v\n", p, err)
						allRegistered = false
						continue
					}

					// Mark as registered
					util.Default.ClearLine()
					util.Default.Printf("✅ Registered watch for: %s\n", p)
					registered[i] = true
				}

				// Check if everything is registered
				for _, v := range registered {
					if !v {
						allRegistered = false
						break
					}
				}

				if allRegistered {
					fmt.Println("✅ All notify-based watches registered and active")
					return
				}

				// Wait before next retry iteration for unregistered paths
				fmt.Println("⏳ Some watches not ready yet, retrying in 5s...")
			}
		}
	}()

	// Registration goroutine started and event handler running.
	// Block here so the agent process stays alive (as previous behavior) —
	// registration still runs asynchronously in background.
	fmt.Println("⏳ Waiting for events (agent will keep running)...")
	<-mainCtx.Done()

	// Clean up notify watchers
	notify.Stop(c)
	close(c)
	fmt.Println("✅ File watcher stopped gracefully")
	return true
}

func handleFileEvent(event notify.EventInfo) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	// Format output for easy parsing by make-sync
	util.Default.ClearLine()
	util.Default.Printf("[%s] EVENT|%s|%s\n", timestamp, event.Event().String(), event.Path())

	// Calculate file hash using xxHash (only for files that exist)
	if info, err := os.Stat(event.Path()); err == nil && !info.IsDir() {
		// Check file size limit before processing
		if globalConfig != nil && globalConfig.Devsync.SizeLimit > 0 {
			fileSizeMB := float64(info.Size()) / (1024 * 1024)
			limitMB := float64(globalConfig.Devsync.SizeLimit)
			if fileSizeMB > limitMB {
				util.Default.ClearLine()
				util.Default.Printf("[%s] SKIP_SIZE|%s|%.2fMB|limit:%.0fMB\n", timestamp, event.Path(), fileSizeMB, limitMB)
				return
			}
		}

		if hash, err := calculateFileHash(event.Path()); err == nil {
			util.Default.ClearLine()
			util.Default.Printf("[%s] HASH|%s|%s\n", timestamp, event.Path(), hash)
		} else {
			util.Default.ClearLine()
			util.Default.Printf("[%s] ERROR|hash_failed|%s|%v\n", timestamp, event.Path(), err)
		}
	} else if err != nil {
		util.Default.ClearLine()
		util.Default.Printf("[%s] ERROR|stat_failed|%s|%v\n", timestamp, event.Path(), err)
	}

	// Flush output immediately
	os.Stdout.Sync()
}

func printIdentity() {
	// Get the current executable path
	execPath, err := os.Executable()
	if err != nil {
		util.Default.ClearLine()
		util.Default.Printf("Error getting executable path: %v\n", err)
		os.Exit(1)
	}

	// Calculate hash of the agent binary itself
	hash, err := calculateFileHash(execPath)
	if err != nil {
		util.Default.ClearLine()
		util.Default.Printf("Error calculating identity hash: %v\n", err)
		os.Exit(1)
	}

	util.Default.ClearLine()
	util.Default.Printf("%s\n", hash)
}

func calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := xxhash.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
