Type: grilling
Status: resolved
Blocked by:

# 08-non-interactive-mode

## Question

Design the scriptable `-p` mode — run a prompt non-interactively and exit, no REPL. Does it run the full agent loop (tools, with confirmations?) or a single LLM call? What does it print — the live token stream, or only the final text? How does the prompt get in (`-p "prompt"` vs stdin), and does it write a session transcript / change ledger like the REPL? Exit codes: success, model/network error, declined confirm, interrupted.

Graduated from 05-repl-behaviour: the destination names a scriptable `-p` mode, and it shares the agent loop and wire, but its decisions (stream vs final, tools on/off, confirms, exit codes, transcript) are its own.

## Answer

Decided by grilling over one round. `-p` is the one-shot, non-interactive surface over the same loop, seam, config, and instruction sources (02) as the REPL.

- **Prompt input**: `-p <string>` takes the prompt; an empty `-p` with piped stdin (stdin not a TTY) reads all of stdin as the prompt. No positional prompt in v1. Empty prompt either way → usage error.
- **Loop + confirms**: full agent loop, tools enabled, confirms **auto-deny** — no `--yes` flag in v1. Denied confirms return `Error: <tool> denied by user` to the model (03's failure semantics), so the agent adapts: reads, edits, and writes to *new* files still work headlessly; bash and overwrites do not. A deny is the default state, not a run failure. A `--yes` escape hatch for headless bash is future config fog.
- **Output**: the final answer text goes to **stdout only** — clean for capture into files/variables. Tool framing (`── <tool> <args> ──`, 05) goes to **stderr** so a headless run stays debuggable without polluting the pipe. No prompts, no confirm text, no usage line.
- **Persistence**: yes — a `-p` run is a one-turn session. It gets a session id and writes the transcript and `.changes.jsonl` ledger (04) into the session dir (07), so headless runs are inspectable via `/changes` later. The session id prints to stderr on completion.
- **Exit codes**: `0` success · `1` run failed (model/provider/network error) · `2` interrupted (Ctrl+C, same cancellation as 05) · `3` usage error (bad flags, empty prompt).
- **Model**: config only (07) — no `-m`/`--model` flag; `-p` inherits the config default or `OG_MODEL`. Headless model switching is later-phase (model presets fog).

The map is complete — every decision the v1 spec depends on is now made.
