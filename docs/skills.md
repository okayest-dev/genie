# Skills

Skills package specialized prompting for a task type — a folding set of instructions, known constraints, and working conventions the model can consult when a job matches. A skill is a directory containing a single `SKILL.md`; the harness discovers skills, filters them against config and the active agent, and injects the survivors into the model's instruction so it knows what it can reach for.

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
Body goes here. This is injected verbatim into the model's context when the
skill binds.
```

| Frontmatter key | Required | Meaning |
|-----------------|----------|---------|
| `name` | yes | Skill name; must match the directory name |
| `description` | yes | One-liner the model reads to decide whether to engage the skill |
| `disable-model-invocation` | no | Parsed and retained on the skill record; not yet acted on by the injected context |
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

The layer lists each bound skill as `- name: description [argument: hint]`, then includes each skill's body under a `### Skill: name` heading. When no skill binds, no layer is injected — the instruction is byte-identical to a harness with no skills configured.