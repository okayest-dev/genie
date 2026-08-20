# Triage Labels

The skills speak in terms of triage roles. This file maps those roles to the actual labels and statuses used in this repo's beads issue tracker.

| Label in mattpocock/skills | Label in our tracker | Meaning                                  |
| -------------------------- | -------------------- | ---------------------------------------- |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent  |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation            |
| `wontfix`                  | `wontfix`            | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table.

## Status mapping

| Old markdown status | Beads status |
| ------------------- | ------------ |
| `needs-triage`      | `open` (no triage label yet) |
| `needs-info`        | `open` + label `needs-info` |
| `ready-for-agent`   | `open` + label `ready-for-agent` |
| `ready-for-human`   | `open` + label `ready-for-human` |
| `wontfix`           | `closed` + label `wontfix` |
| `claimed`           | `in_progress` |
| `resolved`          | `closed` |

## Feature labels

| Feature area          | Label                    |
| --------------------- | ------------------------ |
| og harness (wayfinder)| `feature:og-harness`     |
| og v1 implementation  | `feature:og-v1`          |
| debug/verbose flags   | `feature:debug-verbose-flag` |

## Wayfinder type labels

| Type       | Label            |
| ---------- | ---------------- |
| research   | `type:research`  |
| grilling   | `type:grilling`  |
| prototype  | `type:prototype` |
| task       | `type:task`      |
