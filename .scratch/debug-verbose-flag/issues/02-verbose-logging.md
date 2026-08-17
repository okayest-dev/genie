# 02 — Verbose logging (config + instruct + agent)

**What to build:** `slog.Info` calls in `config`, `instruct`, and `agent` packages that emit high-level flow information when verbose mode is active. Config loading logs the file path and whether it was found, the final resolved model, base URL, and which env var names were applied (not values). Instruction assembly logs the instruction file path (if set), whether AGENTS.md was found, and the total assembled instruction length. Agent turn logs the model name, provider base URL, and prompt length at turn start, and the finish reason plus token usage (prompt, completion, total) when the turn completes. All calls use `slog.Info` so they appear only when the slog level is `LevelInfo` or lower (i.e., `-v` or `-d` is active). Unit tests for each package verify the slog output at the correct level.

**Blocked by:** 01 — Slog infrastructure.

**Status:** ready-for-agent

- [ ] `config.Parse` logs resolved model, base URL, instruction file, session dir at info level
- [ ] `config.Parse` logs env var names applied (not values; API key env var name is logged, its value is not) at info level
- [ ] `config.Load` logs whether the config file was found or missing at info level
- [ ] `instruct.Load` logs instruction file path (if set), AGENTS.md found/not, and total instruction byte length at info level
- [ ] `agent.RunTurn` logs model name, provider base URL, prompt length at turn start at info level
- [ ] `agent.RunTurn` logs finish reason and token usage (prompt, completion, total) at turn completion at info level
- [ ] Unit tests for `config` package verify slog output when global level is `LevelInfo`
- [ ] Unit tests for `instruct` package verify slog output when global level is `LevelInfo`
- [ ] Unit tests for `agent` package verify slog output when global level is `LevelInfo`
