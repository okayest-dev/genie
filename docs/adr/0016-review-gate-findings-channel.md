# 0016 Review Gate Findings Channel

Date: 2026-10-17
Status: Accepted

## Context

The review gate (og-wm7.2) was previously implemented as a stdout parser: the model emitted lines like `FINDING: {"finding_id": "f1", ...}` during its work, and the evaluator harvested these at `task_resolve` time. This had several problems:

- The channel was implicit and coupled to stdout formatting.
- The gate had to parse model output, creating a fragile seam.
- Findings could be missed, duplicated, or malformed without detection.
- There was no structured way to enforce that every finding carries a disposition.

## Decision

Replace the stdout parser with an explicit tool: **`task_review`**. The agent calls `task_review` once (or multiple times) during the task to attest that a review was performed and to report its findings. Each finding must declare a disposition:

- `resolve-inline` — fixed in this task; no ticket created.
- `defer-with-ticket` — a follow-up ticket is created with provenance (`finding_of` = the source task id).
- `wontfix` — requires a `reason`; security/hygiene wontfix additionally requires `human_confirmed=true`.

The `task_review` tool records a `ReviewRecord` (subject, method, human_confirmed, findings[]) into the active `TaskState`. At `task_resolve`, the review records are converted to `gates.Task.Reviews []TaskReview` and passed to the evaluator. The review gate (`evalReview`) consumes this explicit channel:

- If no review recorded → block.
- If any finding lacks a disposition → block.
- If `wontfix` missing reason / missing human_confirmed → block.
- On `defer-with-ticket`, a ticket is created via the `TicketCreator` (with `FindingOf` set); the ticket ID is recorded in gate evidence (`deferred_tickets`) and `GateResult.FollowUps`.

The old `ReviewGateFindings` stdout producer and `FINDING:` parser are removed.

## Consequences

- The review gate is now a strict guardrail: it enforces the *presence* and *completeness* of a review, but does not define *what* is reviewed or *how*. The subject/method/findings are the caller's business.
- The `task_defer_finding` tool remains for ad-hoc deferral outside the review gate.
- The provenance chain (`finding_of`) is recorded at ticket creation time and persisted by the markdown tracker and FakeTracker.
- The `task_review` tool is gated by `task_tools.enable` in config, like other task tools.
- Tests exercise the explicit channel end-to-end (`internal/run/review_gate_e2e_test.go`) and at the tool level (`internal/tasktools/task_review_test.go`).

## Alternatives Considered

1. **Keep stdout parser + add tool** — rejected; two channels is confusing and the parser remains fragile.
2. **Structured tool call fence** — rejected; the model still needs a governed tool to produce the record.
3. **Two-axis review (standards + spec)** — rejected; the gate should not define the review scope.

## Links

- og-646 (review gate integration + E2E tests)
- og-wm7.2 (quality gates)
- og-hd8 (follow-up engine dedup/priority/wontfix)