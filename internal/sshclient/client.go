package sshclient

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"pipeline/internal/util"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// shellEscape escapes a string for safe single-quoted inclusion in a shell command.
// It wraps s in single quotes and escapes any existing single quotes.
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// SSHClient represents an SSH client connection
type SSHClient struct {
	client       *ssh.Client
	config       *ssh.ClientConfig
	host         string
	port         string
	persistent   bool
	session      *ssh.Session // Persistent session for continuous commands
	agentSession *ssh.Session // Persistent session for agent monitoring
}

// NewSSHClient creates a new SSH client
// NewSSHClient creates a new SSH client. If password is provided it will be
// used as the first auth method (password-first). If privateKeyPath is
// provided, the key will be added as an additional auth method. At least one
// auth method must be configured.
func NewSSHClient(username, privateKeyPath, password, host, port string) (*SSHClient, error) {
	var authMethods []ssh.AuthMethod

	// Prefer password auth if provided
	if password != "" {
		authMethods = append(authMethods, ssh.Password(password))
	}

	// If a private key is provided, try to read and add it as an auth method.
	if privateKeyPath != "" {
		key, err := os.ReadFile(privateKeyPath)
		if err != nil {
			// If password auth is available, continue with a warning; otherwise fail
			if len(authMethods) == 0 {
				return nil, fmt.Errorf("unable to read private key: %v", err)
			}
		} else {
			signer, err := ssh.ParsePrivateKey(key)
			if err != nil {
				if len(authMethods) == 0 {
					return nil, fmt.Errorf("unable to parse private key: %v", err)
				}
			} else {
				// append publickey auth after (or before) password depending on preference
				authMethods = append(authMethods, ssh.PublicKeys(signer))
			}
		}
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no authentication method configured (provide password or privateKeyPath)")
	}

	// SSH client config
	config := &ssh.ClientConfig{
		User:            username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // For development - should be improved for production
		Timeout:         30 * time.Second,
	}

	return &SSHClient{
		config:     config,
		host:       host,
		port:       port,
		persistent: false,
	}, nil
}

// NewPersistentSSHClient creates a new SSH client with persistent connection
func NewPersistentSSHClient(username, privateKeyPath, password, host, port string) (*SSHClient, error) {
	client, err := NewSSHClient(username, privateKeyPath, password, host, port)
	if err != nil {
		return nil, err
	}
	client.persistent = true
	return client, nil
}

// Connect establishes the SSH connection
func (c *SSHClient) Connect() error {
	addr := fmt.Sprintf("%s:%s", c.host, c.port)
	client, err := ssh.Dial("tcp", addr, c.config)
	if err != nil {
		return fmt.Errorf("failed to dial: %v", err)
	}
	c.client = client
	return nil
}

// Close closes the SSH connection
func (c *SSHClient) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// UploadFile uploads a local file to remote server
func (c *SSHClient) UploadFile(localPath, remotePath string) error {
	// Detect if remotePath looks like a Windows absolute path (e.g. C:\...)
	isWindowsRemote := false
	if (len(remotePath) >= 3 && remotePath[1] == ':' && (remotePath[2] == '\\' || remotePath[2] == '/')) || strings.HasPrefix(remotePath, "\\\\") {
		isWindowsRemote = true
	}

	// Keep original remotePath for Windows; normalize for POSIX otherwise
	var remotePathForScp string
	if isWindowsRemote {
		remotePathForScp = remotePath
	} else {
		// Defensive normalize remotePath: convert Windows backslashes to POSIX forward slashes
		remotePath = strings.ReplaceAll(remotePath, "\\", "/")
		remotePath = path.Clean(remotePath)
		remotePathForScp = remotePath
	}

	// Open local file
	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %v", err)
	}
	defer localFile.Close()

	// Get file info for size
	stat, err := localFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat local file: %v", err)
	}

	// Create remote file
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	// Create remote directory if it doesn't exist
	var remoteDir string
	if isWindowsRemote {
		// find last slash or backslash
		idx := strings.LastIndexAny(remotePathForScp, "\\/")
		if idx == -1 {
			remoteDir = "."
		} else {
			remoteDir = remotePathForScp[:idx]
		}
		mkdirCmd := fmt.Sprintf("cmd.exe /C if not exist \"%s\" mkdir \"%s\"", remoteDir, remoteDir)
		if err := c.RunCommand(mkdirCmd); err != nil {
			return fmt.Errorf("failed to create remote directory (windows): %v", err)
		}
	} else {
		remoteDir = path.Dir(remotePathForScp)
		// quote remoteDir to protect spaces/special chars
		mkdirCmd := fmt.Sprintf("mkdir -p '%s'", remoteDir)
		if err := c.RunCommand(mkdirCmd); err != nil {
			return fmt.Errorf("failed to create remote directory: %v", err)
		}
	}

	// Use scp protocol properly: run scp -t on remote directory and drive protocol over stdin/stdout

	// Prepare pipes
	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stdin pipe: %v", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stdout pipe: %v", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stderr pipe: %v", err)
	}

	// Start scp in 'to' mode targeting the remote directory (not including filename)
	var targetDir string
	if isWindowsRemote {
		// compute targetDir using last index
		idx := strings.LastIndexAny(remotePathForScp, "\\/")
		if idx == -1 {
			targetDir = "."
		} else {
			targetDir = remotePathForScp[:idx]
		}

		// Convert backslashes to forward slashes for scp command
		// Even for Windows remote, scp expects forward slashes in the command
		targetDir = strings.ReplaceAll(targetDir, "\\", "/")

		// quote with double-quotes for Windows paths
		doubleQuote := func(s string) string {
			return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\""
		}
		cmd := fmt.Sprintf("scp -t %s", doubleQuote(targetDir))
		util.Default.Printf("[sshclient] starting remote scp (windows) with cmd: %s\n", cmd)
		util.Default.ClearLine()
		if err := session.Start(cmd); err != nil {
			session.Close()
			return fmt.Errorf("failed to start scp on remote: %v", err)
		}
	} else {
		// Use POSIX path quoting
		targetDir := path.Dir(remotePathForScp)
		cmd := fmt.Sprintf("scp -t %s", shellEscape(targetDir))
		util.Default.Printf("[sshclient] starting remote scp (posix) with cmd: %s\n", cmd)
		util.Default.ClearLine()
		if err := session.Start(cmd); err != nil {
			session.Close()
			return fmt.Errorf("failed to start scp on remote: %v", err)
		}
	}

	// helper to read a single-byte ACK from remote with timeout to avoid blocking forever
	readAck := func() error {
		buf := make([]byte, 1)
		ch := make(chan error, 1)

		go func() {
			if _, err := stdout.Read(buf); err != nil {
				ch <- fmt.Errorf("failed to read scp ack: %v", err)
				return
			}
			switch buf[0] {
			case 0:
				ch <- nil
			case 1, 2:
				// read error message from stderr
				msg := make([]byte, 2048)
				n, _ := stderr.Read(msg)
				ch <- fmt.Errorf("scp remote error: %s", strings.TrimSpace(string(msg[:n])))
			default:
				ch <- fmt.Errorf("unknown scp ack: %v", buf[0])
			}
		}()

		select {
		case err := <-ch:
			return err
		case <-time.After(10 * time.Second):
			return fmt.Errorf("timeout waiting for scp ack")
		}
	}

	// initial ack
	if err := readAck(); err != nil {
		stdin.Close()
		session.Wait()
		return err
	}

	// send file header: C<mode> <size> <filename>\n
	// filename should be the final component of the remote path
	var filename string
	if isWindowsRemote {
		idx := strings.LastIndexAny(remotePathForScp, "\\/")
		if idx == -1 {
			filename = remotePathForScp
		} else {
			filename = remotePathForScp[idx+1:]
		}
	} else {
		filename = path.Base(remotePathForScp)
	}

	fmt.Fprintf(stdin, "C%04o %d %s\n", stat.Mode().Perm(), stat.Size(), filename)

	if err := readAck(); err != nil {
		stdin.Close()
		session.Wait()
		return err
	}

	// send file data
	if _, err := io.Copy(stdin, localFile); err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to send file data: %v", err)
	}

	// send trailing null byte
	if _, err := fmt.Fprint(stdin, "\x00"); err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to send scp terminator: %v", err)
	}

	if err := readAck(); err != nil {
		stdin.Close()
		session.Wait()
		return err
	}

	// close stdin to indicate EOF to remote scp
	stdin.Close()

	// wait for remote scp to finish
	if err := session.Wait(); err != nil {
		return fmt.Errorf("remote scp command failed: %v", err)
	}

	return nil
}

// SyncFile is an alias for UploadFile for backward compatibility
func (c *SSHClient) SyncFile(localPath, remotePath string) error {
	return c.UploadFile(localPath, remotePath)
}

// RunCommand executes a command on the remote server
func (c *SSHClient) RunCommand(cmd string) error {
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	return session.Run(cmd)
}

// RunCommandWithOutput executes a command on the remote server and returns output
func (c *SSHClient) RunCommandWithOutput(cmd string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	output, err := session.Output(cmd)
	if err != nil {
		return "", fmt.Errorf("command failed: %v", err)
	}
	return string(output), nil
}

// RunCommandWithStreaming executes a command and streams output in real-time
func (c *SSHClient) RunCommandWithStreaming(cmd string, outputCallback func(string)) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stdout pipe: %v", err)
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to get stderr pipe: %v", err)
	}

	if err := session.Start(cmd); err != nil {
		return "", fmt.Errorf("failed to start command: %v", err)
	}

	var outputBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)

	// Read stdout
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			outputCallback(line)
			outputBuf.WriteString(line + "\n")
		}
	}()

	// Read stderr
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			outputCallback(line)
			outputBuf.WriteString(line + "\n")
		}
	}()

	wg.Wait()
	if err := session.Wait(); err != nil {
		return outputBuf.String(), fmt.Errorf("command failed: %v", err)
	}

	return outputBuf.String(), nil
}

// DownloadFile downloads a remote file to localPath using scp -f protocol
func (c *SSHClient) DownloadFile(localPath, remotePath string) error {
	if c.client == nil {
		return fmt.Errorf("SSH client not connected")
	}

	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stdin pipe: %v", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stdout pipe: %v", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stderr pipe: %v", err)
	}

	// Start scp in 'from' mode to fetch the file
	// Detect Windows-style remotes (e.g. C:\path\to\file) and use a
	// Windows-friendly quoting strategy. For POSIX remotes use single-quote
	// shell escaping.
	isWindowsRemote := false
	if (len(remotePath) >= 3 && remotePath[1] == ':' && (remotePath[2] == '\\' || remotePath[2] == '/')) || strings.HasPrefix(remotePath, "\\\\") {
		isWindowsRemote = true
	}

	if isWindowsRemote {
		// For Windows remote, convert backslashes to forward slashes for scp
		// and wrap with double quotes so cmd.exe or similar handles spaces
		// correctly.
		remotePathForScp := strings.ReplaceAll(remotePath, "\\", "/")
		// escape any double quotes inside path
		doubleQuote := func(s string) string { return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\"" }
		cmd := fmt.Sprintf("scp -f %s", doubleQuote(remotePathForScp))
		if err := session.Start(cmd); err != nil {
			session.Close()
			return fmt.Errorf("failed to start scp on remote: %v", err)
		}
	} else {
		// POSIX remote: ensure forward slashes and safe single-quote escaping
		remotePath = strings.ReplaceAll(remotePath, "\\", "/")
		remotePath = path.Clean(remotePath)
		if err := session.Start(fmt.Sprintf("scp -f %s", shellEscape(remotePath))); err != nil {
			session.Close()
			return fmt.Errorf("failed to start scp on remote: %v", err)
		}
	}

	// helper to write a single null byte to request/ack
	writeNull := func() error {
		if _, err := stdin.Write([]byte{0}); err != nil {
			return fmt.Errorf("failed to write scp null byte: %v", err)
		}
		return nil
	}

	// send initial null to start transfer
	if err := writeNull(); err != nil {
		stdin.Close()
		session.Wait()
		return err
	}

	reader := bufio.NewReader(stdout)

	// read header byte
	b, err := reader.ReadByte()
	if err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to read scp header byte: %v", err)
	}

	if b == 1 || b == 2 {
		// error; read message from stderr
		msg := make([]byte, 1024)
		n, _ := stderr.Read(msg)
		stdin.Close()
		session.Wait()
		return fmt.Errorf("scp remote error: %s", strings.TrimSpace(string(msg[:n])))
	}

	if b != 'C' {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("unexpected scp header: %v", b)
	}

	// read header line until newline
	headerLine, err := reader.ReadString('\n')
	if err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to read scp header line: %v", err)
	}

	// header format: <mode> <size> <filename>\n
	parts := strings.Fields(strings.TrimSpace(headerLine))
	if len(parts) < 3 {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("invalid scp header: %s", headerLine)
	}

	// parse size
	size64, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("invalid size in scp header: %v", err)
	}

	// create local directory if needed
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to create local directories: %v", err)
	}

	// open local file for writing
	lf, err := os.Create(localPath)
	if err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to create local file: %v", err)
	}
	defer lf.Close()

	// send null to indicate ready to receive data
	if err := writeNull(); err != nil {
		stdin.Close()
		session.Wait()
		return err
	}

	// copy exactly size bytes from reader to local file
	if _, err := io.CopyN(lf, reader, size64); err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to copy file data: %v", err)
	}

	// read the trailing byte (should be a null)
	if ack, err := reader.ReadByte(); err != nil || ack != 0 {
		// try read error message
		msg := make([]byte, 1024)
		n, _ := stderr.Read(msg)
		stdin.Close()
		session.Wait()
		if err != nil {
			return fmt.Errorf("failed after data copy: %v", err)
		}
		return fmt.Errorf("scp did not acknowledge data: %s", strings.TrimSpace(string(msg[:n])))
	}

	// send final null
	if err := writeNull(); err != nil {
		stdin.Close()
		session.Wait()
		return err
	}

	stdin.Close()
	if err := session.Wait(); err != nil {
		return fmt.Errorf("remote scp command failed: %v", err)
	}

	return nil
}

// StartPersistentSession starts a persistent SSH session for continuous commands
func (c *SSHClient) StartPersistentSession() error {
	if !c.persistent {
		return fmt.Errorf("client not configured for persistent connection")
	}

	if c.client == nil {
		return fmt.Errorf("SSH client not connected")
	}

	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create persistent session: %v", err)
	}

	c.session = session
	return nil
}

// StopPersistentSession stops the persistent session
func (c *SSHClient) StopPersistentSession() error {
	if c.session != nil {
		err := c.session.Close()
		c.session = nil
		return err
	}
	return nil
}

func (c *SSHClient) StopAgentSession() error {
	if c.agentSession != nil {
		err := c.agentSession.Close()
		return err
	}
	return nil
}

// RunCommandPersistent executes a command using the persistent session
func (c *SSHClient) RunCommandPersistent(cmd string) error {
	if c.session == nil {
		return fmt.Errorf("persistent session not started")
	}

	return c.session.Run(cmd)
}

// RunCommandWithStream executes a command and streams output and errors via channels
func (c *SSHClient) RunCommandWithStream(cmd string, usePty bool) (<-chan string, <-chan error, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create session: %v", err)
	}

	// optional PTY
	if usePty {
		if err := session.RequestPty("xterm", 80, 40, ssh.TerminalModes{}); err != nil {
			session.Close()
			return nil, nil, fmt.Errorf("failed to request pty: %v", err)
		}
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, nil, fmt.Errorf("failed to get stdout pipe: %v", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		return nil, nil, fmt.Errorf("failed to get stderr pipe: %v", err)
	}

	outCh := make(chan string, 128)
	errCh := make(chan error, 8)

	var wg sync.WaitGroup
	wg.Add(3) // stdout, stderr, waiter
	var closeSessionOnce sync.Once

	// stdout reader
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, rerr := stdout.Read(buf)
			if n > 0 {
				s := string(buf[:n])
				select {
				case outCh <- s:
				default:
					// drop if nobody reading
				}
			}
			if rerr != nil {
				if rerr != io.EOF {
					select {
					case errCh <- rerr:
					default:
					}
					closeSessionOnce.Do(func() { session.Close() })
				}
				return
			}
		}
	}()

	// stderr reader
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, rerr := stderr.Read(buf)
			if n > 0 {
				s := string(buf[:n])
				select {
				case errCh <- fmt.Errorf("stderr: %s", s):
				default:
					// drop if nobody reading
				}
			}
			if rerr != nil {
				if rerr != io.EOF {
					select {
					case errCh <- rerr:
					default:
					}
					closeSessionOnce.Do(func() { session.Close() })
				}
				return
			}
		}
	}()

	// waiter
	go func() {
		defer wg.Done()
		if err := session.Start(cmd); err != nil {
			select {
			case errCh <- fmt.Errorf("start failed: %v", err):
			default:
			}
			closeSessionOnce.Do(func() { session.Close() })
			return
		}
		if err := session.Wait(); err != nil {
			select {
			case errCh <- fmt.Errorf("command finished with error: %v", err):
			default:
			}
		}
		closeSessionOnce.Do(func() { session.Close() })
	}()

	// coordinator closes channels once all done
	go func() {
		wg.Wait()
		close(outCh)
		close(errCh)
	}()

	return outCh, errCh, nil
}

// IsPersistent returns whether this client uses persistent connections
func (c *SSHClient) IsPersistent() bool {
	return c.persistent
}

// GetSession returns the current persistent session (for advanced usage)
func (c *SSHClient) GetSession() *ssh.Session {
	return c.session
}

// CreateSession creates a new SSH session for command execution
func (c *SSHClient) CreateSession() (*ssh.Session, error) {
	return c.client.NewSession()
}

// CreatePTYSession creates a new SSH session configured for PTY usage
func (c *SSHClient) CreatePTYSession() (*ssh.Session, error) {
	if c.client == nil {
		return nil, fmt.Errorf("SSH client not connected")
	}

	session, err := c.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create PTY session: %v", err)
	}

	return session, nil
}
