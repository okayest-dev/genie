# Context management

Genie manages conversational context so a long session stays inside its model's context window without losing continuity. This page covers the model Genie uses — **layered context** — and every knob you can turn to shape it.

The short version: Genie builds a *context map* over the session's JSONL transcript, assigns each message to a *layer* by role, and assembles every request from a fixed cheap *spine* (the agent instruction plus the current turn) plus per-layer retention of earlier turns. When the conversation approaches its *budget*, Genie *compacts* the oldest turns into a summary; oversized earlier tool output can be *condensed* (or net-dropped) instead.

The design was a deliberate architectural decision — see [ADR-0001](adr/0001-layered-context-map.md).

## Layers

Every message in the conversation belongs to exactly one layer, assigned by role — not by how "important" the content looks:

| Layer | Contents | Retention rule |
|-------|----------|----------------|
| **instruction** | agent instruction (system role) | never evicted; ships once per request |
| **durable-intent** | user + assistant turns | windowed by `context.turns`; compactable |
| **tool-output** | tool results | condensable; net-drop opt-in |

A turn's in-flight messages are the current-turn spine while it runs, and land in their role-layers when the turn commits. There is no separate "working" layer.

## The spine and the map

- **Spine**: every request is the agent instruction plus the current turn's messages, unconditionally. Prior turns are *added* by per-layer retention — never shipped wholesale.
- **Context map**: a derived, layered index over the session's JSONL, keyed by session-line index with a secondary lookup by tool-call id. It carries each message's cached token count, so unchanged history is counted **once** rather than re-counted on every request. The JSONL transcript stays the append-only source of truth; the map is rebuilt from it on each request and is never the canonical store.

## Context window and budget

The **context window** is the model's authoritative token limit. Genie never guesses it; it resolves a window for each model from, in order:

1. a per-model `context.windows["<model>"]` override in config (wins over everything),
2. provider data: the context window a wire plugin reports for the model in `wire/list_models`,
3. a provider model-info probe for native wires (probed lazily once per model, cached for the process),

and if nothing authoritative exists the window is **unknown** (`0`) — no invented number.

The **budget** is the portion of the window a conversation may consume before Genie intervenes. It comes from `context.budget_tokens` (absolute) when set, otherwise `context.budget_percent` of the window (default **75%**), keeping headroom so a request can't silently blow the window. With an unknown window and no absolute budget, the budget is zero and no intervention happens.

## History window

`context.turns` controls how much of the *durable-intent* layer each turn's request carries: the `turns` most recent prior turns. The default, `0`, means **all** — the model remembers every earlier turn, at the token cost that implies. Set a positive number to keep each request lean:

```toml
[context]
turns = 20    # inject at most the 20 most recent prior turns
```

## Compaction

When an assembled request's token total crosses the budget, Genie's built-in compactor evicts the **oldest** preceding durable-intent turns and substitutes a single synthetic summary message, keeping the most recent intent turns raw for continuity. The evicted turns' tool output rides along with them. Compaction is:

- **synchronous** — it runs inside request assembly, before the request is forwarded;
- **persisting** — the evicted range is recorded as a JSONL metadata marker (role `compaction`, with `compacted_from`/`compacted_to` bounds and the summary text), *never* a transcript rewrite, so a resumed session recognises the already-compacted intent layer on load;
- **best-effort** — if nothing older remains to evict, the request ships as assembled.

The summary is deliberately mechanical (200 chars per evicted message, capped overall) — shaping what makes it into summaries is tracked as follow-up work.

## Condensation and net-drop

`context.condense_size`, when set to a positive token count, condenses prior-turn tool results that exceed it into a small excerpt (200 runes, noting the full result is retained in the transcript). This is a **request-time projection**: the full result stays in the session JSONL untouched; only the outgoing request carries the narrowed form. The in-flight current turn is never condensed — the tool loop must observe its own fresh result verbatim.

`context.net_drop` strengthens condensation into dropping oversized prior-turn tool results from requests **entirely**, keeping the assistant tool-call message so the model still sees the call was invoked. Off by default.

## Plugin hooks

Context processing also exposes a plugin seam. Plugins can register:

- `context/before_request` — an ordered filter chain that rewrites the fully-assembled request;
- `context/after_response` — observes completed turns (usage deltas), never rewrites history;
- `context/compact` and `context/condense` — **single-active** implementations. Genie's built-in compactor/condenser is the default; a plugin replaces one when the operator names it in `[context.plugins]` (or a built-in body is selected). Two plugins claiming the same single-active seam without an explicit choice is a startup error. See [writing plugins](plugins/authoring.md#context-plugins).

A failing context hook degrades: its contribution is skipped, a `context degraded: …` line surfaces to the terminal, and the request proceeds.

## Configuration summary

All context knobs live under `[context]` (see [configuration](configuration.md) for the full reference — env vars and defaults):

| Key | Default | Meaning |
|-----|---------|---------|
| `context.turns` | `0` (all) | prior turns of history carried into each turn |
| `context.budget_tokens` | `0` (unset) | absolute budget in tokens; overrides percent |
| `context.budget_percent` | `75` | budget as % of window when no absolute budget |
| `context.windows` | `{}` | per-model window overrides, in tokens |
| `context.condense_size` | `0` (disabled) | token threshold above which prior tool output is condensed |
| `context.net_drop` | `false` | drop oversized prior tool results instead of condensing |
| `[context.plugins] order` | discovery order | chain order for context hooks |
| `[context.plugins] active_compact` | `builtin` | single-active compact implementation |
| `[context.plugins] active_condense` | `builtin` | single-active condense implementation |

## Seeing it work

Run with `-v` (verbose) or `-d` (debug) to watch context in action. Debug logs report each outgoing request's token count against the model's resolved window and budget (`context usage ... window=... budget=...`), so you can see headroom before compaction kicks in. Genie's [session transcript](getting-started.md#sessions-and-the-change-ledger) records compaction markers, so auditing *whether* a conversation was compacted and into what is a straight read of the JSONL.