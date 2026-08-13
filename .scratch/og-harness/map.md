# og-harness map

## Destination

A spec for **og**: a minimal, std-lib-first Go agent harness in the pi mould — an agentic `read`/`write`/`edit`/`bash` loop over an OpenAI-compatible endpoint (OpenCode Zen free models first), a REPL with a minimal slash-command surface, a change ledger rendered by `/changes`, JSONL sessions, and a scriptable `-p` mode — with a later-phase roadmap for a full TUI, file-linked changes view, and native multi-provider support. Hands off for implementation.

## Notes

- **Domain**: minimal terminal agent harness, pi-flavoured (reference: `earendil-works/pi` coding-agent — the shape to stay minimal against).
- **Language**: Go 1.24+, zero runtime deps except the single TOML config lib (BurntSushi/toml, itself zero-dep). Std lib preferred everywhere else.
- **Provider-first**: OpenCode Zen free models via OpenAI-compatible chat/completions. Base URL `https://opencode.ai/zen/v1`, key from env `OPENCODE_API_KEY`. Any OpenAI-compatible base URL is a valid provider.
- **Skills**: `grilling` + `domain-modeling` for the design tickets; `research` for external facts; `prototype` if a UI/logic shape needs a rough take.
- **Vocabulary**: use the glossary in `CONTEXT.md` — harness, agent loop, tool, session, change ledger, changes view, provider, wire protocol.
- **Planning only**: this map produces a spec, not a binary. Implementation hands off when the map is done.

## Decisions so far

<!-- the index — one line per closed ticket: enough to judge relevance, then zoom the link for the detail the ticket holds -->

- [Provider wire spec](issues/01-provider-wire-spec.md) — Zen free models + `big-pickle` all speak OpenAI-compatible chat/completions at `https://opencode.ai/zen/v1` (Bearer, `OPENCODE_API_KEY`); SSE with index-keyed tool-call accumulation; usage only via `stream_options.include_usage` (best-effort). Spec must include a no-tools fallback and tolerate missing usage.
- [System prompt source](issues/02-system-prompt-source.md) — agent instruction comes from three append-only sources, no replacement: built-in default prompt, config `instruction_file` path (unrestricted path, loaded first), and auto-read cwd `AGENTS.md` (cwd-only, no parent walk). Config beats `AGENTS.md`; both load when both present.
- [Tool behaviour detail](issues/03-tool-behaviour-detail.md) — paths resolve to cwd (absolute/`~` ok, no sandbox v1); `read` = path+offset/limit, 2000-line/50KB head-truncate, dirs list children, binary → error; `write` = whole-file, confirm on overwrite only, 1MB cap; `edit` = single pair, exact+unique match, reject-not-found/ambiguous, no confirm; `bash` = confirm-every, 120s default timeout, tail-truncate with temp-file spill, non-zero exit → error; uniform `Error:` tool-result surface; tools array `tool_choice: auto`, `parallel_tool_calls: false`.
- [Change ledger schema](issues/04-change-ledger-schema.md) — ledger is a batch-of-diffs record (git `log`/`show` as template, no git/svn): one batch per agent-loop cycle, baseline-snapshot-at-first-touch then unified diff at close, diff text only stored, no content. Only executed `write`/`edit`; failures and bash never land. Append-only `<session-dir>/<session-id>.changes.jsonl`, one JSON batch per line (`seq` = change id, `ts`, `session`, `tool_call_ids`, `files[]` with `path`/`ops`/`diff`/`delta`). `/changes` lists newest-first (id, byte delta, ts, files; empty → `no changes`); `/changes <id>` prints the stored diffs per file, binary marker, 2000-line/50KB truncate with spill. Drill-down is v1, not fog.
- [REPL behaviour](issues/05-repl-behaviour.md) — plain canonical-mode line reader (no raw mode, no multiline — that's the TUI's later phase); text deltas print live, reasoning fields ignored; Ctrl+C quits at the idle prompt, cancels an in-flight turn (partial turn dropped), declines a pending confirm; slash surface exactly `/changes`, `/new`, `/model`, `/help`, `/quit` (unknown → `og: unknown command 'x' — try /help`); `/new` = fresh session, no confirm; `/model` mirrors `/changes` — no arg lists the `/v1/models` catalog, arg validated against it, no-switch on failure; confirms `…? [y/N]` (default no), declined → `Error: <tool> denied by user` fed back, loop continues; tool runs framed by `── <tool> <args> ──` header + result truncated at the shared caps; prompt `og> `, blank re-prompts, no per-turn usage line.
- [Provider seam](issues/06-provider-seam.md) — seam = `llm` package, one `Client` interface; pull iterator `Stream(ctx, req) (iter.Seq[Event], error)` — open failures as returned error, mid-stream as terminal `Error` event, loop `break` to cancel; seam normalizes + accumulates: `text` delta, completed `tool_call` (at finish), `finish` (`stop`|`tool_calls`|`length`|`other`), best-effort `usage`; partial args unsurfaced, reasoning dropped, no raw payload. `ProviderError{Kind,Message,StatusCode}` — `invalid_request` drives the no-tools fallback (01). Loop holds canonical `Message{Role,Content,ToolCalls,ToolCallID}`; adapter maps per protocol; verbatim assistant-echo at canonical level; `Content` plain string (multi-part later). `ListModels(ctx)` on `Client` drives `/model` (05). v1 = one OpenAI-compatible impl; native wires are more impls of the same interface.
- [Config surface](issues/07-config-surface.md) — TOML at `os.UserConfigDir()/og/config.toml`, no `--config` flag; missing file = defaults. Precedence **defaults < file < env**: `OG_MODEL`/`OG_BASE_URL`/`OG_API_KEY_ENV`/`OG_INSTRUCTION_FILE`/`OG_SESSION_DIR`/`OG_BASH_TIMEOUT` beat the file; API key never in file, read from env named by `api_key_env` (default `OPENCODE_API_KEY`). Six scalars — `model`=`big-pickle`, `base_url`=`https://opencode.ai/zen/v1`, `api_key_env`, `instruction_file` (unset per 02), `session_dir`=`<UserConfigDir>/og/sessions` (hosts transcript + ledger per 04), `bash_timeout`=`120` — plus `[tools]` four booleans (default true; disabled = omitted from `tools`, stale call → `Error: tool 'x' is disabled`). Malformed TOML + unknown keys → fail fast; `/model` never writes the file.
- [Non-interactive mode](issues/08-non-interactive-mode.md) — `-p <string>` (empty value + piped stdin → read stdin; no positional; empty → usage error). Full agent loop, tools enabled, confirms **auto-deny** (no `--yes` v1; denied → `Error: <tool> denied by user` fed back; read/edit/new-write work headless, bash/overwrites don't; deny ≠ failure). Answer text → stdout only; tool framing → stderr; no prompts/usage. Persists a one-turn session: transcript + ledger (04) in session dir (07), session id → stderr. Exit codes `0` success · `1` run failed · `2` interrupted · `3` usage. Model from config only (no `-m` flag).

**Map complete** — all eight decision tickets resolved; every decision the v1 spec depends on is made (see the tickets above for detail). Next: write the spec.

## Not yet specified

- **Changes-view presentation**: where the stored-diff drill-down surfaces in the later phase — inline in the REPL (v1 `/changes <id>` does this already), open in `$EDITOR`, or an alt-screen scrollable list. The diff source is now settled (04); only the presentation seat is open. Sharper once the REPL (05) and full TUI settle.
- **The full TUI**: layout, whether it is a separate renderer over the same agent loop or a distinct mode, and whether it absorbs the REPL. In-scope as a later phase, too loose to ticket yet.
- **Multi-provider beyond the seam**: which native wires land first (OpenAI responses, Anthropic messages, Google generateContent), model routing and cycling. In-scope as a later phase; only the seam (06) is ticketable now.
- **The reach of "lots of scope for configuration"**: future knobs beyond the v1 config keys — per-tool prompts, themes, keybindings, model presets.

## Out of scope

- **MCP support** — pi deliberately omits it; user asked for minimal.
- **Sub-agents / plan mode / to-do lists** — opencode-style features; pi omits them, not requested.
- **Plugin/extension system (pi packages)** — "very bare bones to start" is the point; extension seams are out.
- **Permission popups / granular trust dialogs** — opencode-style; v1 confirmations on `write`/`bash` are the whole story.
- **Hard sandbox (bash containment + file jailing via CLI flag)** — surfaced during 03, deferred to a later release: true bash containment needs chroot/bwrap/sandbox-exec/job-objects (not std-lib, platform-specific), and a partial jail leaks through bash. v1 trust is confirms only.
