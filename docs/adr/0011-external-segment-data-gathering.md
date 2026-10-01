# External segment data gathering for the prompt

Status: accepted. The decisions below are the wayfinder map og-1i1's external-data-gathering decision area (ticket og-1i1.8 "Decision: External segment data gathering", building on ADR-0007's segment vocabulary and ADR-0010's state namespace). It fixes how the prompt renderer gathers segment content genie doesn't hold in-process — today only git's branch and dirty marker — without adding visible latency: the gather's shape, its worst-case latency contract, what counts as dirty, the degraded-timeout draw, and its recovery. It does not fix the theme file's TOML encoding (og-1i1.3's config schema area) nor how themes style git's states in general (ADR-0010) beyond the one amendment below.

## Which segments gather externally

Of ADR-0007's nine segments, eight draw from resident data: dir/provider/model/agent/session from the run handle, tokens from the accumulated EventUsage, time from the clock, prompt_char's exit status from the turn runner. Exactly one — **git** — needs data genie doesn't hold in-process: the branch name and whether the working tree is dirty. Git survived the segment-vocabulary ticket, so this decision is architecture rather than moot. No other segment may quietly grow an external dependency; joining this contract is a deliberate act recorded in a fresh decision.

## The gather contract

- **One subprocess per prompt draw**: `git status --porcelain=v1 -b --untracked-files=all`. A single invocation yields both branch — the first `## <head>` line, after stripping the `...<remote>` and `[ahead N]`/`[behind N]` suffixes — and dirtiness (any subsequent entry line). `--untracked-files=all` is passed explicitly so a user's `core.untrackedFiles = no` cannot silently redefine dirty.
- **Dirty means any entry**: staged, unstaged, conflict, or untracked (`??`). An untracked-only tree is dirty; a clean-looking prompt over an un-added tree is a lie. Upstream ahead/behind lag is not dirtiness and is ignored.
- **Detached HEAD renders `HEAD`** as the branch text with the normal clean/dirty state — detachment does not erase dirtiness. (The degraded path below differs: it cannot see a symbolic ref and collapses.)
- **Bounded, never unbounded**: the subprocess runs under a fixed internal patience deadline — initial value ~150 ms, calibrated later against real monorepo measurements. It is robustness plumbing, not a preference: no config key, no theme key.
- **Degraded timeout draw**: on deadline, partial output is discarded wholesale — a truncated porcelain stream is never parsed. The draw degrades: the segment runs the cheap branch-only path (`git symbolic-ref --short HEAD`, sub-ms, itself bounded) and renders the branch in the **`unknown` state** (added to git's state set below; Classic default dim) — dirtiness is unverifiable, so nothing is claimed. If the cheap call fails — non-repo, git absent, permission error, or detached HEAD (symbolic-ref exits 128) — the segment collapses per ADR-0007's empty-content rule. Failures are content conditions, not output sites: no error text is ever written into the prompt.
- **Degraded recovery**: while degraded, every draw uses the cheap path; the full scan retries on the **first draw after a turn or slash command has run since degradation began**. No periodic timer — recovery is prompt-driven. The staleness this admits (an idle, degraded prompt never re-checks) is accepted and documented: a sitting prompt implies no interaction, git state realistically mutates via turns — the retry point — and a restart also recovers. This rule is scoped strictly to the degraded regime; the healthy regime stays "gather fresh on every draw", including blank-line redraws — a measured ~2 ms full scan (spawn alone ~1 ms) does not earn a staleness rule.

## ADR-0010 amendment: git gains a third state

Git's closed state set grows from {clean, dirty} to **{clean, dirty, unknown}**. `unknown` means: the segment could not verify dirtiness within its deadline; the generator emits it for the degraded draw above. Classic's default for `unknown` is **dim** — visually distinct from both clean (base) and dirty (yellow), so a slow repo's prompt never impersonates an assertion it can't make. ADR-0010's "exactly one state per draw" holds unchanged — degraded emits exactly one state, `unknown` — and its fallback rule does the rest: a theme that maps only clean/dirty renders `unknown` as the segment's base, so simple themes stay safe. Themes cannot invent states for this or any segment; `unknown` is genie-owned like the rest of the set.

## Terminology: degraded ≠ degradation

This ADR's **degraded** draw is unrelated to ADR-0009's **degradation** (an error surface folding into the warning role). The first is a prompt segment whose data source missed its deadline and stopped claiming; the second is an output-site classification when genie can't satisfy a request cleanly. Same root verb, disjoint domains: segment content condition vs output-site classification.

## Considered options

- **Unbounded sync subprocess** — rejected: one hung git (dead NFS, pathological monorepo) bricks the entire REPL with no safety valve, exactly the stall this decision exists to prevent.
- **p10k-style async out-of-band cache with in-place redraw** — rejected: genie's REPL is canonical mode (settled scope); safe prompt repaint requires knowing whether the user has typed-but-unsubmitted characters, which canonical mode cannot observe — the terminal echoes them privately. Repainting blind would clobber typing; observing it needs raw mode, out of scope. The machinery also buys little: git state changes almost only via turns, which redraw the prompt anyway.
- **Turn-boundary cache as the general refresh policy** — rejected for the healthy regime: ~2 ms per scan is imperceptible, so blank-line savings don't justify a staleness rule. The refresh policy survives only where it earned its keep — the degraded regime's recovery cadence.
- **Branch-only on base style (renders as clean)** — rejected late in grilling: pixel-identical to `clean` under Classic, an unearned clean claim in exactly the regime where verification timed out.
- **Periodic retry while degraded** — rejected: the first background timer in an otherwise fully synchronous design, to self-heal a prompt the user isn't interacting with.

## Consequences

- `run.Handle` hosts the gather (per ADR-0007's Consequences); the git generator consumes its result as one of three shapes: branch + clean/dirty, branch + unknown, or absent (collapsed).
- The theme spec receives git's three-state set as a fixed input, with Classic's defaults clean = base, dirty = yellow fg, unknown = dim.
- ADR-0010's git row is amended by this ADR; its state-concept machinery (closed sets, fallback, fail-fast) is untouched.
- The deadline's initial value is an open calibration note, not an open question: tune against real monorepo measurements when implementation lands (outside this map).
