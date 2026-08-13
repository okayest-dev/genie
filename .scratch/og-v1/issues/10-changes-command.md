# 10 — /changes

**What to build:** the change ledger reviewable from the REPL. `/changes` lists the session's change batches newest-first with id, byte delta, timestamp, and touched files; `/changes <id>` prints the stored unified diffs per file in first-touch order with truncation and spill notes. Empty and unknown cases are unambiguous: a session with no changes prints `no changes`; an unknown id prints `no such change 9`.

**Blocked by:** 08 — Change ledger capture; 09 — REPL.

**Status:** ready-for-agent

- [ ] `/changes` lists batches newest-first with id, byte delta, timestamp, and files.
- [ ] `/changes` on a session with no changes prints `no changes`.
- [ ] `/changes <id>` prints the batch's stored diffs per file, with binary markers and truncation notes including spill paths.
- [ ] `/changes <id>` with an unknown id prints `no such change <id>`.
