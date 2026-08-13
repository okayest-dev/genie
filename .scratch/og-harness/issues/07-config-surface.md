Type: grilling
Status: resolved
Blocked by:

# 07-config-surface

## Question

The exact v1 config surface: which keys are configurable (model id, provider base URL, api key env name, per-tool enable/disable, system prompt path, session dir, bash timeout), the config file location (`~/.config/og/`), file format (TOML via zero-dep BurntSushi/toml — Q7), env override precedence, and where sessions live on disk. Note the system-prompt answer (02) feeds in here.

## Answer

Decided by grilling over one round. TOML via BurntSushi/toml (map Notes). Six scalar keys + a `[tools]` table.

- **File location**: `os.UserConfigDir()/og/config.toml` (XDG-aware: `~/.config/og/` on Linux, `~/Library/Application Support/og` on macOS, `%AppData%\og` on Windows). No `--config` flag in v1 — one canonical location, shared by `-p` mode (08). Missing file → pure defaults, no error.
- **Precedence**: **defaults < file < env**. Six env overrides beat the file: `OG_MODEL`, `OG_BASE_URL`, `OG_API_KEY_ENV`, `OG_INSTRUCTION_FILE`, `OG_SESSION_DIR`, `OG_BASH_TIMEOUT`. Tool toggles are file-only.
- **API key**: never in the file. Always read from the env var named by `api_key_env` (default `OPENCODE_API_KEY`, per 01).
- **Session dir**: default `os.UserConfigDir()/og/sessions/`, key `session_dir` — hosts the session transcript and the `.changes.jsonl` ledger as siblings (04). Single root in v1; the XDG data-dir correction (`~/.local/share`) is a trivial later change.
- **Per-tool toggles**: `[tools]` table — `read`, `write`, `edit`, `bash` booleans, all default `true`. Disabled = omitted from the `tools` array (03). If a call to a disabled tool still arrives (stale context), execute nothing and return `Error: tool 'bash' is disabled` — the loop continues per 03.
- **Keys and defaults**:
  - `model` = `big-pickle` (01's verified free tool-use model)
  - `base_url` = `https://opencode.ai/zen/v1`
  - `api_key_env` = `OPENCODE_API_KEY`
  - `instruction_file` = unset (built-in default + cwd `AGENTS.md` only, per 02)
  - `session_dir` = `<UserConfigDir>/og/sessions`
  - `bash_timeout` = `120` (seconds, per 03)
- **Validation**: malformed TOML → **fail fast** at startup with a clear error; **unknown keys → error** (`DisallowUnknownFields` — a `bas_url` typo must not silently no-op). `model` is the per-session default; `/model` (05) overrides for the session and **never writes the file**.
