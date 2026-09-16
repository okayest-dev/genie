# Configuration reference

Genie is configured by one optional TOML file plus environment variables. Everything has a sensible default — a fresh install with no config file works out of the box.

## Where config lives

The config file is `config.toml` in the genie config directory, default `~/.config/genie/config.toml` (XDG-aware). The base directory itself can be overridden with `GENIE_CONFIG_DIR`:

| How | Path |
|-----|------|
| default | `~/.config/genie/config.toml` |
| `GENIE_CONFIG_DIR=/tmp/x` | `/tmp/x/genie/config.toml` |

`GENIE_CONFIG_DIR` also moves the derived default paths for the session directory, the plugin directory, and the agent directory (see below).

A missing config file is **pure defaults**; malformed TOML and unknown keys fail fast at startup (a typo like `bas_url` is caught, not ignored).

## Precedence

```
defaults < config file < environment variables
```

An env var that is set but empty leaves the file value in place. The API key never lives in the config file — it is always read from the environment variable named by `api_key_env` (default `OPENCODE_API_KEY`). The one exception is the copilot provider, which takes no `api_key_env` at all and authenticates through genie's own credential store instead (see [The copilot credential store](#the-copilot-credential-store)).

## Top-level keys

The old flat `model`, `base_url`, `api_key_env`, `wire`, and `gateway` keys are
gone — every boot parameter lives in a `[providers.<name>]` table (next
section). The only top-level selector left is `provider`. The remaining
top-level keys:

| Key | Type | Default | Env var | Meaning |
|-----|------|---------|---------|---------|
| `provider` | string | `""` (none) | `GENIE_PROVIDER` | selects the active provider, named from the `[providers]` tables |
| `instruction_file` | string | `""` (none) | `GENIE_INSTRUCTION_FILE` | extra agent-instruction file, loaded after the built-in default |
| `session_dir` | string | `~/.config/genie/sessions` | `GENIE_SESSION_DIR` | where sessions and their change ledgers are stored |
| `bash_timeout` | integer (seconds) | `120` | `GENIE_BASH_TIMEOUT` | default kill timeout for `bash` tool commands |
| `default_agent` | string | `""` (none) | `GENIE_DEFAULT_AGENT` | name of the agent definition loaded at startup |

Notes:

- `provider` names a table under `[providers]` (or a shipped default — every
  valid provider ships as one). Unset, the REPL prompts you to pick from the
  declared providers; one-shot `-p` mode falls back to the first declared
  provider with a warning. A provider named in `provider` but with no table
  and no shipped default fails at startup.
- `instruction_file` errors at startup if the file is missing.

```toml
# provider = "zen"          # select the active provider (default: none)
# instruction_file = "~/.config/genie/instructions.md"
# session_dir = "~/.config/genie/sessions"
bash_timeout = 120
# default_agent = "orchestrator"
```

## `[providers.*]` — declared providers

Providers are declared as nested tables under a `[providers]` section, keyed
by provider name. A provider is the config unit the harness boots on: one
wire, one endpoint, one default model.

| Key | Type | Meaning |
|-----|------|---------|
| `<name>.wire` | string | the bundled in-process wire that serves the provider: `openai` \| `anthropic` \| `responses` \| `google` \| `copilot` \| `bedrock` (required for a new provider) |
| `<name>.base_url` | string | the provider's endpoint for that wire |
| `<name>.api_key_env` | string | env var holding the API key; a wire with its own auth takes none |
| `<name>.model` | string | the provider's default model (required) |
| `<name>.models` | array of strings | optional catalog override; absent, the catalog comes from the wire's model listing |
| `<name>.opts` | map | wire-specific settings |

Every valid provider ships as a config default, so onboarding is pick a
provider (and set a key if you need to). A file table for a known name merges
over the shipped default — only the non-empty keys you set change, matching the
rest of the config surface where an empty value means "unset". The shipped defaults:

| Provider | Wire | Base URL | Key env | Default model |
|----------|------|----------|---------|---------------|
| `zen` | `openai` | `https://opencode.ai/zen/v1` | `OPENCODE_API_KEY` | `big-pickle` |
| `openai` | `openai` | `https://api.openai.com/v1` | `OPENAI_API_KEY` | `gpt-4o` |
| `anthropic` | `anthropic` | `https://api.anthropic.com` | `ANTHROPIC_API_KEY` | `claude-sonnet-4-5` |
| `responses` | `responses` | `https://api.openai.com/v1` | `OPENAI_API_KEY` | `gpt-4o` |
| `google` | `google` | `https://generativelanguage.googleapis.com/v1beta` | `GEMINI_API_KEY` | `gemini-2.5-pro` |
| `copilot` | `copilot` | (from the token exchange) | none — credential store | `gpt-4o` |
| `bedrock` | `bedrock` | (SDK-resolved regional endpoint) | none — AWS SDK chain | `anthropic.claude-sonnet-4-6` |

The copilot and bedrock rows are the deliberate exceptions to the key-env
rule: they take no `base_url` and no `api_key_env`, because auth flows through
their own channels — copilot's credential store
([below](#the-copilot-credential-store)), and bedrock's AWS SDK standard
credential chain (env vars, shared config, SSO, assume-role,
`credential_process`). Bedrock's endpoint is resolved by the SDK from the
region; select a specific profile or region with `opts.profile` /
`opts.region`, absent which the chain's defaults apply.

Validation at load: a provider missing a default model, missing a wire, or
naming an unknown wire fails startup with a clear error; duplicate provider
tables are rejected by the TOML parser. The verbose startup log (the config
dump) lists every provider as `name:wire:model`.

```toml
# Select the active provider (default: none — the harness picks on startup).
# provider = "zen"

[providers.zen]
base_url = "https://gateway.example/zen/v1"   # override just the endpoint

[providers.deepseek]
wire        = "openai"
base_url    = "https://api.deepseek.com/v1"
api_key_env = "DEEPSEEK_API_KEY"
model       = "deepseek-chat"
models      = ["deepseek-chat", "deepseek-reasoner"]
opts        = { cost = 2 }
```

#### The copilot credential store

The copilot provider authenticates through a genie-owned credential store rather than an environment variable. The store is a host-keyed JSON file at:

```
$XDG_DATA_HOME/genie/copilot/credentials.json
```

(typically `~/.local/share/genie/copilot/credentials.json`; directory `0700`, file `0600`):

```json
{
  "version": 1,
  "hosts": {
    "github.com": {
      "oauth_token": "gho_...",
      "user": "login_name",
      "updated_at": "2026-09-06T12:00:00Z"
    }
  }
}
```

Each `hosts` key is a GitHub host. Its `oauth_token` is the durable GitHub
OAuth token the GitHub device flow grants; the wire exchanges it for a
short-lived Copilot JWT on each request (held in memory only, never written
to disk). Select the host the wire talks to with `opts.domain` on the
provider table — default `github.com`, a GitHub Enterprise tenant like
`tenant.ghe.com` for a GHE login (whose host key must match the tenant's
hostname):

```toml
provider = "copilot"

[providers.copilot]
opts = { domain = "tenant.ghe.com" }
```

A built-in `auth login` device-flow command is planned but not yet shipped;
today the store is provisioned externally (e.g. by the standalone copilot
plugin's `auth` subcommand, which writes the same file). A missing, malformed,
version-mismatched, or host-less store fails auth with a typed error rather
than falling back to a shared key.

## `[tools]` — per-tool toggles

| Key | Type | Default | Env var | Meaning |
|-----|------|---------|---------|---------|
| `tools.read` | boolean | `true` | — | enable the `read` tool |
| `tools.write` | boolean | `true` | — | enable the `write` tool |
| `tools.edit` | boolean | `true` | — | enable the `edit` tool |
| `tools.bash` | boolean | `true` | — | enable the `bash` tool |

A disabled tool is omitted from the tools array sent to the provider; a stale call to it returns `Error: tool 'bash' is disabled`. There is **no env var** for these — the config file is the only switch.

```toml
[tools]
read = true
write = true
edit = true
bash = true
```

## `[plugins]` — plugin discovery

| Key | Type | Default | Env var | Meaning |
|-----|------|---------|---------|---------|
| `plugins.dir` | string | `~/.config/genie/plugins` | `GENIE_PLUGIN_DIR` | directory where plugin executables are discovered |
| `plugins.enable` | array of strings | `[]` (all) | — | allowlist of plugin names to load |
| `plugins.disable` | array of strings | `[]` | — | denylist of plugin names to skip (wins over `enable`) |

See [installing and using plugins](plugins/using.md) for the plugin layouts and discovery rules.

```toml
[plugins]
dir = "~/.config/genie/plugins"
enable = ["my-plugin"]      # explicit allowlist (empty = all)
disable = ["broken-plugin"] # denylist (takes precedence)
```

## `[context]` — context management

| Key | Type | Default | Env var | Meaning |
|-----|------|---------|---------|---------|
| `context.turns` | integer | `0` (all) | `GENIE_CONTEXT_TURNS` | prior turns of history injected into each new turn; `0` = unlimited |
| `context.budget_tokens` | integer (tokens) | `0` (unset) | `GENIE_CONTEXT_BUDGET_TOKENS` | absolute context budget; overrides the percentage below |
| `context.budget_percent` | float | `75` | `GENIE_CONTEXT_BUDGET_PERCENT` | budget as a percentage of the model's context window |
| `context.windows` | map[string]int | `{}` | — | per-model context window overrides, in tokens |
| `context.condense_size` | integer (tokens) | `0` (disabled) | `GENIE_CONTEXT_CONDENSE_SIZE` | threshold above which a prior-turn tool result is condensed |
| `context.net_drop` | boolean | `false` | `GENIE_CONTEXT_NET_DROP` | drop oversized prior-turn tool results entirely (calls stay visible) |
| `context.plugins.order` | array of strings | discovery order | — | chain order for context hooks |
| `context.plugins.active_compact` | string | `builtin` | — | single-active compact implementation (plugin name) |
| `context.plugins.active_condense` | string | `builtin` | — | single-active condense implementation (plugin name) |

See [context management](context-management.md) for how these work. Validation:

- `budget_percent` must land in `(0, 100]`.
- `turns`, `budget_tokens`, `condense_size`, and every `windows` entry must be non-negative; `budget_tokens` and windows must be positive when set.
- Single-active conflict: two plugins claiming the same `active_compact`/`active_condense` gap without an explicit choice is a startup error.

```toml
[context]
turns = 0            # 0 = keep the whole conversation
# budget_tokens = 50000
budget_percent = 75
# windows = { "big-pickle" = 200000 }
condense_size = 0    # 0 = condensation off
net_drop = false

[context.plugins]
# order = ["my-ctx-hook"]
# active_compact = "my-compactor"     # default "builtin"
# active_condense = "my-condenser"    # default "builtin"
```

## `[lifecycle.plugins]` — lifecycle hooks order

| Key | Type | Default | Env var | Meaning |
|-----|------|---------|---------|---------|
| `lifecycle.plugins.order` | array of strings | discovery order | — | ordered chain for lifecycle events |

Request-side events fire in this order; response-side events fire reversed (onion), so paired plugins pack and unpack. Anything not listed is appended in discovery order. See [writing plugins](plugins/authoring.md#lifecycle-plugins).

```toml
[lifecycle.plugins]
order = ["guardrails", "logging"]
```

## Agent definitions

Agents are not keys in `config.toml` — they are separate TOML files discovered from the agent directories. See [agent definitions](agent-definitions.md) for the schema. `default_agent` above selects which one loads at startup.

## Environment variables

The complete set of knobs that can be set from the environment:

| Variable | Setter for | Values / notes |
|----------|-----------|----------------|
| `OPENCODE_API_KEY` | the API key itself (default key holder named by `api_key_env`) | `sk-...` |
| `GENIE_PROVIDER` | `provider` | a provider name from the `[providers]` tables |
| `GENIE_INSTRUCTION_FILE` | `instruction_file` | path to an instruction file |
| `GENIE_SESSION_DIR` | `session_dir` | session storage directory |
| `GENIE_BASH_TIMEOUT` | `bash_timeout` | seconds, positive integer |
| `GENIE_PLUGIN_DIR` | `plugins.dir` | plugin discovery directory |
| `GENIE_DEFAULT_AGENT` | `default_agent` | agent name |
| `GENIE_CONTEXT_TURNS` | `context.turns` | non-negative integer |
| `GENIE_CONTEXT_BUDGET_TOKENS` | `context.budget_tokens` | positive integer |
| `GENIE_CONTEXT_BUDGET_PERCENT` | `context.budget_percent` | number in `(0, 100]` |
| `GENIE_CONTEXT_CONDENSE_SIZE` | `context.condense_size` | non-negative integer |
| `GENIE_CONTEXT_NET_DROP` | `context.net_drop` | boolean |
| `GENIE_DEBUG` | debug mode, same as `-d` | `true` / `1` / `yes` |
| `GENIE_CONFIG_DIR` | base dir for `config.toml` | directory path |

There is **no env var** for `[tools]`, `plugins.enable`/`plugins.disable`, `context.windows`, `context.plugins.*`, or `lifecycle.plugins.*` — those exist only in the config file.

See [commands](commands.md) for `-v`/`-d`/`-p`/`-a` and exit codes.

## Debug and verbose output

```
genie -v          # verbose: high-level flow to stderr
genie -d          # debug: low-level detail to stderr (implies -v)
GENIE_DEBUG=1     # same as -d
```

Verbose shows config resolution, instruction assembly, turn lifecycle, and token usage. Debug adds HTTP requests, SSE chunks, context usage (`tokens`, `window`, `budget`), and full config values.