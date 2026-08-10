Type: grilling
Status: resolved
Blocked by:

# 02-system-prompt-source

## Question

Where does og's agent instruction (the system prompt) come from in v1? Options: a built-in default prompt, a config-file setting, an `AGENTS.md` in the working directory, or a pi-style context file. Decide the sources and their precedence (e.g. config overrides default; does `AGENTS.md` get read automatically?).

This shapes the config surface (07) and the session start flow.

## Answer

Three sources, all appended — no full-prompt replacement in v1:

1. **Built-in default prompt** — always present; the base harness identity and behaviour rules.
2. **Config instruction-file path** — the deliberate, human-set instruction; may point anywhere, including outside the working directory (path traversal is not locked out). Loaded first.
3. **Auto-read `AGENTS.md`** — the `AGENTS.md` in the working directory, read automatically at session start. **cwd only**, no parent-directory walk (pi's root-most → cwd layering is a later phase). Loaded after the config file.

Precedence: **config path beats `AGENTS.md`** (config first, then `AGENTS.md`; both present = both load). `AGENTS.md` discovery is cwd-only, but the explicit config path is unrestricted.

Context: pi walks up to the filesystem root layering all `AGENTS.md`/`CLAUDE.md` (deduped by canonical path), plus a global `~/.pi/agent/AGENTS.md`, and `--system-prompt` replaces the default block while context files append. v1 deliberately drops the global file and the parent walk, and uses append-only for all sources.

Vocabulary (added to `CONTEXT.md`): **Agent instruction** (canonical term for the system prompt), **Instruction file** (the on-disk source; notes the cwd-only-vs-explicit distinction).

Feeds into: config surface (07) — `instruction_file` key; session start flow — read default + config file + cwd `AGENTS.md`, concatenated in that order.
