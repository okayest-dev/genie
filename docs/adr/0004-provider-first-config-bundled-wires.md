# Provider-first config over bundled in-process wires

Status: accepted. This is the decision set produced by the wayfinder map og-o7d (tickets og-o7d.1 through og-o7d.4) and the config-surface revision that followed the all-in-process call. A later implementation effort can build against it without further design decisions.

Genie today talks to one provider. Flat top-level keys set it up: `model`, `base_url`, `api_key_env`, `wire`, `gateway`, `provider` (`internal/config/config.go:112`). Four native wires live under `internal/llm/`, and the harness picks one with `DetectWire` by prefixing the model name (`main.go:176-179`). A subprocess plugin protocol also exposes wires (`wire/init`, `wire/list_models`, `wire/stream`) for plugins like copilot, and `runModel` defaults to a global model the plugin may not serve (`main.go:158-161`). That mismatch bug is the loose end this spec removes.

Research across opencode, pi, aider and goose (`research/provider-wire-harness-landscape.md`) pointed one way: provider as a config unit in the harness, wires as small in-process transforms parameterised per provider, mid-session switching as a slash command. The decision tickets confirmed it, then the user revised where the config lives once the wires were all in-process.

## Decision

### Providers are declared in genie's config.toml

Nested tables under a `providers` section. The table key is the provider name.

```toml
provider = "zen"                     # selector: the active provider; optional

[providers.zen]
wire        = "openai"
base_url    = "https://opencode.ai/zen/v1"
api_key_env = "OPENCODE_API_KEY"
model       = "big-pickle"           # the provider's default model; required

[providers.copilot]
wire  = "copilot"
model = "gpt-4o"
opts  = { ... }                      # wire-specific keys

[providers.openrouter]
wire       = "openai"
base_url   = "https://openrouter.ai/api/v1"
api_key_env = "OPENROUTER_API_KEY"
model      = "anthropic/claude-sonnet-4-5"   # aggregator: vendor-prefixed id on an openai wire
```

The per-provider keys are `wire`, `base_url`, `api_key_env`, `model`, `models`, `opts`:

- `wire` names the bundled in-process wire that serves the provider.
- `base_url` sets the provider's endpoint for that wire. `gateway` is gone; an aggregator is just a provider on an openai-compatible wire with vendor-prefixed model ids.
- `api_key_env` names the environment variable holding the key. A wire with its own auth (copilot and its credential store) takes no `api_key_env`.
- `model` is the provider's default model and is required. The active provider always has a model.
- `models` optionally overrides the catalog; absent, the catalog comes from the wire's model listing.
- `opts` passes wire-specific settings.

The top-level flat keys `model`, `base_url`, `wire`, `api_key_env` and `gateway` are removed. There is no global model. Existing configs break by design; the project is pre-release and accepted that. Precedence stays defaults < file < env. `GENIE_PROVIDER` survives; the env overrides for the removed flat keys (`GENIE_MODEL`, `GENIE_BASE_URL`, `GENIE_API_KEY_ENV`, `GENIE_WIRE`, `GENIE_GATEWAY`) go with them.

All valid providers ship in the config defaults: zen, a generic openai-compatible block, anthropic, responses, google, and copilot. Each carries whatever can be defaulted: a known base_url, the conventional `api_key_env` name, and a sane default model. Onboarding is pick a provider and set a key if you need to. This is a deliberate shift in priorities: ease of onboarding outranks flexibility.

A config key with exactly one valid value is hardcoded and not exposed. The schema does not define knobs for singleton-value settings, so a user cannot configure them wrong.

### Wires are bundled, in-process, and standalone

The four wires stay as Go packages under `internal/llm/`, and a copilot wire lands in-tree. A wire is the transform between genie's internal stream contract and a provider's API: it implements the `llm.Client` surface (`Stream`, `ListModels`) and optionally `ModelInfo`, and it is constructed from a provider's `base_url`, `api_key_env` and `opts`.

The subprocess wire seam is removed from the plugin protocol. The `wires` capability and the `wire/init`, `wire/list_models`, `wire/stream` methods die. Plugin-manager wire code, `cmd/genie/plugin_client.go`, `internal/llm/routing.go` and the plugin wire branch in `main.go:199-225` are deleted. Plugins keep the non-streaming seams: tools, lifecycle, skills, context, commands. Third-party wires are in-tree contributions, not runtime plugins.

### A registry in internal/llm builds clients

One module owns provider to client. Seeded once from the parsed `[providers.*]` tables, it builds an `llm.Client` for any provider name. `main.go` and the repl resolve the active provider through it.

### Startup and switching

The active provider comes from the top-level `provider` key. Unset, the repl prompts the user to pick from the declared set; the one-shot `-p` path uses the first declared provider and prints a warning. Zero declared providers is a startup error.

On load: the registry builds the client for the active provider, `contextmgr` wraps it with the session, and modelinfo resolves from that client (catalog via `ListModels`, with `context.windows` overrides applied). The starting model is the provider's default.

`/provider` lists the declared providers and marks the current one. `/provider <name>` switches to the named provider. An unknown name prints `no such provider: NAME` followed by the available set.

A switch rebuilds: new client from the registry, a fresh `contextmgr` wrap over the same session and `ctxOpts`, modelinfo re-sourced, and the model reset to the new provider's default. The transcript continues; a switch does not start a new session.

`/model` lists only the active provider's catalog and validates against it. After a switch the model is the new provider's default. If the catalog fetch fails, `/model` shows the default model and streaming still runs on it.

Provider and model live in memory for the run. Sessions stay non-resuming; nothing new is persisted.

### Collateral removals

`DetectWire` (`main.go:176-179`) and `RoutingClient` dissolve. Wire selection comes from each provider's `wire` key, and routing is by active provider, not by model id. With no global model, the runModel mismatch bug cannot recur: every model a turn can start on belongs to the active provider.

## Considered options

- **Array-of-tables `[[providers]]` instead of nested `[providers.<name>]`.** Rejected. Nested tables key by name, cannot duplicate, and read naturally; the name does not need restating inside each entry.
- **Provider config owned by the wires themselves (a config.toml per wire).** Chosen in og-o7d.2, then reversed after the all-in-process call. Bundled wires make a separate config file pointless; the declarations sit with the rest of the user's config, and the wire key appears per provider because the hosting wire must be named.
- **Subprocess wires, with the four built-ins repackaged as shipped plugins.** Rejected. Every surveyed harness keeps wires in-process, and the common path would pay IPC and lifecycle machinery for zero gain. The subprocess seam was removed rather than extended.
- **Auto-detection from the model prefix.** Rejected. It encoded the flat config and died with `DetectWire`; a provider now names its wire explicitly.
- **Persisting the active provider across runs.** Rejected, pre-release. The `provider` key is the durable selection; sessions stay non-resuming.
- **First-available provider when the selector is unset.** Rejected for the repl, which prompts instead; kept for `-p`, which cannot prompt.

## Consequences

- `internal/config/config.go`: `Model`, `BaseURL`, `Wire`, `APIKeyEnv` and `Gateway` fields and their env overrides go; a `Providers` map keyed by name and the `Provider` string selector remain, with the shipped defaults for every bundled wire.
- `internal/llm` gains the provider registry and keeps the four wires; `routing.go` is deleted.
- `internal/plugin`: the wire methods and capability leave the protocol; the plugin manager no longer routes model traffic.
- `cmd/genie/main.go`: startup simplifies to resolve the active provider, build one client, wrap it once; the plugin-wire and route branches are deleted.
- `internal/repl`: `/provider` is new; `/model` reworked to the active provider's catalog and the reset-on-switch rule. The repl swaps a provider handle, never strings.
- Copilot's wire returns in-tree; the standalone repo's wire role ends.
- README documents the new config surface, the shipped providers, and `/provider` before this lands.
- Glossary entries update: provider, wire, and the wire registry (which now maps provider names to clients, not model prefixes to protocols).

This ADR, together with the four ticket resolutions on map og-o7d, is the implementation brief.