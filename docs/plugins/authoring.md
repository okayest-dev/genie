# Writing Genie plugins

This guide is for building a plugin: an executable that extends Genie over the NDJSON-RPC protocol. If you're just installing plugins, see [using plugins](using.md) instead; for the complete wire reference see the [protocol reference](protocol.md).

## The mental model

A plugin is a long-running subprocess that Genie spawns at startup and talks to over stdio. Genie writes one JSON-RPC request per line to the plugin's stdin; the plugin writes one JSON response per line to stdout. Anything logged goes to **stderr** — stdout is the protocol, so writing logs there corrupts it.

The plugin starts, receives `capabilities/list` (its *handshake*), answers with the capabilities it provides, then serves the corresponding methods until `shutdown`. Calls arrive serially; each must reply promptly (Genie's RPC budget is **5 seconds**) because a hang marks the plugin inactive and Genie never respawns it.

You can write a plugin in any language — the protocol is newline-delimited JSON. Go plugins can vendor the generated `wireplugin` package (see below) so they don't hand-roll the framing.

## The minimum viable plugin

A `capabilities/list` response is the only hard requirement:

```json
{"jsonrpc":"2.0","id":1,"result":{"tools":false,"wires":false,"providers":false,"commands":false,"version":1}}
```

Plus `ping` and `shutdown` to stay alive cleanly:

```json
{"jsonrpc":"2.0","id":123,"result":{}}
```

Everything else — tools, wires, commands, hooks — is declared in that handshake and implemented as methods. Declare a capability and Genie will call its methods; don't declare it and Genie never calls them. The [example plugin](protocol.md#example-plugin-python) at the bottom of the protocol reference is a complete, runnable skeleton.

## Laying out the plugin

Each plugin is a named executable in the plugin directory (default `~/.config/genie/plugins/`). The plugin's *name* is its executable's filename, and that name is the namespace for its commands in the REPL.

```
~/.config/genie/plugins/
  my-plugin/
    manifest.toml   # recommended
    my-plugin       # executable, chmod +x
    config.toml     # optional — your own config, loaded by you
```

A manifest is optional — without it Genie discovers capabilities by the handshake probe:

```toml
name = "my-plugin"
version = "1.0.0"
capabilities = ["tools", "commands"]
```

The valid manifest capability names mirror the handshake capability names: `tools`, `wires`, `providers`, `commands`, `context_before`, `context_after`, `context_compact`, `context_condense`, `lifecycle_request_built`, `lifecycle_tool_before`, `lifecycle_tool_after`, `lifecycle_response_ready`, `lifecycle_turn_error`. The host enforces capabilities from the `capabilities/list` handshake it probes at load — the manifest declares intent and is validated structurally.

## Tool plugins

Declare `tools: true`. Genie calls `tools/list` once at load to learn the tool set, then `tools/call` when the model invokes a tool. Each tool is defined by a JSON Schema parameters object, exactly like the built-in tools.

`tools/list` returns `{ "tools": [ { "name", "description", "parameters" } ] }`.

`tools/call` receives `{ "name", "arguments" }` and returns results as a content array:

```json
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"Hello, Neovim!"}]}}
```

Rules of the road:

- Tool results are **text**. One content item, `type: "text"`.
- Results are truncated and counted like built-in tool output — keep them as tight as you can.
- A failing call returns a JSON-RPC error with a human-readable message; the error is fed back to the model as a tool error and the loop continues.
- A tool whose name collides with a built-in (`read`, `write`, `edit`, `bash`) is dropped with a warning — the built-in wins.

## Wire plugins

Declare `wires: true`. A wire plugin *is* a provider: Genie stops talking HTTP to a provider directly and streams requests through your plugin instead.

Three methods:

- `wire/init` — receives config (`{ "config": { "api_key", "base_url" } }`) once at load.
- `wire/list_models` — returns `{ "models": [ { "id", "name"?, "context_window"? } ] }`. The models you list are what the user can route to. `context_window` is the *authoritative* token budget Genie plans against; omit it when your provider doesn't expose one — Genie never guesses (users can still override per model in config).
- `wire/stream` — receives a full chat request and streams back response events (text deltas, complete tool calls, finish reason, usage).

Genie routes to your plugin in two ways: the user sets `provider = "<plugin-name>"` (all requests go through you), or leaves it unset and Genie routes by model ID — the models your `wire/list_models` reports are wired to your plugin automatically.

Wire plugins own their auth end-to-end. Keep durable credentials in a plugin-owned file under your XDG data dir (owner-only permissions, atomic writes); derive short-lived tokens in memory per request. See [ADR-0002](../adr/0002-plugin-owned-credentials-request-scoped.md) for the request-scoped credential model the copilot plugin uses.

## Lifecycle plugins

Declare one or more `lifecycle_*` capabilities. Lifecycle hooks observe — and can rewrite — every turn as it runs:

| Event | Fire point | Can rewrite | Capability |
|-------|-----------|-------------|------------|
| `lifecycle/request_built` | once per turn, before the first stream | the request (model, messages, tools) | `lifecycle_request_built` |
| `lifecycle/tool_before` | before each tool executes | tool arguments; also `suppress`, or `set_empty` to wipe them | `lifecycle_tool_before` |
| `lifecycle/tool_after` | after each tool call completes | the result text (errors arrive as a field) | `lifecycle_tool_after` |
| `lifecycle/response_ready` | per streamed text delta, then a `final` release | the delta text | `lifecycle_response_ready` |
| `lifecycle/turn_error` | once, when a turn errors | nothing — observe only | `lifecycle_turn_error` |

Hooks run as **ordered chains**. Request-side events (`request_built`, `tool_before`) fire in `[lifecycle.plugins] order`; response-side events fire in the reverse (onion) order so a pair of plugins can pack and unpack.

**Failure semantics.** Hooks degrade: if a hook errors it is skipped, earlier hooks' contributions are kept, and the turn proceeds. To fail a turn on a hook failure, return `"fatal": true` on the result — Genie aborts the turn with a `FatalHookError` naming your plugin and the event. In `tool_before`: `suppress` kills the tool call (the harness reports `Tool call suppressed by lifecycle hook.`); `set_empty: true` wipes the arguments to `""` and the wiped call still goes through normal argument validation — so it fails closed unless an empty string is valid for the tool. The two are deliberately different: a suppressed call never executes, a wiped call still runs the tool path.

## Command plugins

Declare `commands: true`. Command plugins expose user-typed slash commands. Genie calls `commands/list` once at load, `commands/run` per invocation, and probes an optional `commands/help` lazily.

Each command is `{ "name", "description", "usage"? }`. The REPL addresses it as `/<plugin> <command>`:

```
genie> /my-plugin deploy --env prod
```

`commands/run` receives `{ "name": "deploy", "arguments": "--env prod" }` — arguments are the **raw string** the user typed after the command token; sub-command parsing is yours. Return `{ "text": "..." }` for the REPL to print, or `{ "data": ... }` (printed as compact JSON when `text` is empty).

Command naming rules:

- Names must be single tokens — no whitespace, no leading `/`. Invalid names are dropped with a warning; duplicates resolve last-wins.
- `commands/help` is optional. If your plugin doesn't implement it (respond `-32601`), the bare `/<plugin>` invocation falls back to Genie's flat listing of your commands. Curated help can target one command (`{ "name": "auth" }`) or the whole plugin (omit `name`).

Commands are for *plugin-owned interactive work* — a device-flow login, a status read. Return promptly; genuinely long work finishes asynchronously inside your own process.

## Context plugins

Declare `context_before`, `context_after`, `context_compact`, or `context_condense`. Context hooks sit inside the request pipeline (see [context management](../context-management.md)).

- `context/before_request` — rewrite the outgoing request *after* Genie has injected history, so you see the full message list. Multiple `before` hooks form an ordered filter chain (`[context.plugins] order`); each sees the previous hook's result. A failing hook is skipped and its contribution dropped.
- `context/after_response` — observe a completed turn; return a narrow `usage` delta. Never rewrite history.
- `context/compact` — summarise / evict history to keep a conversation within its budget. **Single-active**: exactly one implementation runs. Genie's built-in compactor is the default; when two or more plugins declare this hook the host fails startup until the operator names one in `[context.plugins] active_compact`.
- `context/condense` — narrow (condense) oversized tool-output history before a request ships. Single-active like compact, governed by `active_condense`.

## RPC plumbing everyone needs

- **pings**: Genie pings you every 30 seconds. Respond `{}` within 5 seconds or you're marked inactive.
- **shutdown**: Genie sends `shutdown`, waits 2 seconds, SIGTERM, waits 2 more, SIGKILL. Persist anything you must, then exit.
- **errors**: use standard JSON-RPC codes. `-32601` (method not found) is the one Genie patterns on — it's how it probes for optional methods like `commands/help`.
- **timeouts**: every request you receive has a 5-second budget. A tool call that needs real work (a login flow) should kick off work in your own process and return promptly, not block the RPC.

## Go: the generated `wireplugin` package

The plugin protocol's Go bindings are **generated from `protocol/schema.yaml`** — the schema is the single source of truth. The genie repo builds them as `protocol/wireplugin`, and standalone plugin repos vendor that output as their own `internal/wireplugin/` (regenerate from `make gen-protocol` in a checkout of genie). The package provides request/response types, method constants, and a dispatch loop, so a Go plugin never hand-rolls the NDJSON framing. Non-Go plugins can treat `protocol/schema.yaml` as the canonical wire description and generate bindings for their own language.

## Checklist before you publish

- [ ] Executable permission set; discovers and loads from the plugin directory.
- [ ] `capabilities/list` handshake is accurate — nothing declared that you don't serve.
- [ ] `ping` and `shutdown` answered; clean exit on shutdown.
- [ ] All logging to stderr, never stdout.
- [ ] Every `tools/list` name is documented for the model; `tools/call` results are text-only.
- [ ] Wire models report a real `context_window` or omit it — never guess.
- [ ] Commands are single-token names with one-line descriptions for `/help`.
- [ ] Interactive work returns promptly from the RPC; long work runs in your own process.
- [ ] Tests against a scripted genie host or a replay of the protocol.