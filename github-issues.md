# GitHub Issues Import

## Issue 13: Session dies silently when a turn hits max completion tokens (finish_reason=length) with no tool call

**Type:** bug
**Priority:** 1
**Labels:** bug, critical

### Description

In session `20261006-130546-b4406bb0` (bedrock provider, model `eu.anthropic.claude-sonnet-5`), the REPL died silently mid-task: no error, no exception, no message to the user. The debug log pinpoints the exact cause on the final turn:

```
level=DEBUG msg="message appended to transcript" session=20261006-130546-b4406bb0 role=assistant
level=INFO msg="turn completed" finish_reason=length prompt_tokens=45456 completion_tokens=4096 total_tokens=49552
```

`completion_tokens=4096` is the max-output-token ceiling for this model/provider. The model was cut off mid-generation before it could emit a `tool_calls` block (or a final answer), `finish_reason` came back as `length`, and genie's loop simply appended the (incomplete) assistant message to the transcript and stopped — no retry, no continuation request, no error surfaced to the user.

### Reproduction

Long-running coding session that accumulates context (prompt_tokens had grown to 45,456 by this point from repeated full-file `cat`/`sed` reads). Eventually a turn's output is long enough (or the model is mid-preamble) that it exceeds the 4096-token completion cap before a tool call is emitted.

### Secondary observation

The session's JSONL transcript (`~/Library/Application Support/genie/sessions/20261006-130546-b4406bb0.jsonl`) shows each assistant turn's `content` (the short preamble before a tool call) growing by concatenation across turns (0 → 62 → 166 → 261 → 384 → 495 chars), each one being the prior turns' preambles plus one new sentence, rather than just that turn's own preamble. This may be an unrelated transcript-recording artifact, but it's worth checking whether the same accumulated/duplicated text is also being resent to the model as part of the prompt on each turn, which would explain some of the unusually rapid prompt-token growth (45,456 tokens after only 17 tool calls, several of which were small greps).

### Impact

- A turn that gets truncated by the provider's max-output-tokens limit silently ends the session with no indication of failure — the user is left staring at a dead prompt.
- No retry-with-higher-budget, no "continue" follow-up turn, and no partial tool call recovery (e.g. if the truncated output contained a partial tool-call JSON fragment, it's discarded).

### Suggested fixes

1. In the agent loop, explicitly check `finish_reason`. If it is `length` and no usable `tool_calls`/final answer was produced, retry the turn (e.g. with a shorter/continuation prompt, or a note asking the model to be more concise) rather than ending the session.
2. Surface a clear error/warning to the user/log when a turn ends due to truncation, instead of exiting silently.
3. Investigate whether assistant preamble text is being duplicated/accumulated into subsequent prompts (see "Secondary observation" above) — if so, trimming this would reduce the chance of hitting the completion-token or context-window ceiling in long sessions.

### Environment

- Binary: `genie` (module `github.com/okayest-dev/genie`), installed via `go install`.
- Provider: bedrock, model `eu.anthropic.claude-sonnet-5` (per `~/Library/Application Support/genie/config.toml`).
- Session file and debug log available on request.

---

## Issue 12: Make tool calls show their output

**Type:** feature
**Priority:** 1
**Labels:** feature, ui

### Description

Tool calls currently don't print their output to the screen. This makes it difficult to track what is happening

```
── bash {"command": "cd . && bd show VER-am3.10 2>&1; echo \"---\"; bd show VER-am3.13.1 2>&1; echo \"---\"; bd show VER-c2o 2>&... ──

── bash {"command": "cd . && bd --help 2>&1 | head -40; echo \"---\"; bd ready --help 2>&1 | head -40"} ──

── bash {"command": "cd . && bd ready --exclude-type epic --explain 2>&1 | head -100"} ──

── bash {"command": "cd . && bd ready --exclude-type epic --limit 0 2>&1 | grep -v epic; echo \"---list sorted by priority/creat... ──

── bash {"command": "cd . && bd show VER-am3.11.2 2>&1 | head -60"} ──

── bash {"command": "cd . && bd children VER-am3.13 2>&1; echo \"---am3.11---\"; bd children VER-am3.11 2>&1; echo \"---am3.12--... ─
```

The tool calls should print their output to the screen so that the user can validate the output.

---

## Issue 8: UI

**Type:** epic
**Priority:** 2
**Labels:** epic, ui

### Description

UI Epic

---

## Issue 7: Support agent invocation of all slash commands

**Type:** feature
**Priority:** 2
**Labels:** feature, agent

### Description

The goal of genie is to be a fully automatable agent harness. As such the agent should have access to all slash commands invokable by the user.

---

## Issue 6: herdr hook integration

**Type:** feature
**Priority:** 2
**Labels:** feature, integration

### Description

integrate herdr hooks for genie so that sessions can be handled by the agent view in herdr

---

## Issue 5: CAA Loop

**Type:** feature
**Priority:** 2
**Labels:** feature, architecture

### Description

Implement a sandboxed, platform agnostic (deno?) code as action loop that allows an agent to make a single scripted code execution rather than multiple expensive tool calls through bash.

---

## Issue 4: Release v1.0

**Type:** epic
**Priority:** 1
**Labels:** epic, release

### Description

All features for release 1.0

---

## Issue 3: release 1

**Type:** task
**Priority:** 2
**Labels:** release

### Description

(no description provided)

---

## Issue 2: R1.0.0

**Type:** task
**Priority:** 2
**Labels:** release

### Description

(no description provided)