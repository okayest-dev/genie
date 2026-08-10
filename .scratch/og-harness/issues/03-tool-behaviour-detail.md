Type: grilling
Status: resolved
Blocked by:

# 03-tool-behaviour-detail

## Question

Pin down the exact v1 behaviour of each tool, in enough detail for the spec to hand off:

- **read**: argument schema, path resolution (relative to cwd?), size caps, binary handling, and whether a directory path lists entries.
- **write**: whole-file write semantics, confirm UX, size caps.
- **edit**: search-and-replace rules — single `old`/`new` pair per call, reject-if-not-found, reject-if-ambiguous, whitespace sensitivity.
- **bash**: confirm UX, timeout, output truncation, cwd, exit-code handling.
- **Tool results**: how results are shaped back into model context (truncation format, error surface).

Also decide how each tool is described to the model (the `tools` array JSON in 01).

## Answer

Decided by grilling over two rounds. All tools resolve `path` relative to the session cwd; absolute paths and `~` allowed; no sandbox in v1. A hard-sandbox CLI flag (bash containment + file jailing) was considered and **deferred to a later release** — true bash containment needs chroot/bwrap/sandbox-exec/job-objects, none std-lib, and a partial jail leaks through bash.

- **read**: `{ path, offset?, limit? }` — `offset` 1-indexed line, `limit` max lines. Head-truncate at 2000 lines / 50KB (shared cap), append `[Showing lines X-Y of N. Use offset=Z to continue.]`. Non-text/binary (NUL byte or invalid UTF-8) → error result pointing at `bash` (`xxd`/`file`/`head -c`); no image support. **Directory path lists its immediate children** — sorted, one per line, dirs end `/`, `limit` honored; no fifth `ls` tool.
- **write**: `{ path, content }` — whole-file write, auto-creates parent dirs, overwrites. **Confirm only when the target already exists**; new-file writes proceed. 1MB content cap, rejected with error. Returns bytes written.
- **edit**: `{ path, oldText, newText }`, single pair per call. `oldText` matched exactly (whitespace-sensitive); **reject if not found; reject if ambiguous** (more than one occurrence). Preserves the file's existing line endings; strips BOM before matching. **No confirm** — the write confirm is the guard; every edit still lands in the change ledger (04).
- **bash**: `{ command, timeout? }` — runs in the session cwd, stdout+stderr merged. **Confirm every command**. Default timeout **120s** (configurable via 07), per-call `timeout` (seconds) overrides; on timeout kills the process tree and returns `Command timed out after Ns` + partial output. Tail-truncate to 2000 lines / 50KB; when truncated, full output saved to a session temp file and its path included in the result. **Non-zero exit → error result** carrying output + `Command exited with code N`.
- **Tool results**: one uniform error shape — `Error: <human-readable reason>` (plus truncated output where relevant), returned as the `role: "tool"` message. No exception aborts; failures flow back to the model.
- **Tools array JSON** (fed to 01's wire): `tool_choice: "auto"`, **`parallel_tool_calls: false`** (strictly serial execution), four `type: "function"` definitions with descriptions exactly as drafted in the session (read/write/edit/bash shapes above; no sandbox sentence).
