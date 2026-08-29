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
	Model           string   // empty = inherit config model
	InstructionFile string   // empty = inherit config instruction_file
	Tools           []string // nil = inherit all; non-nil = exact set
	InheritAgentsMD *bool    // nil = true (inherit); explicit false = don't
}

// fileAgentDef is the TOML schema with pointer types for optionals.
type fileAgentDef struct {
	Model           string   `toml:"model"`
	InstructionFile string   `toml:"instruction_file"`
	Tools           []string `toml:"tools"`
	InheritAgentsMD *bool    `toml:"inherit_agents_md"`
}

// ResolvedAgent is an AgentDef with all fields resolved against harness
// config defaults. No zero-value fields remain — every field is usable as-is.
type ResolvedAgent struct {
	Name            string
	Source          string
	Model           string
	InstructionFile string
	Tools           []string
	InheritAgentsMD bool
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
	}, nil
}

// ResolveAgentDef fills in zero-value fields from harness config.
// Returns a new ResolvedAgent; does not mutate the original.
func ResolveAgentDef(def *AgentDef, cfg *Config) ResolvedAgent {
	r := ResolvedAgent{
		Name:            def.Name,
		Source:          def.Source,
		Model:           cfg.Model,
		InstructionFile: cfg.InstructionFile,
		Tools:           allToolNames(cfg.Tools),
		InheritAgentsMD: true,
	}
	if def.Model != "" {
		r.Model = def.Model
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
	return names
}

// AgentReg holds discovered agent definitions. Created by scanning
// agent directories; entries are parsed on first access.
type AgentReg struct {
	globalDir string            // ~/.config/genie/agents/
	localDir  string            // .genie/agents/ in cwd
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
// applied. Returns error if agent not found.
func (r *AgentReg) GetResolved(name string, cfg *Config) (*ResolvedAgent, error) {
	def, err := r.Get(name)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, fmt.Errorf("agent %q: not found", name)
	}
	resolved := ResolveAgentDef(def, cfg)
	return &resolved, nil
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
