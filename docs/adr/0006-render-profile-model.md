# Render profile model for themable output

Status: accepted. The decisions below are the wayfinder map og-1i1's render-profile decision area (tickets og-1i1.7 "Decision: Render profile — colour depth, glyph tiers, NO_COLOR" and the research it builds on, og-1i1.2 "Research: Terminal detection for the render profile", findings at `research/terminal-capability-detection` @ 6c60739). It records the terminal *capability* decisions so a later theme spec can be written around them; it does not ship code or finalize the config schema (that is og-1i1.3's decision area).

## Terminology

"capability" is retired from the rendering vocabulary: genie already has a first-class plugin capability registry (plugin manifests declaring `capabilities = ["tools", "providers", "commands", …]`, advertised over `capabilities/list`). The terminal sense is now **render profile** — the bare word is meaningless on its own and both senses are named compounds:

- **plugin capability** — the existing registry sense.
- **render profile** — the probed, per-process snapshot of what an output stream can faithfully display.

## What a render profile is

A per-writer, per-process static snapshot: **TTY-ness** (on/off), **colour profile** (the ladder Ascii → ANSI/16 → ANSI256 → TrueColor), and **glyph tier** (nerd / powerline / ascii). Probed once at startup and cached for the process life; **stdout and stderr probed independently** — error and status lines can reach a tty when stdout is piped, and the two writers then render differently.

**Styling-library strategy:** hand-rolled, matching genie's std-lib-first profile. A new `internal/style` package owns probing and emission (~150 lines: TTY ioctl, env colour ladder, NO_COLOR, SGR emitter, glyph mapping). The single new dependency is `golang.org/x/term` for exact TTY detection (one transitive dep, `golang.org/x/sys`). No termenv / lipgloss / fatih/color: termenv and x/term are the only Go-1.24-compatible options; lipgloss v2 (go 1.26.7) and fatih/color (go 1.25.0) exceed the module toolchain, and lipgloss's layout engine is overkill for themed output. genie's `go.sum` currently has no `x/sys`; this is the first new dependency since the module settled to toml + omnitoken.

## Colour depth

- **Detection is environmental, never queried.** The ladder is estimated from `COLORTERM`/`TERM` names/`WT_SESSION`/`ConEmuANSI`/`GOOGLE_CLOUD_SHELL`. No `DA1`/`OSC` queries: DA1 reports colour yes/no at best (never depth), requires raw mode and stdin read-back, and is unusable in pipelines; OSC 10/11 theme queries are at most an optional interactive enhancement and are out of scope.
- **One colour per style, renderer degrades.** Theme files declare one colour per style; the renderer downsamples through the ladder with a small internal downconverter (6×6×6 cube + grayscale ramp → 256, nearest-colour → 16). The p10k-style alternative — per-profile "complete colour" entries per style — is rejected: it is onerous in user-authored TOML (three colours per role across every role). **ADR-0012 widens this shape: a style carries a foreground and an optional background, each a single value downconverted the same way.**
- **No minimum-profile declaration.** Themes never declare a minimum colour profile and nothing refuses to render; a theme degrades gracefully on every terminal. Running under tmux, `screen`, or `TERM=dumb` simply drops fidelity, never errors.
- **Ascii profile** = no colour, no glyphs, ASCII separator fallbacks; genie's framing text (prompt, labels, help) is unchanged.

## Colour conventions

- **NO_COLOR** (present, non-empty), **TERM=dumb**, and **non-empty CI**: colour off. Per no-color.org's settled convention, NO_COLOR kills colour **only** — non-colour SGR attributes (bold, italic, underline) survive, keeping model text readable.
- **No FORCE_COLOR / CLICOLOR_FORCE override.** Theme selection is config-time only; a pipeline that wants colour has no force path (`genie -p | less -R` renders plain). CLICOLOR is not implemented.

## Glyph tiers

- **Selection is explicit config.** Font detection is categorically impossible from a program, so the **`glyph_tier`** key (nerd / powerline / ascii) is an explicit config choice, defaulting to **ascii** — the safe value on unpatched fonts. A theme still renders with its ascii fallbacks when the tier is ascii (Classic's powerline separators become ASCII).
- **Where the key sits in config** (top-level vs table) is settled by og-1i1.3's config-format decision; the render-profile model only fixes that it is explicit and defaults to ascii.
- **A first-run wizard** (the p10k "does this look like a diamond?" confirmation pattern) is in scope for the theme spec that follows this map — not for this ADR, which fixes the mechanism: an explicit key a wizard may later populate.

## Themes vs renderer: who picks what

Themes declare **per-tier glyph entries** for any style that carries a glyph (e.g. a separator: `nerd = "", powerline = "\ue0b0", ascii = ">"`). The renderer selects the entry for the effective glyph tier and never guesses a fallback; a style that omits a tier falls back to its ascii entry. Colours, by contrast, are a single value per style that the renderer downconverts (above) — the renderer's only two jobs are selecting (glyph tier) and converting (colour ladder), both deterministic.

## Consequences

- New package `internal/style`: probe (TTY via `x/term`, env ladder, NO_COLOR, glyph tier from config) + SGR emitter + downconverter + glyph selection. A render profile is resolved per writer (stdout, stderr) once per process.
- `golang.org/x/term` (+ transitive `golang.org/x/sys`) joins `go.mod` — the first new dependency since toml/omnitoken settled.
- "styled output off when not a terminal" is the default posture everywhere: pipes and redirects receive plain framing; no force escape hatch.
- Role → style defaults, the prompt segment/layout model, and the theme config schema are separate decision areas (og-1i1.4, og-1i1.5, og-1i1.3); the render profile is the capability side those themes degrade against.