# 07 — bash tool + confirm gate

**What to build:** the `bash` tool working end-to-end. Commands run in the session working directory with stdout+stderr merged; every command goes through the confirm gate (denied → `Error: bash denied by user` fed back, loop continues; auto-denied headless until ticket 09 wires the interactive prompt). A hung command is killed at the timeout (default 120s, per-call override), long output is tail-truncated at the shared cap with the full copy spilled to a temp file whose path is returned, and a non-zero exit comes back as an error carrying its output and exit code.

**Blocked by:** 05 — Agent tool loop + read.

**Status:** ready-for-agent

- [ ] Commands run in the session cwd with merged stdout+stderr.
- [ ] Every command goes through the confirm gate; a denial feeds back and the loop continues.
- [ ] Timeout kills the process and returns `Command timed out after Ns` with partial output; the per-call override is honored.
- [ ] Truncated output includes the spill-file path; nothing is lost.
- [ ] A non-zero exit returns an error carrying the output and `Command exited with code N`.
