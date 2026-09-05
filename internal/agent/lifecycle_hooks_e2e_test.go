package agent_test

// End-to-end validation of the lifecycle-hooks seam inside the real agent loop
// (og-cbu.5 prototype, framework half). Drives a self-contained script plugin
// (bash + jq, no standalone module) through Manager + LifecycleSeam inside
// agent.RunTurn and asserts the shape and order of the events the framework
// fires: request_built before the first stream (its rewrite must reach the
// client), tool_before/tool_after around execution (suppression kills the call),
// response_ready per delta + a final release carrying usage + finish reason,
// and exactly one turn_error carrying the failing error.

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/agent"
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/plugin"
	"github.com/okayest-dev/genie/internal/tools"
)

// lifecycleScript is a probe plugin that echoes each hook's contribution back
// (markers on request_built, tool_before, tool_after, response_ready) and
// appends one NDJSON line per event to $GENIE_LIFECYCLE_LOG_PATH. When
// LIFECYCLE_SUPPRESS_TOOL names a tool, tool_before suppresses that call.
const lifecycleScript = `#!/bin/bash
log="${GENIE_LIFECYCLE_LOG_PATH:-/dev/null}"
suppress_tool="${LIFECYCLE_SUPPRESS_TOOL:-}"

emit() {
    # emit <event> <payload-object> → one NDJSON line: the payload + the event name.
    echo "$2" | jq -c --arg e "$1" '. + {event:$e}' >>"$log"
}

while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo "$line" | jq -c --argjson id "$id" '
                {jsonrpc:"2.0",
                 result:{lifecycle_request_built:true,lifecycle_tool_before:true,
                         lifecycle_tool_after:true,lifecycle_response_ready:true,
                         lifecycle_turn_error:true,version:1},id:$id}'
            ;;
        "lifecycle/request_built")
            echo "$line" | jq -c --argjson id "$id" '
                [.params.messages[] | if .role=="user" then .content=(.content + "[RB]") else . end] as $m
                | {jsonrpc:"2.0",result:{request:{model:.params.model,messages:$m,tools:.params.tools}},id:$id}' >/dev/null
            emit request_built "$(echo "$line" | jq -c '{model:.params.model,messages:(.params.messages|length),tools:(.params.tools|length)}')"
            echo "$line" | jq -c --argjson id "$id" '
                [.params.messages[] | if .role=="user" then .content=(.content + "[RB]") else . end] as $m
                | {jsonrpc:"2.0",result:{request:{model:.params.model,messages:$m,tools:.params.tools}},id:$id}'
            ;;
        "lifecycle/tool_before")
            name=$(echo "$line" | jq -r '.params.name')
            if [ -n "$suppress_tool" ] && [ "$name" = "$suppress_tool" ]; then
                emit tool_before "$(echo "$line" | jq -c --arg s "$name" '{tool:$s,suppressed:true}')"
                echo "$line" | jq -c --argjson id "$id" '{jsonrpc:"2.0",result:{arguments:"",suppress:true},id:$id}'
            else
                emit tool_before "$(echo "$line" | jq -c --arg s "$name" '{tool:$s}')"
                # Rewrite must stay VALID JSON (the agent executes it): inject a
                # marker property rather than appending raw text.
                echo "$line" | jq -c --argjson id "$id" --arg t "$name" '
                    {jsonrpc:"2.0",
                     result:{arguments:(.params.arguments|fromjson + {tb_marker:$t}|tojson)},
                     id:$id}'
            fi
            ;;
        "lifecycle/tool_after")
            emit tool_after "$(echo "$line" | jq -c '{tool:.params.name,result:.params.result,error:.params.error}')"
            echo "$line" | jq -c --argjson id "$id" '{jsonrpc:"2.0",result:{result:(.params.result + "[TA]")},id:$id}'
            ;;
        "lifecycle/response_ready")
            emit response_ready "$(echo "$line" | jq -c '{chunk:.params.chunk,final:.params.final,finish_reason:.params.finish_reason}')"
            echo "$line" | jq -c --argjson id "$id" '{jsonrpc:"2.0",result:{chunk:.params.chunk},id:$id}'
            ;;
        "lifecycle/turn_error")
            emit turn_error "$(echo "$line" | jq -c '{error:.params.error,phase:.params.phase}')"
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            ;;
        "ping")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            ;;
        "shutdown")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            exit 0
            ;;
        *)
            echo '{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":'"$id"'}'
            ;;
    esac
done
`

// scriptedClient streams events; the first Stream call emits text + a tool
// call, the second (after the tool result landed) emits text + usage + finish.
// It records the request it was handed so tests can assert the request_built
// rewrite reached the client.
type scriptedClient struct {
	calls atomic.Int32
	errOn int32        // >0 → the Nth Stream call fails to open
	last  atomic.Value // llm.Request seen by the last Stream call
}

func (c *scriptedClient) Stream(_ context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	c.last.Store(req)
	n := c.calls.Add(1)
	if c.errOn >= n {
		return nil, fmt.Errorf("stream-open failure (call %d)", n)
	}
	seq := func(yield func(llm.Event) bool) {
		if n == 1 {
			if !yield(llm.Event{Kind: llm.EventText, Text: "Hello "}) {
				return
			}
			if !yield(llm.Event{Kind: llm.EventText, Text: "there"}) {
				return
			}
			if !yield(llm.Event{Kind: llm.EventToolCall, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Arguments: "{}"}}}) {
				return
			}
			yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishToolCalls})
			return
		}
		if !yield(llm.Event{Kind: llm.EventText, Text: "Final"}) {
			return
		}
		if !yield(llm.Event{Kind: llm.EventUsage, Usage: llm.Usage{PromptTokens: 42, CompletionTokens: 8, TotalTokens: 50}}) {
			return
		}
		yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishStop})
	}
	return seq, nil
}

func (c *scriptedClient) ListModels(_ context.Context) ([]llm.Model, error) { return nil, nil }

func (c *scriptedClient) lastRequest(t *testing.T) llm.Request {
	t.Helper()
	v := c.last.Load()
	if v == nil {
		t.Fatal("client saw no request")
	}
	return v.(llm.Request)
}

type echoTool struct{}

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "Echo input" }
func (echoTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (echoTool) Execute(_ json.RawMessage) (string, error) { return "echoed", nil }

// loadLifecycleScript writes the probe plugin into a fresh plugin dir and loads
// it, returning the manager and a seam wired to it.
func loadLifecycleScript(t *testing.T) (*plugin.Manager, *plugin.LifecycleSeam) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "lifecycle-probe")
	if err := os.WriteFile(path, []byte(lifecycleScript), 0o755); err != nil {
		t.Fatalf("write plugin script: %v", err)
	}
	mgr := plugin.NewManager(dir, nil, nil, tools.NewRegistry())
	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}
	seam := plugin.NewLifecycleSeam(mgr.PluginsInOrder(), plugin.LifecycleConfig{}, func(msg string) {
		t.Logf("lifecycle degraded: %s", msg)
	})
	return mgr, seam
}

// eventLog is one NDJSON record as written by the probe plugin.
type eventLog struct {
	Event        string `json:"event"`
	Model        string `json:"model"`
	Messages     int    `json:"messages"`
	Tools        int    `json:"tools"`
	Tool         string `json:"tool"`
	Result       string `json:"result"`
	Error        string `json:"error"`
	Phase        string `json:"phase"`
	Suppressed   bool   `json:"suppressed"`
	Final        bool   `json:"final"`
	FinishReason string `json:"finish_reason"`
}

func readEventLog(t *testing.T, path string) []eventLog {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plugin log: %v", err)
	}
	var out []eventLog
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var rec eventLog
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("bad plugin log line: %v\n%s", err, line)
		}
		out = append(out, rec)
	}
	return out
}

func containsEvent(logs []eventLog, want string) bool {
	for _, l := range logs {
		if l.Event == want {
			return true
		}
	}
	return false
}

func TestLifecycleHooksFireInShapeInRunTurn(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "hooks.ndjson")
	t.Setenv("GENIE_LIFECYCLE_LOG_PATH", logPath)
	mgr, seam := loadLifecycleScript(t)
	defer mgr.Shutdown()

	reg := tools.NewRegistry()
	reg.Register(echoTool{})

	client := &scriptedClient{}
	var out, errOut strings.Builder
	if err := agent.RunTurn(context.Background(), client, "test-model", "sys", "hi", &out, &errOut, nil, reg, nil, "", agent.WithHooks(seam)); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	// request_built rewrite reached the client and the streaming client.
	gotReq := client.lastRequest(t)
	if gotReq.Messages[1].Content != "hi[RB]" {
		t.Errorf("client saw request with messages=%+v, want user content %q", gotReq.Messages, "hi[RB]")
	}

	// The tool executed with the tool_before-rewritten arguments and the
	// tool_after-rewritten result surfaced to the conversation, so the second
	// stream (with the tool message) proceeded to a text+usage+finish end.
	logs := readEventLog(t, logPath)
	for _, want := range []string{"request_built", "tool_before", "tool_after", "response_ready"} {
		if !containsEvent(logs, want) {
			t.Errorf("log missing %q; got events %s", want, eventNames(logs))
		}
	}
	if containsEvent(logs, "turn_error") {
		t.Errorf("happy path logged turn_error: %s", eventNames(logs))
	}

	// tool_before/tool_after pair around the echo call.
	var sawBefore, sawAfter bool
	for _, l := range logs {
		switch l.Event {
		case "tool_before":
			sawBefore = l.Tool == "echo"
		case "tool_after":
			sawAfter = l.Tool == "echo" && l.Result == "echoed"
		}
	}
	if !sawBefore || !sawAfter {
		t.Errorf("tool event pair missing before=%v after=%v (log: %s)", sawBefore, sawAfter, eventNames(logs))
	}

	// response_ready fired per delta (final=false) then a final release.
	var deltas, finals int
	for _, l := range logs {
		if l.Event == "response_ready" {
			if l.Final {
				finals++
			} else {
				deltas++
			}
		}
	}
	if deltas < 2 {
		t.Errorf("expected per-delta response_ready, got %d deltas", deltas)
	}
	if finals != 1 {
		t.Errorf("expected exactly 1 final response_ready, got %d", finals)
	}

	// The model stitched on request_built is what the summary needs.
	if logs[0].Event != "request_built" || logs[0].Model != "test-model" || logs[0].Messages != 2 {
		t.Errorf("request_built record wrong: %+v", logs[0])
	}
}

func TestLifecycleHookToolBeforeSuppressionKillsCall(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "hooks.ndjson")
	t.Setenv("GENIE_LIFECYCLE_LOG_PATH", logPath)
	t.Setenv("LIFECYCLE_SUPPRESS_TOOL", "echo")

	mgr, seam := loadLifecycleScript(t)
	defer mgr.Shutdown()

	// An echo tool that records whether it ever executed.
	type tracked struct{ fired atomic.Int32 }
	tr := &tracked{}
	reg := tools.NewRegistry()
	reg.Register(toolFunc{name: "echo", run: func() (string, error) { tr.fired.Add(1); return "echoed", nil }})

	client := &scriptedClient{}
	var out, errOut strings.Builder
	if err := agent.RunTurn(context.Background(), client, "test-model", "sys", "hi", &out, &errOut, nil, reg, nil, "", agent.WithHooks(seam)); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	if tr.fired.Load() != 0 {
		t.Errorf("suppressed tool executed %d times, want 0", tr.fired.Load())
	}
	if got := out.String(); !strings.Contains(got, "Hello ") || !strings.Contains(got, "there") || !strings.Contains(got, "Final") {
		t.Errorf("out = %q, want the normal text deltas (Hello there, Final)", got)
	}

	logs := readEventLog(t, logPath)
	var suppressed bool
	for _, l := range logs {
		if l.Event == "tool_before" {
			suppressed = l.Suppressed
		}
		if l.Event == "tool_after" {
			t.Errorf("suppressed call still got tool_after: %s", eventNames(logs))
		}
	}
	if !suppressed {
		t.Errorf("tool_before was not marked suppressed: %s", eventNames(logs))
	}
}

func TestLifecycleHookTurnErrorFiresOnceWithRootCause(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "hooks.ndjson")
	t.Setenv("GENIE_LIFECYCLE_LOG_PATH", logPath)
	mgr, seam := loadLifecycleScript(t)
	defer mgr.Shutdown()

	err := agent.RunTurn(context.Background(), &scriptedClient{errOn: 1}, "test-model", "sys", "hi", &strings.Builder{}, &strings.Builder{}, nil, nil, nil, "", agent.WithHooks(seam))
	if err == nil {
		t.Fatal("RunTurn should fail when the stream cannot open")
	}

	logs := readEventLog(t, logPath)
	if !containsEvent(logs, "turn_error") {
		t.Fatalf("turn_error not logged: %s", eventNames(logs))
	}
	for _, l := range logs {
		if l.Event == "turn_error" {
			if !strings.Contains(l.Error, "stream-open failure (call 1)") {
				t.Errorf("turn_error carried %q, want the failing stream error", l.Error)
			}
			if l.Event == "turn_error" && l.Phase != "turn" {
				t.Errorf("turn_error phase = %q, want \"turn\"", l.Phase)
			}
		}
	}
	var turnErrors int
	for _, l := range logs {
		if l.Event == "turn_error" {
			turnErrors++
		}
	}
	if turnErrors != 1 {
		t.Errorf("turn_error fired %d times, want exactly once", turnErrors)
	}
}

func eventNames(logs []eventLog) []string {
	var names []string
	for _, l := range logs {
		names = append(names, l.Event)
	}
	return names
}

// toolFunc is a minimal tools.Tool backed by a closure, used to track execution.
type toolFunc struct {
	name string
	run  func() (string, error)
}

func (f toolFunc) Name() string        { return f.name }
func (f toolFunc) Description() string { return "tool" }
func (f toolFunc) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (f toolFunc) Execute(_ json.RawMessage) (string, error) { return f.run() }
