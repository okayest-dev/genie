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
	// Default policy should be "strict"
	if cfg.TaskTools.TrackerGuardPolicy != "strict" {
		t.Errorf("TaskTools.TrackerGuardPolicy = %q, want strict", cfg.TaskTools.TrackerGuardPolicy)
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
	if cfg.TaskTools.TrackerGuardPolicy != "strict" {
		t.Errorf("TaskTools.TrackerGuardPolicy = %q, want strict default", cfg.TaskTools.TrackerGuardPolicy)
	}
}

func TestTaskToolsConfigTrackerGuardPolicy(t *testing.T) {
	tests := []struct {
		name          string
		toml          string
		expectedPolicy string
		expectError   bool
	}{
		{
			name: "strict policy",
			toml: `
provider = "anthropic"
[task_tools]
enable = true
tracker_guard_policy = "strict"
`,
			expectedPolicy: "strict",
			expectError:    false,
		},
		{
			name: "permissive policy",
			toml: `
provider = "anthropic"
[task_tools]
enable = true
tracker_guard_policy = "permissive"
`,
			expectedPolicy: "permissive",
			expectError:    false,
		},
		{
			name: "off policy",
			toml: `
provider = "anthropic"
[task_tools]
enable = true
tracker_guard_policy = "off"
`,
			expectedPolicy: "off",
			expectError:    false,
		},
		{
			name: "invalid policy",
			toml: `
provider = "anthropic"
[task_tools]
enable = true
tracker_guard_policy = "invalid"
`,
			expectedPolicy: "",
			expectError:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tc.toml), "/tmp", map[string]string{})
			if tc.expectError {
				if err == nil {
					t.Fatal("expected error for invalid policy")
				}
				if !contains(err.Error(), "tracker_guard_policy") {
					t.Errorf("error = %q, want tracker_guard_policy validation error", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			if cfg.TaskTools.TrackerGuardPolicy != tc.expectedPolicy {
				t.Errorf("TrackerGuardPolicy = %q, want %q", cfg.TaskTools.TrackerGuardPolicy, tc.expectedPolicy)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}