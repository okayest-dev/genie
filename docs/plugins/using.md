# Installing and using plugins

Plugins are executables that extend Genie. This guide is for *using* them — installing a plugin someone else wrote, controlling which ones load, and driving their commands from the REPL. If you want to write your own, see [writing plugins](authoring.md).

## Where plugin executables come from

No package manager today. You install a plugin by downloading (or building) its executable and placing it in the plugin directory. The plugin's own release page tells you where to get the binary and what it needs.

Plugin releases are published per-plugin. The Genie project's two former provider plugins — Bedrock and Copilot — now ship in-tree as bundled providers: Bedrock wires through the AWS SDK credential chain and Copilot through genie's own credential store (see [configuration](../configuration.md)). Neither needs a plugin install.

## The plugin directory

The default discovery directory is `~/.config/genie/plugins/` (XDG-aware). Every executable there is treated as a plugin. Two layouts are supported:

**Directory layout (recommended)** — one subdirectory per plugin, so config and credentials stay bundled:

```
~/.config/genie/plugins/
  my-plugin/
    my-plugin          # executable
    config.toml        # optional, plugin-specific
```

**Flat layout (backward compatible)** — binaries and optional manifests side by side:

```
~/.config/genie/plugins/
  my-plugin            # executable
  my-plugin.toml       # optional manifest
```

Discovery rules: the host scans the directory for executables, skips directories-as-binaries, hidden files, and non-executables, and loads at most 16. You don't need a manifest — a plugin with no manifest is probed over the protocol instead.

## Controlling which plugins load

All of this lives in the `[plugins]` table of `config.toml`:

```toml
[plugins]
dir = "~/.config/genie/plugins"    # discovery directory
enable = ["my-plugin"]             # allowlist: only these load (empty = all)
disable = ["broken"]               # denylist (takes precedence over enable)
```

`disable` always wins over `enable`. Prefer the denylist when you're temporarily parked a plugin; prefer the allowlist when you keep many plugins around and only want a few active.

The discovery directory can also be overridden with the `GENIE_PLUGIN_DIR` env var.

When a plugin is disabled it is never spawned — its tools, hooks, and commands simply don't exist that session.

## Using plugin commands in the REPL

Plugins that expose commands are reached with two tokens: `/<plugin> <command>`. Sub-argument parsing beyond the command name is the plugin's own business, and everything after the command is passed through verbatim.

```
genie> /copilot auth login
Open https://github.com/login/device and enter code ABCD-1234

genie> /copilot auth status
```

Facts about the command surface:

- `/help` includes a flat *Plugin commands* section listing every loaded plugin's commands.
- A bare `/<plugin>` shows the plugin's curated help, falling back to its flat command list.
- Plugin command names are namespaced behind the plugin, so nothing collides with Genie's built-in slash commands.
- A plugin whose *name* collides with a built-in (`/help`, `/new`, `/model`, …) is rejected at load — the built-in wins.

The three miss messages you'll see and what they mean:

| You type | You see | Means |
|----------|---------|-------|
| `/foo whatever` | `unknown command: /foo (try /help)` | no such plugin |
| `/copilot nope` | `copilot: no such command: nope` | plugin exists, command doesn't |
| `/copilot auth` | `plugin copilot is not active` | plugin loaded then crashed/timed out |

## Using plugin tools

- **Tools**: a tool plugin's tools appear automatically in the harness's tool list and the model can call them like the built-in ones.
- **Providers**: provider selection is a config concern, not a plugin one. Every bundled wire ships as a declared `[providers.<name>]` default — zen, openai, anthropic, responses, google, copilot, bedrock — and the active one is chosen with the top-level `provider` key (or `GENIE_PROVIDER`). See [configuration](../configuration.md) for the provider config surface.

## When plugins misbehave

Plugins are sandboxed by failure. A plugin that crashes or hangs is marked inactive and everything it provides degrades to a clear error rather than blocking your session. The harness never respawns a failed plugin.

Streaming-completion calls are the one deliberate exception: the stream completion timeout (`[plugins] wire_stream_timeout`, default 10 minutes — see [configuration](../configuration.md)) gives streaming work much longer than the normal 5-second RPC budget. A completion that outlives even that doesn't kill the plugin — genie waits a short grace period for the late response, and only marks the plugin inactive if it never answers.

| Symptom | Cause | Remedy |
|---------|-------|--------|
| `plugin X is not active` | plugin crashed or timed out this session | fix/update the plugin, restart genie |
| `wire stream timeout` | a completion outran `[plugins] wire_stream_timeout` | raise `wire_stream_timeout`; plugin stays usable |
| `unknown command: /X` | not a loaded plugin | check `[plugins] enable`; check the name |
| plugin tools/commands missing | plugin hidden from discovery | `chmod +x` the executable; use a supported layout; check the 16-plugin cap |
| logs look garbled | plugin wrote to stdout | plugins must log to stderr only |