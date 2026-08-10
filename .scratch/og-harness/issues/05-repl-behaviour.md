Type: grilling
Status: open
Blocked by:

# 05-repl-behaviour

## Question

Pin down the REPL's v1 behaviour: token streaming display (print as they arrive?), Ctrl+C interrupt semantics (cancel the in-flight loop vs quit), the slash-command surface (`/changes`, `/new`, `/model`, `/help`, `/quit`), how confirm prompts work, and how tool output interleaves with model tokens in the stream. Keep it std-lib — no screen management.
