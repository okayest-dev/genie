package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// env builds a minimal env map from key/value pairs.
func env(kvs ...string) map[string]string {
	out := map[string]string{}
	for i := 0; i+1 < len(kvs); i += 2 {
		out[kvs[i]] = kvs[i+1]
	}
	return out
}

// pureDefaults is the one known-good literal the tests assert against: every
// scalar and tool toggle with no config file and no env at all.
func TestPureDefaults(t *testing.T) {
	cfg, err := Parse(nil, "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse(nil, ...) returned error: %v", err)
	}
	if cfg.Model != "big-pickle" {
		t.Errorf("Model = %q, want %q", cfg.Model, "big-pickle")
	}
	if cfg.BaseURL != "https://opencode.ai/zen/v1" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "https://opencode.ai/zen/v1")
	}
	if cfg.APIKeyEnv != "OPENCODE_API_KEY" {
		t.Errorf("APIKeyEnv = %q, want %q", cfg.APIKeyEnv, "OPENCODE_API_KEY")
	}
	if cfg.APIKey != "" {
		t.Errorf("APIKey = %q, want empty (no key in env)", cfg.APIKey)
	}
	if cfg.InstructionFile != "" {
		t.Errorf("InstructionFile = %q, want empty", cfg.InstructionFile)
	}
	if cfg.SessionDir != "/home/u/og/sessions" {
		t.Errorf("SessionDir = %q, want %q", cfg.SessionDir, "/home/u/og/sessions")
	}
	if cfg.BashTimeout != 120*time.Second {
		t.Errorf("BashTimeout = %v, want %v", cfg.BashTimeout, 120*time.Second)
	}
	for name, got := range map[string]bool{
		"read": cfg.Tools.Read, "write": cfg.Tools.Write,
		"edit": cfg.Tools.Edit, "bash": cfg.Tools.Bash,
	} {
		if !got {
			t.Errorf("Tools.%s = false, want true by default", name)
		}
	}
	if cfg.Wire != "" {
		t.Errorf("Wire = %q, want empty (auto-detect by default)", cfg.Wire)
	}
	if cfg.Gateway != "" {
		t.Errorf("Gateway = %q, want empty by default", cfg.Gateway)
	}
}

// fullConfig is a config file that sets every v1 key.
const fullConfig = `model = "cfg-model"
base_url = "https://example.com/v1"
api_key_env = "OG_MY_KEY"
instruction_file = "/abs/AGENTS.md"
session_dir = "/tmp/sessions"
bash_timeout = 90

[tools]
read = true
write = false
edit = true
bash = false
`

func TestFileBeatsDefaults(t *testing.T) {
	cfg, err := Parse([]byte(fullConfig), "/home/u", env("OG_MY_KEY", "secret-key"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Model != "cfg-model" {
		t.Errorf("Model = %q, want %q", cfg.Model, "cfg-model")
	}
	if cfg.BaseURL != "https://example.com/v1" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "https://example.com/v1")
	}
	if cfg.APIKeyEnv != "OG_MY_KEY" {
		t.Errorf("APIKeyEnv = %q, want %q", cfg.APIKeyEnv, "OG_MY_KEY")
	}
	if cfg.APIKey != "secret-key" {
		t.Errorf("APIKey = %q, want %q (read from the env var named by api_key_env)", cfg.APIKey, "secret-key")
	}
	if cfg.InstructionFile != "/abs/AGENTS.md" {
		t.Errorf("InstructionFile = %q, want %q", cfg.InstructionFile, "/abs/AGENTS.md")
	}
	if cfg.SessionDir != "/tmp/sessions" {
		t.Errorf("SessionDir = %q, want %q", cfg.SessionDir, "/tmp/sessions")
	}
	if cfg.BashTimeout != 90*time.Second {
		t.Errorf("BashTimeout = %v, want %v", cfg.BashTimeout, 90*time.Second)
	}
	if cfg.Tools.Read != true || cfg.Tools.Write != false || cfg.Tools.Edit != true || cfg.Tools.Bash != false {
		t.Errorf("Tools = %+v, want read=true write=false edit=true bash=false", cfg.Tools)
	}
}

// TestEnvBeatsFile proves each of the six env overrides wins over a
// conflicting config-file value.
func TestEnvBeatsFile(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		envVars map[string]string
		check   func(*testing.T, *Config)
	}{
		{
			name:    "OG_MODEL",
			file:    `model = "file-model"`,
			envVars: env("OG_MODEL", "env-model"),
			check: func(t *testing.T, c *Config) {
				if c.Model != "env-model" {
					t.Errorf("Model = %q, want %q", c.Model, "env-model")
				}
			},
		},
		{
			name:    "OG_BASE_URL",
			file:    `base_url = "https://file.example"`,
			envVars: env("OG_BASE_URL", "https://env.example"),
			check: func(t *testing.T, c *Config) {
				if c.BaseURL != "https://env.example" {
					t.Errorf("BaseURL = %q, want %q", c.BaseURL, "https://env.example")
				}
			},
		},
		{
			name:    "OG_API_KEY_ENV",
			file:    `api_key_env = "FILE_KEY"`,
			envVars: map[string]string{"OG_API_KEY_ENV": "ENV_KEY", "ENV_KEY": "env-secret"},
			check: func(t *testing.T, c *Config) {
				if c.APIKey != "env-secret" {
					t.Errorf("APIKey = %q, want %q", c.APIKey, "env-secret")
				}
			},
		},
		{
			name:    "OG_INSTRUCTION_FILE",
			file:    `instruction_file = "/file"`,
			envVars: env("OG_INSTRUCTION_FILE", "/env"),
			check: func(t *testing.T, c *Config) {
				if c.InstructionFile != "/env" {
					t.Errorf("InstructionFile = %q, want %q", c.InstructionFile, "/env")
				}
			},
		},
		{
			name:    "OG_SESSION_DIR",
			file:    `session_dir = "/file-sessions"`,
			envVars: env("OG_SESSION_DIR", "/env-sessions"),
			check: func(t *testing.T, c *Config) {
				if c.SessionDir != "/env-sessions" {
					t.Errorf("SessionDir = %q, want %q", c.SessionDir, "/env-sessions")
				}
			},
		},
		{
			name:    "OG_BASH_TIMEOUT",
			file:    `bash_timeout = 60`,
			envVars: env("OG_BASH_TIMEOUT", "30"),
			check: func(t *testing.T, c *Config) {
				if c.BashTimeout != 30*time.Second {
					t.Errorf("BashTimeout = %v, want %v", c.BashTimeout, 30*time.Second)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tc.file), "/home/u", tc.envVars)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			tc.check(t, cfg)
		})
	}
}

func TestAPIKeyDefaultsToOPENCODE_API_KEY(t *testing.T) {
	cfg, err := Parse(nil, "/home/u", env("OPENCODE_API_KEY", "default-secret"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.APIKey != "default-secret" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "default-secret")
	}
}

func TestAPIKeyNeverReadFromFile(t *testing.T) {
	_, err := Parse([]byte("api_key = \"plaintext-secret\"\n"), "/home/u", nil)
	if err == nil {
		t.Fatal("Parse accepted an api_key key in the config file; keys must never live in the file")
	}
}

func TestMalformedTOMLFailsFast(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
	}{
		{name: "broken assignment", file: "model ="},
		{name: "unbalanced table", file: "[tools\nread = true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file), "/home/u", nil)
			if err == nil {
				t.Fatal("Parse accepted malformed TOML; want a fail-fast error")
			}
		})
	}
}

func TestUnknownKeysFailFast(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
	}{
		{name: "typo base_url", file: "bas_url = \"https://example.com\""},
		{name: "unknown tool", file: "[tools]\nls = true"},
		{name: "unknown scalar", file: "theme = \"dark\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file), "/home/u", nil)
			if err == nil {
				t.Fatalf("Parse accepted %q; want an unknown-key error", tc.file)
			}
		})
	}
}

func TestToolsDefaultTrueWhenTableOmitted(t *testing.T) {
	cfg, err := Parse([]byte("model = \"m\"\n"), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.Tools.Read || !cfg.Tools.Write || !cfg.Tools.Edit || !cfg.Tools.Bash {
		t.Errorf("Tools = %+v, want all true when [tools] omitted", cfg.Tools)
	}
}

func TestToolsPartialTable(t *testing.T) {
	cfg, err := Parse([]byte("[tools]\nread = false\n"), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Tools.Read {
		t.Errorf("Tools.Read = true, want false")
	}
	if !cfg.Tools.Write || !cfg.Tools.Edit || !cfg.Tools.Bash {
		t.Errorf("Tools = %+v, want write/edit/bash still true", cfg.Tools)
	}
}

func TestBashTimeoutEnvMustParse(t *testing.T) {
	_, err := Parse(nil, "/home/u", env("OG_BASH_TIMEOUT", "not-a-number"))
	if err == nil {
		t.Fatal("Parse accepted a non-numeric OG_BASH_TIMEOUT; want an error")
	}
}

func TestBashTimeoutMustBePositive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		envVars map[string]string
	}{
		{name: "file zero", file: "bash_timeout = 0", envVars: nil},
		{name: "file negative", file: "bash_timeout = -5", envVars: nil},
		{name: "env zero", envVars: env("OG_BASH_TIMEOUT", "0")},
		{name: "env negative", envVars: env("OG_BASH_TIMEOUT", "-5")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file), "/home/u", tc.envVars)
			if err == nil {
				t.Fatalf("Parse accepted %q; want an error for a non-positive timeout", tc.file)
			}
		})
	}
}

func TestEmptyEnvVarMeansUnset(t *testing.T) {
	cfg, err := Parse([]byte("model = \"file-model\"\n"), "/home/u", env("OG_MODEL", ""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Model != "file-model" {
		t.Errorf("Model = %q, want %q (empty env var must not override)", cfg.Model, "file-model")
	}
}

// captureInfo sets up slog at LevelInfo writing to a buffer and returns it.
// Call restoreInfo afterwards to reset the default handler.
func captureInfo(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() {
		slog.SetDefault(slog.New(slog.NewTextHandler(nil, &slog.HandlerOptions{Level: slog.LevelWarn})))
	})
	return &buf
}

func TestParseLogsResolvedConfig(t *testing.T) {
	buf := captureInfo(t)
	_, err := Parse([]byte("model = \"cfg-model\"\nbase_url = \"https://example.com\"\n"), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"model=cfg-model", "base_url=https://example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q:\n%s", want, out)
		}
	}
}

func TestParseLogsEnvVarsApplied(t *testing.T) {
	buf := captureInfo(t)
	_, err := Parse(nil, "/home/u", env("OG_MODEL", "env-model"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "OG_MODEL") {
		t.Errorf("log output missing env var name OG_MODEL:\n%s", out)
	}
	// The env var name appears, but its value must not appear in the
	// "env overrides applied" line.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "env overrides applied") && strings.Contains(line, "env-model") {
			t.Errorf("env overrides line must not contain env var value:\n%s", line)
		}
	}
}

func TestParseDoesNotLogAPIKeyValue(t *testing.T) {
	buf := captureInfo(t)
	_, err := Parse(nil, "/home/u", env("OPENCODE_API_KEY", "secret-key"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "secret-key") {
		t.Errorf("log output must not contain API key value:\n%s", out)
	}
}

func TestWireAndGatewayFromFile(t *testing.T) {
	cfg, err := Parse([]byte(`wire = "anthropic"
gateway = "https://gateway.example.com"
`), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Wire != "anthropic" {
		t.Errorf("Wire = %q, want %q", cfg.Wire, "anthropic")
	}
	if cfg.Gateway != "https://gateway.example.com" {
		t.Errorf("Gateway = %q, want %q", cfg.Gateway, "https://gateway.example.com")
	}
}

func TestOGWireEnvBeatsFile(t *testing.T) {
	cfg, err := Parse([]byte(`wire = "openai"
`), "/home/u", env("OG_WIRE", "google"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Wire != "google" {
		t.Errorf("Wire = %q, want %q (OG_WIRE env should override file)", cfg.Wire, "google")
	}
}

func TestOGGatewayEnvBeatsFile(t *testing.T) {
	cfg, err := Parse([]byte(`gateway = "https://file-gateway.example.com"
`), "/home/u", env("OG_GATEWAY", "https://env-gateway.example.com"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Gateway != "https://env-gateway.example.com" {
		t.Errorf("Gateway = %q, want %q (OG_GATEWAY env should override file)", cfg.Gateway, "https://env-gateway.example.com")
	}
}

func TestEmptyOGWireEnvDoesNotOverrideFile(t *testing.T) {
	cfg, err := Parse([]byte(`wire = "anthropic"
`), "/home/u", env("OG_WIRE", ""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Wire != "anthropic" {
		t.Errorf("Wire = %q, want %q (empty env must not override)", cfg.Wire, "anthropic")
	}
}

func TestEmptyOGGatewayEnvDoesNotOverrideFile(t *testing.T) {
	cfg, err := Parse([]byte(`gateway = "https://file-gw.example.com"
`), "/home/u", env("OG_GATEWAY", ""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Gateway != "https://file-gw.example.com" {
		t.Errorf("Gateway = %q, want %q (empty env must not override)", cfg.Gateway, "https://file-gw.example.com")
	}
}

func TestInvalidWireNameFailsFast(t *testing.T) {
	_, err := Parse([]byte(`wire = "typo"
`), "/home/u", nil)
	if err == nil {
		t.Fatal("Parse accepted unknown wire name; want an error")
	}
}

func TestInvalidWireNameFromEnvFailsFast(t *testing.T) {
	_, err := Parse(nil, "/home/u", env("OG_WIRE", "bogus"))
	if err == nil {
		t.Fatal("Parse accepted unknown wire name from env; want an error")
	}
}
