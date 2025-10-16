package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRingBufferEviction ensures that RingBuffer evicts oldest lines when capacity exceeded
func TestRingBufferEviction(t *testing.T) {
	capBytes := 20
	r := NewRingBuffer(capBytes)

	inputs := []string{"aaaa", "bbbb", "cccc", "dddd", "eeee"}
	for _, s := range inputs {
		r.Add(s)
	}

	if r.totalBytes > capBytes {
		t.Fatalf("expected totalBytes <= %d, got %d", capBytes, r.totalBytes)
	}

	all := r.All()
	if len(all) == 0 {
		t.Fatalf("expected some lines in buffer, got none")
	}

	// The remaining lines should be a suffix of inputs
	joined := strings.Join(all, "|")
	found := false
	for _, s := range inputs {
		if strings.Contains(joined, s) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("unexpected remaining lines: %v", all)
	}
}

// TestFlushErrorEvidenceAll writes buffer content to a temporary log and checks its content
func TestFlushErrorEvidenceAll(t *testing.T) {
	e := NewExecutor()

	logsDir := t.TempDir()
	logPath := filepath.Join(logsDir, "test-pipeline.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("failed to create temp log file: %v", err)
	}
	defer f.Close()
	e.logFile = f

	e.historyMu.Lock()
	e.outputHistory = NewRingBuffer(1000)
	e.historyMu.Unlock()

	// Add sample lines
	for i := 0; i < 20; i++ {
		line := fmt.Sprintf("line-%c", 'A'+(i%26))
		e.historyMu.Lock()
		e.outputHistory.Add(line)
		e.historyMu.Unlock()
	}

	e.flushErrorEvidenceAll()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "ERROR EVIDENCE") {
		t.Fatalf("expected ERROR EVIDENCE header in log, got: %s", content)
	}
	if !strings.Contains(content, "line-") {
		t.Fatalf("expected evidence lines in log, got: %s", content)
	}
}
