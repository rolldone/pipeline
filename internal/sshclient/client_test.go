package sshclient

import (
	"testing"
)

func TestParseJumpHost(t *testing.T) {
	client := &SSHClient{}

	tests := []struct {
		input        string
		expectedHost string
		expectedPort string
	}{
		{"jump.example.com", "jump.example.com", "22"},
		{"jump.example.com:2222", "jump.example.com", "2222"},
		{"user@jump.example.com", "jump.example.com", "22"},
		{"user@jump.example.com:2222", "jump.example.com", "2222"},
		{"192.168.1.100", "192.168.1.100", "22"},
		{"192.168.1.100:8022", "192.168.1.100", "8022"},
	}

	for _, test := range tests {
		host, port := client.parseJumpHost(test.input)
		if host != test.expectedHost || port != test.expectedPort {
			t.Errorf("parseJumpHost(%q) = (%q, %q), expected (%q, %q)",
				test.input, host, port, test.expectedHost, test.expectedPort)
		}
	}
}
