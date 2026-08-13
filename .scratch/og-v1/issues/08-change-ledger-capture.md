# 08 — Change ledger capture

**What to build:** the change ledger's capture and storage. Every agent-loop cycle that mutates files lands exactly one change batch in the session's ledger: on a cycle's first write/edit of a file its pre-mutation content is snapshotted, at cycle close the live file is diffed against the snapshot and only the unified diff text is stored (multiple edits to one file per cycle collapse into one diff). Storage is append-only JSONL in the session dir with huge diffs truncated and spilled to files whose paths are recorded, plus a byte-delta per batch. Only executed `write`/`edit` land diffs — denied/rejected writes, failed edits, and bash never do; a cycle that mutates nothing produces no batch.

**Blocked by:** 04 — Session persistence; 06 — write + edit tools.

**Status:** ready-for-agent

- [ ] A cycle that writes/edits a file produces exactly one batch with per-file diffs.
- [ ] Multiple edits to the same file in one cycle collapse into one diff.
- [ ] Denied/rejected writes and failed edits leave no ledger entry; a cycle with no successful mutations produces no batch.
- [ ] bash mutations never land in the ledger.
- [ ] Huge diffs truncate at the shared cap with the full text spilled and its path recorded; the batch records its byte delta.
