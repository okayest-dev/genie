# 09 — REPL

**What to build:** the interactive front door. The REPL reads a line at a time in canonical mode, prints text deltas live as a turn runs, and gives Ctrl+C three clean zones — quit at the idle prompt, cancel an in-flight turn (drop the partial turn, print `(interrupted)`, return to the prompt), decline a pending confirm. Blank input silently re-prompts; `/help` and `/quit` work; unknown commands point at the surface. `/new` starts a fresh session. Confirm prompts render interactively in the `og: ...? [y/N]` shape and route through the confirm gate from tickets 06/07, so `write`-overwrite and `bash` become interactive here.

**Blocked by:** 04 — Session persistence; 05 — Agent tool loop + read; 06 — write + edit tools; 07 — bash tool + confirm gate.

**Status:** ready-for-agent

- [ ] An `og> ` prompt reads a line, runs a turn, and text streams live; blank input silently re-prompts.
- [ ] Ctrl+C quits at the idle prompt; mid-turn it cancels, prints `(interrupted)`, drops the partial turn, and returns to the prompt; during a confirm it declines.
- [ ] `/help` and `/quit` behave; an unknown command prints `og: unknown command 'x' — try /help`.
- [ ] `/new` starts a fresh session without a confirm.
- [ ] Confirms render as `og: overwrite <path>? [y/N]` / `og: run: <command>? [y/N]`; only `y`/`yes` accepts; a decline feeds `Error: <tool> denied by user` back and the loop continues.
- [ ] Tool runs are framed inline as the turn progresses.
