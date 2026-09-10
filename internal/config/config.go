// Package config loads the harness's v1 configuration: a TOML file at the
// user config dir overlaid on pure defaults, with six env overrides on top
// and an API key that lives only in the environment. Precedence is
// defaults < file < env. A missing config file is pure defaults; malformed
// TOML and unknown keys fail fast with an error.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/okayest-dev/genie/internal/modelinfo"
)

// validWire is the set of wire names accepted by the config. Must stay in
// sync with the llm.Wire* constants.
var validWire = map[string]bool{
	"openai":    true,
	"anthropic": true,
	"responses": true,
	"google":    true,
}

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

// Context holds the harness-level context-management knobs. These control how
// prior conversation turns are carried into each new turn and the budget the
// conversation is kept within.
type Context struct {
	// Turns is the number of prior turns of history injected into each new
	// turn's request. Zero means unlimited (the whole conversation), which is
	// the default so the model remembers every earlier turn.
	Turns int
	// BudgetTokens, when > 0, is the absolute context budget in tokens.
	// When zero, the budget is derived from BudgetPercent of the model's
	// authoritative context window.
	BudgetTokens int
	// BudgetPercent is the context budget expressed as a percentage of the
	// model's context window, used when BudgetTokens is not set. Defaults to
	// 75, keeping headroom so a conversation cannot silently blow the window.
	BudgetPercent float64
	// Windows maps a model ID to an explicit context-window override (in
	// tokens). An override takes precedence over provider-reported data.
	Windows map[string]int
	// PluginsOrder is the deterministic chain order for plugin context hooks
	// (before_request/after_response). A plugin not listed is appended in
	// registration order. Empty means registration order for all.
	PluginsOrder []string
	// ActiveCompact and ActiveCondense name the single-active compact/condense
	// implementation. "builtin" selects the harness's own (the default); empty
	// auto-resolves and errors when ambiguous.
	ActiveCompact  string
	ActiveCondense string
	// CondenseSize is the per-call token threshold above which a prior-turn
	// tool result is condensed (or, with net-drop, dropped) before a request is
	// forwarded. Zero disables condensation.
	CondenseSize int
	// NetDrop drops oversized prior-turn tool results from requests entirely
	// (their assistant tool-call stays), so the model sees the call was
	// invoked. Off by default.
	NetDrop bool
}

// Lifecycle holds the lifecycle-hooks plugin seam knobs (og-cbu.3/og-cbu.4).
type Lifecycle struct {
	// PluginsOrder is the shared chain order for lifecycle events (default:
	// registration order). Request-side events fire in this order, response-side
	// events reversed (onion).
	PluginsOrder []string
}

// Skills holds the skill-discovery knobs (og-uem.12.2). Defaults to a
// three-directory stack in priority order: the project-local .genie/skills,
// the external ecosystem at ~/.agents/skills, and the user config dir.
// Enable is an allowlist (when set, only named skills load); Disable is a
// denylist applied on top.
type Skills struct {
	Dirs    []string
	Enable  []string
	Disable []string
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
	// Wire selects the wire implementation. Empty means auto-detect from
	// the model ID prefix.
	Wire string
	// Provider names a plugin to route all requests through (e.g.
	// "copilot"). When set, the harness looks for a loaded wire plugin
	// with that name and uses it directly. Empty falls back to Wire/model
	// auto-detection.
	Provider string
	// Gateway is an optional URL override for the provider gateway.
	Gateway string
	// InstructionFile is an optional agent-instruction source loaded after
	// the built-in default. Unset means none.
	InstructionFile string
	// SessionDir is where sessions and their change ledgers live.
	SessionDir string
	// BashTimeout is the default kill timeout for bash tool commands.
	BashTimeout time.Duration
	// Tools are the four per-tool enable switches.
	Tools Tools
	// PluginDir is the directory where plugins are discovered.
	PluginDir string
	// PluginEnable is an explicit allowlist of plugin names to load.
	PluginEnable []string
	// PluginDisable is a denylist of plugin names to skip.
	PluginDisable []string
	// DefaultAgent is the name of the agent definition loaded at startup.
	// Empty means no default agent (current behaviour).
	DefaultAgent string
	// Context configures harness-level context management (history window).
	Context   Context
	Lifecycle Lifecycle
	Skills    Skills
	AgentReg  *AgentReg
}

// fileConfig is the TOML schema. Tool booleans and bash_timeout are pointers
// so an omitted key leaves the default; scalars fall back to defaults when
// empty.
type fileConfig struct {
	Model           string        `toml:"model"`
	BaseURL         string        `toml:"base_url"`
	APIKeyEnv       string        `toml:"api_key_env"`
	Wire            string        `toml:"wire"`
	Provider        string        `toml:"provider"`
	Gateway         string        `toml:"gateway"`
	InstructionFile string        `toml:"instruction_file"`
	SessionDir      string        `toml:"session_dir"`
	BashTimeout     *int          `toml:"bash_timeout"` // seconds
	Tools           toolsFile     `toml:"tools"`
	Plugins         pluginsFile   `toml:"plugins"`
	Skills          skillsFile    `toml:"skills"`
	Context         contextFile   `toml:"context"`
	Lifecycle       lifecycleFile `toml:"lifecycle"`
	DefaultAgent    string        `toml:"default_agent"`
}

type toolsFile struct {
	Read  *bool `toml:"read"`
	Write *bool `toml:"write"`
	Edit  *bool `toml:"edit"`
	Bash  *bool `toml:"bash"`
}

type pluginsFile struct {
	Dir     string   `toml:"dir"`
	Enable  []string `toml:"enable"`
	Disable []string `toml:"disable"`
}

type skillsFile struct {
	Dirs    []string `toml:"dirs"`
	Enable  []string `toml:"enable"`
	Disable []string `toml:"disable"`
}

type contextFile struct {
	Turns         *int               `toml:"turns"`
	BudgetTokens  *int               `toml:"budget_tokens"`
	BudgetPercent *float64           `toml:"budget_percent"`
	Windows       map[string]int     `toml:"windows"`
	Plugins       contextPluginsFile `toml:"plugins"`
	CondenseSize  *int               `toml:"condense_size"`
	NetDrop       *bool              `toml:"net_drop"`
}

type contextPluginsFile struct {
	Order          []string `toml:"order"`
	ActiveCompact  string   `toml:"active_compact"`
	ActiveCondense string   `toml:"active_condense"`
}

type lifecycleFile struct {
	Plugins lifecyclePluginsFile `toml:"plugins"`
}

type lifecyclePluginsFile struct {
	// Order is the shared chain order for all lifecycle events (default:
	// registration order). Request-side events fire in this order, response-side
	// events reversed (onion).
	Order []string `toml:"order"`
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
		if fc.Wire != "" {
			cfg.Wire = fc.Wire
		}
		if fc.Provider != "" {
			cfg.Provider = fc.Provider
		}
		if fc.Gateway != "" {
			cfg.Gateway = fc.Gateway
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
		applyPlugins(&cfg, fc.Plugins, userConfigDir)
		applySkills(&cfg, fc.Skills)
		if fc.Context.Turns != nil {
			if *fc.Context.Turns < 0 {
				return nil, fmt.Errorf("config: context.turns must be non-negative, got %d", *fc.Context.Turns)
			}
			cfg.Context.Turns = *fc.Context.Turns
		}
		if fc.Context.BudgetTokens != nil {
			if *fc.Context.BudgetTokens <= 0 {
				return nil, fmt.Errorf("config: context.budget_tokens must be a positive number of tokens, got %d", *fc.Context.BudgetTokens)
			}
			cfg.Context.BudgetTokens = *fc.Context.BudgetTokens
		}
		if fc.Context.BudgetPercent != nil {
			if err := validateBudgetPercent(*fc.Context.BudgetPercent); err != nil {
				return nil, err
			}
			cfg.Context.BudgetPercent = *fc.Context.BudgetPercent
		}
		if len(fc.Context.Windows) > 0 {
			cfg.Context.Windows = make(map[string]int, len(fc.Context.Windows))
			for model, window := range fc.Context.Windows {
				if window <= 0 {
					return nil, fmt.Errorf("config: context.windows[%q] must be a positive number of tokens, got %d", model, window)
				}
				cfg.Context.Windows[model] = window
			}
		}
		if len(fc.Context.Plugins.Order) > 0 {
			cfg.Context.PluginsOrder = fc.Context.Plugins.Order
		}
		if fc.Context.Plugins.ActiveCompact != "" {
			cfg.Context.ActiveCompact = fc.Context.Plugins.ActiveCompact
		}
		if fc.Context.Plugins.ActiveCondense != "" {
			cfg.Context.ActiveCondense = fc.Context.Plugins.ActiveCondense
		}
		if fc.Context.CondenseSize != nil {
			if *fc.Context.CondenseSize < 0 {
				return nil, fmt.Errorf("config: context.condense_size must be non-negative, got %d", *fc.Context.CondenseSize)
			}
			cfg.Context.CondenseSize = *fc.Context.CondenseSize
		}
		if fc.Context.NetDrop != nil {
			cfg.Context.NetDrop = *fc.Context.NetDrop
		}
		if len(fc.Lifecycle.Plugins.Order) > 0 {
			cfg.Lifecycle.PluginsOrder = fc.Lifecycle.Plugins.Order
		}
		if fc.DefaultAgent != "" {
			cfg.DefaultAgent = fc.DefaultAgent
		}
	}

	applied, err := applyEnv(&cfg, env)
	if err != nil {
		return nil, err
	}
	cfg.APIKey = env[cfg.APIKeyEnv]

	if cfg.Wire != "" && !validWire[cfg.Wire] {
		return nil, fmt.Errorf("config: unknown wire %q", cfg.Wire)
	}

	slog.Info("config loaded",
		"model", cfg.Model,
		"base_url", cfg.BaseURL,
		"provider", cfg.Provider,
		"instruction_file", cfg.InstructionFile,
		"session_dir", cfg.SessionDir,
		"default_agent", cfg.DefaultAgent,
	)
	slog.Debug("config resolved",
		"model", cfg.Model,
		"base_url", cfg.BaseURL,
		"api_key_env", cfg.APIKeyEnv,
		"instruction_file", cfg.InstructionFile,
		"session_dir", cfg.SessionDir,
		"bash_timeout_s", int(cfg.BashTimeout.Seconds()),
		"tools_read", cfg.Tools.Read,
		"tools_write", cfg.Tools.Write,
		"tools_edit", cfg.Tools.Edit,
		"tools_bash", cfg.Tools.Bash,
	)
	if len(applied) > 0 {
		slog.Info("env overrides applied", "vars", strings.Join(applied, ","))
	}

	return &cfg, nil
}

// Load reads the config file from os.UserConfigDir()/genie/config.toml (missing
// means pure defaults) and resolves the full configuration from the process
// environment. GENIE_CONFIG_DIR overrides the config directory used for deriving
// default paths.
func Load() (*Config, error) {
	dir := os.Getenv("GENIE_CONFIG_DIR")
	if dir == "" {
		var err error
		dir, err = os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}
	path := filepath.Join(dir, "genie", configFileName)
	file, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("config file not found, using defaults", "path", path)
			file = nil
		} else {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
	} else {
		slog.Info("config file loaded", "path", path)
	}
	return Parse(file, dir, environMap())
}

func defaults(userConfigDir string) Config {
	return Config{
		Model:       defaultModel,
		BaseURL:     defaultBaseURL,
		APIKeyEnv:   defaultAPIKeyEnv,
		SessionDir:  filepath.Join(userConfigDir, "genie", "sessions"),
		BashTimeout: defaultBashTimeout,
		Tools:       Tools{Read: true, Write: true, Edit: true, Bash: true},
		PluginDir:   filepath.Join(userConfigDir, "genie", "plugins"),
		Skills:      Skills{Dirs: defaultSkillDirs(userConfigDir)},
		Context:     Context{BudgetPercent: modelinfo.DefaultBudgetPercent},
	}
}

// defaultSkillDirs is the fresh-install discovery stack in priority order:
// the project-local .genie/skills, the external ecosystem at ~/.agents/skills,
// and the user-config directory.
func defaultSkillDirs(userConfigDir string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = userConfigDir
	}
	return []string{
		".genie/skills",
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(userConfigDir, "genie", "skills"),
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
// var that is set but empty leaves the file value in place. Returns the names
// of env vars that were applied.
func applyEnv(cfg *Config, env map[string]string) ([]string, error) {
	var applied []string
	if v := env["GENIE_MODEL"]; v != "" {
		cfg.Model = v
		applied = append(applied, "GENIE_MODEL")
	}
	if v := env["GENIE_BASE_URL"]; v != "" {
		cfg.BaseURL = v
		applied = append(applied, "GENIE_BASE_URL")
	}
	if v := env["GENIE_API_KEY_ENV"]; v != "" {
		cfg.APIKeyEnv = v
		applied = append(applied, "GENIE_API_KEY_ENV")
	}
	if v := env["GENIE_WIRE"]; v != "" {
		cfg.Wire = v
		applied = append(applied, "GENIE_WIRE")
	}
	if v := env["GENIE_PROVIDER"]; v != "" {
		cfg.Provider = v
		applied = append(applied, "GENIE_PROVIDER")
	}
	if v := env["GENIE_GATEWAY"]; v != "" {
		cfg.Gateway = v
		applied = append(applied, "GENIE_GATEWAY")
	}
	if v := env["GENIE_INSTRUCTION_FILE"]; v != "" {
		cfg.InstructionFile = v
		applied = append(applied, "GENIE_INSTRUCTION_FILE")
	}
	if v := env["GENIE_SESSION_DIR"]; v != "" {
		cfg.SessionDir = v
		applied = append(applied, "GENIE_SESSION_DIR")
	}
	if v := env["GENIE_BASH_TIMEOUT"]; v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("config: GENIE_BASH_TIMEOUT: %q is not a number of seconds", v)
		}
		if secs <= 0 {
			return nil, fmt.Errorf("config: GENIE_BASH_TIMEOUT must be a positive number of seconds, got %d", secs)
		}
		cfg.BashTimeout = time.Duration(secs) * time.Second
		applied = append(applied, "GENIE_BASH_TIMEOUT")
	}
	if v := env["GENIE_PLUGIN_DIR"]; v != "" {
		cfg.PluginDir = v
		applied = append(applied, "GENIE_PLUGIN_DIR")
	}
	if v := env["GENIE_SKILL_DIR"]; v != "" {
		cfg.Skills.Dirs = []string{v}
		applied = append(applied, "GENIE_SKILL_DIR")
	}
	if v := env["GENIE_DEFAULT_AGENT"]; v != "" {
		cfg.DefaultAgent = v
		applied = append(applied, "GENIE_DEFAULT_AGENT")
	}
	if v := env["GENIE_CONTEXT_TURNS"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("config: GENIE_CONTEXT_TURNS: %q is not a number", v)
		}
		if n < 0 {
			return nil, fmt.Errorf("config: GENIE_CONTEXT_TURNS must be non-negative, got %d", n)
		}
		cfg.Context.Turns = n
		applied = append(applied, "GENIE_CONTEXT_TURNS")
	}
	if v := env["GENIE_CONTEXT_BUDGET_TOKENS"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("config: GENIE_CONTEXT_BUDGET_TOKENS: %q is not a number", v)
		}
		if n <= 0 {
			return nil, fmt.Errorf("config: GENIE_CONTEXT_BUDGET_TOKENS must be a positive number of tokens, got %d", n)
		}
		cfg.Context.BudgetTokens = n
		applied = append(applied, "GENIE_CONTEXT_BUDGET_TOKENS")
	}
	if v := env["GENIE_CONTEXT_BUDGET_PERCENT"]; v != "" {
		p, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("config: GENIE_CONTEXT_BUDGET_PERCENT: %q is not a number", v)
		}
		if err := validateBudgetPercent(p); err != nil {
			return nil, fmt.Errorf("config: GENIE_CONTEXT_BUDGET_PERCENT: %w", err)
		}
		cfg.Context.BudgetPercent = p
		applied = append(applied, "GENIE_CONTEXT_BUDGET_PERCENT")
	}
	if v := env["GENIE_CONTEXT_CONDENSE_SIZE"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("config: GENIE_CONTEXT_CONDENSE_SIZE: %q is not a number", v)
		}
		if n < 0 {
			return nil, fmt.Errorf("config: GENIE_CONTEXT_CONDENSE_SIZE must be non-negative, got %d", n)
		}
		cfg.Context.CondenseSize = n
		applied = append(applied, "GENIE_CONTEXT_CONDENSE_SIZE")
	}
	if v := env["GENIE_CONTEXT_NET_DROP"]; v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("config: GENIE_CONTEXT_NET_DROP: %q is not a boolean", v)
		}
		cfg.Context.NetDrop = b
		applied = append(applied, "GENIE_CONTEXT_NET_DROP")
	}
	return applied, nil
}

// validateBudgetPercent enforces that a budget percentage lands in (0, 100]:
// zero or negative leaves no usable budget, and over 100 would budget past
// the context window itself.
func validateBudgetPercent(p float64) error {
	if p <= 0 || p > 100 {
		return fmt.Errorf("context.budget_percent must be in (0, 100], got %v", p)
	}
	return nil
}

func applyPlugins(cfg *Config, src pluginsFile, userConfigDir string) {
	if src.Dir != "" {
		cfg.PluginDir = expandPath(src.Dir)
	} else {
		cfg.PluginDir = filepath.Join(userConfigDir, "genie", "plugins")
	}
	cfg.PluginEnable = src.Enable
	cfg.PluginDisable = src.Disable
}

// applySkills overlays the [skills] table on the defaults. A non-empty dirs
// list replaces the default stack entirely; empty enable/disable keep any
// prior (default) values empty so they stay allowlist/denylist-neutral.
func applySkills(cfg *Config, src skillsFile) {
	if len(src.Dirs) > 0 {
		dirs := make([]string, len(src.Dirs))
		for i, d := range src.Dirs {
			dirs[i] = expandPath(d)
		}
		cfg.Skills.Dirs = dirs
	}
	cfg.Skills.Enable = src.Enable
	cfg.Skills.Disable = src.Disable
}

// expandPath expands a leading ~ or ~/ to the current user's home directory.
func expandPath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
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
