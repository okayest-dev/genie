# 03 — Instruction sources

**What to build:** the agent instruction sent to the provider on every turn of the agent loop as three append-only sources concatenated in order — the built-in default prompt (always present), the config instruction-file path (unrestricted — may point anywhere), and the auto-read `AGENTS.md` in the working directory (cwd only, no parent walk). No source ever silently replaces another; an explicitly configured instruction file that is missing fails clearly at startup.

**Blocked by:** 01 — Wire tracer bullet; 02 — Config surface.

**Status:** resolved

- [x] Every turn carries the built-in default prompt even with no config at all.
- [x] A config instruction file loads after the default; a missing file errors clearly at startup.
- [x] An `AGENTS.md` in the working directory loads automatically, after the config file.
- [x] `AGENTS.md` discovery is cwd-only — parent-directory walk is out.
- [x] When all three sources are present, every turn's instruction appends them in order; the fake provider test asserts the assembled message.
