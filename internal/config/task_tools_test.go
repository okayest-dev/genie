package config

import (
	"testing"
)

func TestTaskToolsConfigParsing(t *testing.T) {
	tomlContent := `
provider = "anthropic"

[task_tools]
enable = true
command_timeout = 30
`
	cfg, err := Parse([]byte(tomlContent), "/tmp", map[string]string{})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if !cfg.TaskTools.Enable {
		t.Error("TaskTools.Enable should be true")
	}
	if cfg.TaskTools.CommandTimeout != 30*1e9 { // 30 seconds in nanoseconds
		t.Errorf("TaskTools.CommandTimeout = %v, want 30s", cfg.TaskTools.CommandTimeout)
	}
}

func TestTaskToolsConfigDefaults(t *testing.T) {
	tomlContent := `
provider = "anthropic"
`
	cfg, err := Parse([]byte(tomlContent), "/tmp", map[string]string{})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if cfg.TaskTools.Enable {
		t.Error("TaskTools.Enable should default to false")
	}
	if cfg.TaskTools.CommandTimeout != 30*1e9 { // default 30 seconds
		t.Errorf("TaskTools.CommandTimeout = %v, want 30s default", cfg.TaskTools.CommandTimeout)
	}
}