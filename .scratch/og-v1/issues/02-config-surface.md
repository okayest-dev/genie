# 02 — Config surface

**What to build:** all of v1 configuration. A TOML config file at the user config dir with precedence **defaults < file < env** across six env overrides (`OG_MODEL`, `OG_BASE_URL`, `OG_API_KEY_ENV`, `OG_INSTRUCTION_FILE`, `OG_SESSION_DIR`, `OG_BASH_TIMEOUT`), a `[tools]` table of four booleans, an API key that lives only in the env var named by config, and fail-fast on malformed TOML and unknown keys. Users set the model, provider base URL, api-key env name, instruction file, session dir, bash timeout, and per-tool toggles — each overridable per invocation via env.

**Blocked by:** 01 — Wire tracer bullet.

**Status:** resolved

- [x] A missing config file means pure defaults; the harness runs unconfigured.
- [x] Every env override beats the config file, which beats defaults.
- [x] Malformed TOML and unknown keys fail fast at startup with a clear message.
- [x] The API key is never read from the config file — only from the env var named by the api-key setting (default `OPENCODE_API_KEY`).
- [x] `[tools]` parses four booleans defaulting to true; disabled tools are omittable from the tools array.

## Resolution

Implemented with the pre-agreed seams (unit tests on config parsing + env-override resolution; the black-box binary seam).

- **New module `internal/config`**: `Parse(file []byte, userConfigDir string, env map[string]string)` is the pure resolution helper — precedence defaults < file < env. `Load()` reads `os.UserConfigDir()/og/config.toml` (missing = pure defaults) and resolves from the process env.
- **Schema** (via `BurntSushi/toml`, the spec's one runtime dep): six scalars — `model`=`big-pickle`, `base_url`=`https://opencode.ai/zen/v1`, `api_key_env`=`OPENCODE_API_KEY`, `instruction_file` (unset), `session_dir`=`<UserConfigDir>/og/sessions`, `bash_timeout`=`120` seconds (a non-positive value is a fail-fast error, from file or env) — plus a `[tools]` table of four booleans (read/write/edit/bash) defaulting to true; tool fields are `*bool` so an omitted key keeps the default.
- **Six env overrides** `OG_MODEL`, `OG_BASE_URL`, `OG_API_KEY_ENV`, `OG_INSTRUCTION_FILE`, `OG_SESSION_DIR`, `OG_BASH_TIMEOUT` (set-but-empty env leaves the file value); a non-numeric or non-positive `OG_BASH_TIMEOUT` fails fast.
- **API key**: never a config key — an `api_key` key is an unknown-key error. `APIKey` is resolved after precedence resolution from the env var named by `api_key_env`; empty when the var is absent (no Authorization header).
- **Fail fast**: malformed TOML and unknown keys (via `md.Undecoded()`, error names the offending key(s)) surface as `Error: config: ...` with a non-zero exit — never a stack trace.
- **Wired into the binary**: `cmd/og` now loads config via `config.Load()` and drives the client from it (replaces the tracer bullet's direct env reads); config failure exits 1.
- **Tests**: unit — defaults, file-beats-defaults, every env override beating the file, api-key resolution (default + custom `api_key_env`, never from file), malformed TOML, unknown top-level/`[tools]`/scalar keys, partial `[tools]` tables, invalid `OG_BASH_TIMEOUT`, empty-env-var semantics. e2e through the compiled binary — config-file drives the wire request (model + Bearer key), env beats file, missing file → `big-pickle` default with no auth, malformed TOML and unknown keys fail fast cleanly, and a custom `api_key_env` names the key's env var. e2e controls the config location via `XDG_CONFIG_HOME`.
