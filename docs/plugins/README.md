# Genie Plugins

Genie's plugin framework extends the harness with external processes. A plugin is any executable that speaks the genie plugin protocol over stdio — you can write one in any language. Plugins add four kinds of behaviour to the harness:

| Kind | What it does |
|------|--------------|
| **Tool plugin** | Adds new tools the model can call (`tools/list`, `tools/call`) |
| **Wire plugin** | Adds a new provider backend, e.g. AWS Bedrock or GitHub Copilot (`wire/*`) |
| **Lifecycle plugin** | Hooks into the agent loop around each turn (`lifecycle/*`) |
| **Command plugin** | Exposes user-typed slash commands in the REPL (`commands/*`) |
| **Context plugin** | Hooks into context management around each request (`context/*`) |

A single plugin can declare any combination of these — a wire plugin commonly also registers slash commands (e.g. `/copilot auth login`).

The protocol is NDJSON-RPC 2.0 over stdio: the host writes a JSON request per line to the plugin's stdin, the plugin replies with one JSON response per line on stdout. Logging goes to stderr. Everything a plugin can do is a method on this protocol.

- **[Installing and using plugins](using.md)** — for people who want to make Genie do more.
- **[Writing plugins](authoring.md)** — for people who want to build a plugin of their own.
- **[Protocol reference](protocol.md)** — the complete wire-level reference (methods, params, results, error handling), plus a worked example.

## How plugins fit in

Plugin discovery is directory-based. Drop an executable into the plugin directory (default `~/.config/genie/plugins/`), and the next `genie` run loads it:

```
~/.config/genie/plugins/
  copilot/
    manifest.toml     # optional: name, version, capabilities
    config.toml       # plugin-specific, optional
    copilot           # the executable
```

Plugins that crash or hang are marked *inactive* and their calls fail fast with a clear message rather than blocking the session. Up to 16 plugins load concurrently.

## Capabilities

At startup the host asks each plugin `capabilities/list`. The plugin's answer declares which features it provides and which hooks it wants to receive. The capability keys are:

| Capability | Methods | Purpose |
|------------|---------|---------|
| `tools` | `tools/list`, `tools/call` | Add tools the model can invoke |
| `wires` | `wire/init`, `wire/stream`, `wire/list_models` | Serve a provider backend |
| `commands` | `commands/list`, `commands/run`, optional `commands/help` | Register REPL slash commands |
| `context_before` | `context/before_request` | Rewrite every outgoing request |
| `context_after` | `context/after_response` | Observe completed turns (usage deltas) |
| `context_compact` | `context/compact` | Summarise / evict history (single-active) |
| `context_condense` | `context/condense` | Narrow tool-output history (single-active) |
| `lifecycle_request_built` | `lifecycle/request_built` | Rewrite the assembled request once per turn |
| `lifecycle_tool_before` | `lifecycle/tool_before` | Inspect/rewrite/suppress a tool call before it runs |
| `lifecycle_tool_after` | `lifecycle/tool_after` | Rewrite a tool result after it completes |
| `lifecycle_response_ready` | `lifecycle/response_ready` | Rewrite streamed response deltas |
| `lifecycle_turn_error` | `lifecycle/turn_error` | Observe a turn that exited with an error |

See [the protocol reference](protocol.md#capabilitieslist) for the exact wire shape and [writing plugins](authoring.md) for what each capability is for.

## Failure semantics

Plugins degrade gracefully. A hook or tool call that errors is skipped and the failure is surfaced to the terminal; a turn is never failed by a plugin unless the plugin explicitly opts into a fatal contract (`"fatal": true` on a lifecycle result). Plugin-owned state — credentials, config — lives in the plugin's own files, not in genie's.

## Plugin environment and limits

- **RPC timeout**: every plugin call runs within a 5-second budget; a hang marks the plugin inactive.
- **Pings**: the host pings live plugins every 30 seconds; a missed ping marks the plugin inactive.
- **Shutdown**: the host sends `shutdown`, waits 2 seconds, then SIGTERM, waits 2 more seconds, then SIGKILL.
- **Max plugins**: 16 loaded concurrently; extra plugins are skipped with a warning.
- **Name collisions**: plugin tools collide with built-in tools → plugin's tool is dropped (built-in wins). Plugin name matching a built-in slash command (`help`, `quit`, `exit`, `new`, `changes`, `model`, `agent`) → plugin rejected at load.