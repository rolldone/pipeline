package executor

import (
	"os"
	"path/filepath"
	"testing"

	"pipeline/internal/pipeline/types"
)

type mockSSHClient struct {
	connected bool
	uploads   []struct {
		Data       []byte
		RemotePath string
		Perm       os.FileMode
	}
}

func (m *mockSSHClient) Connect() error { m.connected = true; return nil }
func (m *mockSSHClient) Close() error   { m.connected = false; return nil }
func (m *mockSSHClient) UploadBytes(data []byte, remotePath string, perm os.FileMode) error {
	m.uploads = append(m.uploads, struct {
		Data       []byte
		RemotePath string
		Perm       os.FileMode
	}{Data: append([]byte(nil), data...), RemotePath: remotePath, Perm: perm})
	return nil
}
func (m *mockSSHClient) UploadFile(localPath, remotePath string) error   { return nil }
func (m *mockSSHClient) DownloadFile(localPath, remotePath string) error { return nil }
func (m *mockSSHClient) RunCommand(cmd string) error                     { return nil }

func TestRunWriteFileStep_RemoteUploadsUseUploadBytes(t *testing.T) {
	e := NewExecutor()
	// inject mock factory
	mock := &mockSSHClient{}
	e.NewSSHClient = func(username, privateKeyPath, password, host, port string) (SSHClient, error) {
		return mock, nil
	}

	// create a template file on disk
	tmpdir := t.TempDir()
	src := filepath.Join(tmpdir, "tmpl.txt")
	if err := os.WriteFile(src, []byte("hi {{who}}"), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	step := &types.Step{
		Name:       "remote write",
		Files:      []types.FileEntry{{Source: src, Destination: "/remote/out.txt", Perm: ""}},
		Template:   "enabled",
		SaveOutput: "wf_remote_out",
	}
	p := &types.Pipeline{ContextVariables: make(map[string]string)}
	e.pipeline = p

	job := &types.Job{Mode: "remote"}
	vars := types.Vars{"who": "friend"}

	if err := e.runWriteFileStep(step, job, map[string]interface{}{"Host": "example"}, vars); err != nil {
		t.Fatalf("runWriteFileStep failed: %v", err)
	}

	if len(mock.uploads) != 1 {
		t.Fatalf("expected 1 upload, got %d", len(mock.uploads))
	}
	up := mock.uploads[0]
	if string(up.Data) != "hi friend" {
		t.Fatalf("unexpected uploaded bytes: %q", string(up.Data))
	}
	if up.RemotePath != "/remote/out.txt" {
		t.Fatalf("unexpected remote path: %s", up.RemotePath)
	}
	// default perm should be 0755
	if up.Perm != os.FileMode(0755) {
		t.Fatalf("unexpected perm: %o", up.Perm)
	}
}
