# Genie

A minimal, std-lib-first Go terminal agent harness. A REPL that runs an agentic loop against an OpenAI-compatible provider.

## Install

Requires Go 1.24+.

```
go install github.com/okayest-dev/genie/cmd/genie@latest
```

Or build from source:

```
git clone https://github.com/okayest-dev/genie && cd genie
make build
```

## Quick start

1. Set your API key:

```
export OPENCODE_API_KEY="sk-..."
```

2. Run:

```
genie
```

You get an interactive `genie>` prompt. Type naturally — the model can read, write, edit files, and run shell commands.

Selecting a different provider that ships by default needs no key: the `copilot` provider authenticates through genie's own credential store instead of an environment variable (see [Providers and copilot auth](#configuration)).

If no provider is configured, genie lists the declared providers on first start and asks you to pick one.

## Usage

### Interactive REPL

```
genie
```

Starts an interactive session at the `genie>` prompt. Each input runs a full agent loop — the model produces text and/or tool calls, the harness executes them, and results are fed back until the model stops calling tools.

### Non-interactive mode

```
genie -p "explain this project"
```

Runs a single prompt, prints the reply to stdout, and exits. Tool calls requiring permissions the base policy does not cover are auto-denied in this mode; use `-p --approve-all` to blanket-approve every escalation for that single run (never persisted, refused in the interactive REPL).

With no provider selected, `-p` runs on the first declared provider and prints a warning — it cannot prompt. No declared providers at all is a startup error.

### Slash commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/quit`, `/exit` | Exit the REPL |
| `/new` | Start a new session |
| `/provider` | List providers (`*` marks the current one) |
| `/provider <id>` | Switch providers mid-session; the next turn runs on that provider's default model and the transcript continues |
| `/model` | List the current provider's model catalog (`*` marks the current model) |
| `/model <id>` | Switch to a different model within the current provider's catalog |

Ctrl+C cancels a running turn. Ctrl+C at the prompt exits.

## Tools

The model has access to five tools:

| Tool | Description |
|------|-------------|
| **read** | Read file contents or list directories. Supports offset/limit pagination. Rejects binary files. |
| **write** | Create or overwrite files. Permission-gated on the write axis (see [Permissions](#permissions)). Auto-creates parent directories. |
| **edit** | Surgical find-and-replace. Exact, whitespace-sensitive matching. One pair at a time. |
| **bash** | Run shell commands via `sh -c`. Permission-gated on the run and net axes (see [Permissions](#permissions)). 120s timeout (configurable). |
| **request_permission** | Pre-negotiate a permission grant for a call you expect to be denied (see [Permissions](#permissions)). Never auto-approved. |

`read`, `write`, `edit`, and `bash` can be individually disabled in config. `request_permission` is always-on for every default agent — it is a negotiation channel, not a capability — so it has no config toggle; an agent whose explicit `tools = [...]` list omits it drops it from that agent's toolset. The `env` axis is gated only at runtime (mid-execution runtime checks); there is no standalone `env` tool.

## Configuration

Config lives at `~/.config/genie/config.toml` (XDG-aware). The file is optional — everything has sensible defaults.

Precedence: **defaults < config file < environment variables**.

### Config file

```toml
# The active provider, named from the [providers] tables below. Everything the
# harness boots on — wire, endpoint, key env, default model — comes from that
# provider's table, so usually you only set this and a base_url/api_key_env.
# Unset, the REPL prompts you to pick from the declared providers; one-shot
# -p mode falls back to the first declared provider with a warning.
provider = "zen"

# api_key_env, model, base_url, and wire live inside [providers.<name>] tables.
# instruction_file = ""      # path to agent instruction file
# session_dir = ""           # defaults to ~/.config/genie/sessions
bash_timeout = 120

[tools]
read = true
write = true
edit = true
bash = true

[plugins]
# dir = "~/.config/genie/plugins"
# enable = ["my-plugin"]
# disable = ["broken-plugin"]

[skills]
# dirs = ["/custom/skills"]   # replaces the default three-directory stack
# enable = ["alpha"]          # allowlist: when set, only named skills load
# disable = ["beta"]          # denylist, applied on top of enable

[context]
# turns = 0   # prior turns of history carried into each new turn; 0 = all

[permissions]
# Per-axis base policy: scopes already covered without escalation. Restrictive
# default when the section is absent — read covers the project tree ("./"),
# and write/net/run/env are empty, so any need on those axes escalates. A
# keyed axis replaces its default scope list outright (replace-not-merge).
# read  = ["."]
# write = []
# net   = []
# run   = []
# env   = []

# Permanent grants loaded at startup and honored across restarts. `granted`
# timestamps are written by the harness when a permanent grant is negotiated.
# [[permissions.permanent]]
# permission = "read"
# scope      = "/etc"
# granted    = 2026-09-10T12:00:00Z

[providers]
# Declared providers, keyed by name. A provider is one wire, one endpoint,
# one default model. Every valid provider ships as a default — one per
# bundled wire: zen, openai, anthropic, responses, google, copilot, bedrock
# — so a table for a known name only needs to override the keys you want to
# change.
#
# [providers.zen]
# base_url = "https://gateway.example/zen/v1"   # override just the endpoint
#
# [providers.deepseek]
# wire        = "openai"
# base_url    = "https://api.deepseek.com/v1"
# api_key_env = "DEEPSEEK_API_KEY"
# model       = "deepseek-chat"
# models      = ["deepseek-chat", "deepseek-reasoner"]  # optional catalog
# opts        = { cost = 2 }                            # wire-specific
#
# [providers.copilot]
# # Copilot needs no api_key_env: the wire authenticates through genie's own
# # credential store. opts.domain points at a GitHub Enterprise tenant; the
# # default is github.com.
# opts        = { domain = "tenant.ghe.com" }
#
# [providers.bedrock]
# # Bedrock needs no api_key_env: the wire authenticates through the AWS SDK
# # standard credential chain (env vars, shared config, SSO, assume-role,
# # credential_process). opts.profile selects an AWS profile and opts.region
# # the region; absent, the chain's defaults apply.
# opts        = { profile = "my-role", region = "us-east-1" }
#
# A provider missing a default model, or naming an unknown wire, fails at
# startup; duplicate provider tables are rejected. Selecting a provider that
# has no table also fails at startup. With
# no provider selected and no declared providers, startup fails naming the
# requirement.
```

### Per-agent permissions

Named agents can declare their own `[permissions]` section in their agent TOML file. A present section **replaces** the harness global base entirely (replace-not-merge, like agent `tools`). An unnamed axis in the agent's section is left empty — there is no axis-level inheritance. Permanent grants (`[[permissions.permanent]]`) are always harness-global and apply additively over either base.

```toml
# ~/.config/genie/agents/restrictive.toml
model = "some-model"

[permissions]
read  = []   # explicit empty = nothing authorized on this axis
write = []
net   = []
run   = []
env   = []
```

The default skill discovery stack is, in priority order (lowest wins): `./.genie/skills`, `~/.agents/skills`, and `~/.config/genie/skills`. Setting `[skills] dirs` or `GENIE_SKILL_DIR` replaces the stack entirely; `enable`/`disable` still apply on top. Individual agents can override the inherited set with a `skills = [...]` key in their agent TOML — unset inherits all discovered skills, `skills = []` binds none, and unknown names error at agent resolution. Skills are injected into the instruction between the instruction file and AGENTS.md. See [docs/agent-definitions.md](docs/agent-definitions.md) and [docs/skills.md](docs/skills.md).

### Permissions

Genie uses a **per-axis permission escalation model** across all tools. When a tool call needs access the effective policy does not already cover, the streaming turn pauses and the harness negotiates inline, one axis at a time, in the fixed order `read` → `write` → `net` → `run` → `env`.

#### The escalation prompt

```
allow write /work/report.md? (o)nce/(s)ession/(p)ermanent/(r)eject:
allow net? (o)nce/(s)ession/(p)ermanent/(r)eject:
```

Answer with a terse key (`o`/`s`/`p`/`r`) or the full word (`once`/`session`/`permanent`/`reject`). An unknown answer prints `:: unknown choice - o/s/p/r` and re-prompts. `^C` while a prompt is live rejects the current axis (it does not cancel the turn).

| Grant | Lifetime |
|-------|----------|
| **once** | Just this call — spent on execute, discarded on deny, never carried forward. |
| **session** | Until the REPL exits. |
| **permanent** | Appended to `~/.config/genie/config.toml` as a `[[permissions.permanent]]` block with a timestamp; honored on every later run. |
| **reject** | The call is not executed; the model receives a composite grant/reject result and can reformulate. |

The prompt shows the **exact normalized scope** for that axis:
- **read / write**: absolute file paths (e.g., `/work/report.md`). A grant covers the path and everything beneath it (prefix match — `/work` covers `/work/src/main.go`).
- **net**: `host:port` or `*.domain:port` wildcard. `*.github.com:443` covers `api.github.com:443`; a bare hostname matches exactly.
- **run**: exact executable path (e.g., `/usr/bin/git`).
- **env**: exact variable name (e.g., `DB_HOST`).
- **Blanket (axis-only)**: scope omitted = blanket access on that axis (covers any scope on that axis).

#### Escalation chain rules

- Axes are prompted one at a time in fixed order (`read` → `write` → `net` → `run` → `env`).
- A denial on one axis **does not stop the chain** — remaining axes still prompt.
- Grants persist **immediately** into the store (so a later axis in the same chain can reuse them).
- The tool call **executes only if every required axis is granted**.
- A granted scope is **never broader than what was requested** — the normalized scope from the prompt is what the grant covers.

#### Effective policy

The effective policy is the union of four tiers, most-specific-first:
1. **Base** — `[permissions]` in config (or per-agent replacement), restrictive default when absent: `read = ["."]`, all others empty.
2. **Permanent** — `[[permissions.permanent]]` entries loaded at startup.
3. **Session** — grants made during the REPL session.
4. **Once** — single-call grants bound to the current tool call.

The model **never sees tiers or provenance** — it only sees a flat, per-axis scope list in the session-start snapshot (see below).

#### Session-start snapshot

Every instruction carries a flat, tier-free snapshot of the resolved base policy plus the negotiation mechanism — one line per axis in fixed order (`read` → `write` → `net` → `run` → `env`), empty axes reading `nothing is authorized`:

```
Current permissions:
- read: /work
- write: nothing is authorized
- net: nothing is authorized
- run: nothing is authorized
- env: nothing is authorized

When a tool call needs a permission you do not already have, the turn pauses for negotiation and returns a Permission granted: or Permission rejected: result line. A granted scope is reused — paths match by prefix, hosts by subdomain wildcard, executables and environment variables exactly. To request access in advance, call request_permission rather than the tool itself. After a rejection, pursue a materially different alternative before re-asking.
```

This snapshot is appended after AGENTS.md (and any skill layer) on every turn, in both `-p` and REPL runs.

#### Headless (`-p`) mode

Non-interactive `-p` runs have no one to ask:
- **Default**: every uncovered requirement is auto-denied; the denial is fed back to the model as a `Permission rejected:` line.
- **`--approve-all`**: blanket-approves every escalation for that single run — the same in-memory grants, effective only for that turn, **never persisted to config**. This flag is **refused in the interactive REPL** (exit code 3, clear error) because a session has a user to ask.

Setting the relevant base scope in config (e.g., `write = ["."]`) authorizes it up front for headless runs.

#### Mid-call runtime denial (`NotCapable`)

If a tool fails mid-execution with a runtime permission error, the harness rewrites it into a fixed composite before it reaches the model:

```
status: call not executed — write unavailable at runtime
hint: inline escalation is not available mid-execution — request write access in advance via request_permission, or reformulate.
```

Inline escalation is not available mid-call; the composite always directs the model to pre-negotiate via `request_permission`.

#### Pre-negotiation with `request_permission`

The model can ask for a grant ahead of a call it expects to be denied — or after a mid-call runtime denial — through the `request_permission` tool:

```json
{"permission": "write", "scope": "/work/output.txt"}
```

- `permission`: one of `read`, `write`, `net`, `run`, `env` (required).
- `scope`: optional; omitted = blanket access on that axis.

The user gets the same terse prompt as inline escalation, and the result is exactly one `Permission granted:` or `Permission rejected:` line. A grant covers **exactly the scope requested**, so the model should request its widest anticipated need. `request_permission` is a negotiation channel, not a capability — it never auto-approves, and a rejection grants nothing. It is always-on in every default agent; an agent whose explicit `tools = [...]` list omits it drops it from that agent's toolset.

#### Retired: the old confirm gates

The previous `tools.Confirmer` seam (bash every-call prompts, write-overwrite prompts) has been removed. The per-axis escalation flow replaces it entirely — there is now exactly one permission mechanism. `config.Tools.AutoApprove` was never implemented and does not ship.

### Environment variables

| Variable | Description |
|----------|-------------|
| `GENIE_PROVIDER` | Selects the active provider by name (beats the config file's `provider` key) |
| `GENIE_INSTRUCTION_FILE` | Path to agent instruction file |
| `GENIE_SESSION_DIR` | Session storage directory |
| `GENIE_BASH_TIMEOUT` | Bash command timeout (seconds) |
| `GENIE_PLUGIN_DIR` | Plugin discovery directory |
| `GENIE_SKILL_DIR` | Skill discovery directory (replaces all skill dirs) |
| `GENIE_CONTEXT_TURNS` | Prior turns of history carried into each new turn (`0` = all) |
| `GENIE_DEBUG` | Enable debug mode (`true`/`1`/`yes`) |

### Debug and verbose modes

```
genie -v          # verbose: high-level flow to stderr
genie -d          # debug: low-level detail (implies -v)
GENIE_DEBUG=1 genie  # same as -d, via env var
```

Verbose shows config resolution, instruction assembly, turn lifecycle, and token usage. Debug adds HTTP requests, SSE chunks, and full config values.

## Wire protocols

The active provider names its wire explicitly (`[providers.<name>] wire`, defaulting to the shipped provider's wire — `zen` is OpenAI-compatible `openai`). Wire selection no longer happens by model-prefix auto-detection or a `GENIE_WIRE` override: a turn starts on the active provider's own wire and model, and there is no global-model fallback.

If a model doesn't support tool calling, the harness retries without tools — letting free/non-tool models still work. In that fallback — and for any other text-only reply — genie also recognises tool invocations the model expresses as fenced code blocks: a block whose info string names a registered tool (e.g. ` ```bash\nmake test\n``` `) is executed like a native tool call, its result fed back, and the turn continues until the model finishes. Fence content that is a JSON object is used verbatim as the tool's arguments; otherwise it is wrapped into the tool's single required string property (e.g. `{"command": "<content>"}` for `bash`).

## Plugins

Genie supports plugins via NDJSON-RPC 2.0 over stdio. Drop an executable into `~/.config/genie/plugins/` and it's loaded automatically.

### Plugin types

- **Tool plugins** — add new tools to the harness
- **Lifecycle plugins** — hook into the agent loop around each turn
- **Command plugins** — expose user-typed slash commands (`/<plugin> <command>`, e.g. `/copilot auth`) via the `commands` capability (`commands/list`, `commands/run`, optional `commands/help`); useful for plugin-owned login/credential workflows. Slash names are single-token within the plugin and fully namespaced, so nothing collides with genie's built-in commands.

### Plugins and commands

A command plugin advertises `commands: true` in its handshake and answers `commands/list` (cached at load) with `{name, description, usage}` entries. The REPL routes two-token input `/<plugin> <command> …` to the plugin via `commands/run`, passing the raw argument string. Bare `/<plugin>` invokes the plugin's optional `commands/help`, falling back to its flat listing. `/help` includes a flat Plugin-commands section enumerating each plugin's command (name + description). Misses surface as `unknown command: /foo (try /help)`, `copilot: no such command: nope`, or `plugin copilot is not active`. A command's `text` is printed, or compact JSON of its `data` when `text` is empty. Command RPC shares the tool-call budget (5s timeout) and failure rules; interactive work (like a device-flow login) completes inside the plugin's own process.

Command names are single-token within a plugin (whitespace or a leading slash is dropped with a warning; duplicate names resolve last-wins). A plugin whose own name matches a built-in slash command (`help`, `quit`, `exit`, `new`, `changes`, `model`, `provider`, `agent`) is rejected at load with a warning — the built-in wins.

### Lifecycle hooks

Lifecycle plugins observe and can rewrite each turn as it runs, via five
synchronous events. The wire schema lives in `protocol/schema.yaml` like the
rest of the plugin protocol.

| Event | Fire point | Rewrites |
|-------|-----------|----------|
| `lifecycle/request_built` | once per turn, before the first stream | the assembled request (model, messages, tools) |
| `lifecycle/tool_before` | before each tool executes | tool arguments; may also `suppress` the call or wipe them via `set_empty` |
| `lifecycle/tool_after` | after each tool call completes (errors are a field) | result text |
| `lifecycle/response_ready` | per streamed text delta, then a `final` release carrying finish reason + usage | the delta text |
| `lifecycle/turn_error` | once, when a turn exits with an error | none (observe-only) |

Hooks run as **sync ordered chains**. Request-side events
(`request_built`, `tool_before`) fire in the order plugins are listed in
`[lifecycle.plugins]`; response-side events (`tool_after`, `response_ready`)
fire in the reverse (onion) order so paired plugins pack and unpack. The list
only needs the plugins you order explicitly — anything omitted is appended in
discovery order:

```toml
[lifecycle.plugins]
order = ["guardrails", "logging"]
```

A plugin declares which events it wants on the wire in its handshake
capabilities: `lifecycle_request_built`, `lifecycle_tool_before`,
`lifecycle_tool_after`, `lifecycle_response_ready`, `lifecycle_turn_error`.

**Failure semantics.** Hooks degrade by default: if a hook errors, it is
skipped and any earlier hooks' contributions are kept — a failing plugin never
fails a turn. A plugin may opt in to a stricter per-event contract by setting
`"fatal": true` on its result; the turn then aborts with a
`FatalHookError` naming the plugin and event (a fatal `turn_error` preserves
the original error it aborted on). Suppressing in `tool_before` kills the tool
call — the harness moves on and keeps the conversation well-formed. A
`tool_before` hook that sets `"set_empty": true` wipes the arguments to the
empty string — deliberately distinct from omitting `arguments` (no change) —
and the wiped call still passes through the normal arguments validation, so it
fails closed unless the empty string is valid for the tool. The wipe is not a
`suppress`: a suppressed call is killed before execution and the harness
reports `"Tool call suppressed by lifecycle hook."`, while a wiped call keeps
running the tool path, its empty arguments hit the same validation as any
rewrite, and a resulting error is surfaced as a normal tool error the model
can act on.

### Plugin layout

Plugins can be laid out in two ways:

**Directory layout (recommended):**
```
~/.config/genie/plugins/
  copilot/
    manifest.toml
    config.toml     # optional, plugin-specific
    copilot         # binary
```

**Flat layout (backward compatible):**
```
~/.config/genie/plugins/
  copilot           # binary
  copilot.toml      # manifest
```

### Plugin manifest (optional)

A TOML file describing the plugin. In directory layout, place it inside the plugin directory as `manifest.toml`. In flat layout, place it next to the executable as `<name>.toml`:

```toml
name = "my-plugin"
version = "1.0.0"
capabilities = ["tools", "commands"]
```

### Plugin enable/disable

```toml
[plugins]
dir = "~/.config/genie/plugins"
enable = ["my-plugin"]  # explicit allowlist (empty = all)
disable = ["broken"]    # denylist (takes precedence)
```

Max 16 plugins loaded concurrently. Plugins that crash or hang are automatically marked inactive.

## Agent instructions

Genie reads `AGENTS.md` from the working directory (if present) and sends it as the agent instruction on every turn. Set `instruction_file` in config or `GENIE_INSTRUCTION_FILE` env var to use a different file. The permission snapshot and negotiation mechanism paragraph (see [Permissions](#permissions)) are appended after AGENTS.md on every turn.

## Session persistence

Sessions are saved as JSONL in `~/.config/genie/sessions/`. Each session carries a change ledger — a record of every file change made during that session, grouped into change batches with unified diffs. Use `/new` to start a fresh session.

## License

GPL v3 — see [LICENSE](LICENSE).
