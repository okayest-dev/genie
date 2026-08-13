# 12 — -p completion

**What to build:** the full scriptable headless surface. A prompt from the flag or — for `-p ""` with piped stdin — from stdin; the full agent loop with tools; confirms auto-denied (denial feeds back so the agent adapts, never a run failure); answer text to stdout only with tool framing on stderr and no prompts or confirm text; a persisted one-turn session with its transcript and change ledger; the session id printed to stderr; and the four exit codes — 0 success, 1 failed run, 2 interrupted (Ctrl+C cancels like a REPL turn), 3 usage. Model comes from config only. Same instruction sources and config as the REPL.

**Blocked by:** 03 — Instruction sources; 04 — Session persistence; 05 — Agent tool loop + read; 06 — write + edit tools; 07 — bash tool + confirm gate; 08 — Change ledger capture.

**Status:** ready-for-agent

- [ ] `-p ""` with piped stdin reads all of stdin as the prompt; an empty prompt either way exits 3.
- [ ] Answer text goes to stdout only; tool framing to stderr; no prompts, confirm text, or usage line.
- [ ] Confirms auto-deny; denied writes/bash feed back to the agent, which adapts; read/edit/new-write work headless.
- [ ] The run persists a one-turn session (transcript + ledger in the session dir) and prints the session id to stderr.
- [ ] Exit codes are 0 success, 1 failed run, 2 interrupted, 3 usage; Ctrl+C cancels like a turn and is never misreported as a failure.
- [ ] Uses the same config and instruction sources as the REPL; model from config only.
