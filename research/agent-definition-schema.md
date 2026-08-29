# Research: Agent Definition Schema and File Discovery

## TOML Parsing

- Library: `github.com/BurntSushi/toml v1.6.0` (only external dep)
- Idiom: `toml.Decode(string(data), &target)` — no `DecodeFile`
- Strict: `MetaData.Undecoded()` detects unknown keys, causes hard failure (`config.go:128`)
- Optional fields: `*bool`, `*int` pointer types distinguish "not set" from zero values

## Config Loading Pattern (`internal/config/config.go`)

- Three-layer: `defaults() → file → env`
- `fileConfig` struct mirrors `Config` with pointer types for optionals
- `Parse()` applies each layer with `if field != nil` / `if field != ""` guards
- Env overrides: 9 `GENIE_*` vars checked one-by-one (lines 261–310)
- Unknown keys → hard failure (line 128–134)

## Plugin Manifest Pattern (`internal/plugin/manifest.go`)

- Two-pass lookup: directory layout first (`<dir>/<name>/manifest.toml`), flat fallback (`<dir>/<name>.toml`)
- `ParseManifest()` validates name/version/capabilities are all present
- Name derived from binary name minus extension

## Plugin Discovery (`internal/plugin/manager.go:70`)

- `os.ReadDir(pluginDir)` → skip dotfiles
- Directory entries: look for `<dir>/<name>/<name>` binary, check executable bit
- Flat entries: use file directly, skip `.go` files, check executable bit
- Max 16 plugins, filtered by enable/disable lists
- Name: `filepath.Base(path)` — file/dir name is the plugin name

## Instruction Assembly (`internal/instruct/instruct.go`)

- `Load(cfg, cwd)` — single function, 50 lines
- Stacking order: DefaultPrompt → cfg.InstructionFile → AGENTS.md (cwd only)
- All joined with `"\n"` separator
- AGENTS.md: silent skip if missing, no parent directory walk
- Returns single string — immutable for session

## REPL Config (`internal/repl/repl.go:26-35`)

```go
type Config struct {
    Client      llm.Client
    Model       string
    Instruction string
    SessionDir  string
    Registry    *tools.Registry
    Stdin       io.Reader
    Stdout      io.Writer
    Stderr      io.Writer
}
```

- Instruction is a plain string, assembled once in main.go
- REPL does not know about config parsing — receives resolved dependencies

## main.go Wiring (`cmd/genie/main.go`)

Startup sequence (line 53, `run()`):
1. Pre-scan args for `-p` flag (lines 57–93)
2. Parse flags (lines 95–103)
3. `config.Load()` → `*config.Config` (line 116)
4. `os.Getwd()` (line 121)
5. `instruct.Load(cfg, cwd)` → instruction string (line 126)
6. Wire detection: `cfg.Wire` or `llm.DetectWire(cfg.Model)` (lines 131–134)
7. `llm.NewClient(wire, baseURL, cfg.APIKey)` (line 139)
8. `buildRegistry(cwd, cfg.Tools, cfg.BashTimeout)` → `*tools.Registry` (line 146)
9. Plugin loading (lines 149–153)
10. Provider/route-table from plugin wires (lines 157–182)
11. Either `repl.Run()` (line 196) or `agent.RunTurn()` (line 228)

`buildRegistry()` (line 249) hardcodes four built-in tools, disables per config.
`repl.Config` constructed inline (lines 186–195).

## Patterns for Agent Definitions

1. **TOML parsing**: Follow `config.go` pattern — `fileAgent` struct with pointer types, strict key checking
2. **File discovery**: Follow `manifest.go` two-pass pattern — dir-first, flat-fallback in `~/.config/genie/agents/`
3. **Config overlay**: Follow `defaults() → file → env` three-layer precedence
4. **Instruction hook**: Modify `instruct.Load()` to accept agent parameter, insert agent instruction at right stacking position
5. **Validation**: Unknown keys cause hard failure — new schema must be exhaustive
6. **Tests**: Table-driven, inline TOML strings, test `Parse()` directly (pattern from `config_test.go`)
7. **File naming**: `<name>.toml` in agents directory, filename stem = agent name
