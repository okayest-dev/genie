# Terminal Agent Permission Escalation UX — Prior Art

Research for genie's interactive permission-escalation design. How six established
terminal agent harnesses present tool-call and permission confirmations in
interactive (REPL/terminal) mode. All facts sourced from official docs and
first-party source code; primary sources cited inline.

Related notes: `research/provider-wire-harness-landscape.md`,
`research/sandboxed-runtimes.md`.

---

## 1. opencode

### Input modality

V2 TUI is a full-screen raw-keypress UI. Permission prompts render in-session
as a dialog route (`packages/tui/src/routes/session/permission.tsx`) with
segmented options (arrow-key navigation) rather than line input.

### Choice surface and effects

- Config `permissions` is an array of `{ action, resource, effect }` rules with
  effects `allow` / `deny` / `ask`. "If no rule matches, OpenCode uses ask."
  ([opencode V2 docs](https://opencode.ai/v2/docs/permissions))
- In ask mode the dialog offers three replies: "Allow once", "Allow always",
  "Reject" — rendered as segmented options; `escapeKey="reject"`, so `Esc`
  rejects ([packages/tui/src/routes/session/permission.tsx](https://github.com/anomalyco/opencode/blob/dev/packages/tui/src/routes/session/permission.tsx)).
- "Always" runs a second stage: it lists the tool's proposed `save` patterns as
  checkboxes (a single `*` = everything) and, on confirm, writes them as durable
  project-scoped saved allow rules — "approve this request and save the tool's
  proposed patterns for the current project". Saved rules "never override a
  configured deny." ([opencode V2 docs](https://opencode.ai/v2/docs/permissions))
- "Reject" opens a `RejectPrompt` with a mandatory confirmation keystroke and an
  optional message field ("Confirm permission rejection"); the message is sent
  as feedback ([permission.tsx](https://github.com/anomalyco/opencode/blob/dev/packages/tui/src/routes/session/permission.tsx)).
- Multi-resource call: "Any deny denies the operation; otherwise any ask asks;
  otherwise the operation is allowed." ([opencode V2 docs](https://opencode.ai/v2/docs/permissions))

### What the model sees on reject

`permission.reply` fails the tool's pending request with `CorrectedError { feedback }`
when the user supplied a message, else `DeclinedError`. A single reject also rejects
every other pending request in the same session; after an "always" save, other
pending asks that now match the saved rules are auto-approved
([packages/core/src/permission.ts](https://github.com/anomalyco/opencode/blob/dev/packages/core/src/permission.ts)).

### Persistence

V1 (current shipped docs): "always" means "approve future requests matching the
suggested patterns (for the rest of the current OpenCode session)" — not
persisted across restarts ([legacy issue #20066](https://github.com/anomalyco/opencode/issues/20066)).
V2 changed this to durable project-scoped saved rules (see above). V1 also had a
non-interactive auto-approve mode (`opencode --auto`); V2 has a TUI/permission
"auto" mode toggle per session.

Sources: [opencode V2 permissions docs](https://opencode.ai/v2/docs/permissions),
[opencode V1 permissions docs](https://opencode.ai/docs/permissions),
[packages/core/src/permission.ts](https://github.com/anomalyco/opencode/blob/dev/packages/core/src/permission.ts),
[packages/tui/src/routes/session/permission.tsx](https://github.com/anomalyco/opencode/blob/dev/packages/tui/src/routes/session/permission.tsx).

---

## 2. Claude Code

### Input modality

Terminal TUI with dialog tabs. On a permission prompt, options run in a dialog;
`Left`/`Right` arrows "navigate between tabs in permission dialogs"; `Tab` "on
most permission prompts, with **Yes** or **No** focused, opens a comment field
on that option"; `Shift`+`Tab` cycles permission modes and on file prompts
selects the "rest of session" option
([interactive mode keybindings](https://code.claude.com/docs/en/interactive-mode)).

### Esc / Ctrl+C semantics during a prompt

- `Esc` — "Stop the current response or tool call mid-turn so you can redirect.
  Claude keeps the work done so far… When a dialog is open, `Esc` closes the
  dialog. On a permission prompt, `Esc` declines the action, the same as **No**
  without a comment."
- `Ctrl+C` — "Interrupt, or clear input. Interrupts a running operation. If
  nothing is running, the first press clears the prompt input and a second press
  exits Claude Code."
  ([interactive mode keybindings](https://code.claude.com/docs/en/interactive-mode))

### Permission tiers

Tiered system: read-only bash commands proceed without approval; file
modification, non-preapproved WebFetch, and WebSearch require approval (frequencies
tunable) ([permissions reference](https://code.claude.com/docs/en/permissions)).

### "Yes, and don't ask again" persistence

| Scope | Request type | Persistence |
|-------|-------------|-------------|
| bash command | "Yes, and don't ask again" | "Permanently per repository and command" |
| file modification | "Yes, and don't ask again" | "Until session end" |
| WebFetch | "Yes, and don't ask again" | "Permanently per repository and domain" |
| WebSearch | "Yes, and don't ask again" | "Permanently per repository" |

Persisted bash-command and web-domain approvals are written to `./.claude/settings.local.json`
at the repo root and apply to future sessions in that repo; file-modification
approval is session-only and not saved. When the prompt can't tell exactly what
it would allow, only a one-time approval is offered. For compound bash commands,
approving with "don't ask again" saves a separate rule per subcommand requiring
approval (max 5 per command) ([permissions reference](https://code.claude.com/docs/en/permissions)).

### Comment / deny-with-reason

On an approval prompt, `Tab` opens a comment field. Sending a comment with
**Yes** posts it after the command completes; a comment with **No** is sent back
as "the reason for your denial" and "Claude will continue working." A **No**
without a comment from the main conversation "stops the turn"
([permissions reference](https://code.claude.com/docs/en/permissions)).

### Permission modes

Modes are a per-session cycle via `Shift`+`Tab`: default (Manual), `acceptEdits`,
`plan`, `auto`, `dontAsk`, `bypassPermissions`; the `auto` mode uses a classifier
instead of prompting ([permission modes](https://code.claude.com/docs/en/permission-modes)).

Sources: [permissions reference](https://code.claude.com/docs/en/permissions),
[permission modes](https://code.claude.com/docs/en/permission-modes),
[interactive mode](https://code.claude.com/docs/en/interactive-mode).

---

## 3. aider

### Input modality

Line-based REPL built on `prompt_toolkit`. Confirmations are single-line text
prompts, not a separate dialog surface.

### Choice surface

`confirm_ask()` in `aider/io.py` renders `" (Y)es/(N)o [Yes]: "` as line input.
Grouped asks add `(A)ll` / `(S)kip all`. When a prompt is marked non-repeatable
(`allow_never`), a `(D)on't ask again` choice is added and the key is recorded in
a `never_prompts` set on the `IOWrapper` instance — in-memory, session-only,
not persisted
([aider/io.py](https://github.com/Aider-AI/aider/blob/main/aider/io.py)).
Example from the FAQ: "Add 6.9k tokens of command output to the chat?
(Y)es/(N)o [Yes]" ([aider FAQ](https://aider.chat/docs/faq.html)).

### Auto-approve

`--yes-always` flag: "Always say yes to every confirmation"
([aider/args.py](https://github.com/Aider-AI/aider/blob/main/aider/args.py)).

### Interrupt / rejection feedback

Two Ctrl+C behaviors in `coders/base_coder.py`:

- During a reply, `^C` appends a user message `^C KeyboardInterrupt` plus an
  assistant message "I see that you interrupted my previous reply." — the
  interruption is fed to the model as conversation context
  ([base_coder.py](https://github.com/Aider-AI/aider/blob/main/aider/coders/base_coder.py)).
- On the input line, a bare state prints "^C again to exit"; a second `^C`
  within 2s exits the app.

### Persistence

No "don't ask again" is persisted. Edits are auto-committed by default (no
per-edit confirmation); `dirty_commits` and `auto_commits` options control commit
behavior ([aider FAQ](https://aider.chat/docs/faq.html)).

Sources: [aider/io.py](https://github.com/Aider-AI/aider/blob/main/aider/io.py),
[aider/args.py](https://github.com/Aider-AI/aider/blob/main/aider/args.py),
[coders/base_coder.py](https://github.com/Aider-AI/aider/blob/main/aider/coders/base_coder.py),
[aider FAQ](https://aider.chat/docs/faq.html).

---

## 4. Goose (block/goose)

Canonical repo moved: [block/goose](https://github.com/block/goose) 301-redirects
to [aaif-goose/goose](https://github.com/aaif-goose/goose). Docs cited from the
redirect target.

### Input modalities

Two surfaces share the same permission model:

- **CLI interactive**: a line-based REPL (`goose-cli/src/session/input.rs`)
  where tool confirmations surface via `cliclack::select`, an interactive
  arrow-key menu
  ([session/mod.rs](https://github.com/aaif-goose/goose/blob/main/crates/goose-cli/src/session/mod.rs)).
- **Desktop (ACP)**: a React confirmation card
  ([ToolCallConfirmation.tsx](https://github.com/aaif-goose/goose/blob/main/ui/desktop/src/components/ToolCallConfirmation.tsx)).

### Permission modes and levels

Three permission modes (docs): Completely Autonomous (default), Manual Approval,
Smart Approval, plus Chat Only; per-tool levels are Always Allow / Ask Before /
Never Allow, configurable from a mode toggle or Settings, effective only in
Manual or Smart mode ([goose permissions guide](https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/managing-tools/goose-permissions.md)).
The mode is read from config (`config.get_goose_mode()`); no approval-mode CLI
flag exists in [goose-cli/src/cli.rs](https://github.com/aaif-goose/goose/blob/main/crates/goose-cli/src/cli.rs).

### CLI confirmation dialog

`prompt_tool_confirmation()` ([session/mod.rs](https://github.com/aaif-goose/goose/blob/main/crates/goose-cli/src/session/mod.rs)):

- Hides thinking, rings an attention bell, renders the tool name, arguments, and
  optional prompt, then asks "Goose would like to call the above tool, do you
  allow?" (custom-prompt case: "Do you allow this tool call?").
- Without a tool-supplied prompt, options are **Allow / Always Allow / Deny /
  Cancel**. With a custom prompt, **Always Allow is hidden** (Allow / Deny / Cancel).
- Cancel = "Cancel the AI response and tool call"; an `Interrupted` error from
  the select (Esc/Ctrl+C) maps to `Permission::Cancel`.
- Non-interactive (`goose run`) auto-allows: "Tool confirmation required in
  non-interactive mode, auto-allowing"
  ([session/mod.rs](https://github.com/aaif-goose/goose/blob/main/crates/goose-cli/src/session/mod.rs)).

### Desktop confirmation card

Buttons **Allow Once**, **Always Allow**, **Deny** (Always Allow hidden when a
custom prompt is present); after resolution the buttons are replaced by a status
line naming the tool and decision
([ToolApprovalButtons.tsx](https://github.com/aaif-goose/goose/blob/main/ui/desktop/src/components/ToolApprovalButtons.tsx)).
The ACP boundary maps these to `allow_once` / `allow_always` / `deny_once` /
`reject_always` ([permissionRequests.ts](https://github.com/aaif-goose/goose/blob/main/ui/desktop/src/acp/permissionRequests.ts)).

### Underlying state machine

`Permission` enum: `AlwaysAllow`, `AllowOnce`, `Cancel`, `DenyOnce`, `AlwaysDeny`
(snake_case serialized) ([permission.rs](https://github.com/aaif-goose/goose/blob/main/crates/goose-provider-types/src/permission.rs)).
Confirmations for a turn are registered and awaited with a per-turn lock held
until all answers arrive
([tool_confirmation_coordinator.rs](https://github.com/aaif-goose/goose/blob/main/crates/goose/src/agents/tool_confirmation_coordinator.rs)).

Sources: [goose permissions guide](https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/managing-tools/goose-permissions.md),
[tool permissions guide](https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/managing-tools/tool-permissions.md),
[session/mod.rs](https://github.com/aaif-goose/goose/blob/main/crates/goose-cli/src/session/mod.rs),
[ToolApprovalButtons.tsx](https://github.com/aaif-goose/goose/blob/main/ui/desktop/src/components/ToolApprovalButtons.tsx),
[crates/goose-provider-types/src/permission.rs](https://github.com/aaif-goose/goose/blob/main/crates/goose-provider-types/src/permission.rs).

---

## 5. Cursor

### Run modes (desktop agent)

Run modes live in Settings > Agents > Approvals & Execution:
**Auto-review** (default), **Allowlist**, **Run Everything**. In Auto-review,
allowlisted shell operations run immediately, the rest execute sandboxed, and an
"intelligent auto-approval" classifier decides whether to allow, tell the agent
to try another approach, or ask the user to approve
([run modes](https://cursor.com/docs/agent/security/run-modes)). Pre-3.5 modes
were Run in Sandbox / Ask Every Time / Run Everything; "Ask Every Time" was
deprecated in 3.5. On a sandbox-blocked request the terminal prompt offers
**Skip** / **Run** / **Add to allowlist** (run now plus auto-approve future)
([terminal docs](https://cursor.com/help/ai-features/terminal)).

### CLI permissions

Config files: `~/.cursor/cli-config.json` (global) and `<project>/.cursor/cli.json`
(project). Permission tokens: `Shell(commandBase)`, `Read(pathOrGlob)`,
`Write(pathOrGlob)`. `permissions.allow` / `permissions.deny` map tokens to
Allow/Deny; `--force` runs without prompting; anything else prompts
([CLI permissions reference](https://cursor.com/docs/cli/reference/permissions)).
Modes: `agent`, `plan`, `ask` (CLI `--mode`).

Sources: [run modes](https://cursor.com/docs/agent/security/run-modes),
[CLI permissions reference](https://cursor.com/docs/cli/reference/permissions),
[terminal docs](https://cursor.com/help/ai-features/terminal).

---

## 6. pi (earendil-works/pi)

### Permission model

No built-in sandbox — pi "runs with the user's permissions"; the security model
is project trust (a decision per project path, `defaultProjectTrust: "ask"`,
decisions persisted per canonical path) plus `--approve` / `--no-approve` flags.
Tool-call permission gating is delegated to extensions via `tool_call` events
([security.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/security.md)).

The reference [permission-gate example extension](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/examples/extensions/permission-gate/src/index.ts)
uses `ctx.ui.select` with `["Yes", "No"]`; declining returns
`{ block: true, reason }` and the reason string is fed back to the model. When
no TUI is available it blocks by default with a reason.

### Input modality

Raw-keypress TUI: arrow keys navigate selectors, `Enter` selects, `Esc`
cancels (`onCancel` → `done(null)`); matching is keypress-level
([tui.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/tui.md)).
The bash component renders "Running… (tui.select.cancel to cancel)" and exposes
a cancel keybinding
([bash-execution.ts](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/modes/interactive/components/bash-execution.ts)).
A first-party [pi-permission-system](https://pi.dev/pkg/pi-permission-system)
package ports OpenCode-style permission policies (allow/deny rules) onto Pi.
Note: the Rust `pi-agent` crate is a separate port and is not cited here.

Sources: [security.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/security.md),
[tui.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/tui.md),
[permission-gate example](https://github.com/earendil-works/pi/tree/main/packages/coding-agent/examples/extensions/permission-gate),
[bash-execution.ts](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/modes/interactive/components/bash-execution.ts).

---

## Summary

### Shared patterns

1. **Two-tier "yes" semantics everywhere:** an allow-once and a
   "don't ask again / allow always" variant; the durable variant is scoped
   (per repo, per project, per command/tool, per domain) and always an explicit
   per-request opt-in — never a global toggle.
2. **Esc and Ctrl+C are split at every harness, differently:** Claude Code
   (`Esc` declines the prompt, `Ctrl+C` interrupts the op / clears input),
   opencode (`Esc` = Reject), Goose (`Esc`/Interrupted in the select = Cancel of
   tool call and AI response), aider (Ctrl+C mid-reply is fed to the model as a
   `^C KeyboardInterrupt` message; bare-line second ^C exits).
3. **Deny-with-reason feeds the model** in Claude Code (comment on **No** sent
   as denial reason), opencode V2 (`RejectPrompt` message → `CorrectedError`),
   and pi (extension returns `{ block: true, reason }`). aider and Cursor have no
   free-form deny reason; Goose's CLI cancel just aborts.
4. **Multi-permission calls are handled both ways:** Claude Code decomposes a
   compound bash approval into per-subcommand saved rules; opencode V2 rejects
   all other pending asks in the session on a single reject and auto-approves
   pending asks after an "always" save; Goose holds a per-turn lock until every
   confirmation answers; aider groups asks with `(A)ll` / `(S)kip all`.
5. **Input modality splits by REPL style:** line-based REPLs use inline
   `y/N` prompts (aider), interactive select menus (Goose cliclack), or config
   files (Cursor CLI); full-screen TUIs use arrow-key dialogs and hotkeys
   (Claude Code, opencode V2, pi).
6. **Auto/classifier modes exist in parallel to asking** (Claude Code `auto`,
   opencode auto, Cursor Auto-review, Goose Completely Autonomous default),
   always as modes the user selects rather than the sole UX.

### Notable divergences

- **Persistence of "always":** opencode changed V1 (session-only "for the rest
  of the current OpenCode session", seen as a bug in issue #20066) to V2
  (durable project-scoped saved rules); Claude Code persists only bash/web
  approvals (`.claude/settings.local.json`), file-mod is session-only; aider
  never persists; Cursor and Goose persist via allowlist / always-allow rules.
- **Default posture:** Goose defaults to Completely Autonomous; Cursor defaults
  to Auto-review (sandbox + classifier); Claude Code, opencode, aider default to
  asking.
- **Deny-reason surface:** only Claude Code, opencode V2, and pi (via
  extensions) let the user explain a rejection to the model.
- **No such feature:** aider has no "allow every command of this type" UI;
  escapes to `--yes-always`.

---

## Sources

| Source | Type | What it provides |
|--------|------|------------------|
| [opencode V2 permissions](https://opencode.ai/v2/docs/permissions) | Official docs | Rule effects, ask fallback, always/save semantics, reject-all-pending, multi-resource arity |
| [opencode V1 permissions](https://opencode.ai/docs/permissions) | Official docs | V1 session-scoped "always", `--auto`; basis for the persistence change |
| [opencode core/src/permission.ts](https://github.com/anomalyco/opencode/blob/dev/packages/core/src/permission.ts) | Primary code | reply handling: always→saved rules, reject→DeclinedError/CorrectedError |
| [opencode tui/src/routes/session/permission.tsx](https://github.com/anomalyco/opencode/blob/dev/packages/tui/src/routes/session/permission.tsx) | Primary code | Allow once/always/Reject segmented options, Esc=reject, RejectPrompt message field |
| [opencode issue #20066](https://github.com/anomalyco/opencode/issues/20066) | Primary issue | allow-always not persisted across restarts (V1) |
| [Claude Code permissions](https://code.claude.com/docs/en/permissions) | Official docs | Tier table, per-scope persistence, comment-on-deny semantics, compound bash rules |
| [Claude Code permission modes](https://code.claude.com/docs/en/permission-modes) | Official docs | Mode list, Shift+Tab cycling |
| [Claude Code interactive mode](https://code.claude.com/docs/en/interactive-mode) | Official docs | Esc/Ctrl+C/Tab/arrow keybindings during prompts |
| [aider `aider/io.py`](https://github.com/Aider-AI/aider/blob/main/aider/io.py) | Primary code | `confirm_ask` options, `never_prompts` session-only set |
| [aider `aider/args.py`](https://github.com/Aider-AI/aider/blob/main/aider/args.py) | Primary code | `--yes-always` |
| [aider `coders/base_coder.py`](https://github.com/Aider-AI/aider/blob/main/aider/coders/base_coder.py) | Primary code | ^C-as-model-feedback, "^C again to exit" |
| [aider FAQ](https://aider.chat/docs/faq.html) | Official docs | Example confirm prompt, auto-commit defaults |
| [Goose permissions guide](https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/managing-tools/goose-permissions.md) | Primary docs | Modes + per-tool levels |
| [Goose tool permissions guide](https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/managing-tools/tool-permissions.md) | Primary docs | Always Allow / Ask Before / Never Allow configuration |
| [Goose `crates/goose-cli/src/session/mod.rs`](https://github.com/aaif-goose/goose/blob/main/crates/goose-cli/src/session/mod.rs) | Primary code | CLI cliclack select options, no-prompt vs custom-prompt cases, non-interactive auto-allow |
| [Goose `ToolApprovalButtons.tsx`](https://github.com/aaif-goose/goose/blob/main/ui/desktop/src/components/ToolApprovalButtons.tsx) | Primary code | Desktop Allow Once / Always Allow / Deny buttons, status line |
| [Goose `permission.rs`](https://github.com/aaif-goose/goose/blob/main/crates/goose-provider-types/src/permission.rs) | Primary code | `Permission` enum values |
| [Goose `cli.rs`](https://github.com/aaif-goose/goose/blob/main/crates/goose-cli/src/cli.rs) | Primary code | No approval-mode CLI flag; mode from config |
| [Cursor run modes](https://cursor.com/docs/agent/security/run-modes) | Official docs | Auto-review/Allowlist/Run Everything, classifier, pre-3.5 modes |
| [Cursor CLI permissions](https://cursor.com/docs/cli/reference/permissions) | Official docs | cli-config.json/cli.json, Shell/Read/Write tokens, `--force` |
| [Cursor terminal docs](https://cursor.com/help/ai-features/terminal) | Official docs | Sandbox-block prompt actions: Skip/Run/Add to allowlist |
| [pi security.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/security.md) | Primary docs | No sandbox, project trust, `--approve`/`--no-approve`, extension-based gating |
| [pi tui.md](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/tui.md) | Primary docs | Raw-keypress selectors, Enter/Esc semantics |
| [pi permission-gate example](https://github.com/earendil-works/pi/tree/main/packages/coding-agent/examples/extensions/permission-gate) | Primary code | `ui.select(["Yes","No"])`, `{block:true, reason}` model feedback |
| [pi bash-execution.ts](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/modes/interactive/components/bash-execution.ts) | Primary code | Cancel keybinding on bash tool |