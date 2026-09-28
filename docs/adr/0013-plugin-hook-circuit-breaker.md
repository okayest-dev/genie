# Plugin hook failures are cut out per (plugin, event), not warned about per occurrence

Status: accepted

Genie degrades a failing plugin hook: the hook is skipped, earlier hooks' contributions are kept, the turn proceeds. That is the right behaviour for a hook that fails once, and the wrong behaviour for a hook that fails every call. `response_ready` fires per streaming delta, so a hook that errors on every delta printed a warning per delta — thousands of lines that buried the session and taught the user nothing after the first. Each `(plugin, event)` pair now has a circuit breaker: the first `plugins.hook_failure_threshold` consecutive failures (default 3) degrade one at a time as before, the failure that crosses the threshold trips the breaker with a single notice, and the event stops being called until `plugins.hook_recovery_seconds` (default 60) has passed. One trial call goes through after the cooldown; success restores the event with a notice, failure re-trips it. The policy is per plugin-process and lives in memory, so it is not persisted across sessions.

## Considered options

- **Warn always, never cut out.** Rejected. It is the status quo and it is the failure mode: a permanently broken hook has a cost (noise, latency) and no benefit, and "never cut out" makes the operator read every one of those lines to find that one. It also loses the information the notice carries — which plugin, which event, and which config key governs it.
- **One breaker per plugin.** Rejected. A plugin's legs are independent capabilities; a `response_ready` bug says nothing about `tool_before`, and disarming a whole plugin because one hook misbehaves punishes the working legs for a defect they do not share.
- **Trip on total failures across all events.** Rejected for the same reason, with the extra confusion that the counter would mix unrelated failures, so a plugin could trip `tool_before` for a fault in `response_ready`.
- **Exponential backoff with repeated trials.** Rejected for now. Backoff needs a cap, a jitter story, and a notice per attempt; the notice is the expensive part. A fixed cooldown is one number to document and one number to tune, and it is enough to keep a broken hook from costing anything per turn.
- **Making the timeout or the crash path feed the hook-failure counter.** Rejected. Those are liveness failures, not hook-behaviour failures, and they are already terminal for the plugin: an inactive plugin is excluded everywhere and reported once. Counting them would mean a plugin that crashes on ping can never be tripped, or worse, can trip for a reason the user cannot act on.
- **Resetting the counter on a `"fatal": true` result.** Rejected. A fatal declaration is the plugin asserting policy, not breakage. Letting it reset would hand a plugin a way to keep a broken hook called forever.

## Consequences

- State lives on `*Plugin` under a dedicated mutex, not on either seam, so the lifecycle and context seams share one verdict for a given `(plugin, event)`. The policy and the notice sink are installed once, on the manager (`WithHookBreaker`), for the same reason.
- Config sits under `[plugins]` rather than `[lifecycle]`: the breaker governs context hooks too, and the key governs hook health in general. `hook_failure_threshold = 0` disables the breaker; `hook_recovery_seconds = 0` is a process-lifetime one-way door. Both reject negatives.
- A tripped `active_compact` or `active_condense` plugin hands the job to Genie's built-in for the cooldown. The alternative — no compaction while the hook is out — is not a degradation, it is an unmanaged context window, which is how a session runs out of budget mid-turn.
- A tripped plugin is still Active: its tools, commands, and other hooks work. That is deliberate (a fault in one leg should not disarm the plugin) and it creates a reporting obligation, so `/help` labels the plugin block with the events it is out of. `CommandSource` gains `TrippedHooks` for exactly this.
- Plugin authors must treat a dropped contribution as recoverable. A trip can discard a `response_ready` hook's work mid-stream, and a single trial call after the cooldown is the only call that event gets in that window.
