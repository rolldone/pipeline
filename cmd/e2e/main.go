package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pipeline/internal/pipeline/executor"
	"pipeline/internal/sshclient"
)

// localShellQuote safely single-quote a path for remote POSIX commands
func localShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// copyDir copies files from src to dst (non-recursive for simplicity, but copies nested dirs too)
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// copyDir recursively copies a directory tree, skipping errors where appropriate.
// copyDir removed (was used in earlier seeding strategy)

func main() {
	fmt.Println("E2E local test: starting")
	e := executor.NewExecutor()

	// Prefer using an existing repository binary if present (avoid building from missing source)
	var bpath string
	candidates := []string{"./pipeline", "pipeline", "./pipeline-agent", "pipeline-agent"}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			bpath = abs
			fmt.Printf("ℹ️  Using existing binary for agent: %s\n", bpath)
			break
		}
	}
	if bpath == "" {
		// Build agent for local linux (fallback)
		buildOpts := executor.BuildOptions{TargetOS: "linux", ProjectRoot: ".", SSHClient: nil}
		var err error
		bpath, err = e.BuildAgentForTarget(buildOpts)
		if err != nil {
			fmt.Printf("build agent failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("built agent: %s\n", bpath)
	}

	// Connect to localhost using provided creds
	user := "donny"
	password := "43lw9rj2"
	host := "localhost"
	port := "22"

	client, err := sshclient.NewPersistentSSHClient(user, "", password, "", host, port, "")
	if err != nil {
		fmt.Printf("NewSSHClient failed: %v\n", err)
		os.Exit(1)
	}
	if err := client.Connect(); err != nil {
		fmt.Printf("ssh connect failed: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	// Prepare remote working dir and ensure .sync_temp is inside it
	remoteWorkdir := os.Getenv("E2E_REMOTE_WORKDIR")
	if remoteWorkdir == "" {
		remoteWorkdir = "/home/donny/ini_test"
	}
	// place .sync_temp inside the target workdir as requested
	remoteSyncTemp := remoteWorkdir + "/.sync_temp"

	// Deploy agent
	deployOpts := executor.DeployOptions{OSTarget: "linux", Timeout: 30 * time.Second, Overwrite: false}
	if err := e.DeployAgent(context.TODO(), nil, client, bpath, remoteSyncTemp, deployOpts); err != nil {
		fmt.Printf("DeployAgent failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("DeployAgent OK")

	// Upload a minimal config.json to remote .sync_temp
	tmpf, err := os.CreateTemp("", "cfg-*.json")
	if err != nil {
		fmt.Printf("create temp failed: %v\n", err)
		os.Exit(1)
	}
	cfg := map[string]interface{}{
		"devsync": map[string]interface{}{
			"working_dir": remoteWorkdir,
		},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	tmpf.Write(b)
	tmpf.Close()
	remoteCfgPath := remoteSyncTemp + "/config.json"
	if err := client.SyncFile(tmpf.Name(), remoteCfgPath); err != nil {
		fmt.Printf("upload config failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("uploadConfigToRemote OK")

	// Run indexing
	out, err := e.RemoteRunAgentIndexing(client, remoteSyncTemp, "linux", true, []string{remoteWorkdir})
	if err != nil {
		fmt.Printf("RemoteRunAgentIndexing failed: %v\nOutput:%s\n", err, out)
		os.Exit(1)
	}
	fmt.Println("RemoteRunAgentIndexing output:")
	fmt.Println(out)

	// Download DB using SSH client directly
	remoteDBPath := remoteSyncTemp + "/indexing_files.db"
	localTmp, err := os.CreateTemp("", "remote-index-*.db")
	if err != nil {
		fmt.Printf("create temp failed: %v\n", err)
		os.Exit(1)
	}
	localTmp.Close()
	if err := client.DownloadFile(localTmp.Name(), remoteDBPath); err != nil {
		fmt.Printf("download DB failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Downloaded DB to: %s\n", localTmp.Name())

	// Run agent-based transfer tests
	// Soft upload: should skip deleting extra remote files
	fmt.Println("\n=== Running agent upload test: soft (no delete policy) ===")
	// For this test use the seeded downloaded-files directory as source (absolute)
	repoSrc, _ := filepath.Abs("./downloaded-files")
	if _, err := os.Stat(repoSrc); os.IsNotExist(err) {
		fmt.Printf("seed source %s not found. Please ensure downloaded-files exists and contains seed files.\n", repoSrc)
		os.Exit(1)
	}
	remoteUploaded := remoteWorkdir + "/uploaded"
	if err := e.AgentUploadTest(client, localTmp.Name(), remoteSyncTemp, "linux", repoSrc, remoteUploaded, ""); err != nil {
		fmt.Printf("AgentUploadTest (soft) failed: %v\n", err)
	} else {
		fmt.Println("AgentUploadTest (soft) completed")
	}

	// Force upload: delete extra files on remote
	fmt.Println("\n=== Running agent upload test: force (delete_policy=force) ===")
	if err := e.AgentUploadTest(client, localTmp.Name(), remoteSyncTemp, "linux", repoSrc, remoteUploaded, "force"); err != nil {
		fmt.Printf("AgentUploadTest (force) failed: %v\n", err)
	} else {
		fmt.Println("AgentUploadTest (force) completed")
	}

	// Print remote directory contents for inspection
	fmt.Println("\n=== Remote directory listing (/home/donny/ini_test) ===")
	if out, err := client.RunCommandWithOutput("ls -la " + localShellQuote(remoteWorkdir)); err != nil {
		fmt.Printf("failed to list remote dir: %v\n", err)
	} else {
		fmt.Println(out)
	}

	// Agent download test: download from remote to a temp local dir
	fmt.Println("\n=== Running agent download test ===")
	localDst, _ := os.MkdirTemp("", "e2e-download-*")
	defer os.RemoveAll(localDst)
	if err := e.AgentDownloadTest(client, localTmp.Name(), remoteSyncTemp, "linux", remoteUploaded, localDst); err != nil {
		fmt.Printf("AgentDownloadTest failed: %v\n", err)
	} else {
		fmt.Printf("AgentDownloadTest completed, files downloaded to: %s\n", localDst)
	}

	// Clean up after short wait
	time.Sleep(1 * time.Second)

	// === Local transfer test: /tmp/fromA -> /tmp/fromB ===
	fmt.Println("\n=== Local transfer test: /tmp/fromA -> /tmp/fromB ===")

	fromA := "/tmp/fromA"
	fromB := "/tmp/fromB"

	// recreate directories
	os.RemoveAll(fromA)
	os.RemoveAll(fromB)
	os.MkdirAll(fromA, 0755)
	os.MkdirAll(fromB, 0755)

	// seed from downloaded-files if present
	seedSrcAbs, _ := filepath.Abs("./downloaded-files")
	if _, serr := os.Stat(seedSrcAbs); serr == nil {
		if err := copyDir(seedSrcAbs, fromA); err != nil {
			fmt.Printf("failed to seed fromA: %v\n", err)
		}
	}

	if err := e.AgentLocalTransferTest(fromA, fromB, ""); err != nil {
		fmt.Printf("AgentLocalTransferTest (soft) failed: %v\n", err)
	} else {
		fmt.Println("AgentLocalTransferTest (soft) completed")
	}

	if err := e.AgentLocalTransferTest(fromA, fromB, "force"); err != nil {
		fmt.Printf("AgentLocalTransferTest (force) failed: %v\n", err)
	} else {
		fmt.Println("AgentLocalTransferTest (force) completed")
	}

	fmt.Println("E2E local test: done")
}
