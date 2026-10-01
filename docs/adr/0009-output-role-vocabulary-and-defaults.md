# Output role vocabulary and default styles for themable output

Status: accepted. The decisions below are the wayfinder map og-1i1's role/style decision area (ticket og-1i1.4 "Decision: Output role vocabulary and defaults", building on the render-profile model of ADR-0006). They fix genie's closed semantic-role vocabulary for the human-facing surface, each role's default style, the fallback a theme leaves unmapped, and the mechanics of mapping role names to styles. They do not fix the *prompt segment* style namespace — whether segment states join this vocabulary is og-1i1.6's question — nor the theme file's TOML encoding or a theme's `[roles.*]` table key names, which remain schema detail for the theme spec.

## Scope: what the vocabulary covers

The vocabulary classifies **surface** — all human-facing terminal output — one role per output site, one site per role. Three boundaries:

- **Model-facing composites are never themed** (the map's out-of-scope ruling, matching ADR-0005): `Permission granted: …`, `status: call not executed`, tool results, the permissions snapshot — they feed the model, not a human.
- **The idle prompt bar is out** — it is the segment bar, styled per-segment through segment style keys (ADR-0007; the namespace is og-1i1.6), not through this output-site vocabulary.
- **Plugin subprocess stderr passthrough** (`internal/plugin/manager.go:203`) is a plugin's own surface, not genie output — genie cannot style bytes it relays verbatim, and it carries no role.

## The closed vocabulary: eleven roles

Genie owns this closed set; themes map its names and cannot add to it.

| Role | Output site | Current sites (pre-styling references) |
|---|---|---|
| `model` | streamed reply text + trailing newline; the `-p` reply to stdout | internal/agent/agent.go:221,251 |
| `tool` | tool-call frame `── name args ──` | internal/agent/agent.go:301 |
| `error` | every `Error: %v` report (startup, REPL, slash, plugin marshal), `genie: …` user errors, flag-parse errors | cmd/genie/main.go, internal/repl/repl.go |
| `warning` | `warning: …` lines and the degradation-sink messages (`context degraded:`, `lifecycle degraded:`) | cmd/genie/main.go:172,221; internal/run/run.go:195 |
| `turn_cancelled` | the `[turn cancelled]` notice | internal/repl/repl.go:174 |
| `banner` | the session line `session: <id>` | internal/run/run.go:274,530 |
| `log` | all slog text lines on stderr — the verbose/debug (`-v`/`-d`) surface, every level | configured sink, cmd/genie/main.go |
| `prompt` | the permission/escalation dialog only | internal/repl/permission.go:121 |
| `pick` | the provider-pick startup dialog (list, prompt, re-prompt notices) | cmd/genie/startup.go:50-80 |
| `slash` | slash-command output — `/help`, `/provider`, `/model`, `/agent`, `/changes`, unknown-command, success lines — plus the flag `usage` help | internal/repl/repl.go |
| `plugin` | plugin command text, Data-JSON fallback, plugin help/list output | internal/repl/repl.go:415-453 |

Grouping calls recorded: the **degradation sink folds into `warning`** (insurance against an unbounded role explosion — degradation is a non-fatal warning and gets its own role only if a future product change earns it); the **flag `usage` help folds into `slash`**; the **provider-pick dialog is a distinct `pick` role** — deliberately not the same role as the security-critical escalation `prompt`.

**Deliberately excluded from the vocabulary:** the prompt bar's *style* (segment domain, og-1i1.6) and its bare newlines, the Exit newlines (bare `\n` on ^C/EOF at idle, no content), and plugin passthrough stderr (above).

## Role → style mechanics

A role maps to the style shape ADR-0006 fixed — **colour + SGR attributes + optional per-tier glyph**. The ticket's "colour only, or colour + attributes + glyphs?" resolves to the latter:

- **Colour**: a single value per role, downconverted by the render profile (ADR-0006); there is no per-role colour ladder.
- **Attributes**: a whitelist of `bold`, `dim`, `italic`, `underline` — no blink, no hidden, no reverse. Non-colour attributes survive NO_COLOR (ADR-0006).
- **Glyphs**: optional per-glyph-tier entries, selected by the render profile's glyph tier; mostly unused by the output-site roles in Classic (glyphs are the segment bar's domain), but a role *may* carry one.
- A role may declare colour-only, attribute-only, or both (an attribute-only role is valid).

The exact `[roles.*]` table key names are theme-spec schema detail, not fixed here.

## Fallback for an unmapped role

A role a theme leaves unmapped renders in the **plain terminal style** — default fg/bg, no attributes, no glyph: the empty style. There is no theme-level default style key. Partial themes are safe by construction — a theme that restyles only the prompt leaves the other ten roles plain — and a theme that wants a base maps the small, closed set explicitly.

## Classic preset defaults

Classic maps every role; this table is the proof that a real theme is expressible and the default look users get. It is a deliberate **ladder of salience**: content plain, harness chatter muted/tinted, problems coloured, the security dialog bold.

| Role | Classic default |
|---|---|
| `model` | plain (terminal default) |
| `tool` | cyan fg |
| `error` | red fg |
| `warning` | yellow fg |
| `turn_cancelled` | dim |
| `banner` | dim |
| `log` | grey fg (downconverts cleanly to bright-black on ANSI) |
| `prompt` | bold terminal default |
| `pick` | plain |
| `slash` | plain |
| `plugin` | plain |

This satisfies the ticket's shape test: log output greyer than model IO, errors visually distinct, warnings distinguishable from errors, and the escalation prompt loud enough to read as a security surface. The three plain roles (`model`, `pick`, `slash`) sit at the terminal default so default output stays byte-identical to today's plain text; themes open them.

## Consequences

- The closed vocabulary fixes the names themes address; `log` stays one role for all slog levels — the level token is already in the text handler's output, and a per-level split would duplicate the vocabulary for the least-styled surface. If level-differentiation ever matters, the per-state carving happens in og-1i1.6's namespace, not here.
- og-1i1.6 now decides whether segment states (dir anchor, git dirty, `prompt_char` ok/error/cancelled) share this vocabulary or form a second namespace; the seam is that this vocabulary is fully the non-prompt surface.
- The theme spec (a later effort, outside this map) receives the role set, the mapping contract, the fallback rule, and the Classic defaults as fixed inputs; only TOML encoding and key naming remain open.