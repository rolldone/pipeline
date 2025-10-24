package syncdata

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/glebarez/sqlite"
)

// Simple printer for logging
var printer = log.Default()

// [Rest of the file remains the same but with stub implementations for functions that depend on external packages]

// generateRemoteConfig creates a config for the remote agent based on local config
func generateRemoteConfig(cfg *Config) *RemoteAgentConfig {
	remoteConfig := &RemoteAgentConfig{}
	// Map values from internal cfg to the remote config under `pipeline` key
	remoteConfig.Pipeline.Ignores = cfg.Devsync.Ignores
	remoteConfig.Pipeline.AgentWatchs = cfg.Devsync.AgentWatchs
	remoteConfig.Pipeline.ManualTransfer = cfg.Devsync.ManualTransfer
	remoteConfig.Pipeline.WorkingDir = cfg.Devsync.Auth.RemotePath

	return remoteConfig
}

// isWindowsTarget returns true when osTarget indicates Windows.
func isWindowsTarget(osTarget string) bool {
	return strings.Contains(strings.ToLower(osTarget), "win")
}

// uploadConfigToRemote creates and uploads config.json to remote .sync_temp directory
func uploadConfigToRemote(client SSHClient, cfg *Config, remoteSyncTemp, osTarget string) error {
	// Generate remote config
	remoteConfig := generateRemoteConfig(cfg)

	// Collect preprocessed ignores from project root and populate remoteConfig.Devsync.Ignores
	root := cfg.LocalPath
	if root == "" {
		wd, err := os.Getwd()
		if err == nil {
			root = wd
		}
	}
	if root != "" {
		if ignores, err := collectPreprocessedIgnores(root); err == nil {
			remoteConfig.Pipeline.Ignores = ignores
		}
	}

	// Convert to JSON
	configJSON, err := json.MarshalIndent(remoteConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config to JSON: %v", err)
	}

	printer.Printf("📤 Uploading config.json with working_dir: %s\n", remoteConfig.Pipeline.WorkingDir)
	printer.Printf("📄 Config content:\n%s\n", string(configJSON))

	// Create temporary local file
	tempFile, err := os.CreateTemp("", "remote-config-*.json")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Write config to temp file
	if _, err := tempFile.Write(configJSON); err != nil {
		return fmt.Errorf("failed to write config to temp file: %v", err)
	}
	tempFile.Close()

	// Always use absolute remotePath for .sync_temp/config.json, but avoid double .sync_temp
	remoteBase := cfg.Devsync.Auth.RemotePath
	var remoteConfigPath string
	if isWindowsTarget(osTarget) {
		base := strings.ReplaceAll(remoteBase, "/", "\\")
		if strings.HasSuffix(base, ".sync_temp") {
			remoteConfigPath = base + "\\config.json"
		} else {
			remoteConfigPath = base + "\\.sync_temp\\config.json"
		}
	} else {
		base := remoteBase
		if strings.HasSuffix(base, ".sync_temp") {
			remoteConfigPath = filepath.Join(base, "config.json")
		} else {
			remoteConfigPath = filepath.Join(base, ".sync_temp", "config.json")
		}
	}

	// Upload config to remote
	if err := client.SyncFile(tempFile.Name(), remoteConfigPath); err != nil {
		return fmt.Errorf("failed to upload config: %v", err)
	}

	printer.Printf("✅ Config uploaded to: %s\n", remoteConfigPath)
	return nil
}

// ConnectSSH creates and connects an SSH client using values from cfg.Devsync.Auth
func ConnectSSH(cfg *Config) (SSHClient, error) {
	// TODO: Implement SSH client creation for pipeline-agent
	// This is a placeholder since the agent binary is maintained as a separate module
	return nil, fmt.Errorf("SSH client creation not implemented in pipeline-agent module")
}

// UploadAgentBinary uploads a local agent binary to the remote directory using direct SyncFile like watcher.
// localBinaryPath: path to local binary (e.g., built pipeline-agent or pipeline-agent.exe)
// remoteDir: absolute remote directory where to place binary (usually <project>/.sync_temp)
// osTarget: string indicating remote OS (contains "win" for windows)
func UploadAgentBinary(client SSHClient, localBinaryPath, remoteDir, osTarget string) error {
	// Create .sync_temp directory on remote first (like watcher does)
	if isWindowsTarget(osTarget) {
		// Use consistent backslash paths for Windows
		winRemoteDir := strings.ReplaceAll(remoteDir, "/", "\\")
		createCmd := fmt.Sprintf("cmd.exe /C if not exist \"%s\" mkdir \"%s\"", winRemoteDir, winRemoteDir)
		printer.Printf("🔧 DEBUG: Creating remote dir with cmd: %s\n", createCmd)
		if err := client.RunCommand(createCmd); err != nil {
			return fmt.Errorf("failed to create remote .sync_temp (windows): %v", err)
		}
	} else {
		remoteCmd := fmt.Sprintf("mkdir -p %s", remoteDir)
		if err := client.RunCommand(remoteCmd); err != nil {
			return fmt.Errorf("failed to create remote .sync_temp: %v", err)
		}
	}

	// Get unique agent binary name from local config
	localConfig, err := GetOrCreateLocalConfig()
	if err != nil {
		return fmt.Errorf("failed to load local config: %v", err)
	}

	remoteExecName := localConfig.GetAgentBinaryName(osTarget)
	var remoteAgentPath string
	if isWindowsTarget(osTarget) {
		base := strings.ReplaceAll(remoteDir, "/", "\\")
		if strings.HasSuffix(base, ".sync_temp") {
			remoteAgentPath = base + "\\" + remoteExecName
		} else {
			remoteAgentPath = base + "\\.sync_temp\\" + remoteExecName
		}
	} else {
		base := remoteDir
		if strings.HasSuffix(base, ".sync_temp") {
			remoteAgentPath = filepath.Join(base, remoteExecName)
		} else {
			remoteAgentPath = filepath.Join(base, ".sync_temp", remoteExecName)
		}
		remoteAgentPath = filepath.ToSlash(remoteAgentPath)
	}

	printer.Printf("🔧 DEBUG: Uploading %s to %s\n", localBinaryPath, remoteAgentPath)

	// Use SyncFile with absolute path
	if err := client.SyncFile(localBinaryPath, remoteAgentPath); err != nil {
		return fmt.Errorf("failed to upload agent: %v", err)
	}

	printer.Printf("✅ Agent binary uploaded successfully\n")

	// Set permissions on Unix (like watcher does)
	if !isWindowsTarget(osTarget) {
		chmodCmd := fmt.Sprintf("chmod +x %s", remoteAgentPath)
		log.Println("[runner.go] DEBUG: Running chmod command:", chmodCmd)
		if err := client.RunCommand(chmodCmd); err != nil {
			log.Println("[runner.go] DEBUG: chmod command failed:", err)
			return fmt.Errorf("failed to make agent executable: %v", err)
		}
		log.Println("[runner.go] DEBUG: chmod command succeeded")
	}

	return nil
}

func RemoteRunAgentIndexing(client SSHClient, remoteDir, osTarget string, bypassIgnore bool, prefixes []string) (string, error) {
	isWin := strings.Contains(strings.ToLower(osTarget), "win")

	// Get unique agent binary name from local config
	localConfig, err := GetOrCreateLocalConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load local config: %v", err)
	}

	// Build the binary path based on OS using unique agent name
	binaryName := localConfig.GetAgentBinaryName(osTarget)
	var cmd string
	remoteBase := remoteDir
	if isWin {
		winRemoteDir := strings.ReplaceAll(remoteBase, "/", "\\")
		var agentPath, cdDir string
		if strings.HasSuffix(winRemoteDir, ".sync_temp") {
			agentPath = winRemoteDir + "\\" + binaryName
			cdDir = winRemoteDir[:len(winRemoteDir)-len(".sync_temp")]
			cdDir = strings.TrimSuffix(cdDir, "\\")
			if cdDir == "" {
				cdDir = winRemoteDir
			} // fallback
		} else {
			agentPath = winRemoteDir + "\\.sync_temp\\" + binaryName
			cdDir = winRemoteDir
		}
		indexingCmd := "indexing"
		if bypassIgnore {
			indexingCmd = "indexing --bypass-ignore"
		}
		if len(prefixes) > 0 {
			joined := strings.Join(prefixes, ",")
			indexingCmd = fmt.Sprintf("%s --manual-transfer %s", indexingCmd, joined)
		}
		cmd = fmt.Sprintf("cmd.exe /C cd /d \"%s\" && \"%s\" %s", cdDir, agentPath, indexingCmd)
	} else {
		var agentPath, cdDir string
		if strings.HasSuffix(remoteBase, ".sync_temp") {
			agentPath = filepath.Join(remoteBase, binaryName)
			cdDir = strings.TrimSuffix(remoteBase, ".sync_temp")
			if cdDir == "" {
				cdDir = remoteBase
			}
		} else {
			agentPath = filepath.Join(remoteBase, ".sync_temp", binaryName)
			cdDir = remoteBase
		}
		// Ensure forward slashes for Linux
		agentPath = filepath.ToSlash(agentPath)
		cdDir = filepath.ToSlash(cdDir)
		indexingCmd := "indexing"
		if bypassIgnore {
			indexingCmd = "indexing --bypass-ignore"
		}
		if len(prefixes) > 0 {
			joined := strings.Join(prefixes, ",")
			indexingCmd = fmt.Sprintf("%s --manual-transfer %s", indexingCmd, shellQuote(joined))
		}
		cmd = fmt.Sprintf("cd %s && %s %s", shellQuote(cdDir), shellQuote(agentPath), indexingCmd)
	}

	fmt.Printf("🔍 DEBUG: Executing remote command: %s\n", cmd)

	// Add timeout context for debugging
	start := time.Now()
	fmt.Printf("🔍 DEBUG: Starting remote command execution at %s\n", start.Format("15:04:05"))

	// Try using RunCommandWithStream for better responsiveness like watcher does
	outputChan, errorChan, err := client.RunCommandWithStream(cmd, false)
	if err != nil {
		return "", fmt.Errorf("failed to start remote indexing command: %v", err)
	}

	var output strings.Builder
	timeout := time.After(60 * time.Second) // 60 second timeout for indexing

	fmt.Printf("🔍 DEBUG: Streaming command output...\n")

	for {
		select {
		case out, ok := <-outputChan:
			if !ok {
				// Command finished
				duration := time.Since(start)
				fmt.Printf("🔍 DEBUG: Command completed in %v\n", duration)

				result := output.String()
				fmt.Printf("🔍 DEBUG: Final output (%d bytes): %s\n", len(result), result)
				return result, nil
			}
			output.WriteString(out)
			fmt.Printf("🔍 DEBUG: Output chunk: %s", out) // Real-time output

		case err := <-errorChan:
			if err != nil {
				duration := time.Since(start)
				fmt.Printf("🔍 DEBUG: Command failed after %v: %v\n", duration, err)
				return output.String(), fmt.Errorf("remote indexing failed: %v", err)
			}

		case <-timeout:
			duration := time.Since(start)
			fmt.Printf("🔍 DEBUG: Command timed out after %v\n", duration)
			return output.String(), fmt.Errorf("remote indexing timed out after 60 seconds")
		}
	}
}

// shellQuote quotes a POSIX path using single quotes, escaping existing single quotes
func shellQuote(s string) string {
	// simple implementation: replace ' with '\'' and wrap with '
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// RunAgentIndexingFlow encapsulates the full remote indexing orchestration:
// - locate local agent binary from provided candidates
// - connect SSH using cfg
// - optionally upload agent into remote .sync_temp (controlled by skipUpload)
// - run remote indexing command
// - download remote indexing_files.db into a local temp file
// Returns the local path of the downloaded DB, the remote output, or an error.
// If skipUpload is true, the function will not upload the agent or config and
// assumes they are already present on the remote (useful when caller already
// deployed the agent).
func RunAgentIndexingFlow(cfg *Config, localCandidates []string, bypassIgnore bool, prefixes []string) (string, string, error) {
	// find local binary
	var localBinary string
	for _, c := range localCandidates {
		if _, err := os.Stat(c); err == nil {
			localBinary = c
			break
		}
	}
	if localBinary == "" {
		return "", "", fmt.Errorf("agent binary not found in candidates: %v", localCandidates)
	}

	// determine remote base path
	remotePath := cfg.Devsync.Auth.RemotePath
	osTarget := strings.ToLower(strings.TrimSpace(cfg.Devsync.OSTarget))
	var remoteSyncTemp string
	if remotePath == "" {
		if strings.Contains(osTarget, "win") {
			remoteSyncTemp = "%TEMP%"
		} else {
			remoteSyncTemp = "/tmp/.sync_temp"
		}
	} else {
		if strings.Contains(osTarget, "win") {
			remoteSyncTemp = filepath.ToSlash(filepath.Join(remotePath, ".sync_temp"))
		} else {
			remoteSyncTemp = filepath.Join(remotePath, ".sync_temp")
		}
	}

	printer.Printf("ℹ️  Local agent binary: %s\n", localBinary)
	printer.Printf("ℹ️  Remote target .sync_temp: %s (os_target=%s)\n", remoteSyncTemp, osTarget)

	// Connect SSH
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return "", "", fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	// Upload agent binary into remoteSyncTemp
	if err := UploadAgentBinary(sshCli, localBinary, remoteSyncTemp, osTarget); err != nil {
		return "", "", fmt.Errorf("upload agent binary failed: %v", err)
	}
	printer.Println("✅ Agent binary uploaded")

	// Upload config.json to remote .sync_temp (needed for agent to know working directory)
	if err := uploadConfigToRemote(sshCli, cfg, remoteSyncTemp, osTarget); err != nil {
		return "", "", fmt.Errorf("upload config failed: %v", err)
	}
	printer.Println("✅ Config uploaded")

	// Run remote indexing
	printer.Println("🔍 Running remote agent indexing...")
	out, err := RemoteRunAgentIndexing(sshCli, remoteSyncTemp, osTarget, bypassIgnore, prefixes)
	if err != nil {
		return "", out, fmt.Errorf("remote indexing failed: %v", err)
	}
	printer.Printf("✅ Remote indexing finished. Remote outaput:\n%s\n", out)
	return "", out, nil
}

// DownloadIndexDB downloads the remote .sync_temp/indexing_files.db into the
// provided local destination directory (localDestFolder). If localDestFolder is
// empty, it will default to cfg.LocalPath or current working directory's .sync_temp.
// Returns the local file path or an error.
func DownloadIndexDB(cfg *Config, localDestFolder string) (string, error) {
	// determine remote base path
	remotePath := cfg.Devsync.Auth.RemotePath
	osTarget := strings.ToLower(strings.TrimSpace(cfg.Devsync.OSTarget))
	var remoteSyncTemp string
	if remotePath == "" {
		if strings.Contains(osTarget, "win") {
			remoteSyncTemp = "%TEMP%"
		} else {
			remoteSyncTemp = "/tmp/.sync_temp"
		}
	} else {
		if strings.Contains(osTarget, "win") {
			remoteSyncTemp = filepath.ToSlash(filepath.Join(remotePath, ".sync_temp"))
		} else {
			remoteSyncTemp = filepath.Join(remotePath, ".sync_temp")
		}
	}

	// remote file path
	var remoteFile string
	if strings.Contains(osTarget, "win") {
		remoteFile = strings.ReplaceAll(remoteSyncTemp, "/", "\\") + "\\indexing_files.db"
	} else {
		remoteFile = filepath.Join(remoteSyncTemp, "indexing_files.db")
	}

	// decide local destination folder
	dest := localDestFolder
	if dest == "" {
		if cfg.LocalPath != "" {
			dest = cfg.LocalPath
		} else {
			dest = "."
		}
	}
	localSyncTemp := filepath.Join(dest, ".sync_temp")
	if err := os.MkdirAll(localSyncTemp, 0755); err != nil {
		return "", fmt.Errorf("failed to create local .sync_temp: %v", err)
	}
	localFile := filepath.Join(localSyncTemp, "indexing_files.db")

	// Connect SSH
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return "", fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	printer.Printf("⬇️  Downloading remote index DB from %s to %s\n", remoteFile, localFile)
	if err := sshCli.DownloadFile(localFile, remoteFile); err != nil {
		return "", fmt.Errorf("failed to download index DB: %v", err)
	}

	printer.Printf("✅ Downloaded index DB to %s\n", localFile)
	return localFile, nil
}

// CompareAndDownloadByHash downloads the remote index DB, builds a local index from
// localRoot (if empty, derived from cfg.LocalPath or current working dir), compares
// by relative path and hash, and downloads files whose hash differ or when remote
// hash is empty. Returns list of local downloaded file paths.
func CompareAndDownloadByHash(cfg *Config, localRoot string) ([]string, error) {
	// TODO: Implement CompareAndDownloadByHash for sync-agent
	return nil, fmt.Errorf("CompareAndDownloadByHash not implemented in sync-agent module")
}

// buildRemotePath constructs an absolute remote path for a given rel using cfg
func buildRemotePath(cfg *Config, rel string) string {
	remoteBase := cfg.Devsync.Auth.RemotePath
	osTarget := strings.ToLower(strings.TrimSpace(cfg.Devsync.OSTarget))
	if strings.Contains(osTarget, "win") {
		// prefer backslashes on windows remote
		p := filepath.ToSlash(filepath.Join(remoteBase, rel))
		return strings.ReplaceAll(p, "/", "\\")
	}
	return filepath.ToSlash(filepath.Join(remoteBase, rel))
}

// CompareAndUploadByHash performs a local-first safe-push:
// - downloads remote index DB into local .sync_temp
// - walks local tree and for each file compares to remote entry by rel/hash
// - uploads files that are new or whose hash differs (or remote hash is empty)
// Returns list of uploaded local paths.
func CompareAndUploadByHash(cfg *Config, localRoot string) ([]string, error) {
	// TODO: Implement CompareAndUploadByHash for sync-agent
	return nil, fmt.Errorf("CompareAndUploadByHash not implemented in sync-agent module")
}

// CompareAndDownloadByHashWithFilter behaves like CompareAndDownloadByHash but
// only operates on remote entries whose rel matches any of the provided prefixes.
// If prefixes is empty, it behaves like the full CompareAndDownloadByHash.
func CompareAndDownloadByHashWithFilter(cfg *Config, localRoot string, prefixes []string) ([]string, error) {
	// TODO: Implement CompareAndDownloadByHashWithFilter for sync-agent
	return nil, fmt.Errorf("CompareAndDownloadByHashWithFilter not implemented in sync-agent module")
}

// CompareAndUploadByHashWithFilter behaves like CompareAndUploadByHash but only
// operates on local paths whose rel starts with any of the provided prefixes.
// If prefixes is empty, it behaves like full CompareAndUploadByHash.
func CompareAndUploadByHashWithFilter(cfg *Config, localRoot string, prefixes []string) ([]string, error) {
	// TODO: Implement CompareAndUploadByHashWithFilter for sync-agent
	return nil, fmt.Errorf("CompareAndUploadByHashWithFilter not implemented in sync-agent module")
}

// collectPreprocessedIgnores walks root and collects .sync_ignore lines, preprocesses
// simple patterns (adds **/ variant) and returns a deduplicated list.
func collectPreprocessedIgnores(root string) ([]string, error) {
	found := map[string]struct{}{}
	// default ignores
	defaults := []string{".sync_temp", "pipeline.yaml", ".sync_ignore", ".sync_collections"}
	for _, d := range defaults {
		found[d] = struct{}{}
	}

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), ".sync_ignore") {
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			lines := strings.Split(string(data), "\n")
			for _, ln := range lines {
				l := strings.TrimSpace(ln)
				if l == "" || strings.HasPrefix(l, "#") {
					continue
				}
				neg := false
				if strings.HasPrefix(l, "!") {
					neg = true
					l = strings.TrimPrefix(l, "!")
				}
				// normalize to forward slashes
				l = filepath.ToSlash(l)
				if strings.Contains(l, "/") || strings.Contains(l, "**") {
					if neg {
						found["!"+l] = struct{}{}
					} else {
						found[l] = struct{}{}
					}
					continue
				}
				// add both forms
				if neg {
					found["!"+l] = struct{}{}
					found["!**/"+l] = struct{}{}
				} else {
					found[l] = struct{}{}
					found["**/"+l] = struct{}{}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// convert map to slice deterministic order
	out := make([]string, 0, len(found))
	for k := range found {
		out = append(out, k)
	}
	// sort for deterministic output
	sort.Strings(out)
	return out, nil
}

// RemoteAgentConfig represents the configuration sent to remote agent
// This mirrors the struct from internal/devsync/types.go
type RemoteAgentConfig struct {
	Pipeline struct {
		Ignores        []string `json:"ignores"`
		AgentWatchs    []string `json:"agent_watchs"`
		ManualTransfer []string `json:"manual_transfer"`
		WorkingDir     string   `json:"working_dir"`
	} `json:"pipeline"`
}
