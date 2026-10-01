# Style namespace for segment states and output roles

Status: accepted. The decisions below are the wayfinder map og-1i1's style-namespace decision area (ticket og-1i1.6 "Decision: Style namespace — roles vs segment states", building on the role vocabulary of ADR-0009 and the segment model of ADR-0007). They fix whether prompt-segment states (dir anchored/shortened, git clean/dirty, prompt_char ok/error/cancelled) share the closed role vocabulary or form a second, segment-scoped namespace; the dividing rule between a role and a segment state; the fallback chain for unmapped segments and states; which segments carry states and Classic's defaults for them; and the style shape a state maps to. They do not fix the theme file's TOML encoding of the segment/state tables (theme-spec schema detail, per ADR-0008), the style shape's possible background colour (og-1i1.9), nor how git computes dirty or dir decides anchored/shortened (content contracts, og-1i1.8 and ADR-0007).

## Two namespaces, not one

A theme addresses two closed, genie-owned vocabularies of styleable names, and they never merge:

- **roles** — the eleven output-site classifications of ADR-0009 (`model`, `tool`, `error`, `warning`, `turn_cancelled`, `banner`, `log`, `prompt`, `pick`, `slash`, `plugin`), a closed set surjective onto the non-prompt human-facing surface.
- **segment states** — a named visual variation of one prompt segment's own content, reachable only through the segment that owns it; a state cannot be styled without naming its segment and cannot stand alone.

**The dividing rule:** a *role* classifies an independent human-facing output site — one role per site, one site per role, and its identity survives any other style's absence. A *state* is a variation of an existing subject's rendered content; it exists only as a child of that subject, has no existence outside it, and never classifies a site. Roles answer "what kind of utterance is this?"; states answer "which variation of this segment's content is showing?"

A **unified** namespace was considered and rejected — one dictionary holding `error` (role), `error` (state), `dir.anchor`, `prompt_char.error` — for three reasons:

1. Folding states into the role table rereads "role" as "any styleable name," destroying the one-site-per-role semantics that make the role vocabulary a usable map of genie's output surface (ADR-0009).
2. The two kinds of name change on different rhythms: a new *role* means genie grew a new output site (rare, versioned); a new *state* means a segment's content contract gained a variation (per-segment, incidental). One dictionary conflates a stable site-map with per-segment detail.
3. Name collisions are unavoidable — `error` as an output role and `error` as a prompt_char state must be prefixed apart anyway (`prompt_char.error`), so the "unified" namespace is a namespaced one in disguise, with none of the separation's clarity.

Segment states live under the segment, structurally: each segment entry declares its base style and, optionally, its state styles. The exact TOML encoding (table nesting, key shapes) remains theme-spec schema detail — what is fixed here is that states are addressed *through* their segment and resolved separately from the role table.

## The state concept is segment-only

**Option 1, segmental scope:** states exist strictly on prompt segments. Output roles carry no states, and `log` stays a single role across all slog levels (ADR-0009's grouping call stands). ADR-0009 left a note that per-state carving for log levels would happen "in og-1i1.6's namespace"; that loop is now closed by refusing the generalization: if output-site variation ever becomes wanted (per-level log styling, say), it is a *new* carve in a fresh decision, not a case of this namespace. Defining "state" as "a variation of any styled subject" was rejected as speculative machinery that would invite a second vocabulary trap — the oh-my-posh "style means shape" mistake research og-1i1.1 warned about.

## The closed state set

Each segment's state set is closed and genie-owned, exactly as the segment vocabulary is; themes map state names and cannot add them. A segment emits **exactly one state per draw** — states are alternatives by construction, so a segment is never "anchored *and* shortened" and style composition across two states is never needed. When more than one condition could apply (a deep `$HOME` tail), precedence is content-contract detail of that segment's generator (ADR-0007), not a namespace concern.

| Segment | States | Classic defaults |
|---|---|---|
| `prompt_char` | `ok`, `error`, `cancelled` (ADR-0007) | ok = base · error = red fg · cancelled = dim |
| `dir` | `anchored` (at `$HOME`, renders `~`), `shortened` (tail-truncated) | anchored = green fg · shortened = dim |
| `git` | `clean`, `dirty` | clean = base · dirty = yellow fg |

The remaining six segments (`provider`, `model`, `agent`, `session`, `time`, `tokens`) carry no states today; a theme styles only each one's base. Unknown segment-state names fail fast, matching the house fail-fast style on unknown config keys (ADR-0008) and on unknown per-segment option keys (ADR-0007). How `git` computes dirty is og-1i1.8's gathering decision; this ADR fixes only that `clean` and `dirty` exist as the state set. **ADR-0011 amends git's set to `clean` / `dirty` / `unknown`** (the degraded gather's state; Classic default dim) — see that ADR.

## Fallback: the mirror of ADR-0009

- A **state** a theme leaves unmapped renders its **segment's base style** — the state inherits rather than falling to plain. A theme can colour every `prompt_char` with one base mapping and leave the exit-status states untouched.
- A **segment** a theme leaves unmapped (no base, no states) renders the **plain terminal style** — default fg/bg, no attributes, no glyph — exactly mirroring an unmapped role's fallback in ADR-0009, with no theme-level default style key.

Partial themes stay safe by construction in both directions: a theme that styles only the output roles leaves the bar plain; a theme that styles only the bar leaves the output roles plain. Classic, as the default theme, maps everything.

## Style shape for a state

A state maps to the same style shape ADR-0006 fixed for a role — **colour + SGR attributes + optional per-tier glyph** — rather than a restricted colour-only shape. Colour-only states were rejected because the p10k pattern the map was named for recolours *and* re-glyphs `prompt_char` per outcome (a theme may want `✗` for `error` against `❯` for `ok`), and Classic may want to recolour only. ADR-0007's rule that generators never emit styling or glyphs is untouched: a state's glyph is theme data, selected by glyph tier exactly like a role's glyph is (ADR-0006). **ADR-0012 widens this shape to include an optional background colour** (the same widened shape applies to roles and segment bases), turning each Classic segment into a background chip and re-deriving this table's Classic defaults — see that ADR.

## Consequences

- The theme spec (outside this map) receives: two closed namespaces, the segment-only scope of states, the closed per-segment state sets with Classic defaults, the state→base→plain fallback chain, fail-fast on unknown state names, and the full style shape — as fixed inputs. Only TOML encoding and key naming remain open there.
- **og-1i1.9** (child decision of this map) opens on the style *shape*: whether a style gains an optional background colour so powerline connectors (ADR-0007's Classic `\ue0b0`) can render as connected chips. It is a shape question that cross-cuts both namespaces — roles and states alike — not a namespace question, and so was split rather than resolved here.
- `git`'s state set is fixed without `og-1i1.8` being done: dirty-state *gathering* remains open on the map; the state *names* do not.
- `dir`'s and `prompt_char`'s generators must report which state they emitted (one per draw), which the segment content contract already implies; `git`'s generator reports `clean`/`dirty` once og-1i1.8 lands.
