# 11 — /model

**What to build:** the model switchable mid-session against the provider's real catalog (via `ListModels` on the seam from ticket 01). `/model` with no argument prints the full model catalog; `/model <id>` validates the id against it and switches the session's model. A typo can never silently change the session.

**Blocked by:** 09 — REPL.

**Status:** ready-for-agent

- [ ] `/model` with no argument prints the full model catalog.
- [ ] `/model <id>` switches the session's model.
- [ ] `/model <unknown>` prints `og: no such model 'x'` and leaves the current model untouched.
- [ ] A catalog fetch failure errors without switching.
