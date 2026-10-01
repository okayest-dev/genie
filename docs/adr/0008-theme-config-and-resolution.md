# Theme selection and resolution in config

Status: accepted. The decisions below are the wayfinder map og-1i1's theme config-format decision area (ticket og-1i1.3 "Decision: Theme config format and resolution", building on research og-1i1.1, findings at `research/theme-file-formats` @ 3f6cfd1). They fix the config *surface* for themable output — how the active theme is named, where user themes live, how env overrides the selection, and how a bad selection fails fast. They do not fix the theme file's internal schema (roles → styles → layout, versioning, encoding of per-segment options): layout shape is ADR-0007's, the render profile is ADR-0006's, and the role/style namespace is og-1i1.4/og-1i1.6's decision areas.

## Config surface

`theme` is a top-level scalar selector alongside `provider`:

```toml
theme = "classic"
```

`glyph_tier` (nerd / powerline / ascii, default ascii) is likewise a top-level scalar — ADR-0006 fixed its meaning and default; this ADR fixes its placement. There is no `[theme]` table: the two are independent singletons (one picks the theme, the other describes the *terminal's* font, which is a property of the terminal rather than the theme), and a table would bundle unrelated concerns behind a single-selection surface. Both stay compatible with the fail-fast unknown-key check.

Unset `theme` selects the **Classic** builtin preset — styled output is the default, never an opt-in.

## Value grammar — name-addressed, like plugin discovery

The theme value is a **bare name**; there is no path form. This mirrors plugin discovery (`internal/plugin/manager.go`): plugins are name-addressed (enable/disable lists name basenames, never paths) against a single config-dir-derived directory. A custom theme is provided by dropping `<name>.toml` into the user theme directory, not by pointing config at an arbitrary path — the same limitation plugins have, accepted for the same consistency.

## Resolution

`theme = "<name>"` resolves, in order:

1. **Builtin preset** — `classic`, `lean`, embedded in the binary via `embed.FS`, looked up by name as a closed set. Builtin names are reserved: a bare name matching a builtin always selects the builtin; a user theme file cannot shadow a builtin by name.
2. **User theme file** — `<themesDir>/<name>.toml`, where `themesDir` is `<userConfigDir>/genie/themes/` (derived from the config dir and honoring `GENIE_CONFIG_DIR`, like the session, plugin and skill directories; there is no separate `theme_dir` key).
3. Otherwise — **config load fails**.

A missing themes directory means "no user themes": an info log, mirroring `manager.go:93`. An unreadable themes directory is a fatal config error, like plugin discovery's dir read (`manager.go:96`).

## Fail-fast

House style is fail-fast on unknown config keys, so a `theme` value that resolves to nothing is a config error naming the value and what was tried — never a silent fallback to Classic. This is a deliberate difference from plugin discovery's tolerance just above: an enable-list name is a soft filter (an unknown name is skipped), but `theme` is a hard single selection, so an unresolvable name errors.

The two existing negative tests that pin `theme = "dark"` as an unknown *key* (`config_test.go:248`, `agent_test.go:264`) are updated: `theme` becomes a known key, `"dark"` becomes an unknown-*value* error, and the unknown-scalar negative test moves to a different key.

## GENIE_THEME

`GENIE_THEME` overlays the config-file value, following the `GENIE_PROVIDER` pattern exactly: it wins over the file, a set-but-empty value leaves the file value in place, it accepts the same bare-name grammar, and it is reported by name in the "env overrides applied" log line (the theme value itself never appears in logs). Precedence remains defaults < file < env.

## Builtin presets

Preset names are kebab-case — `classic`, `lean` — resolved by name from the embedded set, not from files on disk and never with an extension. Additional presets beyond the two (map og-1i1's "Not yet specified") join the closed set without a schema change.

## Consequences

- `internal/config` gains `theme` (string, default `"classic"`) and `glyph_tier`, plus `GENIE_THEME` and a load-time validation that resolves the active theme's source (builtin name or existing readable file) before boot.
- The top-level selector row grows from `provider` to `provider` + `theme` + `glyph_tier` — all singletons, all covered by the unknown-key check.
- The theme file's body schema (roles table, layout table, `version`, `$schema`) is untouched here; it belongs to og-1i1.4/og-1i1.5/og-1i1.6.