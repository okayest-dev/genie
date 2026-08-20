# NDJSON-RPC-over-stdio Plugin Protocol — Throwaway Prototype

**Question being answered:** What does an NDJSON-RPC-over-stdio plugin protocol feel like in Go? How much friction vs. something like HashiCorp go-plugin?

## Running

```bash
chmod +x run.sh && ./run.sh
```

## Expected Output

```
→ Found plugin: ./plugins/plugin
→ Plugin started (PID 12345)

→ [capabilities/list] {"jsonrpc":"2.0","method":"capabilities/list","id":1}
← {"jsonrpc":"2.0","result":{"providers":false,"tools":true,"wires":false},"id":1}
  capabilities:
  {
    "providers": false,
    "tools": true,
    "wires": false
  }

→ [tools/list] {"jsonrpc":"2.0","method":"tools/list","id":2}
← {"jsonrpc":"2.0","result":{"tools":[{"description":"A greeting tool","name":"greet","parameters":{...}}]},"id":2}
  tools:
  { ... }

→ [tools/call] {"jsonrpc":"2.0","method":"tools/call","params":{"arguments":{"name":"Neovim"},"name":"greet"},"id":3}
← {"jsonrpc":"2.0","result":{"content":[{"text":"Hello, Neovim!","type":"text"}]},"id":3}
  greet result:
  {
    "content": [
      { "text": "Hello, Neovim!", "type": "text" }
    ]
  }

→ [foo/bar] {"jsonrpc":"2.0","method":"foo/bar","id":4}
← {"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":4}
  unknown method: ERROR -32601: Method not found

→ Shutting down plugin...
→ Done.
```

## Evaluation

### Lines of Code

| Component | Lines |
|-----------|-------|
| Plugin    | ~115  |
| Host      | ~105  |
| Total     | ~220  |

Zero external dependencies. Two files. That's the good news.

### What's Simple

- **The protocol is trivial.** One JSON object per line. Request in, response out. A junior dev could understand it in 5 minutes.
- **Subprocess lifecycle is straightforward.** `exec.Command`, pipe stdin/stdout, close to shut down. No gRPC, no plugin binaries with handshake protocols.
- **Adding new methods is one `case` statement.** The plugin dispatch table is dead simple.
- **No build system overhead.** `go build` for both sides. No proto files, no codegen.

### What's Painful

- **No type safety on the wire.** Every message is `json.RawMessage` until you unmarshal it yourself. You're one typo in a field name away from a silent bug.
- **Manual request/response correlation.** The host tracks IDs, matches responses. This works at 1 plugin, but imagine 5 concurrent plugins — you need a multiplexer.
- **No streaming or notifications.** If the plugin wants to emit progress events, you're stuck. The protocol is strictly request-response.
- **Stdin/stdout is shared.** If the plugin wants to log, it has to be careful not to write to stdout. In practice, you'd need a convention (e.g., logs go to stderr, protocol goes to stdout). This is fragile.
- **No health check.** If the plugin crashes silently, the host gets EOF and has to figure out what happened. A `ping` method would help but isn't part of the spec.

### Error Handling Story

| Scenario | What Happens |
|----------|-------------|
| Plugin crash | Host gets EOF on scanner, prints error, exits. No recovery. |
| Bad JSON from plugin | Host `json.Unmarshal` fails, returns nil response. Host prints "(no response)". |
| Bad JSON from host | Plugin returns parse error (`-32700`). Clean. |
| Plugin hangs | Host blocks on `scanner.Scan()` forever. No timeout. |
| Plugin writes garbage | Host gets parse error or unmarshal failure. Reasonable. |

**Verdict:** Acceptable for a prototype. For production you'd want timeouts, keepalives, and crash detection.

### What Would You Need for Production

1. **Healthcheck/ping** — periodic `ping` method, host kills plugin if no response within timeout
2. **Graceful shutdown** — `shutdown` method with drain period, SIGTERM fallback
3. **Capability caching** — don't re-query `capabilities/list` on every reconnect
4. **Versioning** — protocol version in handshake (`capabilities/list` response or a dedicated `version` field)
5. **Request multiplexing** — for concurrent tool calls across plugins
6. **Logging convention** — stderr for logs, but structured (JSON lines on stderr? or a separate log fd?)
7. **Plugin manifest** — name, version, capabilities declared in a TOML/YAML next to the binary
8. **Sandboxing** — `unshare`, seccomp, or container for untrusted plugins

### Non-Go Plugin Feasibility

**Yes, trivially.** A Python plugin would be ~40 lines:

```python
import sys, json
for line in sys.stdin:
    req = json.loads(line)
    resp = {"jsonrpc": "2.0", "id": req["id"]}
    if req["method"] == "capabilities/list":
        resp["result"] = {"tools": True, "wires": False, "providers": False}
    # ... etc
    sys.stdout.write(json.dumps(resp) + "\n")
    sys.stdout.flush()
```

Same for Ruby, Python, Node, anything with stdin/stdout and JSON. This is a huge win over go-plugin which requires Go binaries.

### Comparison: What Does go-plugin Give You?

| Feature | go-plugin (gRPC) | This Prototype |
|---------|-------------------|----------------|
| Type safety | Yes (protobuf) | No (raw JSON) |
| Streaming | Yes (gRPC streams) | No |
| Health checks | Built-in | Manual |
| Process supervision | Built-in (netRPC + TLS) | Manual |
| Plugin discovery | HashiCorp convention | Manual dir scan |
| Language support | Go only (net/rpc) | Any language |
| Complexity | High (proto, TLS, handshake) | Low (two files) |
| Debuggability | Hard (gRPC internals) | Easy (read the lines) |
| Dependencies | gRPC, protobuf, go-plugin | None |

**Bottom line:** go-plugin gives you a production-grade, battle-tested system at the cost of significant complexity and Go-only plugins. This NDJSON-RPC approach gives you simplicity and language-agnosticism at the cost of reinventing several wheels.

For og, where plugins might be scripts or simple agents, the NDJSON-RPC approach seems like the right tradeoff — especially if you add health checks and timeouts incrementally.
