# Genie Plugin Protocol

This document describes the NDJSON-RPC-over-stdio protocol used for communication between the genie host and plugin subprocesses.

## Overview

Plugins are external executables that communicate with genie over stdin/stdout using newline-delimited JSON-RPC 2.0. Each message is a single JSON object terminated by a newline character (`\n`).

- **Transport**: stdio (stdin for requests, stdout for responses)
- **Encoding**: UTF-8 JSON
- **Framing**: One JSON object per line (NDJSON)
- **Protocol Version**: 1

Plugins can be written in any language that can read from stdin and write to stdout.

## Message Format

All messages follow JSON-RPC 2.0 specification.

### Request (Host → Plugin)

```json
{
  "jsonrpc": "2.0",
  "method": "<method-name>",
  "params": { ... },
  "id": <number|string>
}
```

- `jsonrpc`: Must be `"2.0"`
- `method`: The method to invoke
- `params`: Optional parameters object
- `id`: Request identifier (used to match responses)

### Response (Plugin → Host)

```json
{
  "jsonrpc": "2.0",
  "result": { ... },
  "id": <number|string>
}
```

### Error Response

```json
{
  "jsonrpc": "2.0",
  "error": {
    "code": <number>,
    "message": "<string>",
    "data": <any>
  },
  "id": <number|string>
}
```

Standard JSON-RPC error codes:
- `-32700`: Parse error
- `-32600`: Invalid request
- `-32601`: Method not found
- `-32602`: Invalid params
- `-32603`: Internal error

## Methods

### capabilities/list

**Direction**: Host → Plugin

**Purpose**: Discover what capabilities the plugin provides. This is the first method called after plugin spawn.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "capabilities/list",
  "id": 1
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "tools": true,
    "commands": true,
    "context_before": true,
    "context_after": false,
    "context_compact": false,
    "context_condense": false,
    "lifecycle_request_built": true,
    "lifecycle_tool_before": false,
    "lifecycle_tool_after": false,
    "lifecycle_response_ready": false,
    "lifecycle_turn_error": false,
    "version": 1
  },
  "id": 1
}
```

**Fields**:
- `tools` (boolean): Plugin provides tools
- `commands` (boolean): Plugin registers slash commands (see `commands/list`)
- `context_before` (boolean): Plugin declares a `context/before_request` hook
- `context_after` (boolean): Plugin declares a `context/after_response` hook
- `context_compact` (boolean): Plugin declares a `context/compact` hook (single-active)
- `context_condense` (boolean): Plugin declares a `context/condense` hook (single-active)
- `lifecycle_*` (boolean): Plugin declares lifecycle hooks (`lifecycle/request_built`, `lifecycle/tool_before`, `lifecycle/tool_after`, `lifecycle/response_ready`, `lifecycle/turn_error`)
- `version` (integer): Protocol version (1)

### tools/list

**Direction**: Host → Plugin

**Purpose**: Get the list of tools provided by the plugin.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "tools/list",
  "id": 2
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "tools": [
      {
        "name": "greet",
        "description": "A greeting tool",
        "parameters": {
          "type": "object",
          "properties": {
            "name": {
              "type": "string",
              "description": "The name to greet"
            }
          },
          "required": ["name"]
        }
      }
    ]
  },
  "id": 2
}
```

Each tool definition contains:
- `name` (string): Unique tool identifier
- `description` (string): Human-readable description for the model
- `parameters` (object): JSON Schema object describing the tool's arguments

### tools/call

**Direction**: Host → Plugin

**Purpose**: Execute a tool with the given arguments.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "greet",
    "arguments": {
      "name": "Neovim"
    }
  },
  "id": 3
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "content": [
      {
        "type": "text",
        "text": "Hello, Neovim!"
      }
    ]
  },
  "id": 3
}
```

**Fields**:
- `name` (string): Tool name to invoke
- `arguments` (object): Tool arguments matching the tool's parameters schema

**Result**:
- `content` (array): Array of content items
  - `type` (string): Content type (currently only "text")
  - `text` (string): Text content

### commands/list

**Direction**: Host → Plugin

**Purpose**: Discover the plugin's registered slash commands. A plugin that declares `commands: true` in its capabilities is queried once at load, mirroring `tools/list`. Each command is addressed in the REPL as `/<plugin> <command>` (e.g. the `auth` command of the `copilot` plugin is run as `/copilot auth`).

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "commands/list",
  "id": 2
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "commands": [
      {
        "name": "auth",
        "description": "Log in, refresh, or show status for the Copilot host",
        "usage": "auth [login|refresh|status] [--host <host>]"
      }
    ]
  },
  "id": 2
}
```

Each command definition contains:
- `name` (string, required): Single-token command identifier — no whitespace, no leading `/`. Names that are not single tokens are dropped with a warning; duplicate names resolve last-wins with a warning; an empty list is tolerated.
- `description` (string, required): One-line human-readable description, shown in the flat `/help` plugin-commands listing.
- `usage` (string, optional): Free-text usage line for the command.

### commands/run

**Direction**: Host → Plugin

**Purpose**: Execute a command. Arguments are delivered as the raw string the user typed after the command token; the plugin owns its own sub-command parsing (e.g. `/copilot auth login --host tenant.ghe.com` reports `name: "auth"`, `arguments: "login --host tenant.ghe.com"`). The call must return promptly: interactive work (a device-flow login) completes asynchronously inside the plugin's own process, driven by the same request-scoped credential rules as the in-tree providers.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "commands/run",
  "params": {
    "name": "auth",
    "arguments": "login --host tenant.ghe.com"
  },
  "id": 3
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "text": "Open https://github.com/login/device and enter code ABCD-1234"
  },
  "id": 3
}
```

**Fields**:
- `name` (string): Command name to invoke
- `arguments` (string): Raw argument string from the REPL line

**Result**:
- `text` (string, optional): Text the REPL prints to the user
- `data` (any, optional): Structured result, printed as compact JSON when `text` is empty

**Failure semantics** mirror `tools/call`: the call runs within the `RequestTimeout` (5s) RPC budget — a timeout marks the plugin inactive; a JSON-RPC error response prints the plugin's message and the plugin stays active; a command invoked on a plugin that has died reports "plugin <name> is not active".

### commands/help

**Direction**: Host → Plugin

**Purpose**: Optional curated help for a plugin or a single command, invoked lazily when the user runs bare `/<plugin>` with no command token. The method is not declared in capabilities — it is probed at call time: an `-32601` (method not found) response means the plugin has no help function and the host falls back to the flat `commands/list` listing. Providing it is the plugin's prerogative, never genie's requirement.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "commands/help",
  "params": {
    "name": "auth"
  },
  "id": 4
}
```

`name` is optional; omitted, it asks for plugin-level help.

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "text": "auth manages the Copilot credential: 'login' starts a device flow, 'refresh' renews the token, 'status' shows the current host."
  },
  "id": 4
}
```

### lifecycle/request_built

**Direction**: Host → Plugin

**Purpose**: Observe and optionally rewrite the assembled turn request (agent instruction + current turn + injected history) before it is sent to the model. Request-side lifecycle hooks (`request_built`, `tool_before`) fire in `[lifecycle.plugins] order`; response-side hooks fire in the reverse (onion) order so paired plugins can pack and unpack.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "lifecycle/request_built",
  "params": { "model": "claude-sonnet-4-5", "messages": [ { "role": "user", "content": "hi" } ] },
  "id": 20
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": { "request": { "model": "claude-sonnet-4-5", "messages": [ { "role": "user", "content": "hi [rewritten]" } ] }, "fatal": false },
  "id": 20
}
```

### lifecycle/tool_before

**Direction**: Host → Plugin

**Purpose**: Guardrail slot before a tool call executes. The hook may rewrite `arguments` (an empty `arguments` means "no change"), `suppress` the call entirely, or `set_empty` to wipe the arguments to `""` so the call still runs the tool path and fails closed unless an empty string is valid. A `fatal: true` result aborts the turn.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "lifecycle/tool_before",
  "params": { "name": "sed", "id": "call_1", "arguments": "--version" },
  "id": 21
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": { "arguments": "--version", "suppress": false, "set_empty": false, "fatal": false },
  "id": 21
}
```

### lifecycle/tool_after

**Direction**: Host → Plugin

**Purpose**: Observe a completed tool call. The call's `error` is carried as a field, not a JSON-RPC error, so a failing call is still observable to every hook in the chain. The hook may rewrite `result` text and/or escalate with `fatal`.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "lifecycle/tool_after",
  "params": { "name": "sed", "id": "call_1", "arguments": "--version", "result": "GNU sed 4.8" },
  "id": 22
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": { "result": "GNU sed 4.8", "fatal": false },
  "id": 22
}
```

### lifecycle/response_ready

**Direction**: Host → Plugin

**Purpose**: Observe one response text delta. **This hook fires per streaming delta**, so it is the worst place for anything slow or noisy — a failing `response_ready` that is not cut out warns per delta (see the circuit breaker below). The final call carries `final: true`, the `finish_reason`, and the usage observed at end-of-stream. The hook may rewrite `chunk` and/or escalate with `fatal`.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "lifecycle/response_ready",
  "params": { "chunk": "Hello" },
  "id": 23
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": { "chunk": "Hello", "fatal": false },
  "id": 23
}
```

### lifecycle/turn_error

**Direction**: Host → Plugin

**Purpose**: Observe a hard turn error; observe-only, but a `fatal` result escalates a failing turn for downstream hooks. `phase` names where the turn failed; `partial` carries the assistant text accumulated before the failure.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "lifecycle/turn_error",
  "params": { "error": "model timed out", "phase": "stream", "partial": "Hello" },
  "id": 24
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": { "fatal": false },
  "id": 24
}
```

### context/before_request

**Direction**: Host → Plugin

**Purpose**: Rewrite the outgoing request before it is sent to the model. This hook runs after the harness has injected prior-turn history, so it sees the full message list. Multiple before hooks form a deterministic ordered filter chain (config `order`, defaulting to registration order); each hook sees the previous hook's result. A failing before hook is skipped (its contribution dropped) and its degradation surfaced to the terminal; the request still proceeds.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "context/before_request",
  "params": {
    "model": "claude-sonnet-4-5",
    "messages": [ { "role": "user", "content": "hi" } ],
    "tools": [ { "name": "greet", "description": "...", "parameters": {} } ]
  },
  "id": 7
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "request": {
      "model": "claude-sonnet-4-5",
      "messages": [ { "role": "user", "content": "hi [rewritten]" } ],
      "tools": [ { "name": "greet", "description": "...", "parameters": {} } ]
    }
  },
  "id": 7
}
```

### context/after_response

**Direction**: Host → Plugin

**Purpose**: Observe a completed turn and report a narrow usage delta. It runs once the model's response for a turn has fully drained. It returns a narrow `usage` delta and must **never** rewrite history.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "context/after_response",
  "params": {
    "request": { "model": "claude-sonnet-4-5", "messages": [] },
    "usage": { "prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120 }
  },
  "id": 8
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "usage": { "prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120 }
  },
  "id": 8
}
```

### context/compact

**Direction**: Host → Plugin

**Purpose**: Summarise / evict history into a rewritten request to keep the conversation within its context budget. **Single-active**: at most one implementation runs. The harness's built-in compactor is the default when no plugin is selected; when multiple plugins declare this hook the host fails at startup until an explicit `active_compact` is chosen in `[context.plugins]`.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "context/compact",
  "params": { "model": "claude-sonnet-4-5", "messages": [ { "role": "user", "content": "..." } ] },
  "id": 9
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": { "request": { "model": "claude-sonnet-4-5", "messages": [ { "role": "user", "content": "[summary]" } ] } },
  "id": 9
}
```

### context/condense

**Direction**: Host → Plugin

**Purpose**: Narrow (condense) tool outputs in history into a rewritten request. Single-active, exactly like `context/compact`, governed by `active_condense`. **NB**: the default built-in compactor/condenser body is inert (a no-op) in this protocol version; the seam simply passes the request through unchanged.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "context/condense",
  "params": { "model": "claude-sonnet-4-5", "messages": [ { "role": "tool", "content": "lots of output" } ] },
  "id": 10
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": { "request": { "model": "claude-sonnet-4-5", "messages": [ { "role": "tool", "content": "[narrowed]" } ] } },
  "id": 10
}
```

### ping

**Direction**: Host → Plugin

**Purpose**: Health check. The host sends this periodically (every 30 seconds by default).

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "ping",
  "id": 1234567890
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {},
  "id": 1234567890
}
```

The plugin must respond within 5 seconds. If it fails to respond, the host will kill the plugin.

### shutdown

**Direction**: Host → Plugin

**Purpose**: Graceful shutdown signal. The plugin should clean up resources and exit.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "shutdown",
  "id": 1234567890
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "ok": true
  },
  "id": 1234567890
}
```

After sending shutdown, the host waits 2 seconds for the plugin to exit, then sends SIGTERM, waits 2 more seconds, then sends SIGKILL.

## Plugin Manifest

Plugins may optionally include a `plugin.toml` manifest file next to the executable. The manifest allows the host to validate the plugin before spawning.

```toml
name = "my-plugin"
version = "1.0.0"
capabilities = ["tools", "commands"]
```

**Fields**:
- `name` (string, required): Plugin name
- `version` (string, required): Plugin version
- `capabilities` (array of strings, required): List of capabilities ("tools", "commands", "context_before", "context_after", "context_compact", "context_condense", "lifecycle_request_built", "lifecycle_tool_before", "lifecycle_tool_after", "lifecycle_response_ready", "lifecycle_turn_error")

If no manifest is present, the host will probe the plugin with `capabilities/list` after spawning.

## Plugin Discovery

Plugins are discovered from a configured directory (default: `~/.config/genie/plugins/`). The host scans for executable files:

- Skips directories
- Skips hidden files (starting with `.`)
- Skips non-executable files
- Skips `.go` source files
- Maximum 16 plugins loaded concurrently

Configuration options (in `config.toml`):

```toml
[plugins]
dir = "~/.config/genie/plugins"    # plugin directory
enable = ["my-tool", "my-command"] # explicit allowlist (empty = all)
disable = ["broken-plugin"]     # denylist (takes precedence)
```

Environment variable override: `GENIE_PLUGIN_DIR`

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Plugin directory missing | Pure defaults, no error, no plugins loaded |
| Manifest malformed | Skip plugin with warning, continue loading |
| Plugin fails to spawn | Skip with warning, continue |
| Plugin crashes mid-session | Mark inactive, tool calls return error |
| Plugin hangs on request | Timeout after 5s, kill plugin, mark inactive |
| Plugin doesn't respond to ping | Kill plugin, mark inactive |
| Plugin returns invalid JSON | Kill plugin, mark inactive, log error |
| Plugin returns unknown method | Error response (-32601), plugin stays active |
| Plugin command exceeds RequestTimeout (5s) | Timeout, mark plugin inactive (mirrors tools/call) |
| Plugin command returns JSON-RPC error | Error response printed, plugin stays active |
| Hook fails repeatedly | After `plugins.hook_failure_threshold` consecutive failures of that one event, trip the breaker for `(plugin, event)`: the hook stops being called and one trip notice replaces the per-occurrence warnings |
| Breaker recovers | After `plugins.hook_recovery_seconds`, one trial call is allowed; success restores the event with a notice, failure re-trips |
| `active_compact`/`active_condense` hook trips | Built-in compactor/condenser takes over for the cooldown |
| `fatal: true` hook result | Turn aborts with `FatalHookError`; neither counts toward the threshold nor resets a counter |

## Name Collision Handling

- **Tool collision**: Plugin tools that collide with built-in tool names are silently dropped (built-in wins). A warning is logged.
- **Built-in slash name collision**: A plugin whose display name matches a built-in REPL slash command (`help`, `quit`, `exit`, `new`, `changes`, `model`, `provider`, `agent`) is rejected at startup with a warning and skipped — the built-in wins. Plugin command names (`/<plugin> <command>`) share the plugin's namespace and never collide with built-ins.

## Logging

Plugins should write logs to stderr. The host captures plugin stderr and forwards it to the main log output. Writing to stdout will corrupt the protocol.

## Example Plugin (Python)

```python
#!/usr/bin/env python3
import sys
import json

def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        req = json.loads(line)
        resp = {"jsonrpc": "2.0", "id": req["id"]}
        
        if req["method"] == "capabilities/list":
            resp["result"] = {
                "tools": True,
                "commands": True,
                "version": 1
            }
        elif req["method"] == "tools/list":
            resp["result"] = {
                "tools": [{
                    "name": "hello",
                    "description": "Say hello",
                    "parameters": {
                        "type": "object",
                        "properties": {
                            "name": {"type": "string"}
                        },
                        "required": ["name"]
                    }
                }]
            }
        elif req["method"] == "tools/call":
            params = req.get("params", {})
            name = params.get("arguments", {}).get("name", "World")
            resp["result"] = {
                "content": [{"type": "text", "text": f"Hello, {name}!"}]
            }
        elif req["method"] == "commands/list":
            resp["result"] = {
                "commands": [{
                    "name": "greet",
                    "description": "Greet the user",
                    "usage": "greet [name]"
                }]
            }
        elif req["method"] == "commands/run":
            params = req.get("params", {})
            args = params.get("arguments", "")
            resp["result"] = {"text": f"Hello, {args or 'World'}!"}
        elif req["method"] == "commands/help":
            resp["result"] = {"text": "greet says hello to you or the name you pass."}
        elif req["method"] == "ping":
            resp["result"] = {}
        elif req["method"] == "shutdown":
            resp["result"] = {"ok": True}
            sys.stdout.write(json.dumps(resp) + "\n")
            sys.stdout.flush()
            break
        else:
            resp["error"] = {"code": -32601, "message": "Method not found"}
        
        sys.stdout.write(json.dumps(resp) + "\n")
        sys.stdout.flush()

if __name__ == "__main__":
    main()
```

