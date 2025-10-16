package util

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// GetProjectRoot returns the project root directory, handling both development mode
// (go run) and production mode (compiled executable).
//
// In development mode (go run), os.Executable() returns a temporary path, so we
// need to find the actual project root by looking for go.mod or other indicators.
//
// In production mode, we use the directory containing the executable.
func GetProjectRoot() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}

	// Store original path for debug
	originalExePath := exePath

	// Debug current working directory vs executable (uncomment for debugging)
	// wd, _ := os.Getwd()
	// fmt.Printf("DEBUG: Working Directory: %s\n", wd)
	// fmt.Printf("DEBUG: Executable (original): %s\n", originalExePath)

	// Resolve symlinks if any - this handles cases where executable is symlinked
	// from /usr/local/bin/make-sync to actual project location
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	// Strategy: Always try executable location first, then fallback to working directory
	// This ensures consistent behavior regardless of where user runs the command

	var projectRoot string
	var detectionError error

	if isDevelopmentMode(exePath) {
		// fmt.Printf("DEBUG: Development mode detected\n")

		// In development mode, executable is in temp dir, so use working directory
		// But validate that working directory actually contains a go project
		projectRoot, detectionError = findProjectRootFromWorkingDir()
		if detectionError != nil {
			// fmt.Printf("DEBUG: Failed to find project root from working dir: %v\n", detectionError)
			return "", detectionError
		}

		// Additional validation: ensure this is actually the make-sync project
		// by checking for expected structure
		if !isValidMakeSyncProject(projectRoot) {
			// If working dir is not make-sync project, try to find it
			// This handles the case where user is in a different project
			actualRoot, err := findMakeSyncProjectRoot()
			if err == nil {
				// fmt.Printf("DEBUG: Working dir is not make-sync project, using detected: %s\n", actualRoot)
				projectRoot = actualRoot
			} else {
				// fmt.Printf("DEBUG: Could not find make-sync project, using working dir: %s\n", projectRoot)
			}
		}

		// fmt.Printf("DEBUG: Development mode final root: %s\n", projectRoot)
	} else {
		// fmt.Printf("DEBUG: Production mode detected\n")

		// Production mode: project root is the directory containing the executable
		// After symlink resolution, this should now point to the actual project location
		projectRoot = filepath.Dir(exePath)

		// Additional safety check: if we resolved a symlink and the resolved path
		// doesn't contain go.mod, try searching upward from the resolved location
		if originalExePath != exePath {
			if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); os.IsNotExist(err) {
				// Symlink was resolved but go.mod not found at executable location
				// Try searching upward from resolved path
				if foundRoot, err := findProjectRootFromPath(projectRoot); err == nil {
					projectRoot = foundRoot
				}
			}
		}

		// fmt.Printf("DEBUG: Production mode final root: %s\n", projectRoot)
	}

	return projectRoot, nil // Production mode: project root is the directory containing the executable
	// After symlink resolution, this should now point to the actual project location
	root := filepath.Dir(exePath)

	// Additional safety check: if we resolved a symlink and the resolved path
	// doesn't contain go.mod, try searching upward from the resolved location
	if originalExePath != exePath {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); os.IsNotExist(err) {
			// Symlink was resolved but go.mod not found at executable location
			// Try searching upward from resolved path
			if foundRoot, err := findProjectRootFromPath(root); err == nil {
				root = foundRoot
			}
		}
	}

	// Debug: uncomment to see path detection
	fmt.Printf("DEBUG: Production mode detected. Original: %s, Resolved: %s, Project root: %s\n", originalExePath, exePath, root)
	return root, nil
}

// isDevelopmentMode checks if the executable path indicates we're running via "go run"
func isDevelopmentMode(exePath string) bool {
	// go run typically creates executables in temp directories or go build cache
	tempDir := os.TempDir()

	// Normalize paths for comparison
	tempDir = filepath.Clean(tempDir)
	exePath = filepath.Clean(exePath)

	// Check if executable is in temp directory (traditional temp dir)
	if strings.HasPrefix(exePath, tempDir) {
		return true
	}

	// Check if executable is in Go build cache directory
	// Go build cache is typically ~/.cache/go-build on Linux/Mac
	homeDir, err := os.UserHomeDir()
	if err == nil {
		goBuildCache := filepath.Join(homeDir, ".cache", "go-build")
		goBuildCache = filepath.Clean(goBuildCache)
		if strings.HasPrefix(exePath, goBuildCache) {
			return true
		}
	}

	// Check for other common Go temporary patterns
	// Go run also sometimes uses patterns like "go-build" in the name
	if strings.Contains(exePath, "go-build") {
		return true
	}

	return false
}

// findProjectRootFromWorkingDir searches upward from working directory to find go.mod
func findProjectRootFromWorkingDir() (string, error) {
	// Start from current working directory
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	return findProjectRootFromPath(wd)
}

// findProjectRootFromPath searches upward from given path to find project root indicators
func findProjectRootFromPath(startPath string) (string, error) {
	currentPath := filepath.Clean(startPath)

	for {
		// Check for go.mod (primary indicator for Go projects)
		if _, err := os.Stat(filepath.Join(currentPath, "go.mod")); err == nil {
			return currentPath, nil
		}

		// Move to parent directory
		parentPath := filepath.Dir(currentPath)

		// Stop if we've reached the root or can't go higher
		if parentPath == currentPath || parentPath == "." {
			break
		}

		currentPath = parentPath
	}

	// Second pass: Look for main.go at project level (fallback for Go projects)
	currentPath = filepath.Clean(startPath)
	for {
		// Check for main.go in the current directory
		if _, err := os.Stat(filepath.Join(currentPath, "main.go")); err == nil {
			return currentPath, nil
		}

		// Move to parent directory
		parentPath := filepath.Dir(currentPath)

		// Stop if we've reached the root or can't go higher
		if parentPath == currentPath || parentPath == "." {
			break
		}

		currentPath = parentPath
	}

	// Third pass: Look for make-sync.yaml but verify it's at project level
	currentPath = filepath.Clean(startPath)
	for {
		// Check for make-sync.yaml (project-specific indicator)
		if _, err := os.Stat(filepath.Join(currentPath, "make-sync.yaml")); err == nil {
			// Only accept make-sync.yaml if it's at a level that also has go.mod or main.go
			// This prevents subfolder configs from being treated as project root
			if _, err := os.Stat(filepath.Join(currentPath, "go.mod")); err == nil {
				return currentPath, nil
			}
			if _, err := os.Stat(filepath.Join(currentPath, "main.go")); err == nil {
				return currentPath, nil
			}
		}

		// Move to parent directory
		parentPath := filepath.Dir(currentPath)

		// Stop if we've reached the root or can't go higher
		if parentPath == currentPath || parentPath == "." {
			break
		}

		currentPath = parentPath
	}

	// Fallback: if we can't find project root, return the original working directory
	wd, err := os.Getwd()
	if err != nil {
		// Last resort: use the directory containing the executable
		exePath, execErr := os.Executable()
		if execErr != nil {
			return "", execErr
		}
		return filepath.Dir(exePath), nil
	}

	return wd, nil
}

// GetProjectRootFromCaller returns project root using runtime caller information
// This is useful when you want to find project root relative to the calling file
func GetProjectRootFromCaller() (string, error) {
	_, filename, _, ok := runtime.Caller(1)
	if !ok {
		return GetProjectRoot() // fallback to regular method
	}

	// Start searching from the directory containing the caller
	return findProjectRootFromPath(filepath.Dir(filename))
}

// isValidMakeSyncProject checks if the given path contains make-sync project structure
func isValidMakeSyncProject(projectPath string) bool {
	// Check for go.mod with make-sync module
	goModPath := filepath.Join(projectPath, "go.mod")
	if content, err := os.ReadFile(goModPath); err == nil {
		// Check if go.mod contains "module make-sync"
		if strings.Contains(string(content), "module make-sync") {
			return true
		}
	}

	// Alternative check: look for make-sync specific structure
	agentPath := filepath.Join(projectPath, "sub_app", "agent")
	if _, err := os.Stat(agentPath); err == nil {
		// Also check for main.go in root
		mainGoPath := filepath.Join(projectPath, "main.go")
		if _, err := os.Stat(mainGoPath); err == nil {
			return true
		}
	}

	return false
}

// findMakeSyncProjectRoot tries to find make-sync project root using various strategies
func findMakeSyncProjectRoot() (string, error) {
	// Strategy 1: Check if executable path resolution gives us the answer
	if exePath, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
			// Try from executable location upward
			if root, err := findProjectRootFromPath(filepath.Dir(resolved)); err == nil {
				if isValidMakeSyncProject(root) {
					return root, nil
				}
			}
		}
	}

	// Strategy 2: Look in common locations relative to executable
	if _, err := os.Executable(); err == nil {
		// If executable is in /usr/local/bin, /usr/bin, etc.,
		// make-sync might be installed, check common source locations
		commonPaths := []string{
			"/home/donny/workspaces/make-sync",
			"/mnt/sda/workspaces/make-sync",
			"/workspaces/make-sync",
		}

		for _, path := range commonPaths {
			if isValidMakeSyncProject(path) {
				return path, nil
			}
		}
	}

	// Strategy 3: Search upward from working directory as last resort
	return findProjectRootFromWorkingDir()
}
