# Layered context map for context management

Status: accepted

The harness manages conversation context by deriving a layered state map from the append-only JSONL session, and assembling each provider request as a fixed cheap spine (agent instruction + current turn) plus per-layer retention of prior turns. This replaces the original design, in which the ContextManager re-injected and re-counted the whole flattened history on every `Stream`.

We decided this because the naive approach burned tokens and bloated context: `injectHistory` rebuilt one flat message list from the session on every request, and `recordUsage` walked every message, tool call, and tool definition to re-count tokens each turn — while tool output, the dominant source of bloat, was not separable from durable user intent.

## Architecture

- **Layers (role-based).** `instruction` (never evicted) · `durable-intent` (user + assistant; windowed, compactable) · `tool-output` (condensable, net-drop opt-in per config). There is no separate working-relay layer: a turn's in-flight messages are the current-turn spine, and land in their role-layers when the turn commits.
- **Map (derived index).** Keyed by session-line index, with a secondary tool-call-id lookup for pruning/condensing a specific tool result. The JSONL stays the append-only source of truth; the map is rebuilt from it and never the canonical store.
- **Request assembly.** A fixed cheap spine (instruction + current turn) is unconditional; prior layers adapt by retention rule (intent windowed by `turns`, tool-output condensed). The wire still receives one full ordered message list per request.
- **Cost model.** Per-message token counts are cached by line index, so history is counted once rather than re-counted on every request.
- **Tool-output handling.** Condensation is a request-time projection — the full result stays in JSONL; the condensed form is per-request, keyed by call-id. Net-drop is config-opted.
- **Compaction.** Synchronous, on the durable-intent layer only, summarising the oldest turns into a persisted summary entry (a JSONL metadata marker, not a transcript rewrite), triggered at a configurable budget threshold (default ~75%).
- **Wiring.** The map is the ContextManager's internal state. The built-in compact/condense implementations plug into the existing plugin seam; external `before_request`/`after_response` hooks are untouched.
- **Resume.** Lazy rebuild: layering and keys are reconstructed on load, compaction markers are read during the index build, token-count caching is deferred until first Stream, and condensation is always a live request-time projection of the raw tool layer.

## Considered options

- **Flat re-injection (the original `ContextManager`).** Rejected: re-counts and re-serialises all history every turn, and cannot separate durable intent from tool bloat.
- **Evictability (hot/warm/cold) layers instead of role layers.** Rejected: cuts orthogonal to message origin, giving each role no clear retention rule.
- **Delta-to-wire.** Rejected: every provider requires a single ordered message list; the map buys cached counting and map-driven assembly rather than a streaming-message protocol.
- **Destructive condensation on the persisted transcript.** Rejected: the full tool result stays available in JSONL; condensation is a projection.
- **Map as the canonical store (JSONL as projection).** Rejected: gave up the resumable append-only transcript contract; compaction deltas are cheaper as map mutations + metadata than as transcript rewrites.

## Consequences

- The brittle "last system message splits the current turn" heuristic (the old `injectHistory` turn boundary) disappears; layers make the boundary structural.
- The `turns` window config (`[context] turns`, `OG_CONTEXT_TURNS`) still governs intent-layer retention.
- Compaction markers must migrate the JSONL metadata surface so a resumed session recognises an already-compacted intent layer.
