# Research: Plugin Protocol Extension for Lifecycle Hooks

**Ticket:** og-cbu.2
**Branch:** research/og-cbu.2-protocol-extension
**Date:** 2026-09-03
**Scope:** RESEARCH ONLY — no code changed during this investigation.

## Summary

This traces how the existing plugin protocol carries a new *context lifecycle
hook* type, using `context/condense` (the last hook added) as the worked
example, and answers three questions: (1) how one hook flows end-to-end through
every file, (2) the exact mechanical checklist a new hook must touch, and (3)
how the two hook topologies (ordered chain vs. single-active) are encoded and
what widening to fan-out would require. It then separates what is
code-generated from `protocol/schema.yaml` vs. what is hand-written, so the
marginal cost of a new hook type is explicit.

The plumbing that carries a new lifecycle hook is **generic** and already built.
Adding a new hook type is mostly repeating the `CondenseHook` pattern in a small
number of hand-written adapter sites plus one generated-SDK block in the schema.
The end-to-end hook mechanism is in place: capability declaration → seam
resolution → dispatch → wire RPC → generated plugin-SDK callback.

---

## 1. End-to-end trace: adding `context/condense`

`context/condense` was added alongside `context/compact` in the same change
(commit `47a3709`, "plugin context seam: granular context hooks over
llm.Client"). Because compact and condense are structurally identical
(single-active request-rewriting hooks), the diff for condense mirrors compact
field-for-field. This section walks each file in the order the data flows when a
plugin dispatches a condense.

### 1.1 `internal/plugin/protocol.go` — the host-side wire contract

All host-side protocol vocabulary lives here (hand-written; more on that in §4).

- **Method constant.** `protocol.go:23`:
  ```go
  MethodContextCondense = "context/condense"
  ```
  grouped with the other `context/*` method constants at `protocol.go:20-23`.

- **Capability flag.** `protocol.go:85` declares `CondenseHook bool
  \`json:"context_condense"\`` inside the `Capabilities` struct (`protocol.go:76-87`).
  The granular flags exist precisely so a plugin participates in one seam without
  faking unrelated ones (comment at `protocol.go:80-81`).

- **Aggregation + validation.** The flag is folded into:
  - `HasAny()` (`protocol.go:91-93`) — a plugin declaring *only* condense still
    passes the "declares at least one capability" guard;
  - the presence bitmask `PresenceCondense` (`protocol.go:99-107`, set in
    `Mask()` at `protocol.go:130-132`) — lets a peer detect the seam without
    re-negotiating the protocol;
  - `Validate()` (`protocol.go:266-274`) — unchanged; it only checks protocol
    version and `HasAny`, so a lone condense flag is valid.

- **Wire request/result types.** The condense RPC reuses the shared
  `ContextRequest` (`protocol.go:196-200`) and `ContextMessage`/`ContextToolCall`
  helpers (`protocol.go:179-191`) as its params, and defines a dedicated result:
  `ContextCondenseResult` (`protocol.go:234-236`) with a single `Request` field.
  It is byte-identical to `ContextCompactResult` (`protocol.go:230-232`) and
  `ContextBeforeRequestResult` (`protocol.go:204-206`).

- **Conversion helpers.** `toContextRequest` (`protocol.go:300-314`) converts an
  `llm.Request` to the wire shape handed to a hook; `fromContextRequest`
  (`protocol.go:318-331`) converts a hook-returned wire request back to
  `llm.Request`. These are shared by every request-rewriting hook (before/compact/
  condense), so condense adds no new conversion code.

### 1.2 `internal/plugin/manager.go` — the RPC round-trip

The generic RPC machinery lives here (hand-written).

- **`callContext`** (`manager.go:581-622`) is the shared, mutex-holding,
  timeout-guarded JSON-RPC round-trip used by all hook invocations. It returns
  the raw `json.RawMessage` result for the caller to decode.

- **`callContextRewrite`** (`manager.go:632-642`) is the shared decode/shape
  helper for request-rewriting hooks (before/compact/condense). It calls
  `callContext` with `toContextRequest(req)`, unmarshals into
  `ContextBeforeRequestResult`, and converts back via `fromContextRequest`. The
  comment at `manager.go:628-631` notes all three named result types are
  identical, so **one** decode serves all three — a new request-rewriting hook
  reuses this method unchanged.

- **Public dispatch method** `CallContextCondense` (`manager.go:680-682`):
  ```go
  func (p *Plugin) CallContextCondense(ctx context.Context, req llm.Request) (llm.Request, error) {
      return p.callContextRewrite(ctx, MethodContextCondense, "context/condense", req)
  }
  ```
  This is the **only** condense-specific line in `manager.go`. The label string
  is used only for error/degradation messages.

### 1.3 `internal/plugin/context.go` — the seam resolution + dispatch

This is where the hook's *topology* is decided (hand-written).

- **`ContextSeam` struct** (`context.go:41-47`) carries the resolved registrants:
  ```go
  type ContextSeam struct {
      before    []*Plugin
      after     []*Plugin
      compact   *Plugin
      condense  *Plugin
      onDegrade func(msg string)
  }
  ```
  Before/after are **slices** (ordered chain); compact/condense are single
  **`*Plugin`** pointers (single-active). `condense` is a new field.

- **`ContextConfig`** (`context.go:27-37`) gains `ActiveCondense string`, the
  explicit config choice for the seam. (`ActiveCompact` is the sibling;
  `Order []string` drives the ordered chains.)

- **Resolution in `NewContextSeam`** (`context.go:56-75`): after building the
  ordered `before`/`after` chains via `orderedPlugins` (`context.go:80-100`),
  it resolves the single-active seams:
  ```go
  if seam.condense, err = resolveSingleActive(byName, plugins, cfg.ActiveCondense,
      "condense", func(p *Plugin) bool { return p.Capabilities.CondenseHook }); err != nil {
      return nil, err
  }
  ```
  (`context.go:70-72`).

- **`resolveSingleActive`** (`context.go:109-144`) is the shared single-active
  resolver whose semantics are detailed in §3. It takes a `pred` filtering by
  the capability flag — condense passes `Capabilities.CondenseHook`.

- **Builtin marker methods.** `CondenseBuiltin()` (`context.go:188`) mirrors
  `CompactBuiltin()` (`context.go:185`), both reporting `s.<field> == nil`. This
  is how the `ContextManager` learns whether to run its own built-in condenser
  or delegate to the selected plugin (see §1.4).

- **Dispatch method** `Condense` (`context.go:202-207`):
  ```go
  func (s *ContextSeam) Condense(ctx context.Context, req llm.Request) (llm.Request, error) {
      if s.condense == nil {
          return req, nil
      }
      return s.condense.CallContextCondense(ctx, req)
  }
  ```
  When no plugin is active (built-in default), it passes the request unchanged so
  the ContextManager runs the built-in. Otherwise it forwards to
  `CallContextCondense`.

### 1.4 `internal/contextmgr/contextmgr.go` — where the seam is dispatched per-Stream

- **`Hooks` interface** (`contextmgr.go:33-53`) is the plugin seam's contract. It
  grows a `Condense` method (`contextmgr.go:50-52`):
  ```go
  // Condense narrows history (e.g. tool-output reduction); it is
  // single-active like Compact. Return req unchanged when not active.
  Condense(ctx context.Context, req llm.Request) (llm.Request, error)
  ```
  The interface doc comment (`contextmgr.go:29-32`) states every operation may be
  absent (nil Hooks is a no-op).

- **`singleActiveSeam`** (`contextmgr.go:61-64`) is the optional capability that
  lets the ContextManager ask the seam whether the built-in is the active
  registrant. It gains `CondenseBuiltin() bool`. A `Hooks` that does not
  implement it is treated as supplying its own implementations.

- **`builtInCondenseActive`** (`contextmgr.go:272-280`) reports whether the
  built-in condenser is active (nil hooks → true; else the seam's
  `CondenseBuiltin()`).

- **Dispatch in `Stream`** (`contextmgr.go:191-221`): the pipeline is
  ```
  injectHistory → runChain(before_request) → compactStep → condenseStep → inner.Stream → after_response
  ```
  at `contextmgr.go:196-197`. `condenseStep` (`contextmgr.go:297-304`) is the
  condense-specific dispatch: if the built-in is active it calls the internal
  `builtinCondense` (`contextmgr.go:572-596`); otherwise it runs the seam via
  `runChain(ctx, req, "condense", func(r) { return m.hooks.Condense(ctx, r) })`.
  `runChain` (`contextmgr.go:228-238`) degrades gracefully on failure
  (contribution skipped, request proceeds) via `degrade` (`contextmgr.go:243-249`).

### 1.5 `protocol/schema.yaml` — capability + generated-SDK declaration

`schema.yaml` is the source of truth for the generated plugin SDK (what a *plugin
author* consumes). Condense is declared in four places:

- **Method constant** (`schema.yaml:30-31`):
  ```yaml
  - name: MethodContextCondense
    value: "context/condense"
  ```
  (with `MethodContextCompact` at `schema.yaml:28-29`).

- **Capability flag** in the `Capabilities` type (`schema.yaml:112-114`):
  ```yaml
  - name: CondenseHook
    json: context_condense
    type: bool
  ```

- **Result wire type** `ContextCondenseResult` (`schema.yaml:238-242`), which the
  generated handler writes in its dispatch case.

- **Handler SDK** — the `handler` block declares the callback field and setter:
  - struct field `onCondense` (`schema.yaml:263-266`);
  - method `OnCondense` (`schema.yaml:305-309`);
  - the `dispatch` case `MethodContextCondense → "call onCondense, write
    ContextCondenseResult"` (`schema.yaml:372-373`).

Regenerating the SDK emits the `OnCondense` callback (§1.6). `generate_test.go`
asserts the generator stays in sync (e.g. `TestGenerateMethodConstants`,
`generate_test.go:47-69`, checks `MethodContextCondense`; the handler test at
`generate_test.go:113-139` checks `OnCondense`).

The generator itself (`protocol/generate.go`, hand-written once) needs **no
change** for a new hook — it just reads the schema YAML into `Schema` structs and
executes the template. See §4.

### 1.6 `protocol/wireplugin.go.tmpl` — the generated plugin SDK callback

The template renders the `Handler` that plugin authors link against. For condense:

- The `struct` field is emitted at `wireplugin.go.tmpl:41-44` (range over
  `Handler.Struct.Fields`, so `onCondense` appears automatically).
- The setter `OnCondense` is generated in the method loop; the special case that
  assigns the callback is at `wireplugin.go.tmpl:54-71`, specifically `{{
  else if eq .Name "OnCondense" }} h.onCondense = fn` at `wireplugin.go.tmpl:68-69`.
- The dispatch case is in the `handleRequest` switch (`wireplugin.go.tmpl:92-196`).
  The `onCondense` branch (`wireplugin.go.tmpl:169-184`) is emitted from the
  dispatch action string `"call onCondense, write ContextCondenseResult"`:
  nil-check the callback, `ParseParams[ContextRequest]`, call it, wrap in
  `ContextCondenseResult{Request: out}`.

A plugin author's consumption of the SDK looks like:
```go
h := wireplugin.NewHandler(wireplugin.Capabilities{CondenseHook: true, Version: 1})
h.OnCondense(func(request wireplugin.ContextRequest) (wireplugin.ContextRequest, error) {
    // narrow tool outputs ...
    return request, nil
})
h.Run()
```

### 1.7 Host wiring: `cmd/genie/main.go`

The seam is assembled against `[context.plugins]` config and attached to the
ContextManager. Only the *existing* fields are wired here; a new hook needs one
mapping (see §2, file 8):
- `main.go:248-252` builds `plugin.NewContextSeam(pluginMgr.PluginsInOrder(),
  plugin.ContextConfig{Order: cfg.Context.PluginsOrder,
  ActiveCompact: cfg.Context.ActiveCompact, ActiveCondense:
  cfg.Context.ActiveCondense}, degradeFn)`.
- `main.go:263` attaches it via `contextmgr.WithHooks(ctxSeam)`. Plugins declare
  hooks in `Capabilities` and `loadPlugin` already parsed them during handshake
  (`manager.go:273-280`); no discovery change is needed.

---

## 2. Checklist: files a new lifecycle hook must touch

Adding a new context hook type `context/foo` (single-active or chain) requires
touching these 9-10 sites. Files marked **[generated]** are fully regenerated
from `schema.yaml`; everything else is hand-written. The `[generated+sync]`
file is the sync test that forces the SDK and host protocol to stay aligned.

| # | File | What you add | Topology note |
|---|---|---|---|
| 1 | `internal/plugin/protocol.go` | `MethodContextFoo` const (`:20` block); `FooHook bool` capability (`Capabilities`); add to `HasAny()`; add `PresenceFoo` bit + set in `Mask()`; a `ContextFooResult`/params type if not reusing `ContextRequest` (else just the result); reuse `toContextRequest`/`fromContextRequest` | Single-active & chain both |
| 2 | `internal/plugin/manager.go` | `CallContextFoo` public method delegating to `callContextRewrite` (reuse) — or a bespoke call if the hook isn't request-rewriting (e.g. like `CallContextAfter`) | Reuse `callContext` always |
| 3 | `internal/plugin/context.go` | `ContextSeam`: if single-active add a `foo *Plugin` field; if chain add `foo []*Plugin`. `ContextConfig`: add `ActiveFoo` (single-active) and/or fold into `Order` (chain). In `NewContextSeam`: `orderedPlugins` for a chain (`:80`), `resolveSingleActive` for single-active (`:109`). Add `FooBuiltin()` marker if single-active with a built-in. Add `Foo` dispatch method (`:202` pattern) | Chain vs single-active diverge here |
| 4 | `internal/contextmgr/contextmgr.go` | Add `Foo` method to `Hooks` interface (`:33`); if single-active add `FooBuiltin()` to `singleActiveSeam` (`:61`), a `builtInFooActive()` helper, and a `fooStep` invoked in `Stream` (`:191-198`) | Both |
| 5 | `protocol/schema.yaml` **[generated]** | Method const (`:11-35`); capability field in `Capabilities` type (`:92-117`); result/params type if new (`types:`); handler struct field (`:244-266`); `OnFoo` method (`:268-309`); dispatch case (`:357-379`) | Both |
| 6 | `protocol/wireplugin.go.tmpl` **[generated, NO edit needed]** | Nothing — the loop over handler fields/methods/dispatch renders `onFoo`/`OnFoo`/case from the schema automatically. Only add a hand edit if a *new call shape* needs a distinct template branch | — |
| 7 | `protocol/generate_test.go` **[generated+sync]** | Add assertions (method const, type, handler `OnFoo`, dispatch case) so the schema/SDK sync is enforced | Both |
| 8 | `cmd/genie/main.go` | If single-active, map `cfg.Context.ActiveFoo` into `plugin.ContextConfig` (`:248-252`); if the hook needs a new config knob, add it in `internal/config/config.go` + `config_test.go` | Single-active only |
| 9 | `docs/plugin-protocol.md` | Document the new method (params/result/semantics), the capability JSON key, and the topology | Both |
| 10 | Tests | `internal/plugin/context_test.go`, `context_e2e_test.go`, `internal/contextmgr/contextmgr_test.go` — resolution, conflict, E2E dispatch, degradation | Both |

Steps, in working order:

1. Declare the method constant, capability flag + presence bit, and wire result
   type in `internal/plugin/protocol.go` (file 1).
2. Regenerate/declare the SDK side in `protocol/schema.yaml` (file 5) and run the
   generator; add the sync test cases (file 7).
3. Add the RPC caller `CallContextFoo` in `internal/plugin/manager.go` (file 2).
4. Add the seam resolution + dispatch in `internal/plugin/context.go` (file 3).
5. Extend the `Hooks` seam + Stream pipeline in `internal/contextmgr/contextmgr.go`
   (file 4).
6. Wire config → seam in `cmd/genie/main.go` (+ `internal/config`) (file 8).
7. Document in `docs/plugin-protocol.md` (file 9).
8. Add unit + E2E tests (file 10), then `go build ./...` and
   `go test ./internal/plugin/... ./internal/contextmgr/...`.

---

## 3. Topology encoding: chain vs. single-active

The seam encodes two distinct hook topologies; a new hook type must pick one.

### 3.1 Ordered chain (before_request, after_response)

- **Storage:** `[]*Plugin` slices — `s.before` / `s.after` (`context.go:42-43`).
- **Resolution:** `orderedPlugins` (`context.go:80-100`) filters plugins by the
  capability predicate, then orders them: names listed in `cfg.Order` first in
  order, then any unlisted declarants in registration (discovery) order. Default
  (empty order) is pure registration order. Discovery order is captured in
  `manager.go:131-133` (`m.pluginOrder`) and exposed via `PluginsInOrder`
  (`manager.go:697-707`).
- **Invocation:** each hook sees the *previous hook's result* — e.g. `BeforeRequest`
  (`context.go:157-168`) threads `cur := req` through `s.before`, feeding each
  plugin's output into the next. `AfterResponse` (`context.go:172-179`) iterates
  the chain over a shared usage value.
- **Failure:** a failing hook is skipped (its contribution dropped, prior
  contributions kept) and surfaced via `degrade` (`context.go:211-217`); the
  request proceeds.

### 3.2 Single-active (compact, condense)

- **Storage:** a single `*Plugin` pointer (`s.compact` / `s.condense`,
  `context.go:44-45`). `nil` means the built-in is the active registrant.
- **Resolution:** `resolveSingleActive` (`context.go:109-144`) with a capability
  predicate. The decision tree:
  - **Explicit config choice** (`active_compact`/`active_condense`) wins; errors
    if the named plugin is absent (`context.go:124-127`) or lacks the capability
    (`context.go:128-131`).
  - **`BuiltinName` ("builtin")** explicitly chooses the harness's own
    implementation, ignoring plugin declarants with an info log (`context.go:117-122`);
    returns `nil, nil`.
  - **Zero declarants** → built-in default (`context.go:134-137`).
  - **One declarant** → it (`context.go:138-139`).
  - **Several declarants with no explicit choice** → **hard startup error** naming
    the seam and requiring the explicit choice (`context.go:140-143`).
- **Invocation:** `Compact`/`Condense` (`context.go:193-207`) returns the request
  unchanged when `s.<field> == nil` (built-in active); otherwise forwards to the
  selected plugin via `CallContext*`.
- **Builtin hand-off:** the `singleActiveSeam` capability (`contextmgr.go:61-64`)
  + `CompactBuiltin`/`CondenseBuiltin` (`context.go:185/188`) tell the
  `ContextManager` whether to run its own built-in (`builtInCondenseActive`,
  `contextmgr.go:272-280`; the built-in body at `contextmgr.go:572-596`) or
  delegate to the seam's plugin.

The purpose of the hard-error-on-multiple design is that a request-rewriting
seam is *exclusive*: two plugins summarising or condensing the same history
would double-apply, so the harness refuses to guess and forces an explicit pick.

### 3.3 Widening to fan-out (multiple handlers per event)

Fan-out would give **N handlers per event, each receiving the same original
input** (a broadcast), versus the existing chain's sequential
result-threading.

What fan-out requires beyond today's chain:

1. **Semantics differ from the chain.** Today's before/after `[]*Plugin` is a
   *pipeline* (each sees the previous result). Fan-out is a *broadcast* (each
   sees the original input and contributes independently). They are not the same
   `[]*Plugin` slice — fan-out needs its own resolution + invocation.
2. **Merge policy is undefined.** When each handler rewrites the same request,
   the host must decide how to combine outputs: first-wins, last-wins,
   per-field merge (e.g. each handler "claims" a tool id), or treat each as a
   pure observer. Condense's whole point is narrowing *different* tool outputs,
   which *suggests* a per-`ToolCallID` merge — but no merge policy exists today,
   and the code has no such structure.
3. **New resolution.** A fan-out set is neither the ordered chain nor
   single-active. It likely mirrors `orderedPlugins` (a filtered slice) but
   could reuse the explicit `Order` config; it does **not** reuse
   `resolveSingleActive` (no exclusivity). If it also supports a built-in
   registrant, the `singleActiveSeam` marker pattern would need a sibling.
4. **Wire shape is unchanged.** The RPC (`callContext`/`callContextRewrite`) and
   generated SDK already support any number of callers — the host just calls them
   in a loop. So fan-out is a **host-side seam/merge** concern, not a protocol or
   SDK change.
5. **Cross-cutting cost.** The `Hooks` interface, `ContextSeam` storage, config
   parsing, tests, and the merge logic all change. This is meaningfully more
   work than adding another single-active or chain hook.

---

## 4. Generated vs. hand-written: the cost per new hook

### What is code-generated (from `schema.yaml`, via `protocol/generate.go` + `wireplugin.go.tmpl`)

The **plugin-author SDK** is the generated surface. `protocol/generate.go:104-141`
reads the YAML into typed structs (`:26-102`) and `generate()` (`:143-166`)
executes `wireplugin.go.tmpl` (embedded, `:21-22`) with helper funcs
(`:149-156`). The generated output is a standalone `wireplugin` package a plugin
vendors as `internal/wireplugin/`. From schema.yaml it generates (per §1.5/§1.6):

- method constants, error codes, and every `types:` struct (`wireplugin.go.tmpl:16-37`);
- the `Handler` struct (`:40-44`), `NewHandler` (`:47-53`), the `On*` setter
  methods (`:54-72`), `Run` (`:74-90`), and the `handleRequest` dispatch switch
  (`:92-196`) with each method case, plus the write helpers and `ParseParams`
  (`:198-233`).

Adding a hook here is a **schema-only** change (file 5 in §2): one capability
field, one constant, one result type, one handler field, one `On*` method, one
dispatch case. `wireplugin.go.tmpl` needs **no hand edit** for a hook whose
call shape matches the existing `ContextRequest → ContextRequest` (or
params/result) patterns, because the template ranges over the schema lists.

### What is hand-written

All host-side plumbing and the seam ($1.1–§1.4, files 1–4 & 8) is hand-written
Go. The generator does **not** produce host-side code; it only produces the
plugin SDK. The hand-written pieces per new hook are:

- `protocol.go`: constant + flag + presence bit + result type + `HasAny`/`Mask`
  folding (~6 small edits).
- `manager.go`: one `CallContextFoo` method (~3 lines, reusing
  `callContextRewrite`).
- `context.go`: seam field + config + resolution call + `FooBuiltin` marker +
  `Foo` dispatch (~15 lines).
- `contextmgr.go`: `Hooks` method + `singleActiveSeam` marker + `builtInFooActive`
  + `fooStep` dispatch in `Stream` (~15 lines).
- `main.go` (and, if a new config knob, `config.go`): one mapping line.
- Tests: resolution + E2E + degradation coverage (required by the 80% rule).

### Cost verdict

For a **request-rewriting hook reusing `ContextRequest`** (the common case, e.g.
a new compact-style hook), the marginal cost is: ~6 small hand edits in
`protocol.go`, a 3-line method in `manager.go`, two small hand-written dispatch
blocks in `context.go` + `contextmgr.go`, one schema block + regeneration, one
config mapping, docs, and tests. The template needs no edit. Nearly all
infrastructure (RPC, conversion, degradation, resolution helpers, generated SDK)
is reused.

A hook with a **non-request-rewriting** shape (like `after_response`, which
carries usage params and never rewrites history) needs `callContext` +
a bespoke params/result type + a bespoke template branch, i.e. more hand-work,
but still no change to the transport or generator.

---

## Appendix: worked-example commit shape

The change that added the granular context hooks (incl. condense) touched 20
files (`git show --stat 47a3709`): `cmd/og/main.go`, `docs/plugin-protocol.md`,
`internal/config/config.go` + test, `internal/contextmgr/contextmgr.go` + test,
`internal/plugin/{context,manager,protocol}.go` + tests, `plugins/*/main.go`,
`plugins/shared/protocol.go`, and `protocol/{generate_compare_test,schema.yaml,
wireplugin.go.tmpl,generate_test}.go`. (The in-repo `plugins/*` + `shared/`
were later extracted per `standalone-plugin-extraction-audit.md`; the protocol
renumbered to v1 in `80e2d4e`, so the live `Capabilities.Version` is 1, matching
`schema.yaml` `protocol.version: 1` and `WireInitResult` at `schema.yaml:119-123`.)
```
