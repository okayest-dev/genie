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

## Not yet specified

- **Changes-view presentation**: how a change ledger entry "links" to its actual file in the later phase — open in `$EDITOR`, an alt-screen scrollable list, an inline line diff, or a `git diff` when the cwd is a repo. Sharper once the ledger schema (04) and REPL (05) settle.
- **The full TUI**: layout, whether it is a separate renderer over the same agent loop or a distinct mode, and whether it absorbs the REPL. In-scope as a later phase, too loose to ticket yet.
- **Multi-provider beyond the seam**: which native wires land first (OpenAI responses, Anthropic messages, Google generateContent), model routing and cycling. In-scope as a later phase; only the seam (06) is ticketable now.
- **The reach of "lots of scope for configuration"**: future knobs beyond the v1 config keys — per-tool prompts, themes, keybindings, model presets.

## Out of scope

- **MCP support** — pi deliberately omits it; user asked for minimal.
- **Sub-agents / plan mode / to-do lists** — opencode-style features; pi omits them, not requested.
- **Plugin/extension system (pi packages)** — "very bare bones to start" is the point; extension seams are out.
- **Permission popups / granular trust dialogs** — opencode-style; v1 confirmations on `write`/`bash` are the whole story.
- **Hard sandbox (bash containment + file jailing via CLI flag)** — surfaced during 03, deferred to a later release: true bash containment needs chroot/bwrap/sandbox-exec/job-objects (not std-lib, platform-specific), and a partial jail leaks through bash. v1 trust is confirms only.
