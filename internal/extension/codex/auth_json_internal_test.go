package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAuthJSONWritesValidContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "auth.json")
	if err := writeAuthJSON(path, "sk-test-token"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	var parsed map[string]string
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("auth.json is not valid JSON: %v", err)
	}
	if parsed["openai_api_key"] != "sk-test-token" {
		t.Fatalf("unexpected token: %q", parsed["openai_api_key"])
	}
}

func TestWriteAuthJSONReturnsErrorOnUnwritablePath(t *testing.T) {
	// A path whose parent is an existing regular file cannot be created.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := writeAuthJSON(filepath.Join(file, "auth.json"), "tok"); err == nil {
		t.Fatal("expected error when parent path is a file, got nil")
	}
}
