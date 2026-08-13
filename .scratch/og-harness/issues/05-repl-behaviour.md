Type: grilling
Status: resolved
Blocked by:

# 05-repl-behaviour

## Question

Pin down the REPL's v1 behaviour: token streaming display (print as they arrive?), Ctrl+C interrupt semantics (cancel the in-flight loop vs quit), the slash-command surface (`/changes`, `/new`, `/model`, `/help`, `/quit`), how confirm prompts work, and how tool output interleaves with model tokens in the stream. Keep it std-lib — no screen management.

## Answer

Decided by grilling over one round, grounded against pi (current pi main is a raw-mode full TUI; og deliberately diverges — std-lib, no screen management, and v1 confirms per 03).

- **Input reading**: plain line reader over stdin in canonical terminal mode. Enter submits; arrow-key history, multi-line composition, and raw mode are out — editing is whatever the terminal's line discipline gives. Multiline + a line editor belong to the full TUI fog.
- **Streaming display**: text deltas print **live** as they arrive — only `delta.content`. Any separate provider reasoning field is ignored in v1. Mid-turn network error → `Error: <reason>` on its own line, the partial turn is dropped, return to the prompt.
- **Ctrl+C**: at the idle prompt → **exit**. During an in-flight turn (streaming or between tool rounds) → **cancel the turn**: abort the HTTP stream, print `(interrupted)`, drop the partial assistant turn from the conversation (the provider never sees it), return to the prompt. During a confirm → **decline** (below), not quit. No double-press logic — canonical-mode Ctrl+C is a signal, so two clean zones suffice.
- **Slash commands**: exactly five — `/changes`, `/new`, `/model`, `/help`, `/quit`. Unknown → `og: unknown command 'x' — try /help`.
- **`/new`**: starts a fresh session (new session id, new transcript file). No confirm — sessions are persisted JSONL, nothing is lost.
- **`/model`**: mirrors `/changes`. No arg → print the full model list from the `GET /v1/models` catalog (unauthenticated per 01). With an arg → validate the id against that catalog, then switch for the session; unknown id → `og: no such model 'x'`; catalog fetch failure → error message, no switch.
- **Confirm prompts**: `og: overwrite <path>? [y/N]` and `og: run: <command>? [y/N]` — only `y`/`yes` (case-insensitive) accepts, anything else declines, default no. Declined → `Error: <tool> denied by user` as the tool result, the agent loop continues (03's failures-flow-back rule). Ctrl+C during a confirm = decline.
- **Tool interleaving**: before executing each tool call print a header line `── <tool> <args> ──`; after execution print the result, truncated at the same 2000-line/50KB cap (with the spill-note when truncated, 03). Error results print via 03's `Error:` shape. For write/edit the header is the note — the diffs live in `/changes` (04).
- **Turn framing**: prompt glyph **`og> `**. No startup banner beyond at most one line (model id + cwd). Blank input → silent re-prompt. No per-turn usage/token line in v1 (usage is best-effort per 01).
- **`-p` mode is out of this ticket's scope** — graduated to issue `08-non-interactive-mode` (non-interactive: no REPL, no prompt, no confirms as-repl; its own decisions).
