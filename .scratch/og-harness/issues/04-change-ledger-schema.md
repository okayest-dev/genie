Type: grilling
Status: resolved
Blocked by:

# 04-change-ledger-schema

## Question

Define the change ledger: the schema of one entry (file, operation, timestamp, old/new content where cheap, tool call id, session id), its per-session JSONL storage and location, and the exact format the `/changes` command prints. Keep it v1-shaped: enough to render a list, cheap enough that the changes view (fog) can build on it later without schema churn.

## Answer

The ledger is a **batch-of-diffs** record — the git `log`/`show` shape as a template, without implementing git/svn.

**Capture**: One batch per agent-loop cycle. On a cycle's first write/edit of a file, snapshot its pre-mutation content; at cycle close, diff the live file against the snapshot and store only the diff text. Multiple edits to one file within a cycle collapse into a single diff. Files mutated mid-cycle from outside the harness (user terminal, bash) are captured honestly in the diff. Content snapshots are discarded — no re-diffing later; nothing needs to.

**Scope**: Only executed `write`/`edit` land diffs. Failures (rejected write, not-found/ambiguous edit) never touch the file → no diff, no batch. Bash is out of the ledger even when it mutates files. A cycle that mutates nothing produces no batch.

**Schema** — one line per batch in `<session-dir>/<session-id>.changes.jsonl`:

```json
{
  "seq": 3,
  "ts": "2026-08-13T10:01:00Z",
  "session": "s-8f2k",
  "tool_call_ids": ["tc-91", "tc-92"],
  "files": [
    {
      "path": "src/main.go",
      "ops": ["edit", "edit"],
      "diff": "--- src/main.go\n+++ src/main.go\n@@ ...",
      "delta": { "+": 18, "-": 4 }
    }
  ]
}
```

- **`seq`** is the change id — monotonic per session; drill in with `/changes 3`. A separate meaningful identifier (short hash) is possible later; not v1.
- **`ts`**: RFC3339, batch close time.
- **`session`**: explicit session id — self-describing, survives the file being moved.
- **`tool_call_ids`**: links the batch's mutations back to transcript turns.
- **`path`**: as first touched; the diff's own headers use cwd-relative paths for readability.
- **`diff`**: unified diff text (`---`/`+++` headers, `@@` hunks). Non-text (binary/NUL) → `[binary]` marker, no diff. Over 2000 lines / 50KB → truncate with the full copy spilled to `<session-dir>/<seq>-<file>.diff` and its path recorded in the entry (mirrors bash truncation, 03).
- **`delta`**: per-file `+`/`-` line counts; the `/changes` list shows the batch total.

**Storage**: append-only JSONL written at batch close — `<session-dir>/<session-id>.changes.jsonl`, sibling to the session transcript (exact session dir is 07's decision). Separate from the transcript so the changes view never parses the whole transcript to answer "what changed". Never rewritten, replayed, or compacted in v1.

**`/changes`** — newest-first, `id` (== seq), byte delta, timestamp, touched files:

```
changes (3)
3  +120/-9  2026-08-13T10:01:00Z  src/main.go, README.md
2   +40/-40  2026-08-13T09:58:00Z  pkg/llm/client.go
1   +57/-0   2026-08-13T09:50:00Z  README.md
```

Empty session prints `no changes`.

**`/changes <id>`** — the batch's stored diffs, one file at a time in first-touch order:

```
src/main.go
--- src/main.go
+++ src/main.go
@@ -12,4 +12,5 @@ ...
```

Binary file prints `src/blob.bin [binary]`; a truncated diff prints the truncation note and the spill path. Unknown id → `no such change 9`.

The drill-down graduates out of the changes-view fog — v1 has an inline diff surface; what remains in fog is the later *where* (alt-screen list, `$EDITOR`).
