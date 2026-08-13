# 06 — write + edit tools

**What to build:** the `write` and `edit` tools working end-to-end. `write` creates whole files: new files proceed without confirmation and auto-create parent directories, overwrites go through the confirm gate (denied → `Error: write denied by user` fed back, loop continues; auto-denied headless until ticket 09 wires the interactive prompt), and content over the cap is rejected. `edit` makes surgical single-pair changes with exact, whitespace-sensitive matching: reject if not found, reject if ambiguous, preserve the file's line endings. A turn can scaffold a file and then fix it with `edit` in one pass.

**Blocked by:** 05 — Agent tool loop + read.

**Status:** ready-for-agent

- [ ] `write` to a new file proceeds without confirmation and auto-creates parent directories.
- [ ] `write` over the 1MB cap is rejected with a clear error.
- [ ] `write` to an existing file goes through the confirm gate; a denial feeds back and the loop continues.
- [ ] `edit` applies exactly one `oldText`→`newText` replacement; not-found and ambiguous matches are rejected with clear errors; the file's line endings survive.
- [ ] A single agent turn scaffolds a file and then fixes it via `edit`.
