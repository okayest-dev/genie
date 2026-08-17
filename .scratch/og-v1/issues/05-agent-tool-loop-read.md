# 05 — Agent tool loop + read

**What to build:** the agent loop running full turns with tools. When the model finishes a turn with tool calls, each call is validated against its JSON schema and executed strictly serially; results — success or uniform `Error: ...` — feed back until the model stops calling tools. Tool runs are framed by `── <tool> <args> ──` headers routed to stderr. A stale call to a disabled tool returns a clear error and the loop continues. When the provider rejects the tools array, the turn retries without tools. The `read` tool works end-to-end: offset/limit, the shared 2000-line/50KB head-truncate with a continue pointer, directory listing, and binary files erroring with pointers at shell inspection.

**Blocked by:** 01 — Wire tracer bullet; 02 — Config surface.

**Status:** resolved

- [ ] Serial execution: one tool call per cycle, results fed back, loop repeats until the model stops calling tools.
- [ ] Malformed tool-call arguments are rejected before execution and fed back as an error — never a crash.
- [ ] Tool runs are framed by headers; framing goes to stderr.
- [ ] A stale call to a disabled tool returns `Error: tool '<name>' is disabled` and the loop continues.
- [ ] A provider `invalid_request` on the tools array triggers a no-tools retry of the turn.
- [ ] `read` honors offset/limit, truncates at the shared cap with `[Showing lines X-Y of N. Use offset=Z to continue.]`, lists directory children (dirs marked with a trailing `/`), and errors on binary content pointing at `xxd`/`file`/`head -c`.
