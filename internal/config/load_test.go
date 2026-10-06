package config

import (
	"testing"
)

func TestLoadRealConfig(t *testing.T) {
	// Test loading the actual config file
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	t.Logf("TaskTools.Enable: %v", cfg.TaskTools.Enable)
	t.Logf("TaskTools.CommandTimeout: %v", cfg.TaskTools.CommandTimeout)
}