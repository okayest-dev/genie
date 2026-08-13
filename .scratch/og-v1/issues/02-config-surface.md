# 02 — Config surface

**What to build:** all of v1 configuration. A TOML config file at the user config dir with precedence **defaults < file < env** across six env overrides (`OG_MODEL`, `OG_BASE_URL`, `OG_API_KEY_ENV`, `OG_INSTRUCTION_FILE`, `OG_SESSION_DIR`, `OG_BASH_TIMEOUT`), a `[tools]` table of four booleans, an API key that lives only in the env var named by config, and fail-fast on malformed TOML and unknown keys. Users set the model, provider base URL, api-key env name, instruction file, session dir, bash timeout, and per-tool toggles — each overridable per invocation via env.

**Blocked by:** 01 — Wire tracer bullet.

**Status:** ready-for-agent

- [ ] A missing config file means pure defaults; the harness runs unconfigured.
- [ ] Every env override beats the config file, which beats defaults.
- [ ] Malformed TOML and unknown keys fail fast at startup with a clear message.
- [ ] The API key is never read from the config file — only from the env var named by the api-key setting (default `OPENCODE_API_KEY`).
- [ ] `[tools]` parses four booleans defaulting to true; disabled tools are omittable from the tools array.
