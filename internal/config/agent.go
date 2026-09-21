package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// AgentDef is an agent definition parsed from a TOML file. All fields are
// optional; zero values mean "inherit from harness config". The Name is
// derived from the filename stem, not from the TOML content.
type AgentDef struct {
	Name            string   // derived from filename stem
	Source          string   // absolute path to the .toml file
	Model           string   // empty = no model (active provider's default applies)
	InstructionFile string   // empty = inherit config instruction_file
	Tools           []string // nil = inherit all; non-nil = exact set
	InheritAgentsMD *bool    // nil = true (inherit); explicit false = don't
	Skills          []string // nil = inherit all; non-nil = exact set (empty = none)
	// Permissions is the agent's [permissions] base section. nil = inherit the
	// harness global base; a present section replaces it wholly (og-uy5.7).
	// Agent files never declare permanent grants — those are harness-global and
	// apply additively over either base.
	Permissions *AgentPermissions
}

// AgentPermissions is the per-agent [permissions] TOML section: per-axis base
// scopes. A present section replaces the harness global base wholly — an
// unnamed axis is left empty, with no axis-level inheritance, matching agent
// tools' replace-not-merge semantics.
type AgentPermissions struct {
	Read  []string `toml:"read"`
	Write []string `toml:"write"`
	Net   []string `toml:"net"`
	Run   []string `toml:"run"`
	Env   []string `toml:"env"`
}

// fileAgentDef is the TOML schema with pointer types for optionals.
type fileAgentDef struct {
	Model           string            `toml:"model"`
	InstructionFile string            `toml:"instruction_file"`
	Tools           []string          `toml:"tools"`
	InheritAgentsMD *bool             `toml:"inherit_agents_md"`
	Skills          []string          `toml:"skills"`
	Permissions     *AgentPermissions `toml:"permissions"`
}

// ResolvedAgent is an AgentDef resolved against harness config defaults. The
// only field that may stay empty is Model — an agent that does not declare
// one leaves it unset so the runtime uses the active provider's default
// model; every other field is usable as-is.
type ResolvedAgent struct {
	Name            string
	Source          string
	Model           string
	InstructionFile string
	Tools           []string
	InheritAgentsMD bool
	Skills          []string // guaranteed non-nil; inherit-all or exact set
	// Permissions is nil for an agent that declares no [permissions] section
	// (inherits the harness global base), non-nil when the agent replaces the
	// global base wholly (og-uy5.7).
	Permissions *AgentPermissions
}

// ParseAgentDef parses a TOML agent definition file. The name is derived
// from the filename stem (not the TOML content). Strict: unknown keys
// cause a hard error, matching config.go behaviour.
func ParseAgentDef(data []byte, name, sourcePath string) (*AgentDef, error) {
	var fa fileAgentDef
	md, err := toml.Decode(string(data), &fa)
	if err != nil {
		return nil, fmt.Errorf("agent %s: %w", name, err)
	}
	if unknown := md.Undecoded(); len(unknown) > 0 {
		keys := make([]string, len(unknown))
		for i, k := range unknown {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("agent %s: unknown key(s): %s", name, strings.Join(keys, ", "))
	}
	return &AgentDef{
		Name:            name,
		Source:          sourcePath,
		Model:           fa.Model,
		InstructionFile: fa.InstructionFile,
		Tools:           fa.Tools,
		InheritAgentsMD: fa.InheritAgentsMD,
		Skills:          fa.Skills,
		Permissions:     fa.Permissions,
	}, nil
}

// ResolveAgentDef resolves an AgentDef against harness config defaults.
// availableSkills is the discovered skill pool; an absent Skills field on the
// def inherits it, an explicit set (possibly empty) overrides it. There is no
// global model to inherit: an agent that does not declare a model ends up
// with an empty Model, and the runtime starts the turn on the active
// provider's default. The result is a new ResolvedAgent; the original def,
// cfg, and pool are not mutated.
func ResolveAgentDef(def *AgentDef, cfg *Config, availableSkills []string) ResolvedAgent {
	skills := []string{}
	if def.Skills != nil {
		skills = append(skills, def.Skills...)
	} else {
		skills = append(skills, availableSkills...)
	}
	r := ResolvedAgent{
		Name:            def.Name,
		Source:          def.Source,
		Model:           def.Model,
		InstructionFile: cfg.InstructionFile,
		Tools:           allToolNames(cfg.Tools),
		InheritAgentsMD: true,
		Skills:          skills,
		Permissions:     def.Permissions,
	}
	if def.InstructionFile != "" {
		r.InstructionFile = def.InstructionFile
	}
	if def.Tools != nil {
		r.Tools = def.Tools
	}
	if def.InheritAgentsMD != nil {
		r.InheritAgentsMD = *def.InheritAgentsMD
	}
	return r
}

// allToolNames returns the names of all enabled tools from config.
// request_permission is always-on for inherit-all agents: it is a negotiation
// channel, not a capability, so it has no config toggle here. An agent that
// lists its tools explicitly can still scope it out.
func allToolNames(t Tools) []string {
	var names []string
	if t.Read {
		names = append(names, "read")
	}
	if t.Write {
		names = append(names, "write")
	}
	if t.Edit {
		names = append(names, "edit")
	}
	if t.Bash {
		names = append(names, "bash")
	}
	names = append(names, "request_permission")
	return names
}

// HasExplicitModel reports whether the agent declares its own model rather
// than leaving the runtime on the active provider's default. Resolution does
// not fill Model from any global, so a non-empty Model is always an explicit
// declaration. This is the "explicit-only" override rule (og-z1m.3): a
// request may leave the active provider's default model only for a model an
// agent names outright.
func (a *ResolvedAgent) HasExplicitModel() bool {
	return a != nil && a.Model != ""
}

// EffectiveBase resolves the agent's base policy as name-keyed scopes. An
// agent that declares a [permissions] section replaces the harness global
// base wholly: only the axes it names are covered, every other axis is empty
// (no axis-level inheritance) — the same replace-not-merge rule agent tools
// follow. An agent without a section, and a nil agent, inherit the global base
// unchanged. The global map is never mutated, and both paths return a fresh
// map (the declared scopes and the global scopes are copied), so callers can
// mutate the result without aliasing the source of truth (og-uy5.7).
func (a *ResolvedAgent) EffectiveBase(global map[string][]string) map[string][]string {
	if a == nil || a.Permissions == nil {
		out := make(map[string][]string, len(global))
		for axis, scopes := range global {
			out[axis] = append([]string(nil), scopes...)
		}
		return out
	}
	out := make(map[string][]string)
	for axis, scopes := range map[string][]string{
		"read":  a.Permissions.Read,
		"write": a.Permissions.Write,
		"net":   a.Permissions.Net,
		"run":   a.Permissions.Run,
		"env":   a.Permissions.Env,
	} {
		if scopes != nil {
			out[axis] = append([]string(nil), scopes...)
		}
	}
	return out
}

// AgentReg holds discovered agent definitions. Created by scanning
// agent directories; entries are parsed on first access.
type AgentReg struct {
	globalDir string               // ~/.config/genie/agents/
	localDir  string               // .genie/agents/ in cwd
	cache     map[string]*AgentDef // name → parsed def
}

// NewAgentReg creates a registry. Directories are scanned lazily.
func NewAgentReg(globalDir, localDir string) *AgentReg {
	return &AgentReg{
		globalDir: globalDir,
		localDir:  localDir,
		cache:     make(map[string]*AgentDef),
	}
}

// List returns all available agent names. Scans both directories,
// merges (local overrides global), and caches.
func (r *AgentReg) List() []string {
	seen := make(map[string]bool)
	var names []string

	// Global agents first.
	for _, def := range r.scanDir(r.globalDir) {
		if !seen[def.Name] {
			seen[def.Name] = true
			names = append(names, def.Name)
		}
		r.cache[def.Name] = def
	}

	// Local agents override.
	for _, def := range r.scanDir(r.localDir) {
		if !seen[def.Name] {
			seen[def.Name] = true
			names = append(names, def.Name)
		}
		r.cache[def.Name] = def
	}

	slog.Debug("agent list", "count", len(names), "names", strings.Join(names, ", "))
	return names
}

// Get returns the named agent definition, or nil if not found.
// Scans and caches on first access.
func (r *AgentReg) Get(name string) (*AgentDef, error) {
	// Check cache first.
	if def, ok := r.cache[name]; ok {
		return def, nil
	}

	// Scan both dirs to populate cache.
	r.List()

	if def, ok := r.cache[name]; ok {
		return def, nil
	}
	return nil, nil
}

// GetResolved returns a fully resolved agent with config defaults
// applied. availableSkills is the discovered skill pool used both to fill in
// "inherit all" and to validate an agent's explicit skills list — a name not
// in the pool is a hard error. Returns error if agent not found.
func (r *AgentReg) GetResolved(name string, cfg *Config, availableSkills []string) (*ResolvedAgent, error) {
	def, err := r.Get(name)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, fmt.Errorf("agent %q: not found", name)
	}
	if err := validateAgentSkills(name, def.Skills, availableSkills); err != nil {
		return nil, err
	}
	resolved := ResolveAgentDef(def, cfg, availableSkills)
	return &resolved, nil
}

// validateAgentSkills rejects skill names in an agent's explicit skills list
// (non-nil) that are not present in the discovered pool. Absent (nil) skills
// inherit the pool, so nothing to validate; an empty list selects none.
func validateAgentSkills(agentName string, declared, available []string) error {
	if declared == nil {
		return nil
	}
	pool := make(map[string]bool, len(available))
	for _, name := range available {
		pool[name] = true
	}
	for _, name := range declared {
		if !pool[name] {
			return fmt.Errorf("agent %q: unknown skill %q (not in the discovered skill pool)", agentName, name)
		}
	}
	return nil
}

// scanDir reads a directory for .toml agent definition files.
// Skips dotfiles. Returns parsed AgentDefs (cached by caller).
func (r *AgentReg) scanDir(dir string) []*AgentDef {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		slog.Warn("failed to read agent directory", "dir", dir, "error", err)
		return nil
	}

	var defs []*AgentDef
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !strings.HasSuffix(name, ".toml") {
			continue
		}

		agentName := strings.TrimSuffix(name, ".toml")
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("failed to read agent file", "path", path, "error", err)
			continue
		}

		def, err := ParseAgentDef(data, agentName, path)
		if err != nil {
			slog.Warn("failed to parse agent file", "path", path, "error", err)
			continue
		}
		defs = append(defs, def)
	}
	return defs
}
