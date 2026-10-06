package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigFile(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := `
provider = "anthropic"

[task_tools]
enable = true
command_timeout = 30
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Read the file and parse it
	file, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}

	cfg, err := Parse(file, tmpDir, map[string]string{})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if !cfg.TaskTools.Enable {
		t.Error("TaskTools.Enable should be true")
	}
	if cfg.TaskTools.CommandTimeout != 30*1e9 {
		t.Errorf("TaskTools.CommandTimeout = %v, want 30s", cfg.TaskTools.CommandTimeout)
	}
}