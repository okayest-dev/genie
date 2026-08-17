# 01 — Slog infrastructure

**What to build:** two CLI flags (`-v` verbose, `-d` debug) and one env var (`OG_DEBUG`) that control structured diagnostic output via `log/slog` to stderr. Slog is configured once in `main.go`, early — before config loading, instruction assembly, or any other work. The handler is `slog.NewTextHandler` writing to `os.Stderr`, with `ReplaceAttr` stripping the `time` key to remove timestamps. The log level is set based on flag/env precedence: no flags/env → `slog.LevelWarn` (silent); `-v` only → `slog.LevelInfo`; `-d` or `OG_DEBUG=true` → `slog.LevelDebug` (debug implies verbose). `OG_DEBUG` is parsed before `config.Load()` using truthy string comparison (`"true"`, `"1"`, `"yes"`); `-d` takes precedence — either one being active enables debug. A one-line banner confirms the flag took effect before any other output appears. Usage text updated to document both flags and the `OG_DEBUG` env var. Neither flag affects stdout, exit codes, or the model's reply.

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] `-v` and `-d` bool flags added to the existing `flag.NewFlagSet` in `main.go`
- [ ] `OG_DEBUG` env var parsed before `config.Load()` with truthy comparison (`"true"`, `"1"`, `"yes"`)
- [ ] `log/slog` configured with `slog.NewTextHandler` to `os.Stderr`, timestamps stripped via `ReplaceAttr`
- [ ] Level selection: no flags → `LevelWarn`; `-v` only → `LevelInfo`; `-d` or `OG_DEBUG` active → `LevelDebug`
- [ ] Startup banner: verbose emits `level=INFO msg="verbose mode enabled"`, debug emits `level=DEBUG msg="debug mode enabled"`
- [ ] Usage string updated to document `-v`, `-d`, and `OG_DEBUG`
- [ ] Stdout unaffected by flags; exit codes unchanged
- [ ] E2e test: stderr is empty when neither `-v` nor `-d` is passed
