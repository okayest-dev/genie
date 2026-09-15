package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
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
// scalar and tool toggle with no config file and no env at all. There is no
// top-level global model/base_url/api_key_env/wire/gateway — boot parameters
// live in each provider's table (og-z1m.8).
func TestPureDefaults(t *testing.T) {
	cfg, err := Parse(nil, "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse(nil, ...) returned error: %v", err)
	}
	if cfg.Provider != "" {
		t.Errorf("Provider = %q, want empty (unset selector prompts)", cfg.Provider)
	}
	if cfg.InstructionFile != "" {
		t.Errorf("InstructionFile = %q, want empty", cfg.InstructionFile)
	}
	if cfg.SessionDir != "/home/u/genie/sessions" {
		t.Errorf("SessionDir = %q, want %q", cfg.SessionDir, "/home/u/genie/sessions")
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
	if cfg.Context.Turns != 0 {
		t.Errorf("Context.Turns = %d, want 0 (unlimited history) by default", cfg.Context.Turns)
	}
	if cfg.Context.BudgetTokens != 0 {
		t.Errorf("Context.BudgetTokens = %d, want 0 (unset → percent) by default", cfg.Context.BudgetTokens)
	}
	if cfg.Context.BudgetPercent != 75 {
		t.Errorf("Context.BudgetPercent = %v, want 75 by default", cfg.Context.BudgetPercent)
	}
	if len(cfg.Context.Windows) != 0 {
		t.Errorf("Context.Windows = %v, want empty by default", cfg.Context.Windows)
	}
}

// fullConfig is a config file that sets every remaining v1 key (the flat
// model/base_url/api_key_env/wire/gateway keys no longer exist — a provider's
// table is the only home for those).
const fullConfig = `provider = "zen"
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
	cfg, err := Parse([]byte(fullConfig), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Provider != "zen" {
		t.Errorf("Provider = %q, want %q", cfg.Provider, "zen")
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

// TestEnvBeatsFile proves each of the surviving env overrides wins over a
// conflicting config-file value.
func TestEnvBeatsFile(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		envVars map[string]string
		check   func(*testing.T, *Config)
	}{
		{
			name:    "GENIE_PROVIDER",
			file:    `provider = "file-provider"`,
			envVars: env("GENIE_PROVIDER", "env-provider"),
			check: func(t *testing.T, c *Config) {
				if c.Provider != "env-provider" {
					t.Errorf("Provider = %q, want %q", c.Provider, "env-provider")
				}
			},
		},
		{
			name:    "GENIE_INSTRUCTION_FILE",
			file:    `instruction_file = "/file"`,
			envVars: env("GENIE_INSTRUCTION_FILE", "/env"),
			check: func(t *testing.T, c *Config) {
				if c.InstructionFile != "/env" {
					t.Errorf("InstructionFile = %q, want %q", c.InstructionFile, "/env")
				}
			},
		},
		{
			name:    "GENIE_SESSION_DIR",
			file:    `session_dir = "/file-sessions"`,
			envVars: env("GENIE_SESSION_DIR", "/env-sessions"),
			check: func(t *testing.T, c *Config) {
				if c.SessionDir != "/env-sessions" {
					t.Errorf("SessionDir = %q, want %q", c.SessionDir, "/env-sessions")
				}
			},
		},
		{
			name:    "GENIE_BASH_TIMEOUT",
			file:    `bash_timeout = 60`,
			envVars: env("GENIE_BASH_TIMEOUT", "30"),
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

// TestTopLevelFlatKeysRejected pins og-z1m.8 AC1: the flat top-level keys
// model/base_url/wire/api_key_env/gateway no longer parse — a config still
// carrying one fails fast as an unknown key. Their boot parameters now live
// only in [providers.*] tables.
func TestTopLevelFlatKeysRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
	}{
		{name: "model", file: `model = "big-pickle"`},
		{name: "base_url", file: `base_url = "https://example.com"`},
		{name: "api_key_env", file: `api_key_env = "MY_KEY"`},
		{name: "wire", file: `wire = "openai"`},
		{name: "gateway", file: `gateway = "https://gw.example.com"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file), "/home/u", nil)
			if err == nil {
				t.Fatalf("Parse accepted a top-level %q key; want an unknown-key error (og-z1m.8)", tc.name)
			}
			if !strings.Contains(err.Error(), "unknown key") {
				t.Errorf("error = %q, want an unknown-key error", err)
			}
		})
	}
}

// TestEnvOverridesForFlatKeysGone pins og-z1m.8 AC1: the env overrides for the
// removed flat keys are inert — they are no longer read, so a set-but-inert
// var leaves the config on pure defaults and is never reported as applied.
func TestEnvOverridesForFlatKeysGone(t *testing.T) {
	buf := captureInfo(t)
	cfg, err := Parse(nil, "/home/u", env(
		"GENIE_MODEL", "env-model",
		"GENIE_BASE_URL", "https://env.example",
		"GENIE_API_KEY_ENV", "ENV_KEY",
		"GENIE_WIRE", "google",
		"GENIE_GATEWAY", "https://env-gw.example",
	))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Provider != "" {
		t.Errorf("Provider = %q, want empty (flat-key env vars must not fire)", cfg.Provider)
	}
	out := buf.String()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "env overrides applied") && strings.Contains(line, "GENIE_MODEL") {
			t.Errorf("env overrides line still lists GENIE_MODEL:\n%s", line)
		}
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
	cfg, err := Parse([]byte("provider = \"zen\"\n"), "/home/u", nil)
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
	_, err := Parse(nil, "/home/u", env("GENIE_BASH_TIMEOUT", "not-a-number"))
	if err == nil {
		t.Fatal("Parse accepted a non-numeric GENIE_BASH_TIMEOUT; want an error")
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
		{name: "env zero", envVars: env("GENIE_BASH_TIMEOUT", "0")},
		{name: "env negative", envVars: env("GENIE_BASH_TIMEOUT", "-5")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file), "/home/u", tc.envVars)
			if err == nil {
				t.Fatalf("Parse accepted %q; want an error for a non-positive timeout", tc.file)
			}
		})
	}
}

// TestEmptyEnvVarMeansUnset: a set-but-empty env var leaves the file value in
// place for the surviving selector key.
func TestEmptyEnvVarMeansUnset(t *testing.T) {
	cfg, err := Parse([]byte("provider = \"file-provider\"\n"), "/home/u", env("GENIE_PROVIDER", ""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Provider != "file-provider" {
		t.Errorf("Provider = %q, want %q (empty env var must not override)", cfg.Provider, "file-provider")
	}
}

func TestPluginWireStreamTimeoutDefault(t *testing.T) {
	cfg, err := Parse(nil, "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.PluginWireStreamTimeout != 10*time.Minute {
		t.Errorf("PluginWireStreamTimeout = %v, want the default 10m", cfg.PluginWireStreamTimeout)
	}
}

func TestPluginWireStreamTimeoutFromFile(t *testing.T) {
	cfg, err := Parse([]byte("[plugins]\nwire_stream_timeout = 600\n"), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.PluginWireStreamTimeout != 600*time.Second {
		t.Errorf("PluginWireStreamTimeout = %v, want 600s from file", cfg.PluginWireStreamTimeout)
	}
}

func TestPluginWireStreamTimeoutEnvOverridesFile(t *testing.T) {
	cfg, err := Parse([]byte("[plugins]\nwire_stream_timeout = 600\n"), "/home/u",
		env("GENIE_PLUGIN_WIRE_STREAM_TIMEOUT", "300"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.PluginWireStreamTimeout != 300*time.Second {
		t.Errorf("PluginWireStreamTimeout = %v, want 300s (env overrides file)", cfg.PluginWireStreamTimeout)
	}
}

func TestPluginWireStreamTimeoutMustBePositive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		envVars map[string]string
	}{
		{name: "file zero", file: "[plugins]\nwire_stream_timeout = 0", envVars: nil},
		{name: "file negative", file: "[plugins]\nwire_stream_timeout = -5", envVars: nil},
		{name: "env zero", envVars: env("GENIE_PLUGIN_WIRE_STREAM_TIMEOUT", "0")},
		{name: "env non-numeric", envVars: env("GENIE_PLUGIN_WIRE_STREAM_TIMEOUT", "lots")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file), "/home/u", tc.envVars)
			if err == nil {
				t.Fatalf("Parse accepted %q %v; want an error for a non-positive stream timeout", tc.file, tc.envVars)
			}
		})
	}
}

func TestEmptyPluginWireStreamTimeoutEnvDoesNotOverride(t *testing.T) {
	cfg, err := Parse([]byte("[plugins]\nwire_stream_timeout = 600\n"), "/home/u",
		env("GENIE_PLUGIN_WIRE_STREAM_TIMEOUT", ""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.PluginWireStreamTimeout != 600*time.Second {
		t.Errorf("PluginWireStreamTimeout = %v, want 600s (empty env must not override)", cfg.PluginWireStreamTimeout)
	}
}

func TestContextTurnsFromFile(t *testing.T) {
	cfg, err := Parse([]byte("[context]\nturns = 5\n"), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Context.Turns != 5 {
		t.Errorf("Context.Turns = %d, want 5", cfg.Context.Turns)
	}
}

func TestContextTurnsEnvOverridesFile(t *testing.T) {
	cfg, err := Parse([]byte("[context]\nturns = 5\n"), "/home/u", env("GENIE_CONTEXT_TURNS", "3"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Context.Turns != 3 {
		t.Errorf("Context.Turns = %d, want 3 (env overrides file)", cfg.Context.Turns)
	}
}

func TestContextTurnsMustNotBeNegative(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		envVars map[string]string
	}{
		{name: "file negative", file: "[context]\nturns = -1", envVars: nil},
		{name: "env negative", envVars: env("GENIE_CONTEXT_TURNS", "-1")},
		{name: "env non-numeric", envVars: env("GENIE_CONTEXT_TURNS", "lots")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file), "/home/u", tc.envVars)
			if err == nil {
				t.Fatalf("Parse accepted %q %v; want an error for an invalid context turns", tc.file, tc.envVars)
			}
		})
	}
}

func TestBudgetDefaultsToPercentOfWindow(t *testing.T) {
	cfg, err := Parse(nil, "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Context.BudgetTokens != 0 {
		t.Errorf("Context.BudgetTokens = %d, want 0 (unset → derived from percent)", cfg.Context.BudgetTokens)
	}
	if cfg.Context.BudgetPercent != 75 {
		t.Errorf("Context.BudgetPercent = %v, want default 75", cfg.Context.BudgetPercent)
	}
}

func TestBudgetTokensFromFile(t *testing.T) {
	cfg, err := Parse([]byte("[context]\nbudget_tokens = 200000\n"), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Context.BudgetTokens != 200000 {
		t.Errorf("Context.BudgetTokens = %d, want 200000", cfg.Context.BudgetTokens)
	}
	// Setting an explicit token budget leaves the percent default untouched.
	if cfg.Context.BudgetPercent != 75 {
		t.Errorf("Context.BudgetPercent = %v, want still 75", cfg.Context.BudgetPercent)
	}
}

func TestBudgetPercentFromFile(t *testing.T) {
	cfg, err := Parse([]byte("[context]\nbudget_percent = 90\n"), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Context.BudgetPercent != 90 {
		t.Errorf("Context.BudgetPercent = %v, want 90", cfg.Context.BudgetPercent)
	}
}

func TestContextWindowOverridesFromFile(t *testing.T) {
	cfg, err := Parse([]byte("[context.windows]\n\"gpt-4o\" = 128000\n\"claude-3-5-sonnet\" = 200000\n"), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.Context.Windows["gpt-4o"]; got != 128000 {
		t.Errorf("Windows[gpt-4o] = %d, want 128000", got)
	}
	if got := cfg.Context.Windows["claude-3-5-sonnet"]; got != 200000 {
		t.Errorf("Windows[claude-3-5-sonnet] = %d, want 200000", got)
	}
}

func TestContextPluginsFromFile(t *testing.T) {
	file := `[context.plugins]
order = ["b", "a"]
active_compact = "builtin"
active_condense = "my-condenser"
`
	cfg, err := Parse([]byte(file), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Context.PluginsOrder) != 2 || cfg.Context.PluginsOrder[0] != "b" || cfg.Context.PluginsOrder[1] != "a" {
		t.Errorf("PluginsOrder = %v, want [b a]", cfg.Context.PluginsOrder)
	}
	if cfg.Context.ActiveCompact != "builtin" {
		t.Errorf("ActiveCompact = %q, want builtin", cfg.Context.ActiveCompact)
	}
	if cfg.Context.ActiveCondense != "my-condenser" {
		t.Errorf("ActiveCondense = %q, want my-condenser", cfg.Context.ActiveCondense)
	}
}

func TestContextPluginsUnsetByDefault(t *testing.T) {
	cfg, err := Parse(nil, "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Context.PluginsOrder) != 0 || cfg.Context.ActiveCompact != "" || cfg.Context.ActiveCondense != "" {
		t.Errorf("context plugins should default empty, got order=%v compact=%q condense=%q",
			cfg.Context.PluginsOrder, cfg.Context.ActiveCompact, cfg.Context.ActiveCondense)
	}
}

func TestBudgetTokensEnvOverridesFile(t *testing.T) {
	cfg, err := Parse([]byte("[context]\nbudget_tokens = 100000\n"), "/home/u", env("GENIE_CONTEXT_BUDGET_TOKENS", "300000"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Context.BudgetTokens != 300000 {
		t.Errorf("Context.BudgetTokens = %d, want 300000 (env overrides file)", cfg.Context.BudgetTokens)
	}
}

func TestBudgetPercentEnvOverridesFile(t *testing.T) {
	cfg, err := Parse([]byte("[context]\nbudget_percent = 60\n"), "/home/u", env("GENIE_CONTEXT_BUDGET_PERCENT", "80"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Context.BudgetPercent != 80 {
		t.Errorf("Context.BudgetPercent = %v, want 80 (env overrides file)", cfg.Context.BudgetPercent)
	}
}

func TestBudgetTokensMustBePositive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		envVars map[string]string
	}{
		{name: "file zero", file: "[context]\nbudget_tokens = 0", envVars: nil},
		{name: "file negative", file: "[context]\nbudget_tokens = -5", envVars: nil},
		{name: "env zero", envVars: env("GENIE_CONTEXT_BUDGET_TOKENS", "0")},
		{name: "env non-numeric", envVars: env("GENIE_CONTEXT_BUDGET_TOKENS", "lots")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file), "/home/u", tc.envVars)
			if err == nil {
				t.Fatalf("Parse accepted %q %v; want an error for an invalid budget_tokens", tc.file, tc.envVars)
			}
		})
	}
}

func TestBudgetPercentRange(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		envVars map[string]string
	}{
		{name: "file zero", file: "[context]\nbudget_percent = 0", envVars: nil},
		{name: "file negative", file: "[context]\nbudget_percent = -10", envVars: nil},
		{name: "file over 100", file: "[context]\nbudget_percent = 150", envVars: nil},
		{name: "env zero", envVars: env("GENIE_CONTEXT_BUDGET_PERCENT", "0")},
		{name: "env non-numeric", envVars: env("GENIE_CONTEXT_BUDGET_PERCENT", "half")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file), "/home/u", tc.envVars)
			if err == nil {
				t.Fatalf("Parse accepted %q %v; want an error for an invalid budget_percent", tc.file, tc.envVars)
			}
		})
	}
}

func TestContextWindowOverrideMustBePositive(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
	}{
		{name: "zero", file: "[context.windows]\n\"m\" = 0"},
		{name: "negative", file: "[context.windows]\n\"m\" = -5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file), "/home/u", nil)
			if err == nil {
				t.Fatalf("Parse accepted %q; want an error for a non-positive context window override", tc.file)
			}
		})
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
	_, err := Parse([]byte(providersFile), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"provider=zen", "providers=", "zen:openai:big-pickle", "deepseek:openai:deepseek-chat"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q:\n%s", want, out)
		}
	}
}

func TestParseLogsEnvVarsApplied(t *testing.T) {
	buf := captureInfo(t)
	_, err := Parse(nil, "/home/u", env("GENIE_PROVIDER", "zen"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "GENIE_PROVIDER") {
		t.Errorf("log output missing env var name GENIE_PROVIDER:\n%s", out)
	}
	// The env var name appears, but its value must not appear in the
	// "env overrides applied" line.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "env overrides applied") && strings.Contains(line, "zen") {
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

func TestPluginDirDerivedFromConfigDir(t *testing.T) {
	cfg, err := Parse(nil, "/home/u/.config", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := "/home/u/.config/genie/plugins"
	if cfg.PluginDir != want {
		t.Errorf("PluginDir = %q, want %q (should be derived from config dir)", cfg.PluginDir, want)
	}
}

// TestConfigDirEnvOverride verifies that GENIE_CONFIG_DIR overrides the config
// directory used for deriving default paths (session dir, plugin dir).
func TestConfigDirEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GENIE_CONFIG_DIR", dir)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantPlugins := filepath.Join(dir, "genie", "plugins")
	if cfg.PluginDir != wantPlugins {
		t.Errorf("PluginDir = %q, want %q", cfg.PluginDir, wantPlugins)
	}
	wantSessions := filepath.Join(dir, "genie", "sessions")
	if cfg.SessionDir != wantSessions {
		t.Errorf("SessionDir = %q, want %q", cfg.SessionDir, wantSessions)
	}
}

func TestLifecyclePluginsOrderParses(t *testing.T) {
	// [lifecycle.plugins] is the parallel registration block for the
	// lifecycle-hooks seam.
	file := `[lifecycle.plugins]
order = ["b-plugin", "a-plugin"]
`
	cfg, err := Parse([]byte(file), "/tmp", map[string]string{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Lifecycle.PluginsOrder) != 2 || cfg.Lifecycle.PluginsOrder[0] != "b-plugin" || cfg.Lifecycle.PluginsOrder[1] != "a-plugin" {
		t.Errorf("PluginsOrder = %v", cfg.Lifecycle.PluginsOrder)
	}
}

// wantDefaultSkillDirs is the fresh-install three-directory stack: a local
// .genie/skills, the external ecosystem at ~/.agents/skills, and the global
// config dir. Returned in priority order (lowest index wins).
func wantDefaultSkillDirs(t *testing.T, userConfigDir string) []string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return []string{
		".genie/skills",
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(userConfigDir, "genie", "skills"),
	}
}

func TestSkillsDefaultStack(t *testing.T) {
	cfg, err := Parse(nil, "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := wantDefaultSkillDirs(t, "/home/u")
	if len(cfg.Skills.Dirs) != len(want) {
		t.Fatalf("Skills.Dirs = %v, want %v", cfg.Skills.Dirs, want)
	}
	for i := range want {
		if cfg.Skills.Dirs[i] != want[i] {
			t.Errorf("Skills.Dirs[%d] = %q, want %q", i, cfg.Skills.Dirs[i], want[i])
		}
	}
	if len(cfg.Skills.Enable) != 0 || len(cfg.Skills.Disable) != 0 {
		t.Errorf("Skills.Enable/Disable should default empty, got enable=%v disable=%v", cfg.Skills.Enable, cfg.Skills.Disable)
	}
}

func TestSkillsFromFile(t *testing.T) {
	file := `[skills]
dirs = ["/custom/skills", "~/shared/skills"]
enable = ["alpha", "beta"]
disable = ["gamma"]
`
	cfg, err := Parse([]byte(file), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	wantDirs := []string{"/custom/skills", filepath.Join(home, "shared", "skills")}
	if len(cfg.Skills.Dirs) != 2 || cfg.Skills.Dirs[0] != wantDirs[0] || cfg.Skills.Dirs[1] != wantDirs[1] {
		t.Errorf("Skills.Dirs = %v, want %v", cfg.Skills.Dirs, wantDirs)
	}
	if cfg.Skills.Enable[0] != "alpha" || cfg.Skills.Enable[1] != "beta" {
		t.Errorf("Skills.Enable = %v, want [alpha beta]", cfg.Skills.Enable)
	}
	if cfg.Skills.Disable[0] != "gamma" {
		t.Errorf("Skills.Disable = %v, want [gamma]", cfg.Skills.Disable)
	}
}

func TestSkillsEnvReplacesDirsOnly(t *testing.T) {
	file := `[skills]
dirs = ["/file/skills"]
enable = ["alpha"]
disable = ["gamma"]
`
	cfg, err := Parse([]byte(file), "/home/u", env("GENIE_SKILL_DIR", "/env/skills"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	wantDirs := []string{"/env/skills"}
	if len(cfg.Skills.Dirs) != 1 || cfg.Skills.Dirs[0] != wantDirs[0] {
		t.Errorf("Skills.Dirs = %v, want %v (env replaces dirs)", cfg.Skills.Dirs, wantDirs)
	}
	// enable/disable from the file still apply on top.
	if cfg.Skills.Enable[0] != "alpha" {
		t.Errorf("Skills.Enable = %v, want [alpha]", cfg.Skills.Enable)
	}
	if cfg.Skills.Disable[0] != "gamma" {
		t.Errorf("Skills.Disable = %v, want [gamma]", cfg.Skills.Disable)
	}
}

func TestSkillsEnvReplacesDefaults(t *testing.T) {
	cfg, err := Parse(nil, "/home/u", env("GENIE_SKILL_DIR", "/env/skills"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Skills.Dirs) != 1 || cfg.Skills.Dirs[0] != "/env/skills" {
		t.Errorf("Skills.Dirs = %v, want [/env/skills]", cfg.Skills.Dirs)
	}
}

// providersFile is a config file declaring several providers: one overlaying
// a shipped default (zen), one restating another shipped default (anthropic),
// and one brand-new provider with a full spec.
const providersFile = `
provider = "zen"

[providers.zen]
base_url = "https://gateway.example/zen/v1"

[providers.anthropic]
wire  = "anthropic"
model = "claude-sonnet-4-5"

[providers.deepseek]
wire        = "openai"
base_url    = "https://api.deepseek.com/v1"
api_key_env = "DEEPSEEK_API_KEY"
model       = "deepseek-chat"
models      = ["deepseek-chat", "deepseek-reasoner"]
opts        = { cost = 2 }
`

// TestProvidersParseFromFileAndDump verifies AC1: a config declaring several
// providers parses, and the config dump lists each provider with its wire and
// default model.
func TestProvidersParseFromFileAndDump(t *testing.T) {
	// The config dump is the "config loaded" log; capture it from the parse.
	buf := captureInfo(t)
	cfg, err := Parse([]byte(providersFile), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// zen overlays the shipped default: base_url overridden, wire/model/key
	// inherited.
	zen := cfg.Providers["zen"]
	if zen.Wire != "openai" {
		t.Errorf("zen.Wire = %q, want openai (inherited from shipped default)", zen.Wire)
	}
	if zen.BaseURL != "https://gateway.example/zen/v1" {
		t.Errorf("zen.BaseURL = %q, want the file override", zen.BaseURL)
	}
	if zen.Model != "big-pickle" {
		t.Errorf("zen.Model = %q, want big-pickle (inherited default)", zen.Model)
	}
	if zen.APIKeyEnv != "OPENCODE_API_KEY" {
		t.Errorf("zen.APIKeyEnv = %q, want OPENCODE_API_KEY", zen.APIKeyEnv)
	}

	// anthropic restates the shipped default; base_url and key inherit.
	anth := cfg.Providers["anthropic"]
	if anth.Wire != "anthropic" || anth.Model != "claude-sonnet-4-5" {
		t.Errorf("anthropic = %+v, want wire=anthropic model=claude-sonnet-4-5", anth)
	}
	if anth.BaseURL != "https://api.anthropic.com" {
		t.Errorf("anthropic.BaseURL = %q, want shipped default", anth.BaseURL)
	}

	// deepseek is a brand-new provider carrying its full spec.
	ds := cfg.Providers["deepseek"]
	if ds.Wire != "openai" || ds.BaseURL != "https://api.deepseek.com/v1" ||
		ds.APIKeyEnv != "DEEPSEEK_API_KEY" || ds.Model != "deepseek-chat" {
		t.Errorf("deepseek = %+v, want the declared spec", ds)
	}
	if len(ds.Models) != 2 || ds.Models[0] != "deepseek-chat" || ds.Models[1] != "deepseek-reasoner" {
		t.Errorf("deepseek.Models = %v, want the two declared models", ds.Models)
	}
	if len(ds.Opts) != 1 || ds.Opts["cost"] != int64(2) {
		t.Errorf("deepseek.Opts = %v, want {cost: 2}", ds.Opts)
	}

	// The config dump lists each provider with its wire and default model.
	out := buf.String()
	for _, want := range []string{
		"zen:openai:big-pickle",
		"anthropic:anthropic:claude-sonnet-4-5",
		"deepseek:openai:deepseek-chat",
		"google:google:gemini-2.5-pro",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config dump missing provider %q:\n%s", want, out)
		}
	}
}

// TestShippedProviderDefaults verifies AC4: defaults exist for zen, a generic
// openai-compatible provider, anthropic, responses, google, copilot and
// bedrock, each with usable default values. Copilot and bedrock are the
// wires-with-their-own-auth exceptions: they carry no base_url and no
// api_key_env (copilot uses its credential store, ADR-0002; bedrock the AWS
// SDK credential chain).
func TestShippedProviderDefaults(t *testing.T) {
	cfg, err := Parse(nil, "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Providers) != 7 {
		t.Fatalf("len(Providers) = %d, want 7 shipped defaults", len(cfg.Providers))
	}
	for name, p := range cfg.Providers {
		if p.Wire == "" {
			t.Errorf("%s: no wire", name)
		}
		if !validWire[p.Wire] {
			t.Errorf("%s: wire %q is not a known wire", name, p.Wire)
		}
		switch name {
		case "copilot", "bedrock":
			if p.APIKeyEnv != "" {
				t.Errorf("%s: api_key_env = %q, want empty (own-auth wire)", name, p.APIKeyEnv)
			}
		default:
			if p.BaseURL == "" {
				t.Errorf("%s: no base_url", name)
			}
			if p.APIKeyEnv == "" {
				t.Errorf("%s: no api_key_env", name)
			}
		}
		if p.Model == "" {
			t.Errorf("%s: no default model", name)
		}
	}
	// The zen default must match the flat defaults it carries today.
	zen := cfg.Providers["zen"]
	if zen.Wire != "openai" || zen.BaseURL != defaultBaseURL ||
		zen.APIKeyEnv != defaultAPIKeyEnv || zen.Model != defaultModel {
		t.Errorf("zen = %+v, want the flat-key defaults", zen)
	}
	// Copilot's default block is usable out of the box: a wire, a model, and
	// no api_key_env — auth flows through the credential store.
	cop := cfg.Providers["copilot"]
	if cop.Wire != "copilot" || cop.Model == "" {
		t.Errorf("copilot = %+v, want wire=copilot with a default model", cop)
	}
	// Bedrock's default block is likewise usable out of the box: a wire and a
	// model; auth flows through the AWS SDK credential chain.
	bed := cfg.Providers["bedrock"]
	if bed.Wire != "bedrock" || bed.Model == "" {
		t.Errorf("bedrock = %+v, want wire=bedrock with a default model", bed)
	}
}

// TestProviderMissingDefaultModelFails verifies AC2: a provider declared
// without a default model fails config load with a clear error.
func TestProviderMissingDefaultModelFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
	}{
		{name: "no model key", file: "[providers.mine]\nwire = \"openai\"\n"},
		{name: "empty model", file: "[providers.mine]\nwire = \"openai\"\nmodel = \"\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file), "/home/u", nil)
			if err == nil {
				t.Fatal("Parse accepted a provider with no default model; want an error")
			}
			if !strings.Contains(err.Error(), "missing default model") {
				t.Errorf("error = %q, want a clear missing-default-model error", err)
			}
		})
	}
}

// TestProviderMissingWireFails: a brand-new provider that names no wire cannot
// be served and fails load.
func TestProviderMissingWireFails(t *testing.T) {
	_, err := Parse([]byte("[providers.mine]\nmodel = \"x\"\n"), "/home/u", nil)
	if err == nil {
		t.Fatal("Parse accepted a provider with no wire; want an error")
	}
	if !strings.Contains(err.Error(), "missing wire") {
		t.Errorf("error = %q, want a clear missing-wire error", err)
	}
}

// TestProviderUnknownWireFails verifies AC2: a provider naming an unknown wire
// fails config load with a clear error.
func TestProviderUnknownWireFails(t *testing.T) {
	_, err := Parse([]byte("[providers.mine]\nwire = \"bogus\"\nmodel = \"x\"\n"), "/home/u", nil)
	if err == nil {
		t.Fatal("Parse accepted a provider with an unknown wire; want an error")
	}
	if !strings.Contains(err.Error(), `provider "mine"`) || !strings.Contains(err.Error(), "unknown wire") {
		t.Errorf("error = %q, want a clear unknown-wire error", err)
	}
}

// TestProviderDuplicateNameRejected verifies AC3: duplicate provider tables
// are rejected at parse time.
func TestProviderDuplicateNameRejected(t *testing.T) {
	file := `[providers.zen]
model = "m1"

[providers.zen]
model = "m2"
`
	_, err := Parse([]byte(file), "/home/u", nil)
	if err == nil {
		t.Fatal("Parse accepted duplicate provider tables; want an error")
	}
	if !strings.Contains(err.Error(), "already been defined") {
		t.Errorf("error = %q, want the TOML duplicate-key error", err)
	}
}

// TestProviderOverlaysShippedDefault: a file table for a shipped provider name
// merges over the default; only the keys it sets change.
func TestProviderOverlaysShippedDefault(t *testing.T) {
	cfg, err := Parse([]byte("[providers.openai]\nmodel = \"gpt-5\"\n"), "/home/u", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p := cfg.Providers["openai"]
	if p.Model != "gpt-5" {
		t.Errorf("openai.Model = %q, want gpt-5 (file override)", p.Model)
	}
	if p.Wire != "openai" || p.BaseURL != "https://api.openai.com/v1" || p.APIKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("openai = %+v, want wire/base_url/api_key_env inherited from the shipped default", p)
	}
}

// TestProviderUnknownKeyRejected: a typo inside a provider table is caught by
// the fail-fast unknown-key rule.
func TestProviderUnknownKeyRejected(t *testing.T) {
	_, err := Parse([]byte("[providers.mine]\nwiree = \"openai\"\nmodel = \"x\"\n"), "/home/u", nil)
	if err == nil {
		t.Fatal("Parse accepted an unknown provider key; want an error")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("error = %q, want an unknown-key error", err)
	}
}

// TestProviderSchemaHasNoSingletonKnobs verifies AC5 on the surface as built:
// every key a provider table accepts is a genuine knob with more than one
// valid value. Each knob is exercised with two different values — if both
// resolve identically, the key can only ever hold one value, which is a
// singleton setting that belongs hardcoded in code, not exposed as a knob.
// Singleton settings (the active provider, a provider's wire hosting) live in
// the top-level selector, never as a fixed-value table key.
func TestProviderSchemaHasNoSingletonKnobs(t *testing.T) {
	newProvider := func(file string) Provider {
		cfg, err := Parse([]byte(file), "/home/u", nil)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		return cfg.Providers["mine"]
	}
	tests := []struct {
		key     string
		file    string // first valid value
		variant string // a different valid value
		value   func(Provider) any
	}{
		{
			key: "wire",
			file: `[providers.mine]
wire = "anthropic"
model = "m"
`,
			variant: `[providers.mine]
wire = "google"
model = "m"
`,
			value: func(p Provider) any { return p.Wire },
		},
		{
			key: "base_url",
			file: `[providers.mine]
wire = "openai"
base_url = "https://a.example"
model = "m"
`,
			variant: `[providers.mine]
wire = "openai"
base_url = "https://b.example"
model = "m"
`,
			value: func(p Provider) any { return p.BaseURL },
		},
		{
			key: "api_key_env",
			file: `[providers.mine]
wire = "openai"
api_key_env = "A_KEY"
model = "m"
`,
			variant: `[providers.mine]
wire = "openai"
api_key_env = "B_KEY"
model = "m"
`,
			value: func(p Provider) any { return p.APIKeyEnv },
		},
		{
			key: "model",
			file: `[providers.mine]
wire = "openai"
model = "model-a"
`,
			variant: `[providers.mine]
wire = "openai"
model = "model-b"
`,
			value: func(p Provider) any { return p.Model },
		},
		{
			key: "models",
			file: `[providers.mine]
wire = "openai"
model = "m"
models = ["a"]
`,
			variant: `[providers.mine]
wire = "openai"
model = "m"
models = ["b"]
`,
			value: func(p Provider) any { return p.Models },
		},
		{
			key: "opts",
			file: `[providers.mine]
wire = "openai"
model = "m"
opts = { retries = 3 }
`,
			variant: `[providers.mine]
wire = "openai"
model = "m"
opts = { retries = 9 }
`,
			value: func(p Provider) any { return p.Opts },
		},
	}
	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			v1 := tc.value(newProvider(tc.file))
			v2 := tc.value(newProvider(tc.variant))
			if reflect.DeepEqual(v1, v2) {
				t.Errorf("key %q resolved to %v for two different values; a key that can only ever hold one value is a singleton setting, not a knob", tc.key, v1)
			}
		})
	}
}

func TestSkillsUnknownKeyRejected(t *testing.T) {
	file := `[skills]
enabel = ["typo"]
`
	_, err := Parse([]byte(file), "/home/u", nil)
	if err == nil {
		t.Fatal("Parse accepted unknown [skills] key; want an error")
	}
}

func TestSkillsEmptyEnvVarDoesNotOverride(t *testing.T) {
	cfg, err := Parse(nil, "/home/u", env("GENIE_SKILL_DIR", ""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := wantDefaultSkillDirs(t, "/home/u")
	if len(cfg.Skills.Dirs) != len(want) || cfg.Skills.Dirs[0] != want[0] {
		t.Errorf("Skills.Dirs = %v, want default stack %v (empty env must not override)", cfg.Skills.Dirs, want)
	}
}
