# Design: Agent Definition Schema and File Discovery

**Ticket**: genie-7l6
**Status**: Resolution

---

## Agent Definition TOML Schema

An agent definition is a flat TOML file. Every field is optional; omitted fields inherit from the harness config. The filename stem is the agent name.

```toml
# ~/.config/genie/agents/coder.toml

# Model ID to use when this agent is active. Empty = inherit from config.
model = "big-pickle"

# Path to the instruction file for this agent. Empty = inherit from config.
# Supports ~ for home directory.
instruction_file = "~/.config/genie/agents/coder-instructions.md"

# Tool names this agent may use. Absent or empty = inherit all tools.
# When set, ONLY these tools are available (replaces the global set).
# Names must match registered tool names: read, write, edit, bash,
# plus any plugin-registered tool names.
tools = ["read", "write", "edit", "bash"]

# Whether to include AGENTS.md from the working directory.
# Default: true. Set to false to isolate the agent from project context.
inherit_agents_md = true
```

### Go types

Following the config.go pattern — pointer types for optional fields, a resolver function:

```go
// internal/config/agent.go

// AgentDef is the resolved agent definition. All fields are zero-value when
// inherited from harness config; the caller checks for zero to decide
// whether to use the agent's value or the config default.
type AgentDef struct {
    Name            string   // derived from filename stem, not in TOML
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
```

### Defaults resolution

The `AgentDef` fields are checked at use-site, not at parse-time. The caller applies defaults:

```go
// ResolveAgentDef fills in zero-value fields from harness config.
// Returns a new AgentDef; does not mutate the original.
func ResolveAgentDef(def *AgentDef, cfg *Config) ResolvedAgent {
    r := ResolvedAgent{
        Name:   def.Name,
        Source: def.Source,
        Model:  cfg.Model,           // default: harness config model
        InstructionFile: cfg.InstructionFile, // default: harness config
        Tools:  allToolNames(cfg.Tools),      // default: all enabled tools
        InheritAgentsMD: true,                // default: include AGENTS.md
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

type ResolvedAgent struct {
    Name            string
    Source          string
    Model           string
    InstructionFile string
    Tools           []string
    InheritAgentsMD bool
}
```

### Validation

At parse time:
- Unknown TOML keys → hard error (strict, matches config.go)
- Tool names are NOT validated at parse time (plugins may not be loaded yet); validated at use-time when the registry is available

No required fields. An empty file (`[empty]` or truly empty) produces an `AgentDef` with all zero values — meaning "inherit everything from config."

### Why no version field

The schema is small and flat. A `version` field adds complexity with no benefit until there's a second schema version to distinguish. Add it when v2 arrives, not before.

---

## File Discovery

### Directory layout

Two directories, scanned in order:

1. **Global**: `~/.config/genie/agents/` (or `$GENIE_CONFIG_DIR/genie/agents/` when `GENIE_CONFIG_DIR` is set)
2. **Project-local**: `.genie/agents/` in the working directory

Project-local agents override global agents of the same name.

### Scan rules

Following the plugin discovery pattern (`internal/plugin/manager.go`):

1. Read the directory with `os.ReadDir`
2. Skip entries starting with `.` (dotfiles, dotdirs)
3. For each entry:
   - **File**: must end with `.toml`. Name stem = agent name. Skip otherwise.
   - **Directory**: ignored (no nested agent directories — flat layout only)
4. Parse each `.toml` file with `ParseAgentDef`, deriving the name from the filename stem

```
~/.config/genie/agents/
  coder.toml          → agent "coder"
  reviewer.toml       → agent "reviewer"
  .hidden.toml        → skipped (dotfile)
  notes.txt           → skipped (not .toml)

.genie/agents/
  coder.toml          → overrides global "coder"
  project-bot.toml    → agent "project-bot" (project-local only)
```

### Name derivation

- Filename stem: `code-reviewer.toml` → `code-reviewer`
- Allowed characters: lowercase alphanumeric + hyphens (`[a-z0-9-]`)
- Names are case-sensitive (filesystem is case-sensitive on Linux)
- No reserved names — any valid filename stem works
- Duplicate in same directory: last-one-wins (os.ReadDir order is filesystem-dependent; log a warning)

### Loading strategy: lazy

Agent definitions are **not** scanned at startup. They are loaded on demand:

- **`/agent` (list)**: scan both directories, merge, return the list
- **`/agent <name>`**: scan both directories, find the named agent, parse and return
- **`-a <name>`**: scan both directories at startup, find the named agent, parse and resolve
- **`@name`**: scan both directories, find the named agent, parse and return

This avoids startup cost when agents aren't used, and matches the plugin pattern (plugins are scanned at startup because they register tools/wires, but agents are lighter-weight).

### AgentReg: the agent registry

A lightweight in-memory cache populated by scanning:

```go
// internal/config/agent.go

// AgentReg holds discovered agent definitions. Created by scanning
// agent directories; entries are parsed on first access.
type AgentReg struct {
    globalDir string // ~/.config/genie/agents/
    localDir  string // .genie/agents/ in cwd
    cache     map[string]*AgentDef // name → parsed def
}

// NewAgentReg creates a registry. Directories are scanned lazily.
func NewAgentReg(globalDir, localDir string) *AgentReg

// List returns all available agent names. Scans both directories,
// merges (local overrides global), and caches.
func (r *AgentReg) List() []string

// Get returns the named agent definition, or nil if not found.
// Scans and caches on first access.
func (r *AgentReg) Get(name string) (*AgentDef, error)

// GetResolved returns a fully resolved agent with config defaults
// applied. Returns error if agent not found.
func (r *AgentReg) GetResolved(name string, cfg *Config) (*ResolvedAgent, error)
```

### Interaction with config

The `config.Config` struct gains one new field:

```go
type Config struct {
    // ... existing fields ...
    DefaultAgent string // default_agent in TOML, GENIE_DEFAULT_AGENT env
}
```

This lets users set a default agent in `config.toml`:

```toml
default_agent = "coder"
```

When set, the harness starts with this agent active instead of the "no named agent" default. The `DefaultAgent` is resolved at config-load time (same precedence as other config fields).

---

## Example agent definitions

### Minimal (inherit everything)

```toml
# ~/.config/genie/agents/coder.toml
# Empty file — agent "coder" inherits all config defaults.
```

### Model-only

```toml
# ~/.config/genie/agents/fast.toml
model = "gpt-4o-mini"
```

### Restricted tools, no AGENTS.md

```toml
# ~/.config/genie/agents/reviewer.toml
model = "big-pickle"
tools = ["read", "bash"]
inherit_agents_md = false
```

### Full definition

```toml
# ~/.config/genie/agents/dev.toml
model = "big-pickle"
instruction_file = "~/genie-agents/dev-instructions.md"
tools = ["read", "write", "edit", "bash"]
inherit_agents_md = true
```

---

## Files to create/modify

| File | Action | Purpose |
|------|--------|---------|
| `internal/config/agent.go` | **Create** | AgentDef, ParseAgentDef, AgentReg, ResolveAgentDef |
| `internal/config/agent_test.go` | **Create** | Tests for parsing, discovery, resolution |
| `internal/config/config.go` | **Modify** | Add `DefaultAgent` field to Config struct and fileConfig |
| `internal/instruct/instruct.go` | **Modify** | Accept ResolvedAgent, adjust stacking (later ticket) |
| `internal/repl/repl.go` | **Modify** | Add AgentReg to Config, /agent command (later ticket) |
| `cmd/genie/main.go` | **Modify** | Wire AgentReg, resolve default agent (later ticket) |

The schema and discovery design is complete. Implementation is blocked on the downstream tickets (instruction stacking, tool-set switching, REPL switching).
