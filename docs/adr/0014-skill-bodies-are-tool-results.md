# Skill bodies are tool results, not instruction-layer content

Status: accepted. Closes the design question behind og-uem.12 ("Skill bodies: where do they live?"). An earlier draft of this work put engaged bodies in the never-evicted instruction layer and tracked a session-resident set. This ADR records why that was dropped.

## The question

The `## Skills` layer sits in the instruction layer, which context management never evicts. That is the obvious place to put a skill's instructions: it is the one place a body cannot be lost. The cost is that discovery and engagement become the same event — a body is in context from the moment its skill is discovered, whether or not the task needs it.

The alternative is an index-only layer plus a `skill` tool that returns the body on demand, leaving the body as a tool result.

## What settled it

A body in the tool-output layer is not meaningfully more fragile than one in the instruction layer, in the configurations that exist today:

- Every request re-sends the whole conversation. A body in history costs the same per request as a body in the instruction layer. There is no ongoing saving from "residing" in the layer — the only thing the instruction layer buys is immunity to eviction, and it is not immunity to *loss* either.
- `context.condense_size` narrows a prior-turn result to a bounded excerpt in the outgoing request, and only when that one result exceeds the threshold. Its default is 0, which disables condensation entirely. So by default nothing is condensed.
- `context.net_drop` can drop an oversized result outright. It is off by default.
- Compaction summarises the durable-intent layer and never touches tool output.

So the resident set bought immunity to a non-default configuration, and paid for it with a `Resident` type threaded through the layer builder and the pipeline signature, a `Bound()` accessor on the run handle, and reset hooks on every agent switch and reset.

It was also wrong in the way that mattered most. The instruction is resolved once per **user turn** and baked into the system message before the tool loop starts. A body that only exists in the instruction layer therefore reaches the model a turn *after* it asks for it — the model calls the tool, is told it worked, and cannot act on the instructions until the user prompts again. `og-uem.12.9`'s e2e caught this: the second request inside the same turn did not carry the body. A tool whose payload arrives after the turn that wanted it is a poor loop for an agent.

A tool result has neither problem. It reaches the model in the turn that asked, and it is in the transcript for every request afterwards.

## Decision

The layer is **index only**: one line per bound skill, `- name: description [argument: hint]`, no bodies. A skill costs a line from discovery until something makes it worth paying for.

The `skill` tool returns the named body's contents verbatim as a tool result. The model gets the instructions in the turn it asked for them and keeps them for the rest of the conversation, because every request re-sends the conversation. Nothing is cached or marked, so there is no residency state to invalidate when a `SKILL.md` is edited, the agent changes, or a skill leaves disk — each call re-reads the pool resolved for the current turn.

The tool resolves against the **bound** pool rather than the discovered pool, so an agent's `skills` list is an actual boundary: a skill that agent did not bind is unreachable however it is spelled in config.

The accepted costs:

- An unknown name returns an error naming the available skills, rather than a silent no-op.
- A body sits in the tool-output layer, so `condense_size` and `net_drop` can narrow or drop it when they are switched on. Documented rather than engineered around; re-invoking the tool restores the text.
- The body is not protected from condensation the way an instruction-layer copy would be. Given the defaults, this buys little.

## Rejected: re-resolving the instruction inside the tool loop

The other way to get same-turn delivery is to keep bodies in the layer and refresh the system message between tool-loop iterations. Rejected: it mutates the system message after the `request_built` context hook has already fired, so a plugin rewriting the request would never see the refreshed instruction. Trading a broken tool loop for a hook that observes a stale instruction is a bad swap.

## Also deferred

`disable-model-invocation` is parsed and retained on the skill record, and still does nothing. It is orthogonal to where bodies live and stays out of scope here.
