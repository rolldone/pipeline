package sshclient

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
)

// SSHTunnel represents an SSH tunnel for forwarding traffic
type SSHTunnel struct {
	localAddr  string
	remoteAddr string
	sshClient  *SSHClient
	listener   net.Listener
	stopChan   chan bool
	wg         sync.WaitGroup
}

// Use ssh package to avoid import error
var _ ssh.Conn = (*ssh.Client)(nil)

// NewSSHTunnel creates a new SSH tunnel
func NewSSHTunnel(sshClient *SSHClient, localPort, remoteHost, remotePort string) *SSHTunnel {
	localAddr := fmt.Sprintf("localhost:%s", localPort)
	remoteAddr := fmt.Sprintf("%s:%s", remoteHost, remotePort)

	return &SSHTunnel{
		localAddr:  localAddr,
		remoteAddr: remoteAddr,
		sshClient:  sshClient,
		stopChan:   make(chan bool),
	}
}

// Start starts the SSH tunnel
func (t *SSHTunnel) Start() error {
	if t.sshClient.client == nil {
		return fmt.Errorf("SSH client not connected")
	}

	// Start local listener
	listener, err := net.Listen("tcp", t.localAddr)
	if err != nil {
		return fmt.Errorf("failed to start local listener: %v", err)
	}
	t.listener = listener

	fmt.Printf("🚇 SSH Tunnel started: %s -> %s\n", t.localAddr, t.remoteAddr)

	// Accept connections in a goroutine
	t.wg.Add(1)
	go t.acceptConnections()

	return nil
}

// acceptConnections accepts and forwards connections
func (t *SSHTunnel) acceptConnections() {
	defer t.wg.Done()

	for {
		select {
		case <-t.stopChan:
			return
		default:
			conn, err := t.listener.Accept()
			if err != nil {
				fmt.Printf("⚠️  Failed to accept connection: %v\n", err)
				continue
			}

			// Handle connection in a new goroutine
			go t.handleConnection(conn)
		}
	}
}

// handleConnection forwards data between local and remote connections
func (t *SSHTunnel) handleConnection(localConn net.Conn) {
	defer localConn.Close()

	// Establish remote connection through SSH
	remoteConn, err := t.sshClient.client.Dial("tcp", t.remoteAddr)
	if err != nil {
		fmt.Printf("⚠️  Failed to connect to remote: %v\n", err)
		return
	}
	defer remoteConn.Close()

	// Forward data in both directions
	t.wg.Add(2)
	go t.forward(localConn, remoteConn)
	go t.forward(remoteConn, localConn)

	t.wg.Wait()
}

// forward forwards data from src to dst
func (t *SSHTunnel) forward(src, dst net.Conn) {
	defer t.wg.Done()
	io.Copy(dst, src)
}

// Stop stops the SSH tunnel
func (t *SSHTunnel) Stop() error {
	close(t.stopChan)

	if t.listener != nil {
		t.listener.Close()
	}

	t.wg.Wait()
	fmt.Println("🚇 SSH Tunnel stopped")
	return nil
}

// GetLocalAddr returns the local address of the tunnel
func (t *SSHTunnel) GetLocalAddr() string {
	return t.localAddr
}

// SetupWebsocketTunnel sets up an SSH tunnel for websocket traffic
func (c *SSHClient) SetupWebsocketTunnel(localPort, remoteHost string, remotePort int) (*SSHTunnel, error) {
	if c.client == nil {
		return nil, fmt.Errorf("SSH client not connected")
	}

	remotePortStr := strconv.Itoa(remotePort)
	tunnel := NewSSHTunnel(c, localPort, remoteHost, remotePortStr)

	if err := tunnel.Start(); err != nil {
		return nil, fmt.Errorf("failed to start websocket tunnel: %v", err)
	}

	return tunnel, nil
}
