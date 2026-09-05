// PROTOTYPE — the lifecycle-hooks logging plugin (og-cbu.5). Throwaway code
// that validates the og-cbu.3 event model / og-cbu.4 registration API against
// a realistic consumer: this plugin logs every tool call and its result, plus a
// turn-end summary (model, usage, finish reason), to a scratch NDJSON file.
// Wipe when done — the answers live on the ticket, not in this plugin.
package main

import (
	"encoding/json"
	"os"
	"time"

	"github.com/okayest-dev/genie-logging-plugin-proto/internal/wireplugin"
)

// caps declares every lifecycle event granularly; the harness resolves this
// plugin into exactly the chains it opts into (confirm the handshake bools).
var caps = wireplugin.Capabilities{
	Version:                wireplugin.ProtocolVersion,
	LifecycleRequestBuilt:  true,
	LifecycleToolBefore:    true,
	LifecycleToolAfter:     true,
	LifecycleResponseReady: true,
	LifecycleTurnError:     true,
}

func main() {
	logf, err := openLog()
	if err != nil {
		os.Stderr.WriteString("logging-plugin: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer logf.Close()

	// Per-turn state the events must stitch together: the model rides
	// request_built (there is no model on response_ready), usage + finishReason
	// ride the final response_ready. A realistic plugin needs both halves.
	var model string

	h := wireplugin.NewHandler(caps)
	h.OnLifecycleRequestBuilt(func(req wireplugin.ContextRequest) (wireplugin.LifecycleRequestBuiltResult, error) {
		model = req.Model
		logLine(logf, map[string]any{
			"event": "request_built", "model": req.Model,
			"messages": len(req.Messages), "tools": len(req.Tools),
		})
		return wireplugin.LifecycleRequestBuiltResult{Request: req}, nil
	})
	h.OnLifecycleToolBefore(func(name, id, args string) (wireplugin.LifecycleToolBeforeResult, error) {
		logLine(logf, map[string]any{
			"event": "tool_before", "tool": name, "id": id, "arguments": args,
		})
		return wireplugin.LifecycleToolBeforeResult{Arguments: args}, nil
	})
	h.OnLifecycleToolAfter(func(name, id, args, result, errText string) (wireplugin.LifecycleToolAfterResult, error) {
		// The property og-cbu.5 pins down: every tool call AND its result lands
		// here, with the error as a field so failures are still observable.
		logLine(logf, map[string]any{
			"event": "tool_after", "tool": name, "id": id, "arguments": args,
			"result": result, "error": errText,
		})
		return wireplugin.LifecycleToolAfterResult{Result: result}, nil
	})
	h.OnLifecycleResponseReady(func(chunk string, final bool, finishReason string, usage wireplugin.Usage) (wireplugin.LifecycleResponseReadyResult, error) {
		logLine(logf, map[string]any{
			"event": "response_ready", "final": final, "chunk": chunk,
			"finish_reason": finishReason, "usage": usage, "model": model,
		})
		if final {
			// The turn-end summary: model (from request_built) + usage/finish
			// reason (from the final release) — the two must compose.
			logLine(logf, map[string]any{
				"event": "turn_end", "model": model,
				"finish_reason": finishReason,
				"prompt_tokens": usage.PromptTokens, "completion_tokens": usage.CompletionTokens,
				"total_tokens": usage.TotalTokens,
			})
		}
		return wireplugin.LifecycleResponseReadyResult{Chunk: chunk}, nil
	})
	h.OnLifecycleTurnError(func(errText, phase, partial string) (wireplugin.LifecycleTurnErrorResult, error) {
		logLine(logf, map[string]any{
			"event": "turn_error", "error": errText, "phase": phase, "partial": partial,
		})
		return wireplugin.LifecycleTurnErrorResult{}, nil
	})

	if err := h.Run(); err != nil {
		os.Stderr.WriteString("logging-plugin: " + err.Error() + "\n")
		os.Exit(1)
	}
}

// openLog picks a scratch NDJSON file: GENIE_LIFECYCLE_LOG_PATH wins, else a
// clearly-marked PROTOTYPE file in the plugin's working directory.
func openLog() (*os.File, error) {
	path := os.Getenv("GENIE_LIFECYCLE_LOG_PATH")
	if path == "" {
		path = "PROTOTYPE-logging-plugin.ndjson"
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// logLine writes one NDJSON record, never dropping a hook call on a write
// error (a logging plugin must not corrupt the turn because the disk died).
func logLine(f *os.File, rec map[string]any) {
	rec["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f.Write(append(b, '\n'))
}
