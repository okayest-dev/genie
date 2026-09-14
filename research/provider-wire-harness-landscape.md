# Provider & Wire/Transport Layer in Cross-Provider Harnesses

**Date:** 2026-09-12
**Purpose:** Inform the genie architectural question of whether to standardise on
"a wire plugin for every provider" (provider = a service that hosts or routes to
hosted models; wire plugin = the transformation between genie's internal stream
contract and the provider's API). Each harness below is reported on four points:
(a) is the provider the unit of configuration, (b) what is the transport seam,
(c) how do model aggregators like OpenRouter fit, (d) is a mid-session
provider/model switch surfaced. Every claim is cited to the primary source
(official docs or repository source code) that owns it.

Harnesses covered: **opencode**, **pi**, **aider**, **goose**.

## Summary

All four harnesses converge on the same provider model genie is considering:

- **(a) Provider is the unit of configuration** in every harness except aider.
  opencode, pi, and goose each carry a config document keyed by provider ID,
  where a provider owns name, base URL, auth (API-key env var name or a key
  store), a default model, and a per-provider model list/catalog. Aider is the
  outlier: its unit of configuration is the **model** named `provider/model`,
  with per-provider API keys and base URLs as secondary knobs.
- **(b) The transport seam** is always a small, **in-process set of wire
  protocols** selected per provider — never one OpenAI-compatible-only protocol,
  and never (in three of four) a plugin subprocess. opencode's native core even
  names the abstraction "Protocol", the same word genie uses informally for its
  wires. a partially contrary pole: aider hides all wires behind the LiteLLM
  library, which internally translates per provider into an OpenAI-shaped
  canonical form; the wiring still exists, it is just vendored in a dependency.
- **(c) Aggregators are a single provider.** Every harness treats OpenRouter as
  one provider reached over its OpenAI-compatible endpoint with one API key; the
  underlying backends appear only as vendor-prefixed model IDs, and each harness
  forwards OpenRouter's own routing directives verbatim (as request-body
  `provider` fields). None models the backends as separate providers.
- **(d) A mid-session switch is surfaced everywhere** as a slash command —
  `/models` or the model picker (opencode, aider, goose), `/model` (pi, aider,
  goose), with per-provider model switching added explicitly to goose in 2026.

## 1. opencode

Two distinct projects share this name and both matter for genie:

- **github.com/opencode-ai/opencode** — the archived (Sep 2025) Go rewrite;
  `internal/llm/` with providers, a flat `providers` config (each entry a bare
  `{ "apiKey": ..., "disabled": ... }`), per-agent `model` strings, and a
  `LOCAL_ENDPOINT` env var for self-hosted OpenAI-style endpoints
  (https://github.com/opencode-ai/opencode). It is now archived; the README
  redirects development to https://github.com/charmbracelet/crush. Genie's
  layout (`internal/llm`, flat single-provider config) is close to this lineage.
- **github.com/anomalyco/opencode** at opencode.ai — the current, actively
  developed project the docs describe. This section reports on it.

**(a) Provider is the unit of configuration — yes.** The docs' "Custom provider"
section defines a provider as a config block keyed by an arbitrary provider ID
carrying (in the current docs) `npm` (driver package), `name` (display), `models`
(a map of model ID → `{ name, limit }`), and `options.baseURL` / `options.apiKey`
/ `options.headers`
(https://opencode.ai/docs/providers/#custom-provider). The v2 docs widen the
per-provider fields to `name`, `env` (ordered env-var names that can supply the
credential), `package` (runtime provider package, e.g.
`@opencode-ai/ai/providers/openai-compatible`), `canonical` (a built-in provider
whose catalog defaults are inherited), `settings` (incl. `baseURL`), `headers`,
`body`, `models`, `websocket`, and `compaction`
(https://opencode.ai/v2/docs/providers). Per-model entries add `modelID` (the id
sent to the provider, distinct from the local key), `name`, `family`, `package`,
`settings`, `capabilities`, `compatibility`, `cost`, `limit`, `disabled`,
`variants` (https://opencode.ai/v2/docs/providers). The default model is the
config key `model` in `provider/model` form, or the `--model`/`-m` CLI flag
(https://opencode.ai/docs/cli/, https://dev.opencode.ai/docs/models/). Credentials
are collected interactively with `/connect` or `opencode auth login` and stored in
`~/.local/share/opencode/auth.json`; the login command is explicitly "powered by
the provider list at Models.dev", where each catalog provider defines its
credential env var, driver package, and API endpoint
(https://opencode.ai/docs/cli/#auth and https://opencode.ai/docs/providers/).
Env substitution uses `{env:VARIABLE_NAME}` syntax, e.g.
`"apiKey": "{env:ANTHROPIC_API_KEY}"` (https://dev.opencode.ai/docs/config/).
Note on `$OPENCODE_PROVIDER`: this shorthand appears in third-party wrappers
(e.g. https://nanoclaw.dev/extend/providers, npm `@open-mercato/ai-assistant`),
but it is **not** in opencode's own documented environment-variable list
(https://opencode.ai/docs/cli/#environment-variables), whose provider surface is
the JSON config plus `OPENCODE_CONFIG*`/`OPENCODE_MODELS_URL` plumbing. In the
current opencode the provider is configured as JSON, not a single env var.

**(b) Transport seam — per-protocol wires selected by provider config.** Two
layers, both documented in source:

1. The default AI-SDK path: a provider's `npm` field selects the SDK package that
   implements its wire. The docs are explicit that this is a wire choice:
   user `@ai-sdk/openai-compatible` for providers using `/v1/chat/completions`,
   `@ai-sdk/openai` when the model uses `/v1/responses`, with "mixed setups under
   one provider" handled by a per-model `provider.npm` override
   (https://opencode.ai/docs/providers/#troubleshooting,
   https://opencode.ai/docs/providers/#custom-provider).
2. The new native LLM core (`packages/llm`): the API contract is a
   `Protocol` — "The semantic API contract of one model server family … how a
   common `LLMRequest` becomes a provider-native body … how the streaming response
   decodes back into common `LLMEvent`s", with concrete implementations
   `OpenAIChat.protocol`, `OpenAIResponses.protocol`, `AnthropicMessages.protocol`,
   `Gemini.protocol`, `BedrockConverse.protocol`
   (https://github.com/sst/opencode/blob/47f33329/packages/llm/src/route/protocol.ts).
   The same file makes the provider/wire split explicit: "A `Protocol` is **not**
   a deployment. It does not know which URL, which headers, or which auth scheme
   to use … owned by `Route.make(...)` … This separation is what lets DeepSeek,
   TogetherAI, Cerebras, etc. all reuse `OpenAIChat.protocol` without forking 300
   lines per provider." Provider packages under `@opencode-ai/llm/providers/*`
   implement `ProviderPackage.define((modelID, settings) => Model)` and "All
   provider knowledge (routes, auth shapes, backend quirks) lives in the
   packages" (https://github.com/anomalyco/opencode/issues/35212). The v2 catalog
   also exposes a `compatibility` field per model for "request and response
   compatibility overrides" (https://opencode.ai/v2/docs/providers).

So opencode speaks several wire protocols, but they are in-process
library/protocol objects — selected per provider (or per model) from config,
never OpenAI-only, and never subprocess plugins.

**(c) OpenRouter — a single provider.** The directory lists OpenRouter like any
other provider: `/connect` with one API key, many models "preloaded by default",
selected with `/models`; extra models can be added under
`provider.openrouter.models`, and per-model routing directives are forwarded by
config, e.g. `options.provider: { "order": ["baseten"], "allow_fallbacks": false }`
(https://opencode.ai/docs/providers/#openrouter). The opencode model registry
also lists OpenRouter as an addressable provider in the native LLM catalog
(https://github.com/anomalyco/opencode/pull/24712). Backends are not modelled;
only model IDs and pass-through routing options are.

**(d) Mid-session switch — yes.** `/models` in the TUI opens the cross-provider
model picker; the docs walk-throughs use `/models` to select any available model
after `/connect` (https://opencode.ai/docs/providers/,
https://opencode.ai/docs/models/). v2 docs confirm the switch is session-scoped:
"A model already selected for a session takes precedence over the configured
default" and "Switching a session's model does not rewrite the config file"
(https://opencode.ai/v2/docs/models). `--model`/`-m provider/model` sets it from
the CLI (https://opencode.ai/docs/cli/). The archived Go version did the same via
a model dialog (`Ctrl+O`) navigable per provider
(https://github.com/opencode-ai/opencode).

## 2. pi

**Identity:** pi is the Earendil Works agent harness — a TypeScript monorepo
(`@earendil-works/pi-coding-agent`, agent core, `pi-ai`, TUI) at
github.com/earendil-works/pi with docs and install at https://pi.dev. Confirmed
against primary sources: the project's own README
(https://github.com/earendil-works/pi/blob/main/README.md),
https://pi.dev, and the author's project post
(https://mariozechner.at/posts/2025-11-30-pi-coding-agent/).

**(a) Provider is the unit of configuration — yes.** "For each built-in provider,
pi maintains a list of tool-capable models" and "configured provider catalogs
refresh automatically; run `pi update --models` to force an immediate refresh"
(https://github.com/earendil-works/pi/blob/main/packages/coding-agent/README.md).
Custom providers and models are declared in `~/.pi/agent/models.json`: a
`providers` map keyed by provider ID, each with `baseUrl`, `api` (the wire it
speaks, below), `apiKey` (supporting `$ENV_VAR` interpolation, `!` shell-command
resolution, and literals), `oauth`, `headers`, `authHeader`, a `models[]` array,
and `modelOverrides`
(https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md).
Per model: `id`, `name`, `api`, `reasoning`, `thinkingLevelMap`, `input`,
`contextWindow`, `maxTokens`, `samplingParams`, `cost`, `compat`. A built-in
provider can be re-pointed at a proxy with just a `baseUrl` override while all
built-in models remain (https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md#overriding-built-in-providers).
Auth is credentialed per provider via `/login` (subscription OAuth or API key) or
CLI `--api-key`/`--provider`/`--model`
(https://github.com/earendil-works/pi/blob/main/packages/coding-agent/README.md).
This is exactly opencode's shape: provider keyed by id, carrying endpoint, auth,
and a model list.

**(b) Transport seam — a small fixed set of in-process wire adapters chosen per
provider.** Models is explicit: "the `api` field … selects the API type"
(https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md#supported-apis)
and the supported set is `openai-completions` (OpenAI chat/completions),
`openai-responses` (OpenAI Responses), `anthropic-messages`, and
`google-generative-ai`, set at provider level and overridable per model
(https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md#supported-apis).
Below that sits `@earendil-works/pi-ai`, described as a "unified multi-provider
LLM API (OpenAI, Anthropic, Google, etc.)" with "seamless cross-provider context
handoffs" (https://github.com/earendil-works/pi/blob/main/README.md,
https://mariozechner.at/posts/2025-11-30-pi-coding-agent/). Inter-server
deviation is absorbed by a per-provider/per-model `compat` knob file rather than
by new wires: for OpenAI-compatible servers `supportsDeveloperRole`,
`supportsReasoningEffort`, `maxTokensField`, `requiresThinkingAsText`, `thinkingFormat`,
etc., and for Anthropic-style servers `supportsEagerToolInputStreaming`,
`forceAdaptiveThinking`, `supportsStrictTools`, `allowEmptySignature`
(https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md#anthropic-messages-compatibility
and #openai-compatibility). So: four wires, in process, per-provider wire choice
in config — the same set of wires genie bakes in (openai, anthropic, responses,
google). pi's own `/settings` even has a "transport" option
(https://github.com/earendil-works/pi/blob/main/packages/coding-agent/README.md).

**(c) OpenRouter — a single provider.** OpenRouter is a built-in provider reached
at `https://openrouter.ai/api/v1` over `openai-completions` with
`$OPENROUTER_API_KEY` (https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md#per-model-overrides).
Routing preferences are forwarded verbatim to OpenRouter through the
`compat.openRouterRouting` field: `order`, `only`, `ignore`, `allow_fallbacks`,
`max_price`, latency/throughput constraints — "This object is sent as-is in the
`provider` field of the OpenRouter API request"
(https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md#openai-compatibility).
Vercel AI Gateway gets the analogous `vercelGatewayRouting`. Underlying backends
are only names inside model IDs, never separate providers.

**(d) Mid-session switch — yes, first-class.** "Switch models mid-session with
`/model` or `Ctrl+L`. Cycle through your favorites with `Ctrl+P`"
(https://pi.dev). Command table: `/model` — "Switch models";
`/scoped-models` — "Enable/disable models for Ctrl+P cycling"; `/login`/`/logout`
— "Manage provider credentials"; the models.json file "reloads each time you
open `/model`. Edit during session; no restart needed"
(https://github.com/earendil-works/pi/blob/main/packages/coding-agent/README.md,
https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md).

## 3. aider

**Identity:** aider is the Python pair-programming CLI (Aider-AI/aider), docs at
aider.chat. It is the oldest and most model-centric of the four.

**(a) Provider is the unit of configuration — no; the *model* is.** Aider's
configuration unit is a litellm-style string `provider/model`, for example
`aider --model openrouter/anthropic/claude-3.7-sonnet`; the provider is the
prefix of the model name, and the registry of known models comes from litellm's
`model_prices_and_context_window.json`
(https://github.com/Aider-AI/aider/blob/main/aider/website/docs/llms/other.md,
https://github.com/Aider-AI/aider/blob/main/aider/website/docs/config/adv-model-settings.md).
There is no named "provider" config object; per-provider surface is reduced to
API-key env vars (a page-length list: `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`,
`OPENROUTER_API_KEY`, …) and per-provider base-URL options (e.g.
`--openai-api-base`, `--anthropic-api-url`, plus an "openai-compatible" family
with its own `--openai-compatible-api-base`)
(https://aider.chat/docs/llms/other.html). Unknown models can be declared in
`.aider.model.metadata.json` (cost/limits and, tellingly, a `litellm_provider`
field that selects the transport family)
(https://github.com/Aider-AI/aider/blob/main/aider/website/docs/config/adv-model-settings.md).
Aliases let short names expand to `provider/model` (https://aider.chat/docs/config/model-aliases.html).

**(b) Transport seam — one vendored library, LiteLLM, that internally performs
per-provider wire translation.** "Aider uses the litellm package to connect to
hundreds of other models" (https://aider.chat/docs/llms/other.html). Inside
litellm, each provider is a `ProviderConfig` (`llms/{provider}/chat/transformation.py`)
with `transform_request()` / `transform_response()` converting between OpenAI
format and provider-native format, isolated per provider and unit-tested without
network (https://github.com/BerriAI/litellm/blob/87e120d9589ce3697fefd7520e3e893716a21173/ARCHITECTURE.md).
So aider is the "collapse onto one canonical shape" pole: the harness itself never
sees wires; the translation business lives in a dependency, and the canonical
form is OpenAI-compatible. (Aider's own *edit formats* —
whole/diff/udiff/editor — are a separate concern about code editing, not
transport: https://aider.chat/docs/more/edit-formats.html.)

**(c) OpenRouter — a single provider prefixed in the model registry.** `openrouter/`
models use `OPENROUTER_API_KEY`; `aider --list-models openrouter/` lists them;
provider routing is controlled via `.aider.model.settings.yml` pass-through
request-body fields: `extra_params.extra_body.provider` with `order`,
`allow_fallbacks`, `data_collection`, `require_parameters`
(https://github.com/Aider-AI/aider/blob/main/aider/website/docs/llms/openrouter.md).
Backends again only appear as vendor-prefixed model IDs.

**(d) Mid-session switch — yes.** In-chat slash commands include `/model`
("Switch the Main Model to a new LLM"), `/models` ("Search the list of available
models"), `/weak-model`, and `/editor-model`
(https://aider.chat/docs/usage/commands.html).

## 4. goose

**Identity:** goose is Block's open-source Go agent (github.com/block/goose; the
project now develops under the aaif-goose org but the canonical docs remain at
https://block.github.io/goose/docs). Source lives in `crates/goose/`.

**(a) Provider is the unit of configuration — yes.** The config file
`~/.config/goose/config.yaml` stores an `active_provider` plus a `providers` map
with per-provider `enabled`/`model`/`configured`; legacy flat `GOOSE_PROVIDER` /
`GOOSE_MODEL` keys are still read and migrated
(https://github.com/block/goose/blob/main/documentation/docs/guides/config-files.md).
`GOOSE_PROVIDER` and `GOOSE_MODEL` (plus `GOOSE_PLANNER_PROVIDER`/`MODEL`) remain
as per-process env overrides. Each provider has its own credential env var
(`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GOOSE_…`) and endpoint knobs (`OPENAI_HOST`,
`OPENAI_BASE_PATH`), and API keys never go in config.yaml — they live in the
system keyring or `secrets.yaml`
(https://github.com/block/goose/blob/main/documentation/docs/getting-started/providers.md,
https://github.com/block/goose/blob/main/documentation/docs/guides/config-files.md).
Custom providers are a declarative JSON file auto-discovered in
`~/.config/goose/custom_providers/` with `name`, `engine`, `display_name`,
`description`, `api_key_env`, `base_url`, a `models` list (name, `context_limit`,
token costs), `headers`, `supports_streaming`, `requires_auth`
(https://github.com/block/goose/blob/main/documentation/docs/getting-started/providers.md#custom-providers),
or a first-class Rust `Provider` trait implementation registered in
`crates/goose/src/providers/` (https://block-goose.mintlify.app/guides/custom-providers).
Model catalogs are discovered per provider (e.g. fetched from the provider's
model endpoint) and selectable via `goose configure`.

**(b) Transport seam — per-provider Rust implementations reusing a small engine
set.** Every built-in provider is a `Provider` trait impl (`anthropic`, `openai`,
`ollama`, `databricks`, `openrouter`, …) in `crates/goose/src/providers/`. The
declarative custom-provider mechanism reuses an existing wire engine via the
`engine` field ("API format to use: `openai`, `anthropic`, or `ollama`"), so a
new hostname never needs a new transport (https://block-goose.mintlify.app/guides/custom-providers).
The native OpenAI provider handles both the chat-completions path and the
Responses API (the `OPENAI_STORE` option surfaces Responses persistence)
(https://github.com/block/goose/blob/main/documentation/docs/getting-started/providers.md).
This is the same shape as genie: a few in-process wire engines, bound to
URLs/auth at provider-config time. Goose additionally has *agent*-level
subprocess providers (Claude Code, Codex, Cursor Agent, Gemini CLI via ACP) —
but those host a whole other agent rather than transform a wire
(https://github.com/aaif-goose/goose/pull/9658).

**(c) OpenRouter — a single built-in provider.** There is a first-class
`OpenRouterProvider` (https://github.com/block/goose/pull/538). It uses one API
key (`OPENROUTER_API_KEY`) and one OpenAI-compatible endpoint
(`api/v1/chat/completions` on `OPENROUTER_HOST`, default `https://openrouter.ai`),
with a `KNOWN_MODELS` list plus dynamic inventory refresh from the OpenRouter
models API for the picker (https://github.com/aaif-goose/goose/pull/10641,
https://github.com/block/goose/blob/main/crates/goose/src/providers/openrouter.rs).
Model-prefix behaviour is handled inside the OpenRouter provider: Anthropic-named
models get cache-control request surgery and Gemini-named models get reasoning
fields added (https://github.com/block/goose/blob/main/crates/goose/src/providers/openrouter.rs).
Still one provider; backends surface only as model prefixes.

**(d) Mid-session switch — yes, added explicitly in 2026.** A `/model` slash
command shows the current model and switches the live session's model within the
current provider (https://github.com/aaif-goose/goose/pull/8747), and a bare
`/model` opens a cross-provider picker that aggregates every *configured*
provider's models and switches provider + model on selection, probing the chosen
model before committing and skipping context-managed/ACP providers
(https://github.com/aaif-goose/goose/pull/9658). `goose run --model <model>`
selects a model for a single session, and the desktop app has a "Switch models"
settings flow (https://github.com/block/goose/blob/main/documentation/docs/getting-started/providers.md).

## Synthesis vs Genie

### Does the evidence support provider-as-service + wire-plugin-as-transform?

Yes. The four harnesses all decompose the LLM layer exactly along that seam:

- **Provider** — a named config unit owning a service identity, base URL,
  auth (env-var name or key store), and a model list/catalog. opencode's
  `providers.<id>`, pi's `models.json` `providers.<id>`, goose's
  `providers.<name>` / custom-provider JSON, and (weaker, model-prefix-based)
  aider all conform.
- **Wire/transport** — a reusable in-process transform between one canonical
  internal message/stream contract and a provider's HTTP API. opencode's
  `Protocol` objects are the clearest statement, and its `protocol.ts` comment
  articulates genie's exact proposed split: the protocol "does not know which
  URL, which headers, or which auth scheme to use" — those are deployment
  concerns bound per provider, "which lets DeepSeek, TogetherAI, Cerebras, etc.
  all reuse `OpenAIChat.protocol`" (https://github.com/sst/opencode/blob/47f33329/packages/llm/src/route/protocol.ts).
  pi's `api` field (`openai-completions`, `openai-responses`, `anthropic-messages`,
  `google-generative-ai`) is the same vocabulary genie already uses for its four
  wires (openai, anthropic, responses, google). litellm's per-provider
  `transform_request`/`transform_response` is the same idea vendored as a
  dependency; goose's `engine` field is the same idea behind a Rust trait.

So genie's proposed "a wire plugin for every provider" split is *more* aligned
with the mainstream than its current shape. The relevant delta between genie and
the field is not the split itself but **which side config sits on**: the other
harnesses make *providers* the config carrier and let a provider select its wire,
whereas genie currently has a flat single-provider config (one model, one
base_url, one api_key_env, one wire, one optional gateway) and infers the wire
from the model-ID prefix (`internal/llm/wire.go: DetectWire`). Converted to the
harness shape, that becomes: provider config carries baseURL + auth-env + a model
list, and the wire is either declared on the provider or defaulted per model —
exactly how opencode (`compatibility`, `npm`/`package`), pi (`api` field +
`compat`), and goose (`engine`) handle it.

### Where do genie's baked-in wires sit relative to these harnesses?

They are the right primitives; the harnesses merely *parameterise* them.

- The **set of wires is the same everywhere**: opencode implements OpenAI Chat,
  OpenAI Responses, Anthropic Messages, Gemini generateContent, Bedrock Converse;
  pi implements openai-completions, openai-responses, anthropic-messages,
  google-generative-ai; litellm has a transformation file per provider family;
  goose's `engine` enum is openai/anthropic/ollama. Genie's four wires sit
  squarely inside every harness's wire set — none of the four got by with an
  OpenAI-compatible-only transport.
- The **difference is binding**: in opencode/pi/goose, one wire object is shared
  by many providers through config (baseURL + auth + model list per provider),
  with a `compat`/`compatibility` override layer for per-server quirks (pi's
  `compat` and opencode's `compatibility` are notable; genie currently encodes
  such quirks — e.g. sending system vs developer role — inside the wire itself).
  In genie the wire is bound to a *single* global config block and picked by
  model-name prefix at runtime — effectively a built-in approximation of what the
  other harnesses do declaratively at config time.
- **No harness uses a subprocess JSON-RPC layer as its wire seam.** opencode, pi,
  aider, and goose all implement wires in-process (SDK packages, protocol
  objects, library transforms, Rust traits). genie's existing JSON-RPC `wire
  plugins` (wire/init, wire/stream, wire/list_models) have no counterpart among
  these four. The closest analogue is goose's ACP subprocess providers (Claude
  Code, Codex, Gemini CLI), which are whole-agent hosts, not wire transforms.
  Nothing in this survey argues the JSON-RPC seam is *wrong*; it just is not what
  the mainstream does — the mainstream keeps the transform in process and pushes
  per-provider variation into config. (If genie wants to keep a subprocess seam,
  the harnesses suggest it belongs behind the *provider* boundary — run a whole
  wire-capable provider out of process, like goose's ACP providers — rather than
  one subprocess per wire-family.) Note too that a "provider with a model list"
  is already what genie's wire plugins deliver via `wire/list_models`; that
  contract maps directly onto opencode's models.dev catalog entries, pi's
  built-in catalogs, and goose's per-provider model fetch.

### How do aggregators get handled?

Uniformly as **one provider over an OpenAI-compatible endpoint**, and this is
where genie's "gateway" concept maps cleanly:

- One API key, one base URL: opencode's `openrouter` directory entry
  (https://opencode.ai/docs/providers/#openrouter), pi's built-in `openrouter`
  at `https://openrouter.ai/api/v1` (docs/models.md), aider's `openrouter/`
  model prefix + `OPENROUTER_API_KEY`, goose's `OpenRouterProvider`
  (https://github.com/block/goose/pull/538).
- The underlying backends are *never* modelled as providers. They exist only as
  vendor-prefixed model IDs (`openrouter/anthropic/claude-sonnet-4`), and each
  harness forwards OpenRouter's routing primitives verbatim: opencode's
  `options.provider.order`, pi's `compat.openRouterRouting` ("sent as-is in the
  `provider` field"), aider's `extra_params.extra_body.provider`, goose's model
  prefix-based request surgery (cache-control for `anthropic/*`, reasoning fields
  for `gemini/*`).
- Corollary for genie: an aggregator should be a **provider whose wire is
  `openai` (or a declared variant)** with a model list of vendor-prefixed IDs —
  matching opencode/pi/aider/goose — and the existing "optional gateway" concept
  disappears into ordinary provider config (base URL + key). The multi-wire
  *gateway* of genie's context note ("one optional gateway") has no parallel: the
  harnesses treat a gateway as just another provider address.

### Net position

The evidence supports genie's provider/wire split, and genie's existing wire set
is the field's standard set. The two structural moves the other harnesses all
make that genie currently does not are: (1) carry provider as a *repeatable
first-class config unit* (name, base URL, auth-env, model catalog, declared wire)
rather than a flat single-provider block, and (2) let the wire be selected/overridden
per provider or per model from config rather than inferred from the model name.
Both moves are what make mid-session `/provider`-style switching (a) and (d)
above) straightforward — every harness already surfaces it as a slash command
over a provider-keyed model registry.

## Sources

- opencode providers docs: https://opencode.ai/docs/providers/
- opencode v2 providers docs: https://opencode.ai/v2/docs/providers/
- opencode v2 models docs: https://opencode.ai/v2/docs/models
- opencode models docs (dev): https://dev.opencode.ai/docs/models/
- opencode config docs (dev): https://dev.opencode.ai/docs/config/
- opencode CLI docs: https://opencode.ai/docs/cli/
- opencode native Protocol seam: https://github.com/sst/opencode/blob/47f33329/packages/llm/src/route/protocol.ts
- opencode native LLM core PR: https://github.com/anomalyco/opencode/pull/24712
- opencode provider-package contract issue: https://github.com/anomalyco/opencode/issues/35212
- opencode catalog env/npm/api shape: https://github.com/anomalyco/opencode/issues/27853
- archived Go opencode (genie lineage): https://github.com/opencode-ai/opencode
- pi home: https://pi.dev
- pi README (coding-agent): https://github.com/earendil-works/pi/blob/main/packages/coding-agent/README.md
- pi models.json/custom providers: https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md
- pi monorepo README: https://github.com/earendil-works/pi/blob/main/README.md
- pi author post (architecture): https://mariozechner.at/posts/2025-11-30-pi-coding-agent/
- aider "Other LLMs": https://aider.chat/docs/llms/other.html
- aider OpenRouter: https://github.com/Aider-AI/aider/blob/main/aider/website/docs/llms/openrouter.md
- aider advanced model settings: https://github.com/Aider-AI/aider/blob/main/aider/website/docs/config/adv-model-settings.md
- aider model aliases: https://aider.chat/docs/config/model-aliases.html
- aider in-chat commands: https://aider.chat/docs/usage/commands.html
- litellm architecture (provider transforms): https://github.com/BerriAI/litellm/blob/87e120d9589ce3697fefd7520e3e893716a21173/ARCHITECTURE.md
- goose getting-started providers: https://github.com/block/goose/blob/main/documentation/docs/getting-started/providers.md
- goose config files: https://github.com/block/goose/blob/main/documentation/docs/guides/config-files.md
- goose custom providers guide: https://block-goose.mintlify.app/guides/custom-providers
- goose OpenRouterProvider source: https://github.com/block/goose/blob/main/crates/goose/src/providers/openrouter.rs
- goose OpenRouter provider initial PR: https://github.com/block/goose/pull/538
- goose OpenRouter inventory refresh: https://github.com/aaif-goose/goose/pull/10641
- goose `/model` in-session switch: https://github.com/aaif-goose/goose/pull/8747
- goose cross-provider `/model` picker: https://github.com/aaif-goose/goose/pull/9658