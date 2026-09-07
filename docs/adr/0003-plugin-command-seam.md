# Plugin command seam: commands/list + commands/run (+ optional commands/help), protocol stays v1

Status: accepted (supersedes the "native harness slash-command rejected for P2" option in ADR-0002)

Genie wire plugins may register user-typed slash commands behind a per-plugin, optional `commands` capability. The REPL addresses a command with two tokens, `/<plugin> <command>` (`/copilot auth login` = plugin `copilot`, command `auth`, raw arguments `login`); sub-command trees beyond the plugin name are the plugin's internal business. Discovery rides `commands/list`, returning `{name, description, usage}` per command (cached at plugin load, mirroring `tools/list`); execution rides `commands/run` with `{name, arguments}` (raw string) and returns `{text?, data?}` — the REPL prints `text`, or compact JSON of `data` when `text` is empty. An optional `commands/help` method (`{name?}` → `{text}`) lets a plugin supply richer help: the bare `/<plugin>` invokes it lazily, and an `-32601` (method not found) response falls back to the flat `commands/list` listing. The protocol **stays v1** — this is an additive, optional capability, not a breaking change — so no version bump and no lockstep migration story for the standalone plugin repos.

We decided this because ADR-0002's rejection was conditional on genie having "no command registry" — the seam removes that condition (the slash switch at `internal/repl/repl.go:255` gains a plugin branch fed by a narrow registry). The careful limits are what make it framework-level rather than copilot-specific: plugin-scoped flat names keep genie's slash namespace intact without a registry protocol of its own, raw-string arguments keep the wire shape minimal (usage text, not an argument schema, is the contract), and the message-shape was deliberately small (text-or-data) to stay symmetric with `tools/call` while letting interactive work — a device flow — complete asynchronously inside the plugin's own process after an RPC that returns promptly.

## Considered options

- **Riding protocol v2 with a lockstep bump** (the ticket's original framing). Rejected — the family is pre-initial-release; additive capabilities under v1 cost less than forcing every plugin repo to bump in step, and the harness's hard-fail on unsupported versions is preserved for real breaking changes.
- **Structured argument schema per command** (mirroring tool JSON-schema). Rejected — slash commands are user-typed shells, not model call sites; `usage` text plus raw arguments covers login/refresh/status with no schema language to invent.
- **Absolute slash names** (`/copilot-auth`) in a single flat namespace. Rejected — genie's built-in names stay reserved and single-token; the two-token plugin form avoids collisions and namespacing rituals.
- **Plugins forced to provide a help function for /help rendering.** Rejected — flat listing is genie's job; help is the plugin's optional upgrade (probed lazily, `-32601` = absent).
- **Warn-and-drop when a plugin name collides with a built-in.** Rejected and strengthened to skip-the-plugin-at-startup — a plugin owning a built-in name is a config error the operator should see, not a silently shadowed command.

## Consequences

- Built-in slash names are reserved — `help`, `quit`, `exit`, `new`, `changes`, `model`, `agent` — and a plugin using one as its display name is rejected at startup with a warning.
- `commands/run` shares `tools/call` failure semantics: the `RequestTimeout` (5s) budget applies, a timeout marks the plugin inactive, a JSON-RPC error prints its message and the plugin stays active, and failed plugins are never respawned (a command on one surfaces "plugin <name> is not active").
- The np REPL gains a narrow `CommandSource` interface (list, run, optional help) wired from the plugin manager; `repl.Config` carries the interface, not the manager.
- Three distinct miss messages: `/foo …` → "unknown command: /foo (try /help)"; `/copilot nope` → "copilot: no such command: nope"; inactive plugin → "plugin copilot is not active".
- The copilot plugin is the first adopter: `auth` (login/refresh/status) as `/copilot auth …`.
- ADR-0002's "native harness slash-command rejected for P2" stance is obsolete — this seam is the mechanism the map decided to build.
- The og-ixj.6 P3 fail-fast message ("Run 'genie-plugin-wire-copilot login --host <host>'") needs revising to the REPL command form once the seam lands.