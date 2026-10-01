# Prompt segment vocabulary and layout for themable output

Status: accepted. The decisions below are the wayfinder map og-1i1's prompt layout decision area (ticket og-1i1.5 "Decision: Prompt segment vocabulary and layout"). It fixes which prompt segments genie offers, what each renders, the content-generator contract, the layout/separator model, and the two builtin preset layouts (Classic, Lean), so the style-namespace decision (og-1i1.6) and the external-data-gathering decision (og-1i1.8) can build on it. It does not fix the TOML config encoding of the theme file (that is og-1i1.3's decision area) or the role/style namespace (og-1i1.6).

## The segment vocabulary

Genie owns a closed vocabulary of **nine prompt segments**: `dir`, `provider`, `model`, `agent`, `session`, `git`, `time`, `tokens`, and `prompt_char`. Each renders fixed content by contract; the theme owns ordering, separators, and styling — it can neither add segments nor change what a segment says.

Content contracts:

- **dir** — the working directory; `$HOME` shortened to `~`, deep paths tail-truncated (p10k-style). Content otherwise absolute.
- **provider** — the bare provider name (`zen`).
- **model** — the model id (`big-pickle`). Provider and model are separate segments because the slash commands switch them independently.
- **agent** — the current agent name; collapses on the default flow (no agent active).
- **session** — the trailing suffix of the session id only — the `a1b2c3d4` in `20260923-150405-a1b2c3d4` (session.go:258), not the timestamp prefix.
- **git** — current branch + dirty marker; collapses outside a git repository. It is the one segment whose data genie doesn't hold in-process — its *existence* is settled here, its *gathering* is og-1i1.8.
- **time** — the wall clock.
- **tokens** — the running total of the session's **completed** turns' provider-reported `TotalTokens` (best-effort accounting, `EventUsage`; the in-flight turn is not counted mid-draw), formatted human-compact (`12k`, `1.2M`). Renders `0` before the first completed turn.
- **prompt_char** — the terminal prompt glyph the cursor lands after; it carries the **exit-status state** (ok / error / cancelled) so a theme can colour it per outcome, p10k-style. Exit status is a *state* of this segment, not a segment in its own right.

Cut from the candidates: **host** and **user** (no signal in a single-user dev CLI); a **brand/identity segment** (no fixed "genie" literal — the prompt is fully segment-driven, and recognisability comes from `dir` + `prompt_char`; fixed content was the one thing escaping theme control, so it's gone).

## Content-generator contract

- Genie owns one content generator per segment. A generator takes the run's resident data plus the segment's option map and returns **plain text** (empty allowed). Generators never emit styling or glyphs: glyphs are theme data selected by glyph tier (og-1i1.7), styles flow through the role mapping (og-1i1.4, og-1i1.6).
- The prompt renders only at **idle**, between turns — there is no in-turn prompt.
- Genre-resident data at draw time: dir/provider/model/agent/session from the run handle; tokens from the provider's per-turn `EventUsage`; time from the clock; git needs external gathering (og-1i1.8).
- **Per-segment options** are a small whitelisted key set per segment (e.g. `time.format`, `dir.max_len`, `session.len`, `always`) — enough for p10k-style tweaks without handing over the generator. Unknown option keys fail fast, matching the house fail-fast style.

## Layout and separators

- A layout is a **flat, ordered list** of segment entries on **one line**; a right-side statusline is out of scope.
- **Empty content collapses the segment** and its adjacent separators with it; a rendered bar never shows a leading/trailing orphan or a double separator. The `always` option forces a slot to render even when empty.
- **Separators** are theme data: a theme-level default with per-segment overrides, rendered only *between* two rendered segments. Whitespace is itself a separator type (the **gap**), which is how Lean gets its spacing.
- **Per-segment style keys** are declared on the segment entry, optional: an entry may name a style for the segment; omitted, the segment falls back to its builtin default role. Which *namespace* those keys draw from — shared roles vs segment-scoped states — is og-1i1.6's question.
- The exact TOML encoding of the ordered list, options, separators, and threshold keys is og-1i1.3's config schema area; this ADR fixes their shape.

## Cursor-below rule (segment overflow)

When the rendered segment bar's display width would exceed a **threshold** of the terminal width, render the bar, then a newline, and put `prompt_char` (and the user's typing) on the fresh line below — the prompt never hides and never wraps the input area mid-typing. This is distinct from the out-of-scope transient prompt: the prompt stays visible, it just drops a line.

- The threshold is a theme-owned layout option `prompt.split_threshold`, default **50%** of the terminal width.
- The bar always splits when it exceeds 100% of the terminal width (wrap is otherwise forced).
- The rule applies only when the render profile reports a known terminal width (interactive TTY); with no width, the prompt is always single-line.

## Builtin preset layouts

- **Classic** (powerline, background-filled chips welded by angled separators; ADR-0012 fixes the chip posture and connector painting), left to right:
  `dir` · `git` · `provider` · `model` · `agent` · `time` · `tokens` · `prompt_char`
  — e.g. `~ on main  zen  big-pickle  12:04:31  12k  ❯`, where each segment is a chip on the terminal background, the `` connectors are painted with the preceding chip's colour tapering into the following chip's fill, and same-band segments weld invisibly (see ADR-0012's re-derived Classic defaults).
- **Lean** (gap-separated, no connectors):
  `dir` · `git` · `agent` · `prompt_char`
  — e.g. `~ main ❯`; provider/model/time/tokens are left out by default.

## Consequences

- `run.Handle` grows two pieces of soon-needed resident data: a **tokens accumulator** (sum of completed turns' `EventUsage`) and whatever the prompt draw needs for git. Display-width measurement of the rendered bar (for the split rule) lands in `internal/style` alongside the render-profile machinery (og-1i1.7).
- Classic and Lean prove the config generalizes: the same segment vocabulary, two different separators/thresholds/rosters.
- og-1i1.6 now decides the style namespace with `prompt_char`'s ok/error/cancelled states as the template case; og-1i1.8 is live because `git` survived the vocabulary.
- A future theme cannot reword genie's segments (content is genie-owned), cannot add segments (closed vocabulary), and cannot override the fixed pieces (nothing fixed remains).