# Background colour for powerline connectors in the style shape

Status: accepted. The decisions below are the wayfinder map og-1i1's style-shape decision area (ticket og-1i1.9 "Decision: Background colour for powerline connectors in the style shape", building on the render-profile model of ADR-0006, the prompt layout of ADR-0007, the role vocabulary of ADR-0009, and the style namespace of ADR-0010). They fix whether a style carries an optional background colour, how the powerline connector between prompt segments is painted, and how hard Classic's defaults lean on background-filled chips. They do not fix the theme file's TOML encoding of a background key or the final Classic chip palette (theme-spec schema and styling detail, per ADR-0008).

## Why the connector needed a background

ADR-0006's style shape is a foreground colour + SGR attributes + optional per-tier glyphs; ADR-0007's Classic preset separates its segments with the powerline `\ue0b0`. In the p10k rendering model a segment is painted *fg on bg* (a **chip**), and the `\ue0b0` between two segments is drawn with **foreground = the left segment's background** and **background = the right segment's background** — the triangle reads as the left chip's colour tapering into the right chip's fill. A foreground-only shape leaves no background to derive, so the connector floats as a disconnected outline on the terminal background. This decision widens the shape so the connected-chip look is expressible, then fixes how the connector uses it.

## The style shape gains an optional background colour

ADR-0006's shape becomes **foreground (optional) + optional background + SGR attributes + optional per-tier glyphs**, uniformly across both style namespaces (ADR-0009 roles, ADR-0010 states) and segment bases:

- **One background value per style**, downconverted through the same colour ladder as the foreground — no per-profile backgrounds, mirroring the single-colour rule of ADR-0006. The renderer's two jobs are unchanged: select the glyph tier's entry, downconvert colours.
- **Absent background = terminal default (transparent).** An fg-only style renders byte-identical to today, and the plain fallback (unmapped role, unmapped segment, Classic's plain roles) stays default fg/bg with no attributes.
- **Background without foreground is valid** (terminal-default text on a filled chip), as are foreground alone and attribute-only styles; any subset of {fg, bg, attrs, per-tier glyph} is valid.
- **NO_COLOR / TERM=dumb / CI kill the background exactly as the foreground** — it is colour, and non-colour attributes survive (ADR-0006). The Ascii colour profile renders a background as nothing; no special case.
- The glossary term *style* is amended to match.

## Powerline connectors are strictly derived

A separator stays **glyph-only theme data** (ADR-0007: per-tier glyph entries, the whitespace gap, theme-level default with per-segment overrides) — it gains no colour slots. The renderer paints a connector between segments A and B with:

- **foreground = A's resolved background** — the connector's wedge is the chip it follows, tapering into the next;
- **background = B's resolved background** — it sits on the chip it precedes;

where "resolved" means through the segment's state mapping and fallback chain (ADR-0010: unmapped state → its segment's base; unmapped segment → plain). A plain segment carries no background, so a neighbour without one drops that channel to terminal default. Consequences:

- The connected look appears automatically wherever both neighbours carry backgrounds — no per-tie authoring; today's floating outline is exactly the fallback when either side has none. Nothing special-cased, nothing guessed.
- Adjacent segments sharing a background render an **invisible connector** (fg equals bg), which welds same-band segments into one continuous group.
- The painting rule is **tier-independent**: the effective tier's glyph (`\ue0b0`, ascii `>`, …) gets the same derived two colours. The renderer never judges whether a glyph "looks like a wedge" (ADR-0006's never-guesses rule). At ascii tier a `>` between two chips renders as a small chevron in the left chip's colour on the right chip's fill — deliberate breadcrumbs, not a degraded wedge.
- The derived rule belongs to connectors alone: a role/state style's own optional glyph (ADR-0006) paints as ordinary span text inside that style. Lean is untouched — its whitespace separators carry no connector.

## Classic is the chip showcase

Classic becomes the connected p10k look: **every prompt segment is a background-filled chip** (text tint on a per-segment, per-state background), so the powerline tier renders one connected bar and the ascii tier a breadcrumb bar. The calm argument is carried by the two-preset split, not by nerfing Classic: **Lean** stays the quiet default alternative (gap-separated, no connectors), and its defaults are unchanged.

The chip posture stops at the prompt bar. Classic's eleven **role** defaults (ADR-0009) remain fg/attr-only — the salience ladder and the byte-identical plain roles stand. Roles and states gain the *capability* (any style may name a background); Classic's defaults simply do not use it outside the prompt.

## ADR-0010 amendment: Classic state defaults become chips

ADR-0010's Classic default table is re-derived from fg-only to fg-on-chip values. The table below is **illustrative** — pinned here to keep this decision self-consistent; the theme spec owns final styling polish (ADR-0008). The sketch is two welded bands plus per-state `prompt_char` chips:

| Segment | States | Classic default (chip) |
|---|---|---|
| `dir` | anchored · shortened | location band: green fg · dim fg |
| `git` | clean · dirty · unknown | location band: base fg · yellow fg · dim fg |
| `provider` · `model` · `agent` · `session` · `time` · `tokens` | (none) | neutral band: base fg |
| `prompt_char` | ok · error · cancelled | ok = neutral chip `❯` · error = red chip · cancelled = dim chip |

`dir` and `git` share the **location band**, so the invisible-connector rule welds them into one group; the six information segments share the **neutral band**; visible wedges land only at band transitions; `prompt_char` carries its own per-state chips so exit status pops as a block. The bar's first rendered segment needs no leading edge and `prompt_char` is its end (ADR-0007 has no end-cap or status line). ADR-0007's Classic example line is rewritten accordingly.

## Considered options

- **Declared connector styles** (separators carry their own fg/bg) — rejected: hand-wires chip adjacency into every theme, breaks the connected illusion at the first authoring slip, and turns Classic's defaults into a chore rather than a consequence of segment styles.
- **Derived with a fallback slot** (connector may name an fg for the outline case) — rejected: always-derive already produces the floating outline for free when a neighbour has no background; a theme's lever is simply not to paint that segment.
- **Classic without chips** (keep fg-only defaults) — rejected: at powerline tier `\ue0b0` would float disconnected — the exact broken look this decision exists to fix — or Classic would have to quietly abandon powerline separators and become Lean. The calm-default case justifies itself as Lean, which frees Classic to be the showcase.
- **Tier-dependent painting** (derive colours only for powerline/nerd glyphs, print ascii `>` plainly) — rejected: forces the renderer to judge glyph shape, breaks ADR-0006's uniform rule, and pushes a second, inconsistent connector vocabulary into the theme spec.
- **Chipping Classic's output roles** (error as a red block, …) — rejected for the default: the non-prompt surface's calm salience ladder is settled (ADR-0009); the capability stays available to themes.

## Consequences

- `internal/style` gains background emission and a background downconvert leg in its SGR emitter (16-colour backgrounds are the 40–47 / 100–107 codes; same ladder otherwise). Chip rendering rides the existing TTY/glyph machinery with no layout or width change — SGR only, so the cursor-below split rule measures the same display width (ADR-0007).
- ADR-0006's shape section, ADR-0007's Classic example line, and ADR-0010's Classic default table are amended by this ADR; the glossary *style* term is updated.
- The theme spec receives: optional background in the style shape (the bg key name is schema detail), the derived connector rule, separators staying glyph-only, Classic's chip posture with the illustrative palette (band/state values are the spec's to finalise), and the tier-independent painting rule.
- Chips degrade gracefully under NO_COLOR and the Ascii colour profile (plain text), and an unmapped or plain style is indistinguishable from today — the default palette is a Classic concern, not a risk to partial themes.