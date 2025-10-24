package indexer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSimpleIgnoreCache_AuthoritativeDevsyncIgnores(t *testing.T) {
	// create temp dir
	dir := t.TempDir()
	// create a .sync_ignore in a nested dir that would normally ignore foo.txt
	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	syncIgnorePath := filepath.Join(nested, ".sync_ignore")
	if err := os.WriteFile(syncIgnorePath, []byte("foo.txt\n"), 0644); err != nil {
		t.Fatalf("write .sync_ignore: %v", err)
	}

	// create devsync config in root .sync_temp with ignores that do NOT include foo.txt
	cfgDir := filepath.Join(dir, ".sync_temp")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("mkdir .sync_temp: %v", err)
	}
	cfg := map[string]map[string]interface{}{"devsync": {"ignores": []string{}, "manual_transfer": []string{}}}
	// explicit empty list means no ignores from devsync
	cfg["devsync"]["ignores"] = []string{"**/*.bak"}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), data, 0644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	// create the file that would normally be ignored
	foo := filepath.Join(nested, "foo.txt")
	if err := os.WriteFile(foo, []byte("hello"), 0644); err != nil {
		t.Fatalf("write foo: %v", err)
	}

	// create cache and check behavior
	ic := NewSimpleIgnoreCache(dir)

	// Because devsync.ignores exists and is authoritative, we expect foo.txt NOT to be ignored
	if ic.Match(foo, false) {
		t.Fatalf("expected foo.txt not to be ignored when devsync.ignores is authoritative")
	}
}

func TestSimpleIgnoreCache_ManualTransferOverride(t *testing.T) {
	// create temp dir
	dir := t.TempDir()

	// create devsync config with ignores that include "vendor" and manual_transfer that includes "vendor"
	cfgDir := filepath.Join(dir, ".sync_temp")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("mkdir .sync_temp: %v", err)
	}
	cfg := map[string]map[string]interface{}{
		"devsync": {
			"ignores":         []string{"vendor", "**/vendor", "*vendor*"},
			"manual_transfer": []string{"vendor"},
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), data, 0644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	// create vendor directory and files
	vendorDir := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendorDir, 0755); err != nil {
		t.Fatalf("mkdir vendor: %v", err)
	}

	vendorFile := filepath.Join(vendorDir, "lib.go")
	if err := os.WriteFile(vendorFile, []byte("package vendor"), 0644); err != nil {
		t.Fatalf("write vendor/lib.go: %v", err)
	}

	// create cache and check behavior
	ic := NewSimpleIgnoreCache(dir)

	// vendor/ directory should NOT be ignored (manual transfer override)
	if ic.MatchWithManualTransfer(vendorDir, true) {
		t.Fatalf("expected vendor/ directory not to be ignored due to manual_transfer")
	}

	// vendor/lib.go should NOT be ignored (belongs to manual transfer endpoint)
	if ic.MatchWithManualTransfer(vendorFile, false) {
		t.Fatalf("expected vendor/lib.go not to be ignored due to manual_transfer")
	}

	// Test file outside manual transfer should still be ignored
	normalIgnoreFile := filepath.Join(dir, "some_vendor_file.txt")
	if err := os.WriteFile(normalIgnoreFile, []byte("content"), 0644); err != nil {
		t.Fatalf("write some_vendor_file.txt: %v", err)
	}

	// This file should be ignored because it matches "**/vendor" pattern (contains "vendor" in filename)
	if !ic.MatchWithManualTransfer(normalIgnoreFile, false) {
		t.Fatalf("expected some_vendor_file.txt to be ignored by '**/*vendor*' pattern")
	}
}
