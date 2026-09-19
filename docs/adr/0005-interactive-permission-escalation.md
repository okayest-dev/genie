# Interactive per-axis permission escalation across all tools

Status: accepted. This is the terminal artifact of the wayfinder map og-73l (tickets og-73l.1 through og-73l.6) plus the Sandboxed Code-as-Action map og-3z5 where its design carries over. It is an implementation-ready spec: a cold read must let an implementer build the whole flow without further design decisions.

Genie today has no permission system. The only gate is `tools.Confirmer` — a boolean interface hard-wired to `tools.AutoDeny` on bash (every call) and write (overwrites only); reads and edits are ungated (`internal/tools/confirm.go`, `cmd/genie/main.go:388-391`). This ADR replaces that with a per-axis permission model that generalizes across every tool: when a tool call needs access the effective policy does not already cover, the streaming turn pauses and the harness negotiates with the user inline, one axis at a time, then either runs the call or sends the model a composite grant/reject result. The model never sees tiers, never sees base-vs-acquired, and learns its effective set from results only.

## The effective-policy store

New package `internal/permissions`, greenfield (no permission code exists today). The store holds **tier-tagged grants**, so once-tier eviction, session-end discard, and permanent persistence all key off the tag:

```
type Axis = string   // read | write | net | run | env

type Tier = string   // "once" | "session" | "permanent"

type Grant struct {
    Axis  Axis
    Scope string // normalized expression; "" = blanket/axis-only
    Tier  Tier
}

type Store struct { ... }
```

- **Effective policy** = base ∪ permanent ∪ session ∪ single-call.
  - **base**: the resolved config `[permissions]` section (global, or a per-agent override — see config below). Modelled as per-axis scope lists.
  - **permanent**: `[[permissions.permanent]]` entries loaded at startup.
  - **session**: grants the user made "session" tier; in-memory, discarded at session end (session ends when the REPL exits / the `-p` run finishes).
  - **single-call**: grants the user made "once" tier, bound to the tool call that negotiated them; see lifecycle below.
- **Coverage check.** A requested `(axis, scope)` is covered when any grant/scope in the effective policy matches it under the scope-matching semantics below. `""` (blanket) requests are covered only by a blanket grant.
- **No provenance for the model.** The model-facing view is a flat per-axis list of covered scopes. No view ever labels an entry base/permanent/session/once, and no view ever carries a tier.

### Single-call (once) tier lifecycle

Call-bound. `once` grants are tagged with the tool call ID that negotiated them; a grant is:

- spent (removed, silently) when that call resolves on all axes — after it executes;
- discarded (removed, silently) when the call is denied overall;
- never carried forward to a later call.

Cleanup is silent: spent `once` grants are never narrated to the model (og-73l.4 "spent once-tier grants silent").

### Scope matching

Most-specific wins. Exact > prefix > wildcard; longest suffix for wildcards.

- **Filesystem paths (read/write):** prefix only. `/app` covers `/app/src/file.go`. No glob patterns.
- **Network hosts (net):** wildcard subdomains. `*.github.com:443` suffix-matches `api.github.com:443`; a bare hostname is exact.
- **Executables (run):** exact only. `git` or `/usr/bin/git` — no directory prefixes.
- **Environment variables (env):** exact only. `DB_HOST`, never `DB_*`.

Scopes are **normalized** when they enter the store: relative file paths resolve against the working directory; a host without a port is kept as-is (exact match); hosts/executables/env names keep their literal form. The normalized expression is what grant/reject feedback lines quote.

## Config surface and reconciliation

Carries og-3z5.4's design unchanged apart from the reconciliations below.

```toml
# config.toml
[permissions]
read  = ["."]          # restrictive default when the section is absent
write = []
net   = []
run   = []
env   = []

[[permissions.permanent]]
permission = "read"
scope      = "/etc"
granted    = 2026-09-10T12:00:00Z   # written by the harness on a permanent grant

[[permissions.permanent]]
permission = "net"
scope      = "api.openai.com:443"
granted    = 2026-09-10T14:30:00Z
```

- **Restrictive no-config default.** No `[permissions]` section → `read=["."], write=[], net=[], run=[], env=[]`. The tier prompt **supersedes** today's Confirmer gates (bash every-call, write-overwrite); the `tools.Confirmer` seam and its `AutoDeny` wiring **retire** (deleted).
- **Per-agent.**
  ```toml
  # agents/ops.toml
  [permissions]
  read = ["/app", "/etc"]
  net  = ["api.openai.com:443"]
  ```
  A per-agent `[permissions]` section **replaces the whole global base** (not merge); an unnamed axis is left **empty** — no axis-level inheritance. Permanent is always additive over either base. This matches AgentDef's `tools` replace-not-merge, already in code (`internal/config/agent.go`).
- **Persistence.** A "permanent" tier grant (a) appends a `[[permissions.permanent]]` entry to `config.toml` via the TOML library preserving existing structure, and (b) is added to the in-memory effective policy immediately. Permanent entries are loaded at startup. **No dedup/compaction at load** — duplicates are harmless because the coverage intersection is idempotent; manual config edits are always valid, and editing config is the only revoke mechanism. No separate audit log: the session transcript plus `granted` timestamps in the config are the audit trail.

### Agent instruction snapshot

The instruction carried to the model each turn gains two pieces (og-73l.4), rendered into the instruction assembled by `internal/instruct`:

1. **Session-start snapshot of the resolved base policy**, flat and provenance-free, one bullet per axis. Empty axes say "nothing is authorized":
   ```
   Current permissions:
   - read: ., /etc
   - write: nothing is authorized
   - net: api.openai.com:443
   - run: nothing is authorized
   - env: nothing is authorized
   ```
   The snapshot lists the base scopes from the resolved config only. Permanent/session/once grants are intentionally absent — the model learns them from results, preserving base-vs-acquired invisibility.
2. **The mechanism, stated tersely with zero tier vocabulary**: denied calls pause for negotiation returning `Permission granted:` / `Permission rejected:` result lines; the scope-matching semantics (prefix for paths, wildcard subdomains for hosts, exact for executables and env vars) so granted scopes can be reused; `request_permission` as the pre-negotiation fallback; re-ask allowed only after a materially different alternative.

## The escalation gate at the deny point

The deny point is the tool-execution boundary in `agent.RunTurn`'s tool loop (`internal/agent/agent.go:290`), before `tool.Execute`. Insert a pre-execution gate driven by the effective-policy store:

1. **Declare requirements.** Each bundled tool implements a `Permissioned` interface:
   ```go
   type Requirement struct {
       Axis  string // read|write|net|run|env
       Scope string // normalized where determinable; "" = blanket/best-effort unknown
   }

   type Permissioned interface {
       RequiredPermissions(args json.RawMessage) ([]Requirement, error)
   }
   ```
   - `read`/`write`/`edit`: the path argument, on the read/write (and edit: read+write) axis/axes.
   - `bash`: best-effort extraction from the command — the leading executable on `run`, URLs on `net` (host[:port] where parseable). When a scope cannot be determined, the requirement stays axis-only (`Scope: ""`).
   - Tools that do not implement `Permissioned` are ungated: their calls run without escalation (today that means plugin tools).
2. **Evaluate.** Required set `R` = the call's requirements minus those already covered by the effective policy. Empty `R` → execute immediately, no prompts, no feedback lines.
3. **Negotiate.** Render one prompt per uncovered requirement (see renderer). Any grants enter the store immediately — session/permanent persist across the call; once is tagged with the call ID.
4. **Execute or deny.** Every requirement granted → run `tool.Execute` and append the tool result. Any requirement rejected → **do not execute**; append the composite denied result (below). The call consumes/discards its once-tier grants per the lifecycle above either way.
5. **Mid-call NotCapable** is **model-reported only** (og-73l.3): a runtime permission failure inside a tool (the code tool's structured Deno error) never triggers inline escalation at the denial point — execution may have partially run. The harness recognizes the NotCapable marker in the tool result and rewrites it to the pinned status/hint form; the model decides whether to follow up with `request_permission` or reformulate.

The gate is injected into `RunTurn` via a functional option (a `permissions.Gate` carrying the store + a negotiator), keeping the agent package plugin-free like the existing `agent.Hooks` seam. The REPL wires the interactive negotiator; the `-p` path wires the auto-deny (or `--approve-all`) negotiator.

## The prompt renderer contract

Winner of the og-73l.2 human-reaction prototype (framing A — terse aider-style; framings B banner and C prose rejected). Canonical-mode line input, no raw TUI.

- **One line per axis, always showing the axis; scope appended when present.**
  ```
  allow net api.github.com:443? (o)nce/(s)ession/(p)ermanent/(r)eject: 
  allow net? (o)nce/(s)ession/(p)ermanent/(r)eject: 
  ```
- **Keys**: `o`/`s`/`p`/`r` (full words `once`/`session`/`permanent`/`reject` also accepted). Unknown input prints `:: unknown choice - o/s/p/r` and re-prompts (loops). All four options are always shown, uniformly.
- **Tiers** once / session / permanent, plus reject; the user chooses, never the model.
- **Ordering**: the chain iterates uncovered requirements in the fixed axis order read → write → net → run → env; within an axis, in declaration order.
- **Denial does not stop the chain** (og-73l.3): the user judges each axis independently, all grants from the chain persist, and the call runs only if every required axis is granted.
- **No bundled grants**: one dialog per requirement; a grant covers exactly the requested (normalized) scope.
- **^C rejects the current axis** — not a no-op, not a turn cancel. While a prompt is active the escalator must own SIGINT delivery so the REPL's turn-cancel does not fire; the chain continues to the next axis.
- **Resume-on-next-line**: after the user's Enter resolves the prompt, the streaming turn resumes on the next line.
- **stdout-only**: dialog text goes to the terminal, never into the session transcript or model history.

## Model-facing feedback

One **composite result per escalated call**, riding the existing tool-result channel (`RoleTool` messages, as `agent.go` already appends them). No new wire/event messages — event-style `RoleTool` messages are silently dropped on the anthropic/bedrock/google/responses wires, which match tool results by `RoleUser` + `ToolCallID`.

- **Call executed**: grant lines (newly-negotiated axes only, fixed axis order, normalized granted scope) followed by the tool output. No status line.
  ```
  Permission granted: write /tmp
  <tool output>
  ```
- **Call denied**: grant lines then reject lines, then the status + hint pair. No re-listing summary block.
  ```
  Permission granted: net api.github.com:443
  Permission rejected: run python3 — consider an alternative
  status: call not executed
  hint: granted axes remain available — reformulate without the denied axis.
  ```
- **Grant line**: `Permission granted: <axis> <scope>` — only the newly-negotiated axes, never already-permitted axes, never a tier. Scope text omitted (axis-only) for a blanket grant.
- **Reject line**: `Permission rejected: <axis> <scope> — consider an alternative` — the constant hint, no captured user reason, same shape single- and multi-axis.
- **Spent once-tier grants stay silent** — durability is discovered by re-prompting, never narrated.
- **Mid-call NotCapable** (model-reported):
  ```
  status: call not executed — net unavailable at runtime
  hint: inline escalation is not available mid-execution — request net access in advance via request_permission, or reformulate
  ```
  Guides, doesn't mandate: `request_permission` is named as the pre-negotiation route, reformulation is left open.

## request_permission generalization

The code-tool-only tool generalizes to all tools, schema carrying over untouched (`{ permission: enum[read|write|net|run|env], scope?: string }`, `required: ["permission"]`), one axis per call. A multi-axis need = several calls in one turn, each chained through the same prompt renderer. `scope` omitted = blanket access.

- **Description** (text embedded so the model reads the rule from the tool itself):
  > Ask the user for permission ahead of a call you expect to be denied, or after a mid-call runtime permission denial. Otherwise call the tool directly — a denied call is escalated inline without this tool. A grant covers exactly the scope you request, so request your widest anticipated need. The user decides; the result is a "Permission granted:" or "Permission rejected:" line. On rejection, pursue an alternative before re-requesting.
- **`permission`**: "Permission axis to escalate: read, write, net, run, or env."
- **`scope`**: "Resource scope per axis — filesystem path (read/write), host[:port] (net), executable (run), env var name (env); omit for blanket access on that axis."
- **Usage rule is soft**: inline-first is guidance in the Description plus the instruction mention — there is no harness gate. The harness never blocks `request_permission`; the user judges every request. The NotCapable round-trip is model-echoed: the hint names axis+scope, and the model builds the `request_permission` call itself (the NotCapable resource is a valid scope string for that axis).
- **Results** reuse the og-73l.4 vocabulary as single lines, no status/hint padding:
  - `Permission granted: <axis> <scope>`
  - `Permission rejected: <axis> <scope> — consider an alternative`
- **Renderer**: the identical terse line as inline prompts — no differentiation; the model's visible tool call framed by the REPL tool header is the context.
- **Registration**: always-on in every agent toolset (a negotiation channel, not a capability); a per-agent `tools` Subset can scope it out. Never auto-approved (confirm-gate auto-approval never applies to escalations).

## Headless (`-p`) mode

Headless is verified one-shot (`cmd/genie/main.go:321-367`; new session per invocation, single `RunTurn`, exit; no persistence between invocations), so grants are in-memory for that single turn only.

- **Default (no flag)**: auto-deny — the escalator denies every request; `request_permission` returns the standard reject line within the turn.
- **`--approve-all`** (new flag, headless-only; refuses in the interactive REPL): approves everything — blanket authorization for the single run, effective for that one turn only, never persisted to config. This adopts the epic's deferred auto-override flag in heads-up form (lineage: og-73l.5 → og-3z5's global auto-approve intent).

## Retirements and carry-over

- **`tools.Confirmer` and `tools.AutoDeny` retire** (deleted) along with the write-overwrite and bash every-call gates (`internal/tools/confirm.go`, bashtool/writetool confirm wiring, `main.go:388-391`). AutoDeny survives only inside the headless escalator as its deny-everything negotiator — not as the Confirmer interface.
- **`config.Tools.AutoApprove` does not ship**: it was never implemented (the current `config.Tools` has only `Read`/`Write`/`Edit`/`Bash`), and the confirm gates it would have auto-approved are retired. The escalation flow is always user-judged.
- **Carries over unchanged** from og-3z5.2/og-3z5.3: the code tool's schema and permission intersection (`granted = model_request ∩ policy`), the `request_permission` schema, the NotCapable structured error parsing, and the "the model never knows base vs acquired" instruction discipline. The perspective broadens from "the code tool" to "every tool".

## Testing strategy

In the harness norm: **subprocess seam + goldens**.

- **Renderer goldens.** The prompt-renderer contract is deterministic given a scripted input reader; snapshot the exact bytes in golden files (`testdata/*.golden`) covering: scoped line, axis-only (blanket) line, each tier key, full-word keys, unknown choice, reject, ^C-rejects-axis, and the resume-on-next-line byte layout.
- **Subprocess seam.** Drive the compiled binary as a subprocess against the scripted fake provider, asserting observable stdout/stderr/exit codes — the existing `internal/e2e` norm (`TestMain` builds the binary; `runWithStdin` scripts input). Scenarios: single-axis grant chain; multi-axis chained pipeline; mixed grant/reject producing the composite denied result; executed call with grants + output but no status line; ^C; headless auto-deny; `-p --approve-all` (and its interactive-mode refusal); permanent-grant persistence across two `-p` runs (config rewrite + reload); instruction snapshot bytes.
- **Unit tests.** Effective-policy store: tier tagging, once-tier spend-on-execute / discard-on-deny / never-carry-forward, session-end discard, most-specific precedence (exact > prefix > wildcard, longest suffix). Config: restrictive no-config default, per-agent replace-not-merge with empty unnamed axes, `[[permissions.permanent]]` load/persist without dedup. Coverage of new code above 80% and API surface documented in README before this lands.

## Consequences

- `internal/permissions` is new: Store (effective policy, tier lifecycle, scope matching) + Gate (deny-point evaluator, negotiator seam) + renderer.
- `agent.RunTurn` gains a gate option (alongside `agent.WithHooks`); the REPL passes the interactive negotiator bound to its own stdin reader, the `-p` path passes auto-deny / approve-all.
- `internal/config/config.go` and `internal/config/agent.go` gain the `[permissions]` surface (base + permanent + per-agent), with `--approve-all` threaded through `main.go` and the REPL.
- `internal/instruct` renders the policy snapshot + mechanism paragraph (needs the resolved base policy passed in; the snapshot changes on `/agent` switches).
- `request_permission` lands in `buildRegistry`; `tools` gains the `Permissioned` seam and read/write/edit/bash implement it.
- Today's ungated reads become gated: reading outside `read=["."]` (or the configured base) now escalates — a deliberate behavior change.
- The alarming-but-simple consequence the map chose: under the restrictive no-config default, every bash call (run axis) escalates until the user grants — with grants learned from results and no base-vs-acquired labeling.

This ADR, together with the og-73l.1 research file (`docs/research/interactive-escalation-ux-prior-art.md`), the og-73l.2 prototype branch (`prototype/og-73l.2-escalation-prompt`), and the ticket resolutions on maps og-73l and og-3z5, is the implementation brief.