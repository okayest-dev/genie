# og-cbu.5 prototype: logging plugin over the lifecycle-hooks seam

Throwaway prototype. Validates the lifecycle-hooks seam design (event model
from `og-cbu.3`, plugin registration/ordering from `og-cbu.4`, wire protocol
from `og-cbu.2`) by building the first consumer plugin: one that logs every
tool call and result, and a per-turn summary (model, usage, finish reason), to
an NDJSON file.

**Verdict: seam carries the data and the ergonomics are good — adopt the
design.** See the comments on ticket `og-cbu.5` for the full write-up and the
prototype-finding list.

## Layout

- `logging-plugin/` — the plugin as a standalone Go module
  (`github.com/okayest-dev/genie-logging-plugin-proto`). It vendors its own
  copy of the generated plugin SDK (`internal/wireplugin`, from
  `protocol/schema.yaml`) so the prototype is self-contained.
- `plugins/` — populated by `make proto-logging-plugin` with the built binary
  + manifest, i.e. the layout the manager expects.
- The seam itself lives in the main repo (`internal/plugin/lifecycle.go`) with
  the agent-side hooks in `internal/agent/agent.go`.

## Run it against a real genie

```sh
make proto-logging-plugin            # build + place the plugin
export GENIE_LIFECYCLE_LOG_PATH=/tmp/og-cbu5.log
genie -c ./proto-run.toml            # see below
cat /tmp/og-cbu5.log                 # the NDJSON lifecycle log
```

`proto-run.toml`:

```toml
[plugins]
dir = "prototype/og-cbu.5/plugins"

[lifecycle.plugins]
order = ["logging-plugin-proto"]
```

Any turn then writes one line per event: a `request_built`, a `tool_before` /
`tool_after` pair per tool call, a `response_ready` per streamed delta (plus a
`final: true` release carrying usage + finish reason), and — on failure — a
`turn_error`. Model is stitched from `request_built` (agent supply makes it
available there; it is not re-sent on `response_ready`).

## Test it

```sh
make proto-plugin-test
```

- `internal/plugin`: script-plugin seam tests — chain order, onion reversal,
  configure-vs-manifest ordering, suppress short-circuit, degrade-by-default,
  fatal escalation per event.
- `internal/agent`: end-to-end tests that build this real plugin, run it
  through `Manager` + `LifecycleSeam` inside `agent.RunTurn`, and assert the
  logged tool calls and turn-end summary.

## Branch / capture

Committed on throwaway branch `prototype/og-cbu.5` (out of `main`), per the
prototype skill. Verdict and findings live on ticket `og-cbu.5`.