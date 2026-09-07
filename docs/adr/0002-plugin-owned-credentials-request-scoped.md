# Plugin-owned credentials served request-scoped (copilot wire plugin)

Status: accepted

The copilot wire plugin owns its authentication end-to-end rather than importing the shared Copilot `apps.json`: a host-keyed **credential store** at `$XDG_DATA_HOME/genie/copilot/credentials.json` (dir `0700`, file `0600`, atomic temp+rename writes) persists the durable GitHub OAuth credential record, while the short-lived Copilot JWT (the **derived token**) lives in memory only. Credentials are served request-scoped — per `wire/stream` the plugin reads the store and re-exchanges the derived token when it is absent or within `refresh_in − 60 s` of expiry — with no startup exchange and no background refresh ticker. An attended `login` / `logout` / `status` subcommand drives the device flow against the configured GitHub host (github.com or a `*.ghe.com` tenant), requesting `offline_access` and rotating when a refresh token is issued, treating GHE tokens as long-lived. Auth-class failures (HTTP 401/403 from the exchange or chat API) invalidate the stored record and fail fast with a typed auth error; re-login is user-invoked, never auto-triggered. The only harness change is preserving the JSON-RPC error code on the plugin wire path and mapping it to `llm.ProviderError{Kind: KindAuth}`.

We decided this because the shared `apps.json` was the original failure mode (a stale shared token breaking the session), the ecosystem's polite convention is a plugin-owned 0600 store, and a 25-minute artifact only matters while a turn is in flight — so a request-scoped cache is correct with fewer moving parts than a refresh ticker.

## Considered options

- **Re-reading the shared Copilot `apps.json` as the source of truth.** Rejected — a shared store's staleness is exactly the failure mode this design exists to remove.
- **Persisting the derived JWT with a proactive refresh ticker** (the copilot-api / privapps pattern). Rejected — a 25-minute token on disk invites cross-process staleness, and the plugin only needs a token during a request; request-scoped serving has no timer.
- **A native harness slash-command (`/copilot login`) via a new plugin command capability.** Rejected for P2 — genie has no command registry and plugins are never respawned; the standalone subcommand keeps the harness untouched, and the live plugin picks up fresh records by re-reading the store.

## Consequences

- The first exchange happens lazily on the first request, not at boot.
- A device-flow `login` launched while the plugin is alive is not observed until the next request — atomic renames make that hand-off race-free.
- The active host stays a plugin-side `config.toml` (`domain`) value; no harness config channel is added.
- The `ErrAuth` fail-fast seam (P3) needs the harness to stop discarding the JSON-RPC error code on the plugin wire path.