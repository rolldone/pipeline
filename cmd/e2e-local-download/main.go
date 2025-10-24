package main

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/cespare/xxhash/v2"

	"pipeline/internal/pipeline/executor"
	"pipeline/internal/pipeline/types"
)

// LocalSSHClient is a minimal SSHClient implementation that copies files locally.
type LocalSSHClient struct{}

func (l *LocalSSHClient) Connect() error { return nil }
func (l *LocalSSHClient) Close() error   { return nil }
func (l *LocalSSHClient) UploadBytes(data []byte, remotePath string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(remotePath), 0755); err != nil {
		return err
	}
	return os.WriteFile(remotePath, data, perm)
}
func (l *LocalSSHClient) UploadFile(localPath, remotePath string) error {
	if err := os.MkdirAll(filepath.Dir(remotePath), 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	return os.WriteFile(remotePath, data, 0644)
}
func (l *LocalSSHClient) DownloadFile(localPath, remotePath string) error {
	// Copy remotePath -> localPath
	data, err := os.ReadFile(remotePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(localPath, data, 0644)
}
func (l *LocalSSHClient) RunCommand(cmd string) error                     { return nil }
func (l *LocalSSHClient) RunCommandWithOutput(cmd string) (string, error) { return "", nil }
func (l *LocalSSHClient) SyncFile(localPath, remotePath string) error {
	return l.UploadFile(localPath, remotePath)
}
func (l *LocalSSHClient) RunCommandWithStream(cmd string, usePty bool) (<-chan string, <-chan error, error) {
	outc := make(chan string, 1)
	errc := make(chan error, 1)
	close(outc)
	close(errc)
	return outc, errc, nil
}

func computeXXHashHex(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := xxhash.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func createIndexDB(dbPath string, entries []struct {
	Path  string
	Rel   string
	Size  int64
	Mod   int64
	Hash  string
	IsDir int
}) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS files (
        path TEXT PRIMARY KEY,
        rel TEXT,
        size INTEGER,
        mod_time INTEGER,
        hash TEXT,
        is_dir INTEGER,
        checked INTEGER DEFAULT 0
    )`); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM files`); err != nil {
		tx.Rollback()
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO files(path, rel, size, mod_time, hash, is_dir, checked) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.Exec(e.Path, e.Rel, e.Size, e.Mod, e.Hash, e.IsDir, 0); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func main() {
	fmt.Println("E2E-local-download: start")

	// Prepare remote dir and files
	remoteDir, _ := os.MkdirTemp("", "e2e-remote-*")
	defer os.RemoveAll(remoteDir)
	os.MkdirAll(remoteDir, 0755)
	// create files
	helloPath := filepath.Join(remoteDir, "hello.txt")
	testPath := filepath.Join(remoteDir, "test.txt")
	os.WriteFile(helloPath, []byte("hello world"), 0644)
	os.WriteFile(testPath, []byte("this is test"), 0644)

	// compute hashes
	h1, _ := computeXXHashHex(helloPath)
	h2, _ := computeXXHashHex(testPath)

	// Create index DB file
	dbFile, _ := os.CreateTemp("", "index-*.db")
	dbFile.Close()
	entries := []struct {
		Path  string
		Rel   string
		Size  int64
		Mod   int64
		Hash  string
		IsDir int
	}{}
	info1, _ := os.Stat(helloPath)
	info2, _ := os.Stat(testPath)
	entries = append(entries, struct {
		Path, Rel string
		Size      int64
		Mod       int64
		Hash      string
		IsDir     int
	}{Path: filepath.ToSlash(helloPath), Rel: "hello.txt", Size: info1.Size(), Mod: info1.ModTime().UnixNano(), Hash: h1, IsDir: 0})
	entries = append(entries, struct {
		Path, Rel string
		Size      int64
		Mod       int64
		Hash      string
		IsDir     int
	}{Path: filepath.ToSlash(testPath), Rel: "test.txt", Size: info2.Size(), Mod: info2.ModTime().UnixNano(), Hash: h2, IsDir: 0})
	if err := createIndexDB(dbFile.Name(), entries); err != nil {
		fmt.Printf("failed create db: %v\n", err)
		os.Exit(1)
	}

	// Prepare local destination
	localDst, _ := os.MkdirTemp("", "e2e-dst-*")
	defer os.RemoveAll(localDst)

	// Build executor and fake SSH client
	e := executor.NewExecutor()
	client := &LocalSSHClient{}

	// Build step with Ignores: ignore *.txt but re-include test.txt
	step := &types.Step{
		Source:      remoteDir,
		Destination: localDst,
		Direction:   "download",
		Ignores:     []string{"*.txt", "!test.txt", ".sync_temp"},
	}

	// Call AgentDownloadWithDB (vars empty)
	if err := e.AgentDownloadWithDB(step, client, dbFile.Name(), remoteDir, "linux"); err != nil {
		fmt.Printf("performAgentDownloadWithFilter failed: %v\n", err)
		os.Exit(1)
	}

	// List files in destination
	fmt.Println("Files in destination:")
	filepath.Walk(localDst, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(localDst, p)
		if rel == "." {
			return nil
		}
		fmt.Println(" -", rel)
		return nil
	})

	fmt.Println("E2E-local-download: done")
}
