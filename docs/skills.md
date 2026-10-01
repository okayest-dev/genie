# Skills

Skills package specialized prompting for a task type — a folding set of instructions, known constraints, and working conventions the model can consult when a job matches. A skill is a directory containing a single `SKILL.md`; the harness discovers skills, filters them against config and the active agent, and injects the survivors into the model's instruction as an index so it knows what it can reach for. The model pulls a body's contents on demand with the `skill` tool.

## The SKILL.md format

A skill lives in its own directory named after it, so `alpha/SKILL.md` defines skill `alpha`. The file is YAML frontmatter plus an optional markdown body:

```markdown
---
name: code-review
description: Review the changes since a fixed point along two axes.
# disable-model-invocation: true   # parsed; reserved (not yet acted on)
# argument-hint: <base>            # shown in the skills list so the model
#                                  # knows what to fill in
---
Body goes here. The `skill` tool returns this verbatim when the model asks.
```

| Frontmatter key | Required | Meaning |
|-----------------|----------|---------|
| `name` | yes | Skill name; must match the directory name |
| `description` | yes | One-liner the model reads to decide whether to engage the skill |
| `disable-model-invocation` | no | Parsed and retained on the skill record; not yet acted on |
| `argument-hint` | no | A placeholder shown in the skills list, e.g. `<base>` |

Unknown frontmatter keys are ignored with a warning. A missing `name` or `description`, or a missing frontmatter block, marks the file invalid — it is skipped with a warning rather than failing startup. The body is everything after the closing `---`, trimmed of trailing whitespace.

## Discovery

Directories are scanned in priority order — the first directory in the list that defines a skill wins, so project-local skills shadow ecosystem ones of the same name:

| Directory | Scope |
|-----------|-------|
| `./.genie/skills` | project-local, checked out with the repo |
| `~/.agents/skills` | the external agent-skills ecosystem |
| `~/.config/genie/skills` | your machine-wide collection |

The stack is replaced, not extended: setting `[skills] dirs` in the config, or `GENIE_SKILL_DIR`, swaps in exactly the directories you name. `enable`/`disable` still apply on top.

## Config overrides

```toml
[skills]
# dirs = ["/custom/skills"]   # replaces the default three-directory stack
# enable = ["alpha"]          # allowlist: when set, only named skills load
# disable = ["beta"]          # denylist, applied after enable
```

`enable` is an allowlist (empty = everything passes), `disable` a denylist that wins over `enable`. A disabled skill produces a `warning:` line on stderr, so you can see config quietly hiding something.

## Agent bindings

An agent definition can constrain which skills it binds (see [docs/agent-definitions.md](docs/agent-definitions.md)):

| Agent `skills` | Effect |
|----------------|--------|
| unset | inherit the whole discovered pool |
| `["alpha"]` | bind exactly `alpha` |
| `[]` | bind nothing |

Explicit names are validated against the discovered pool at agent resolution — a typo (`skills = ["alpah"]`) is a hard startup error, not a silent no-op.

## Injection

The surviving skills are assembled into a `## Skills` section and injected into the instruction **between the instruction file and AGENTS.md**:

1. the built-in default prompt (always present)
2. the config/agent instruction file, if any
3. the `## Skills` layer (only when at least one skill binds)
4. the cwd `AGENTS.md`, if present

The layer is an **index only**: one line per bound skill, `- name: description [argument: hint]`, and no bodies. When no skill binds, no layer is injected — the instruction is byte-identical to a harness with no skills configured.

Index-only is the point. A body in the instruction layer costs context from the moment its skill is discovered, whether or not the task ever needs it, and it costs again on every request for the rest of the session. Indexing makes a skill cost a line until something makes it worth paying for.

## The `skill` tool

A `## Skills` index is a menu, not an answer. The model needs a way to read what is behind an entry, and it needs it *during* the turn where the task matched — not a turn later, once the body would be sitting in an instruction layer assembled before the model asked.

So the harness registers a `skill` tool alongside the others. It takes one argument, the skill name, and returns that skill's body verbatim:

```json
{"name": "code-review"}
```

The result is an ordinary tool result. That placement is deliberate:

- **Available immediately.** The body reaches the model in the same turn that requested it, so it can act on the instructions rather than reporting back that it has them.
- **In the transcript afterwards.** Every request re-sends the conversation, so the body stays in context for the rest of the session without the harness holding a second copy of it in a layer.
- **Free of residency bookkeeping.** Nothing is cached or marked, so there is no state to invalidate when a `SKILL.md` is edited, when the agent changes, or when a skill is removed from disk. Each call re-reads the pool for the current turn.
- **Narrow on errors.** An unknown name returns an error naming the available skills, so a typo produces a correction the model can act on rather than a silent no-op. A skill with a description and no body returns a note saying so instead of failing, which would otherwise invite a retry of a name that was already correct.

The tool resolves against the **bound** pool, not the discovered one. A skill the active agent did not bind is unreachable however it is spelled in config, which is what makes an agent's `skills` list an actual boundary rather than a preference. The tool is registered on every registry, but an agent that names its tools in `[tools]` gets exactly those, so an agent can opt out of it explicitly.

One caveat, inherited from every tool result: `context.condense_size` can narrow a single oversized result in an outgoing request, and `context.net_drop` can drop it entirely. Both are off by default (`condense_size` defaults to 0, meaning no condensation). Re-invoking the tool restores the full text, and the model is free to do so.