# Issue tracker: Beads (bd)

Issues live in the bd (beads) database at `.beads/`. The issue prefix is `og`, so issues are named `og-<hash>` (e.g. `og-rtt`).

## Conventions

- Issues are created with `bd create "Title" -t <type>` and linked with `bd dep add <child> <parent>`
- Triage state is tracked via issue status (`open` / `in_progress` / `closed`) and labels
- Feature areas use labels: `feature:og-harness`, `feature:og-v1`, `feature:debug-verbose-flag`
- Wayfinder tickets use type labels: `type:research`, `type:grilling`, `type:prototype`, `type:task`
- Wayfinder maps use the `wayfinder:map` label

## When a skill says "publish to the issue tracker"

Run `bd create` to create a new issue. Apply appropriate type, labels, and description.

## When a skill says "fetch the relevant ticket"

Run `bd show <id>` to view the full issue body and audit trail.

## Wayfinding operations

Used by `/wayfinder`. The **map** is a bd issue with the `wayfinder:map` label — the canonical artifact. Its tickets are child issues linked via `bd dep add`.

- **Map**: `bd show <map-id>` — the low-res view of the effort.
- **Child ticket**: `bd create` as child of the map, with type label (`wayfinder:research`, `wayfinder:grilling`, etc.)
- **Blocking**: `bd dep add <child> <parent> --type blocks` — a ticket is unblocked when every ticket blocking it is closed.
- **Frontier**: `bd ready` returns open, unblocked, unclaimed tickets — the edge of the known.
- **Claim**: `bd update <id> --claim` sets assignee and in_progress status before any work.
- **Resolve**: record the answer in a comment, close with `bd close <id> --reason "..."`, and update the map's Decisions-so-far if needed.
