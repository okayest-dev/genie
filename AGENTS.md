# Genie

This project uses bd (beads) for issue tracking.

- Run `bd prime` for workflow context and command guidance.
- Use `bd ready`, `bd show <id>`, `bd update <id> --claim`, and `bd close <id>`.
- Use `bd remember "insight"` for persistent project memory; do not create MEMORY.md files.
- Do not use markdown TODO lists for task tracking.

## Agent skills

### Issue tracker

Issues live in beads (bd). See `docs/agents/issue-tracker.md`.

### Triage labels

ready-for-agent / ready-for-human / wontfix. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` at the root plus `docs/adr/`. See `docs/agents/domain.

### Build and Test

Build and test commands should be handled by the Makefile. Repeatable build actions should be added to the makefile and used.

```
```
```
```
### Completion Criteria

**MUST DO**: These things must be true before a coding task can be declared Completion
- Code coverage of new code **MUST** be above 80%.
- API changes **MUST** be documented in README.md.
- All temp docs **MUST** be cleaned up.

