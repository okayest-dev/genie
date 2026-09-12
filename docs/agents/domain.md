# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- The **domain glossary, stored in beads memory** — look terms up with `bd memories <term>`, or run `bd memories` for the full list. Entries are keyed `glossary-*`.
- **`docs/adr/`** — read ADRs that touch the area you're about to work in. Decisions stay in `docs/adr/` even though terminology moved to beads memory.

If a glossary entry or ADR for the area doesn't exist, **proceed silently**. Don't flag its absence; don't suggest creating it upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates ADRs lazily when terms or decisions actually get resolved.

## File structure

```
/
├── docs/adr/
│   ├── 0001-layered-context-map.md
│   └── 0002-plugin-owned-credentials.md
└── src/
```

Long-lived project memory (domain terms, decisions, glossary) lives in the beads memory store, not markdown files.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in the glossary — `bd memories <term>`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal — either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0007 (event-sourced orders) — but worth reopening because…_
