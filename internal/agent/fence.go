package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/tools"
)

// parseFencedToolCalls recognises tool invocations a model expressed as fenced
// code blocks in a text-only reply — the fallback path for wires that cannot
// do native tool calling. Each block whose info string names a registered,
// enabled tool becomes a synthesized ToolCall:
//
//	```bash
//	go build ./...
//	```
//
// The fence content is used verbatim as the arguments when it is a JSON
// object; otherwise it is wrapped into the tool's single required string
// property (e.g. {"command": "<content>"} for bash). Blocks that do not name a
// registered tool, or whose content cannot be mapped to the tool's schema, are
// left alone (returned as no call). Zero matches returns nil.
func parseFencedToolCalls(text string, reg *tools.Registry) []llm.ToolCall {
	var calls []llm.ToolCall
	var info string
	var buf []string
	inFence := false

	flush := func() {
		if !inFence {
			return
		}
		name, ok := fenceToolName(info, reg)
		if ok {
			if tool, ok2 := reg.Get(name); ok2 {
				if args, ok3 := synthesizeArguments(tool, strings.Join(buf, "\n")); ok3 {
					calls = append(calls, llm.ToolCall{
						ID:        fmt.Sprintf("fence-%d", len(calls)+1),
						Name:      name,
						Arguments: args,
					})
				}
			}
		}
		inFence = false
		info = ""
		buf = buf[:0]
	}

	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inFence {
				flush()
			} else {
				inFence = true
				info = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "```"))
			}
			continue
		}
		if inFence {
			buf = append(buf, line)
		}
	}
	if inFence {
		flush()
	}
	return calls
}

// fenceToolName extracts the tool name from a fence info string. It accepts
// the bare spelling (```bash) and the "tool <name>" spelling (``` tool bash).
// The first token that is a registered, enabled tool names the block; any
// other first token (e.g. a non-tool language like "python") is not a call.
func fenceToolName(info string, reg *tools.Registry) (string, bool) {
	for _, f := range strings.Fields(info) {
		if f == "tool" {
			continue
		}
		if _, ok := reg.Get(f); ok {
			return f, true
		}
		return "", false
	}
	return "", false
}

// synthesizeArguments maps a fence's raw content to a tool's JSON arguments:
// verbatim when the content is already a JSON object, otherwise wrapped into
// the tool's single required string property. Returns false when the content
// is empty or cannot be safely mapped to the tool's schema.
func synthesizeArguments(tool tools.Tool, content string) (string, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", false
	}

	var obj map[string]any
	if json.Unmarshal([]byte(content), &obj) == nil {
		return content, true
	}

	params := tool.Parameters()
	required, ok := params["required"].([]any)
	if !ok || len(required) != 1 {
		return "", false
	}
	name, ok := required[0].(string)
	if !ok {
		return "", false
	}

	// Only wrap into a property that is declared as a string; wrapping into an
	// integer (e.g. offset) would be a lie.
	props, ok := params["properties"].(map[string]any)
	if !ok {
		return "", false
	}
	prop, ok := props[name].(map[string]any)
	if !ok || prop["type"] != "string" {
		return "", false
	}

	args, err := json.Marshal(map[string]any{name: content})
	if err != nil {
		return "", false
	}
	return string(args), true
}