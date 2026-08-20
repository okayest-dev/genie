# go-plugin Prototype — OG Agent Harness

**Question being answered:** Does Hashicorp go-plugin feel right for the og plugin protocol?

## Running

```bash
# Build both binaries
cd plugin && go build -o plugin . && cp plugin ../host/plugins/
cd ../host && go build -o host . && ./host
```

Requires the plugin binary at `host/plugins/plugin`.

## Lines of Code

| File | Lines |
|---|---|
| shared/interface.go | 22 |
| shared/netrpc.go | 124 |
| plugin/main.go | 56 |
| host/main.go | 83 |
| **Total** | **285** |

## Evaluation

### What go-plugin gives you for free

- **Process lifecycle** — launches plugin binary, detects crash, cleans up on exit. You get `client.Kill()` and reattach support out of the box.
- **Handshake** — magic cookie prevents accidental double-execution (e.g. running plugin as host). Concrete and useful.
- **Protocol negotiation** — supports net/rpc and gRPC. You can start with net/rpc (zero protobuf) and graduate to gRPC later without changing the interface.
- **Logging** — structured hclog integration, plugin logs flow to host's logger.
- **MuxBroker** — multiplexed connections for multiple plugin instances. Needed if you ever want concurrent sub-plugins.
- **Versioning** — protocol version in handshake, so host and plugin can evolve independently.
- **Reattach** — can reconnect to an already-running plugin process (useful for long-lived plugins).

### Dependency cost

- **Binary size:** ~18MB plugin, ~19MB host. The bulk is grpc + protobuf + hclog + yamux. This is Go — a statically linked binary with everything baked in.
- **Compile time:** noticeable. `go build` pulls in grpc, protobuf reflection, and hclog. Roughly 3-5 seconds on a warm cache.
- **Transitive deps:** go-plugin pulls in grpc, protobuf, go-cmp, hclog, yamux, and testing libraries. It's heavy for what net/rpc alone would need.

### Non-Go plugin feasibility

**Not really.** go-plugin requires the plugin binary to implement its internal protocol (handshake, mux broker, net/rpc or gRPC server). There's no official client library for Python, Rust, or Node. You'd have to reverse-engineer the net/rpc protocol or use gRPC and generate stubs from go-plugin's proto definitions. This is the biggest dealbreaker for a polyglot plugin ecosystem.

**Verdict:** go-plugin is a Go-to-Go solution. If you need Python/Rust plugins, you'd build your own protocol (like NDJSON-RPC) or use raw gRPC with generated stubs.

### The protobuf/gRPC setup story

We used the **net/rpc path** — zero protobuf, zero codegen. Just plain Go structs and `net/rpc` conventions. This is the simplest go-plugin path and it worked cleanly.

If you want gRPC, you'd need:
1. A `.proto` file defining the service
2. `protoc` + `protoc-gen-go` + `protoc-gen-go-grpc`
3. Generated stubs in both host and plugin
4. The go-plugin `GRPCPlugin` interface instead of `NetRPCPlugin`

The net/rpc option is a genuine escape hatch. You can defer the protobuf tax until you need streaming, cross-language support, or better performance.

### Error handling

Error handling is manual but clean:
- Server methods return errors as strings in the reply struct (gob can't encode error interfaces).
- Client methods check `reply.Err` and reconstruct the error.
- This is the standard go-plugin pattern — not magical, but predictable.

**Gotcha we hit:** gob can't encode `nil` args or complex `map[string]any` with nested slices. The fix was `Parameters string` (raw JSON) instead of `Parameters map[string]any`. This is a real constraint — if your data model uses complex nested maps, you either serialize to string or use gRPC.

### Comparison: what would you build yourself with NDJSON-RPC?

With a hand-rolled NDJSON-RPC protocol, you'd need to build:

| go-plugin provides | You'd build yourself |
|---|---|
| Process launch + cleanup | `exec.Command`, signal handling, `defer cleanup()` |
| Handshake + magic cookie | A hello message + version check |
| Crash detection | `cmd.Process.Wait()` + error propagation |
| Reattach to running process | PID file + connection retry |
| Structured logging | Your own log bridge |
| Protocol versioning | A version field in your messages |
| MuxBroker (multi-connection) | Your own multiplexer |
| TLS between host/plugin | TLS config + cert management |

**Roughly 150-200 lines of process management code** that go-plugin handles for you. The question is whether those 200 lines are worth an 18MB binary and a Go-only constraint.

### Verdict

go-plugin is excellent infrastructure for **Go-to-Go plugin systems** where you control both sides. The process management, crash handling, and handshake are genuinely useful and would take real effort to get right yourself.

But it's **not suitable for og** because:
1. og needs polyglot plugins (Python, eventually Rust/JS) — go-plugin is Go-only
2. 18MB per binary is heavy for a plugin that might just wrap an API call
3. The gob encoding constraints on complex data structures are limiting
4. You'd be locked into Go's ecosystem for all plugins

**For a Go-only agent harness where plugins are Go modules:** strong yes.
**For og's polyglot vision:** no. Build your own protocol (NDJSON-RPC or raw gRPC with generated stubs).
