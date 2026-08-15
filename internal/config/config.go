// Package config loads the harness's v1 configuration: a TOML file at the
// user config dir overlaid on pure defaults, with six env overrides on top
// and an API key that lives only in the environment. Precedence is
// defaults < file < env. A missing config file is pure defaults; malformed
// TOML and unknown keys fail fast with an error.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Defaults for every configurable scalar.
const (
	defaultModel       = "big-pickle"
	defaultBaseURL     = "https://opencode.ai/zen/v1"
	defaultAPIKeyEnv   = "OPENCODE_API_KEY"
	defaultBashTimeout = 120 * time.Second

	configFileName = "config.toml"
)

// Tools holds the four per-tool toggles. All default to enabled; a disabled
// tool is omitted from the tools array sent to the provider.
type Tools struct {
	Read  bool
	Write bool
	Edit  bool
	Bash  bool
}

// Config is the resolved harness configuration.
type Config struct {
	// Model is the session's model id (default big-pickle).
	Model string
	// BaseURL is the provider's wire base (default OpenCode Zen).
	BaseURL string
	// APIKeyEnv names the env var the API key lives in; the key itself is
	// never stored in the config file.
	APIKeyEnv string
	// APIKey is the resolved key read from the env var named by APIKeyEnv.
	APIKey string
	// InstructionFile is an optional agent-instruction source loaded after
	// the built-in default. Unset means none.
	InstructionFile string
	// SessionDir is where sessions and their change ledgers live.
	SessionDir string
	// BashTimeout is the default kill timeout for bash tool commands.
	BashTimeout time.Duration
	// Tools are the four per-tool enable switches.
	Tools Tools
}

// fileConfig is the TOML schema. Tool booleans and bash_timeout are pointers
// so an omitted key leaves the default; scalars fall back to defaults when
// empty.
type fileConfig struct {
	Model           string    `toml:"model"`
	BaseURL         string    `toml:"base_url"`
	APIKeyEnv       string    `toml:"api_key_env"`
	InstructionFile string    `toml:"instruction_file"`
	SessionDir      string    `toml:"session_dir"`
	BashTimeout     *int      `toml:"bash_timeout"` // seconds
	Tools           toolsFile `toml:"tools"`
}

type toolsFile struct {
	Read  *bool `toml:"read"`
	Write *bool `toml:"write"`
	Edit  *bool `toml:"edit"`
	Bash  *bool `toml:"bash"`
}

// Parse resolves the full configuration from raw config-file content and an
// environment, with precedence defaults < file < env. userConfigDir is the
// base the session-dir default is derived from. A nil file is pure defaults.
func Parse(file []byte, userConfigDir string, env map[string]string) (*Config, error) {
	cfg := defaults(userConfigDir)

	if len(file) > 0 {
		var fc fileConfig
		md, err := toml.Decode(string(file), &fc)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		if unknown := md.Undecoded(); len(unknown) > 0 {
			keys := make([]string, len(unknown))
			for i, k := range unknown {
				keys[i] = k.String()
			}
			return nil, fmt.Errorf("config: unknown key(s): %s", strings.Join(keys, ", "))
		}
		if fc.Model != "" {
			cfg.Model = fc.Model
		}
		if fc.BaseURL != "" {
			cfg.BaseURL = fc.BaseURL
		}
		if fc.APIKeyEnv != "" {
			cfg.APIKeyEnv = fc.APIKeyEnv
		}
		cfg.InstructionFile = fc.InstructionFile
		if fc.SessionDir != "" {
			cfg.SessionDir = fc.SessionDir
		}
		if fc.BashTimeout != nil {
			if *fc.BashTimeout <= 0 {
				return nil, fmt.Errorf("config: bash_timeout must be a positive number of seconds, got %d", *fc.BashTimeout)
			}
			cfg.BashTimeout = time.Duration(*fc.BashTimeout) * time.Second
		}
		applyTools(&cfg.Tools, fc.Tools)
	}

	if err := applyEnv(&cfg, env); err != nil {
		return nil, err
	}
	cfg.APIKey = env[cfg.APIKeyEnv]
	return &cfg, nil
}

// Load reads the config file from os.UserConfigDir()/og/config.toml (missing
// means pure defaults) and resolves the full configuration from the process
// environment.
func Load() (*Config, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	path := filepath.Join(dir, "og", configFileName)
	file, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			file = nil
		} else {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
	}
	return Parse(file, dir, environMap())
}

func defaults(userConfigDir string) Config {
	return Config{
		Model:       defaultModel,
		BaseURL:     defaultBaseURL,
		APIKeyEnv:   defaultAPIKeyEnv,
		SessionDir:  filepath.Join(userConfigDir, "og", "sessions"),
		BashTimeout: defaultBashTimeout,
		Tools:       Tools{Read: true, Write: true, Edit: true, Bash: true},
	}
}

func applyTools(dst *Tools, src toolsFile) {
	if src.Read != nil {
		dst.Read = *src.Read
	}
	if src.Write != nil {
		dst.Write = *src.Write
	}
	if src.Edit != nil {
		dst.Edit = *src.Edit
	}
	if src.Bash != nil {
		dst.Bash = *src.Bash
	}
}

// applyEnv overlays the six env overrides on top of the config file. An env
// var that is set but empty leaves the file value in place.
func applyEnv(cfg *Config, env map[string]string) error {
	if v := env["OG_MODEL"]; v != "" {
		cfg.Model = v
	}
	if v := env["OG_BASE_URL"]; v != "" {
		cfg.BaseURL = v
	}
	if v := env["OG_API_KEY_ENV"]; v != "" {
		cfg.APIKeyEnv = v
	}
	if v := env["OG_INSTRUCTION_FILE"]; v != "" {
		cfg.InstructionFile = v
	}
	if v := env["OG_SESSION_DIR"]; v != "" {
		cfg.SessionDir = v
	}
	if v := env["OG_BASH_TIMEOUT"]; v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("config: OG_BASH_TIMEOUT: %q is not a number of seconds", v)
		}
		if secs <= 0 {
			return fmt.Errorf("config: OG_BASH_TIMEOUT must be a positive number of seconds, got %d", secs)
		}
		cfg.BashTimeout = time.Duration(secs) * time.Second
	}
	return nil
}

func environMap() map[string]string {
	out := make(map[string]string)
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}
