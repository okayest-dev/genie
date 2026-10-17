# REPL input is a line editor with persistent history

Status: accepted. Supersedes the "plain line reader in canonical mode" clause of decision `og-b0u` (05-repl-behaviour).

## The question

The original REPL read stdin with a `bufio.Scanner` over a goroutine that fanned lines onto a channel: one reader owned the terminal, and the escalating permission prompt read from the same channel so a blocked scan could never race a prompt. That is correct, but it gives the operator nothing beyond raw canonical-mode input — no cursor keys, no in-line editing, no recall. Every line is retyped from scratch.

## Decision

The interactive input layer is `github.com/chzyer/readline`. It owns the terminal while reading, provides cursor movement, in-line editing, and Up/Down history, and persists history to `<session_dir>/history` so it survives across sessions.

The negotiator no longer reads the shared line channel. It takes a small `lineReader` interface (`Readline`, `SetPrompt`) and reads directly, blanking readline's idle prompt while a permission question is live so the question renders exactly once.

## Consequences

- **Ctrl+C.** While readline is reading it consumes `^C` itself and returns `readline.ErrInterrupt`; the reply path is unchanged (idle → exit, permission question → reject the axis). During a turn the terminal is back in canonical mode, so `SIGINT` still reaches the interrupt router and cancels the turn.
- **Canonical vs raw.** readline puts the terminal in raw mode for the duration of a read and restores it after. Non-tty stdin (a pipe) fails the raw-mode call harmlessly and lines are still read, so `echo "/quit" | genie` keeps working.
- **Prompt ownership.** A configured prompt bar is rendered as before; its final line (the prompt character) becomes readline's prompt so cursor tracking stays correct on long wrapped lines, and the preceding lines are printed above it.
- **A dependency.** One new direct dependency (`chzyer/readline`) and its transitive tree.
