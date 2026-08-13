# 04 — Session persistence

**What to build:** every session persists. Starting a session creates a session id and a JSONL transcript of the conversation in the session dir (created on demand if it does not exist yet), and the transcript reconstructs the canonical conversation in order so a session is resumable. Upgrades the minimal `-p` path from ticket 01 to write its one-turn transcript.

**Blocked by:** 01 — Wire tracer bullet.

**Status:** ready-for-agent

- [ ] Starting a session creates an id and a transcript file; the conversation is written to it as JSONL.
- [ ] The session directory is created if missing — a fresh install runs without manual setup.
- [ ] The transcript reconstructs the canonical conversation in order.
- [ ] A `-p` run persists its one-turn transcript to the session dir.
