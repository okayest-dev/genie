# og Plugin Protocol

This document describes the NDJSON-RPC-over-stdio protocol used for communication between the og host and plugin subprocesses.

## Overview

Plugins are external executables that communicate with og over stdin/stdout using newline-delimited JSON-RPC 2.0. Each message is a single JSON object terminated by a newline character (`\n`).

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
    "wires": false,
    "providers": false,
    "version": 1
  },
  "id": 1
}
```

**Fields**:
- `tools` (boolean): Plugin provides tools
- `wires` (boolean): Plugin provides wire protocols
- `providers` (boolean): Plugin provides provider access (collapsed into wires)
- `version` (integer): Protocol version (must be 1)

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

### wire/init

**Direction**: Host → Plugin

**Purpose**: Initialize a wire plugin with configuration.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "wire/init",
  "params": {
    "config": {
      "api_key": "...",
      "base_url": "..."
    }
  },
  "id": 4
}
```

**Response**:
```json
{
  "jsonrpc": "2.0",
  "result": {
    "ok": true
  },
  "id": 4
}
```

### wire/stream

**Direction**: Host → Plugin

**Purpose**: Send a chat request to the wire plugin and receive a streaming response.

**Request**:
```json
{
  "jsonrpc": "2.0",
  "method": "wire/stream",
  "params": {
    "request": { ... }
  },
  "id": 5
}
```

**Response**: The plugin should return a response compatible with the wire protocol (streaming chunks).

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
capabilities = ["tools", "wires"]
```

**Fields**:
- `name` (string, required): Plugin name
- `version` (string, required): Plugin version
- `capabilities` (array of strings, required): List of capabilities ("tools", "wires", "providers")

If no manifest is present, the host will probe the plugin with `capabilities/list` after spawning.

## Plugin Discovery

Plugins are discovered from a configured directory (default: `~/.config/og/plugins/`). The host scans for executable files:

- Skips directories
- Skips hidden files (starting with `.`)
- Skips non-executable files
- Skips `.go` source files
- Maximum 16 plugins loaded concurrently

Configuration options (in `config.toml`):

```toml
[plugins]
dir = "~/.config/og/plugins"    # plugin directory
enable = ["my-tool", "my-wire"] # explicit allowlist (empty = all)
disable = ["broken-plugin"]     # denylist (takes precedence)
```

Environment variable override: `OG_PLUGIN_DIR`

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

## Name Collision Handling

- **Tool collision**: Plugin tools that collide with built-in tool names are silently dropped (built-in wins). A warning is logged.
- **Wire collision**: Plugin wires that collide with registered wire names are rejected with a warning. Core wires are never overridden.

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
                "wires": False,
                "providers": False,
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

## Example Plugin (Go)

See `prototype/ndjson-rpc/plugin/main.go` for a reference Go implementation.