# Standalone Plugin Extraction Audit

Context pointer for ticket **og-1hz.7**. Primary sources: files in this repo, read directly. Nothing below is inferred from web sources.

Scope: the two in-repo wire plugins (`plugins/bedrock`, `plugins/copilot`) and the surface an extraction spec must contend with. Goal is to record facts, not to design the extraction.

---

## 1. Module & build

### Module path and Go version

- `go.mod:1` — `module github.com/okayest-dev/genie` (confirmed; prior history `115b8a4 genie: rename product and module from og to genie`).
- `go.mod:3` — `go 1.24`.
- Requires (`go.mod:5-8`): `github.com/BurntSushi/toml v1.6.0`, `github.com/ron2111/omnitoken v0.1.7`, `gopkg.in/yaml.v3 v3.0.1`. `omnitoken` is used only by `internal/tokens/tokens.go:11` (token counting — harness-side, not needed by plugins). `yaml.v3` is used only by `protocol/generate.go:18` and `protocol/generate_test.go:8`.

### In-repo imports each plugin needs

- `plugins/bedrock/main.go` imports stdlib plus exactly two third-party/in-repo packages:
  - `github.com/BurntSushi/toml` (`plugins/bedrock/main.go:39`)
  - `github.com/okayest-dev/genie/plugins/shared` (`plugins/bedrock/main.go:40`)
  The **only** in-repo import is `plugins/shared`.
- `plugins/copilot/main.go` imports the same two:
  - `github.com/BurntSushi/toml` (`plugins/copilot/main.go:18`)
  - `github.com/okayest-dev/genie/plugins/shared` (`plugins/copilot/main.go:19`)
  Again, **only** in-repo import is `plugins/shared`.
- Both are `package main` with `func main()`. `go build ./plugins/bedrock` and `go build ./plugins/copilot` both succeed today inside the module (verified; and `go vet ./plugins/...` passes).
- `plugins/shared/protocol.go` imports **only stdlib** (`bufio`, `encoding/json`, `fmt`, `os`; `plugins/shared/protocol.go:4-9`).

**Standalone-build assessment:** A standalone repo for either plugin is technically feasible with no genie-module dependency, because the sole in-repo coupling is `plugins/shared`, which is stdlib-only and trivially vendorable. The intended replacement is declared in `protocol/schema.yaml:2-3`: "Each plugin vendors the generated output as internal/wireplugin/." So an external plugin repo would need (a) the generated/vendored protocol package and (b) `BurntSushi/toml` for its `config.toml` parsing. Caveat — the generated package is *not* fully identical to `plugins/shared/protocol.go` (see §3 "Divergences"): an external repo vendoring `internal/wireplugin` would lose `ModelDef.ContextWindow` relative to the current shared package.

### Makefile

`Makefile` targets (`.PHONY: build install test vet clean`, `Makefile:3`):

- `build` — `go build -o genie ./cmd/genie` (`Makefile:5-6`)
- `install` — `go install ./cmd/genie` (`Makefile:8-9`)
- `test` — `go test ./...` (`Makefile:11-12`)
- `vet` — `go vet ./...` (`Makefile:14-15`)
- `clean` — `rm -f genie` (`Makefile:17-18`)

There is **no** target that builds the plugins. `test`/`vet` do reach them because they run `./...` over the whole module (including `plugins/...`).

---

## 2. Plugin contents

### `plugins/bedrock/main.go`

- **Config struct** `bedrockConfig`: `Region`, `Profile`, `MaxTokens` (int), `EndpointURL`, TOML-tagged `region`/`profile`/`max_tokens`/`endpoint_url` (`plugins/bedrock/main.go:43-48`). Read from `config.toml` **next to the executable** (`os.Executable()` dir, `loadConfig`, `plugins/bedrock/main.go:109-128`).
- **Env-var overrides** (each `GENIE_BEDROCK_*`; doc comment `plugins/bedrock/main.go:12-17`):
  - `GENIE_BEDROCK_PROFILE` (`:137`) — precedence env > `AWS_PROFILE` > config > `"default"`.
  - `GENIE_BEDROCK_REGION` (`:150`) — precedence env > `AWS_REGION` > config > `~/.aws/config` section `region` > `"us-east-1"` default (`:191-193`).
  - Standard AWS vars also honored: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_DEFAULT_REGION` (`:178-189`).
  - `GENIE_BEDROCK_MAX_TOKENS` (`loadMaxTokens`, `:204-215`) — env > config > default `4096`.
  - `GENIE_BEDROCK_ENDPOINT_URL` (`loadEndpointURL`, `:218-223`) — env > config (empty default; custom VPC endpoint).
  - Credentials read from `~/.aws/credentials` and region from `~/.aws/config` via hand-rolled INI parsing (`parseINISection`, `:225-253`; paths at `:160`, `:169`).
- **Provider naming:** this plugin itself has no name field; the host derives its name from the plugin-binary base name (see §4 — `filepath.Base(path)`, `internal/plugin/manager.go:166`). The binary is expected to be named `bedrock` (`README.md:195`).
- **Protocol/version spoken:** version 2 via `shared.ProtocolVersion`; capabilities `{Tools:false, Wires:true, Providers:false}` (`plugins/bedrock/main.go:81-86`). Registers `OnInit`, `OnStream` only. Handled methods (via shared handler): `capabilities/list`, `wire/init`, `wire/list_models`, `wire/stream`, `ping`, `shutdown`.
- **API surface:** SigV4-signs and POSTs Amazon Bedrock `ConverseStream` at `https://bedrock-runtime.<region>.amazonaws.com/model/<model>/converse-stream` (`:301-316`). Streams NDJSON event lines, returns the unified wire shape `{text, tool_calls[], usage}` (`parseConverseStream`, `:332-411`).
- **Model list:** hardcoded `listModels()` — 11 models including `anthropic.claude-sonnet-4-6`, `anthropic.claude-opus-4-x`, `meta.llama*`, `amazon.nova-*`, `mistral.mistral-large-2407-v1:0`, `cohere.command-r-v1:0` (`:255-269`).
- Note: tools capability is false; the plugin still parses tool-use deltas into `tool_calls` in the stream (`:369-388`).

### `plugins/copilot/main.go`

- **Config struct** `copilotConfig`: `Domain` (TOML `domain`) (`plugins/copilot/main.go:22-24`). Read from `config.toml` next to the executable (`loadConfig`, `:104-123`).
- **GHE support:** `domain` drives both the GitHub token source and the exchange endpoint:
  - Host key in `~/.config/github-copilot/hosts.json` is `github.com` unless `domain` is set (`readCopilotToken`, `:125-154`).
  - Token exchange URL is `https://api.github.com/copilot_internal/v2/token`, or `https://api.<domain>/copilot_internal/v2/token` when configured (`:156-160`). Documented in `README.md:198-206`.
- **OAuth/token exchange:** reads the user's GitHub OAuth token from hosts.json (`account.OAuthToken`), exchanges it via `GET copilot_internal/v2/token` with `Authorization: token <githubOAuth>` (`:166-167`) for a short-lived Copilot JWT (`TokenExchange{token, endpoints, expires_at, refresh_in}`, `:31-36`). Refreshes when the token is within 5 minutes of expiry (`:90-94`, `refreshToken` `:156-194`).
- **API surface:** OpenAI-compatible `POST <apiBase>/v1/chat/completions` with `stream: true, stream_options.include_usage` (`:208-228`). `apiBase` comes from `endpoints["api"]`, falling back to `https://api.githubcopilot.com` (`:186-189`). Sends Copilot-identifying headers `Editor-Version: genie/0.1.0`, `Editor-Plugin-Version: genie-copilot/0.1.0`, `Copilot-Integration-Id: vscode-chat`, `User-Agent: GithubCopilot/genie-0.1.0` (`:227-230`). SSE-parses with a hand-rolled line scanner (`:246-329`).
- **Version/naming:** protocol v2, capabilities `{Tools:false, Wires:true, Providers:false}` (`:68-73`). Binary expected named `copilot` (`README.md:196`). Hardcoded model list — 6 models incl. `gpt-4o`, `gpt-5.4`, `gpt-5.5`, `claude-sonnet-4-5`, `claude-opus-4-7`, `gemini-3.5-pro` (`:75-82`).

### `plugins/shared/`

Single file `plugins/shared/protocol.go`:

- **Method constants** (`:11-22`): `MethodCapabilitiesList` = `"capabilities/list"`, `MethodWireInit` = `"wire/init"`, `MethodWireStream` = `"wire/stream"`, `MethodWireListModels` = `"wire/list_models"`, `MethodContextBeforeRequest`, `MethodContextAfterResponse`, `MethodContextCompact`, `MethodContextCondense`, `MethodPing` = `"ping"`, `MethodShutdown` = `"shutdown"`. **No** `tools/list`/`tools/call` constants here (they exist only host-side and in the schema — see §3).
- **`ProtocolVersion = 2`** (`:24-26`), commented "v2 adds granular context-hook capabilities."
- **Types** (`:28-131`): `Request`, `Response`, `Error`, `Capabilities` (8 fields incl. granular `context_before/after/compact/condense` bools + `Version int`), `WireInitResult`, `ModelDef` — note it **has** `ContextWindow int` (`:73`), `WireListModelsResult`, context-hook types (`ContextMessage`, `ContextToolCall`, `ContextRequest`, `ContextBeforeRequestResult`, `ContextAfterResponseParams`, `Usage`, `ContextAfterResponseResult`, `ContextCompactResult`, `ContextCondenseResult`), `ToolDef`.
- **Capabilities method:** `HasAny()` (`:60-62`).
- **Handler** (`:133-325`): `NewHandler(caps)`, `SetModels`, `OnInit`, `OnStream`, `OnBeforeRequest`, `OnAfterResponse`, `OnCompact`, `OnCondense`, `Run`, generic `ParseParams[T any]`.
- **Error codes are inline literals**, not named constants — `-32700` (`:191`), `-32601` (`:216,227,243,259,275,295`), `-32602`, `-32603` inside `handleRequest`/`writeError` calls. Contrast: the schema/generated package defines named `ParseError`/`MethodNotFound`/`InvalidParams`/`InternalError` consts (§3).

---

## 3. Codegen path for vendored `internal/wireplugin`

### `protocol/generate.go`

- Command shape (usage comment `protocol/generate.go:4-6`):
  ```
  go run ./protocol -schema protocol/schema.yaml -out /path/to/internal/wireplugin
  ```
- Flags: `-schema` defaults to `protocol/schema.yaml` (`:105`); `-out` default empty = print to stdout (`:106`). With `-out`, it `MkdirAll`s the dir, writes exactly one file named **`wireplugin.go`**, and logs `wrote <path>/wireplugin.go` to stderr (`:127-140`).
- `generate(schema)` runs the embedded `wireplugin.go.tmpl` (`//go:embed`, `:21-22`, `:144`) with template funcs `join/lower/hasPrefix/trimPrefix/contains/bt` (`:149-156`).
- The **generated package is fully standalone**: `package wireplugin` with imports only `bufio`, `encoding/json`, `fmt`, `os` (`protocol/wireplugin.go.tmpl:3-10`).

### `protocol/schema.yaml`

- `protocol: {version: 2, package: wireplugin, transport: stdio, encoding: ndjson-rpc-2.0}` (`:5-9`). Header comments call it "the source of truth for generating standalone Go plugin packages. Each plugin vendors the generated output as internal/wireplugin/." (`:1-3`).
- Constants (`:11-36`): **12** method constants — the 10 above **plus** `MethodToolsList` = `"tools/list"` and `MethodToolsCall` = `"tools/call"` (the ones missing from `plugins/shared`).
- Error codes (`:37-47`): `ParseError=-32700`, `InvalidRequest=-32600`, `MethodNotFound=-32601`, `InvalidParams=-32602`, `InternalError=-32603`.
- Types (`:49-238`): `Request`, `Response`, `Error`, `Capabilities` (8 fields), `WireInitResult`, `ModelDef` — **ID and Name only, no `ContextWindow`** (`:125-133`), `WireListModelsResult`, `ToolDef`, `ContextMessage`, `ContextToolCall`, `ContextRequest`, `ContextBeforeRequestResult`, `Usage`, `ContextAfterResponseParams/Result`, `ContextCompactResult`, `ContextCondenseResult`.
- Handler (`:240-375`): struct fields, 7 configurable methods (`SetModels`, `OnInit`, `OnStream`, `OnBeforeRequest`, `OnAfterResponse`, `OnCompact`, `OnCondense`), static methods (`NewHandler`, `Run`, `writeResult`, `writeRawResult`, `writeError`, `handleRequest`), and the dispatch table (`dispatch` `:353-375`).
- `parse_params` (`:377-384`): generic `ParseParams[T any]`.

### `protocol/wireplugin.go.tmpl`

- Emits the whole file, including the "Code generated from protocol/schema.yaml. DO NOT EDIT." header (`:1`), `const ProtocolVersion = <version>` (`:13`), method/error-code const blocks, all typed structs, and the `Handler` with the dispatch `switch` built from the schema's `dispatch` entries (`:92-196`). `shutdown` case calls `os.Exit(0)` (`:189-191`).

### Parity/verification tests

- `protocol/generate_test.go` — **token/substring assertions only** on freshly generated output: package `wireplugin` (`:30-32`), imports `bufio/encoding/json/fmt/os` (`:35-39`), `ProtocolVersion = 2` (`:42-44`), the 10 method constants (`:51-62`), selected error codes (`:75-79`), selected types (`:92-100`), handler parts (`:113-128`), dispatch cases (`:141-149`), `func ParseParams[T any](` (`:162`), struct tags (`:172-181`).
- `protocol/generate_compare_test.go` — **AST-parity check** between generated `wireplugin.go` and `../plugins/shared/protocol.go` (`:21`). It compares only an **allowlist** of types (`:32-39`), funcs `NewHandler`/`ParseParams` (`:54-56`), Handler methods (`:71-74`), and the 10 wire method constants + values (`:89-118`). It does not demand equality beyond that allowlist.
- **Divergences the tests do not catch** (facts, from reading both sources):
  1. Generated code defines `MethodToolsList`/`MethodToolsCall` and named error-code constants; `plugins/shared/protocol.go` has neither (inlines numeric codes).
  2. `plugins/shared/protocol.go` `ModelDef` carries `ContextWindow` (`:73`), which the schema/template/generated output lack — so a vendor of the generated package cannot report a model's context window, even though the host honors it (`cmd/genie/main.go:238-242`, `internal/plugin/protocol.go:237-243`).
  In short, the two files are not interchangeable today; a later extraction spec must decide the source-of-truth direction.

---

## 4. Config + discovery surface in genie

### `GENIE_PROVIDER` / `GENIE_PLUGIN_DIR` routing

- `GENIE_PROVIDER` → `cfg.Provider` (`internal/config/config.go:391-394`); same effect via config key `provider` (`internal/config/config.go:209-211`). Semantics documented at `internal/config/config.go:101-105` ("names a plugin to route all requests through").
- Routing happens in `cmd/genie/main.go:198-224`:
  - If `cfg.Provider != ""`: look it up **by plugin name** — `pluginMgr.GetPlugins()[cfg.Provider]` — error `provider %q not found (loaded plugins: ...)` if absent (`:200-204`); error if the plugin doesn't declare `Wires` (`:205-208`); else wrap in `newPluginWireClient(p)` (`:209`).
  - Otherwise: build a **model-based route table** across all wire plugins — `modelRoutes[model.ID] = wireClient` (`:210-223`), wrapped in `llm.NewRoutingClient`.
- `GENIE_PLUGIN_DIR` → `cfg.PluginDir` (`internal/config/config.go:418-421`). Same via `[plugins] dir` (`internal/config/config.go:156-159`, `applyPlugins` `:491-499`).
- **Plugin name = executable base name.** `internal/plugin/manager.go:166` — `name := filepath.Base(path)`. So a binary installed as `<dir>/copilot/copilot` (or flat `<dir>/copilot`) is addressed as provider `"copilot"`, matching the merged provider/model naming used at startup.
- Plugin process is spawned with `GENIE_PLUGIN=1` in its env (`internal/plugin/manager.go:175`).

### Discovery directory / layout

- Default: `filepath.Join(userConfigDir, "genie", "plugins")` — i.e. `~/.config/genie/plugins/` — set in `defaults` (`internal/config/config.go:350`) and re-derived in `applyPlugins` (`:495`). `userConfigDir` is `GENIE_CONFIG_DIR` if set, else `os.UserConfigDir()` (`internal/config/config.go:319-326`).
- Two layouts (`internal/plugin/manager.go:81-112`):
  - **Directory (recommended)**: `<dir>/<name>/<name>` executable, `stat` checks executable bit (`:89-96`).
  - **Flat (backward compat)**: `<dir>/<name>` executable (`:99-111`), skipping non-executables and `*.go` files (`:105-110`).
- Hard cap `MaxPlugins = 16` (`internal/plugin/manager.go:20`, enforced `:114-117`). Hidden files (`.*`) skipped (`:84-86`).
- Load sequence per plugin: start binary → `capabilities/list` handshake (timeout `RequestTimeout = 5s`, `:240-283`) → if `Tools`, `tools/list` + register (`:218-227`) → if `Wires`, `wire/init` (`params: {"config": {}}`, `:311-336`) then `wire/list_models` (`:349-372`). Liveness pings every `PingInterval = 30s` (`:22`, `:390-421`). Graceful shutdown via `shutdown` method then `SIGINT` then kill (`:450-484`).

### `manifest.toml`

- `Manifest{name, version, capabilities}` TOML (`internal/plugin/manifest.go:12-16`).
- Lookup: directory layout `<pluginDir>/<name>/manifest.toml` first, falling back to flat `<pluginDir>/<name>.toml` (`manifest.go:18-42`). Directory takes precedence (`TestParseManifestDirectoryTakesPrecedence`, `internal/plugin/manager_test.go:443-480`).
- `name`, `version`, and ≥1 `capabilities` entry are required (`manifest.go:50-58`); capabilities validated against `{tools, wires, providers}` (`:72-80`). A missing/unparseable manifest is non-fatal — the plugin is still started, just probed (`internal/plugin/manager.go:169-172`).

### Enable/disable

- `[plugins]` table: `dir`, `enable`, `disable` (`internal/config/config.go:156-160`). Disable is a denylist applied first; when `enable` is non-empty it acts as an allowlist (`internal/plugin/manager.go:120-127`). Documented `README.md:208-217`.

### In-repo assumptions that plugins live inside the genie module tree

- **Runtime: none.** Grep of `internal/` for `plugins/shared|plugins/bedrock|plugins/copilot` returns no matches; discovery is entirely from `cfg.PluginDir`, and the plugin binaries are arbitrary external executables. The e2e suite runs with no plugins installed (see §5).
- **Build-time coupling, two places:**
  1. The two plugin `main.go` files `import "github.com/okayest-dev/genie/plugins/shared"` (section 1).
  2. `protocol/generate_compare_test.go:21` reads `../plugins/shared/protocol.go` — the parity test pins the shared package's repo-relative path.
- Doc-level assumptions: `README.md:193-206` lists "Included plugins" bedrock/copilot and documents their `~/.config/genie/plugins/...` config. `docs/plugin-protocol.md:492` documents the `[plugins]` block.
- Named error/capability vocabulary that must stay in sync across three copies: `plugins/shared/protocol.go`, `internal/plugin/protocol.go` (host-side), and the generated package. All three currently agree on `ProtocolVersion = 2` (`plugins/shared/protocol.go:26`, `internal/plugin/protocol.go:11`, `protocol/schema.yaml:6`); there is no shared telemetry or test asserting cross-copy equality beyond the allowlist test in §3.

---

## 5. Test infrastructure

### `internal/e2e/e2e_test.go`

- Full-suite black-box tests run the compiled `cmd/genie` binary (`run`/`runWithStdin` helpers, `:108-170`ish) against scripted fake HTTP providers (`internal/e2e/fake/fake.go`; `providerEnv` `:118-119`). Coverage: prompt-mode, REPL, env/flag precedence, wire auto-detection (`GENIE_WIRE=anthropic/responses/google`, `:1225-1274`), session persistence, stdin piping, debug/verbose, context window/budget probing, tool calls.
- **The only plugin-related e2e** is `TestOGProviderEnvRoutesThroughPlugin` (`e2e_test.go:1414-1427`): it sets `GENIE_PROVIDER=copilot` with **no plugins installed** and asserts a clean failure mentioning `"provider"` (`assertCleanFailure(t, ..., "provider")`, `:1423-1426`). The comment at `:1423-1424` states the intent: "GENIE_PROVIDER would normally name a loaded plugin, but with no plugins installed it should error."
- **The real bedrock/copilot binaries are never exercised** by `internal/e2e`. No e2e test installs a plugin into a discovery dir.

### Other tests referencing plugins

- `internal/plugin/manager_test.go` (fake **bash-script** plugins speaking NDJSON to `jq`):
  - `TestManagerLoadPlugins` (`:105`) — `tools` capability, tool registration + tool call round-trip.
  - `TestManagerLoadWirePlugin` (`:251`) — a fake plugin **named `copilot`** declaring `wires:true`; asserts `wire/init`, `wire/list_models` (2 models), `wire/stream` round-trips (`:306-322`).
  - `TestManagerLoadDirectoryPlugin` (`:325`) — directory layout discovery + manifest.
  - Manifest parsing/validation (`:13-76`, `:409-480`), codec round-trip (`:78-103`), protocol & capabilities validation (`:186-234`), error/success response builders (`:236-249`).
- `internal/plugin/context_e2e_test.go` — bash plugin declaring all four context hooks; asserts the seam invokes `before_request`/`after_response`/`compact`/`condense` and degrades gracefully on hook failure (`:106-176`).
- `protocol/generate_test.go` and `protocol/generate_compare_test.go` — see §3; the compare test is the only place the plugins' shared package is cross-checked against the codegen output.
- `internal/config/config_test.go` — `TestPluginDirDerivedFromConfigDir` (`:646-671`) asserts the `~/.config/genie/plugins` default and the `GENIE_CONFIG_DIR`-based derivation; config-dir env override at `:660-671`.
- There are **no test files inside `plugins/`** (`plugins/bedrock`, `plugins/copilot`, `plugins/shared` each contain a single source file; verified by directory listing). The plugins' protocol behavior and HTTP/SigV4/SSE code are therefore untested in-repo.

---

## Open questions / ambiguity to resolve in the spec

1. **Source of truth for the protocol package.** `protocol/schema.yaml` ("source of truth", vendored as `internal/wireplugin` per `schema.yaml:2-3`) and `plugins/shared/protocol.go` have diverged: schema has `tools/list`+`tools/call` constants and named error codes but no `ModelDef.ContextWindow`; shared has `ContextWindow` but no tool constants/named codes. The AST compare test (allowlist-style) does not catch this.
2. **Testing the extracted plugins.** Neither plugin currently has unit tests, and e2e never runs a real plugin. Extraction specs likely need new SDK-level or e2e harnesses.
3. **`HasAny()` and host-side-only APIs** (`internal/plugin/protocol.go`'s `PresenceMask`, `Validate`, error vars) are not part of the generated package; plugins built on the generated SDK can't cross-check `ProtocolVersion` beyond what the template emits.
4. **Config files are read from the executable's own directory** (`bedrock main.go:109-128`, `copilot main.go:104-123`), matching the documented `~/.config/genie/plugins/<name>/config.toml` layout; an external repo must keep this "config next to binary" contract or the host must start passing config another way (`wire/init` currently sends `{"config": {}}`; `manager.go:315`).