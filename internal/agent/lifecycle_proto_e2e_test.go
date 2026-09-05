package agent_test

// End-to-end validation for the lifecycle-hooks seam prototype (og-cbu.5).
// Drives the REAL logging plugin (prototype/og-cbu.5/logging-plugin — a
// standalone Go module vendoring the generated wireplugin SDK) through a real
// Leaderless manager + LifecycleSeam + agent.RunTurn, then asserts the NDJSON
// log the plugin wrote: every tool call and its result, a turn-end summary
// (model, usage, finish reason), and turn_error on the mid-stream failure path.

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"os/exec"
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

var loggingPluginRel = filepath.Join("..", "..", "prototype", "og-cbu.5", "logging-plugin")

// lifecycleLog is one parsed NDJSON record from the plugin log.
type lifecycleLog struct {
	Event         string `json:"event"`
	Tool          string `json:"tool"`
	Result        string `json:"result"`
	Error         string `json:"error"`
	Model         string `json:"model"`
	FinishReason  string `json:"finish_reason"`
	PromptTokens  int    `json:"prompt_tokens"`
	CompletionTok int    `json:"completion_tokens"`
	TotalTokens   int    `json:"total_tokens"`
	Final         bool   `json:"final"`
	Messages      int    `json:"messages"`
	Tools         int    `json:"tools"`
}

func (l lifecycleLog) String() string {
	return fmt.Sprintf("%s(model=%s tool=%s)", l.Event, l.Model, l.Tool)
}

// echoToolStr is the arguments JSON the fake model uses for the echo tool call.
const echoToolCallJSON = `{"id":"call-1","name":"echo"}`

// scriptedClient streams events; the first Stream call emits text + a tool
// call, the second (after the tool result landed) emits text + usage + finish.
type scriptedClient struct {
	calls atomic.Int32
	errOn int32 // >0 → the Nth Stream call fails to open
}

func (c *scriptedClient) Stream(_ context.Context, _ llm.Request) (iter.Seq[llm.Event], error) {
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

type echoTool struct{}

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "Echo input" }
func (echoTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (echoTool) Execute(_ json.RawMessage) (string, error) { return "echoed", nil }

// buildLoggingPlugin builds the standalone prototype logging plugin into a
// temp plugins dir (dir layout: <dir>/<name>/<name> + manifest.toml).
func buildLoggingPlugin(t *testing.T, pluginDir string) {
	t.Helper()
	src := filepath.Join(loggingPluginRel)
	dir := filepath.Join(pluginDir, "logging-plugin-proto")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(src, "manifest.toml"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	bin := filepath.Join(dir, "logging-plugin-proto")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build logging plugin: %v\n%s", err, out)
	}
}

// loadLifecyclePlugin builds, loads, and hands back a manager + lifecycle seam
// wired to the real logging plugin, logging to logPath.
func loadLifecyclePlugin(t *testing.T, logPath string) (*plugin.Manager, *plugin.LifecycleSeam) {
	t.Helper()
	pluginDir := t.TempDir()
	buildLoggingPlugin(t, pluginDir)
	mgr := plugin.NewManager(pluginDir, nil, nil, tools.NewRegistry())
	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}
	seam := plugin.NewLifecycleSeam(mgr.PluginsInOrder(), plugin.LifecycleConfig{}, func(msg string) {
		t.Logf("lifecycle degraded: %s", msg)
	})
	return mgr, seam
}

func readLifecycleLog(t *testing.T, path string) []lifecycleLog {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plugin log: %v", err)
	}
	var out []lifecycleLog
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var rec lifecycleLog
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("bad plugin log line: %v\n%s", err, line)
		}
		out = append(out, rec)
	}
	return out
}

func eventNames(logs []lifecycleLog) []string {
	var names []string
	for _, l := range logs {
		names = append(names, l.Event)
	}
	return names
}

func TestLoggingPluginCarriesToolCallAndTurnEnd(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "PROTOTYPE-logging-plugin.ndjson")
	t.Setenv("GENIE_LIFECYCLE_LOG_PATH", logPath)

	mgr, seam := loadLifecyclePlugin(t, logPath)
	defer mgr.Shutdown()

	reg := tools.NewRegistry()
	reg.Register(echoTool{})

	var out, errOut strings.Builder
	err := agent.RunTurn(context.Background(), &scriptedClient{}, "test-model", "sys", "hi", &out, &errOut, nil, reg, nil, "", agent.WithHooks(seam))
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	logs := readLifecycleLog(t, logPath)
	names := eventNames(logs)

	// Every event the seam exposes fired, in a sensible shape.
	for _, want := range []string{"request_built", "tool_before", "tool_after", "response_ready", "turn_end"} {
		if !contains(names, want) {
			t.Errorf("log missing %q event; got %v", want, names)
		}
	}
	if contains(names, "turn_error") {
		t.Errorf("happy-path turn logged turn_error: %v", names)
	}

	// request_built carried the model (the plugin needs it for the summary).
	if logs[0].Event != "request_built" || logs[0].Model != "test-model" || logs[0].Messages != 2 {
		t.Errorf("request_built record wrong: %+v", logs[0])
	}

	// tool_before + tool_after both carry the tool name+args; tool_after carries
	// the result (the ticket's core property).
	var sawBefore, sawAfter bool
	for _, l := range logs {
		if l.Event == "tool_before" {
			sawBefore = true
			if l.Tool != "echo" || l.Error != "" {
				t.Errorf("tool_before record wrong: %+v", l)
			}
		}
		if l.Event == "tool_after" {
			sawAfter = true
			if l.Tool != "echo" || l.Result != "echoed" || l.Error != "" {
				t.Errorf("tool_after record wrong: %+v", l)
			}
		}
	}
	if !sawBefore || !sawAfter {
		t.Errorf("tool event missing before=%v after=%v", sawBefore, sawAfter)
	}

	// turn-end summary carries model + usage + finish reason.
	var end *lifecycleLog
	for i := range logs {
		if logs[i].Event == "turn_end" {
			end = &logs[i]
		}
	}
	if end == nil {
		t.Fatal("no turn_end record")
	}
	if end.Model != "test-model" || end.FinishReason != "stop" || end.PromptTokens != 42 || end.CompletionTok != 8 || end.TotalTokens != 50 {
		t.Errorf("turn_end summary wrong: %+v", end)
	}
}

func TestLoggingPluginTurnError(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "PROTOTYPE-logging-plugin.ndjson")
	t.Setenv("GENIE_LIFECYCLE_LOG_PATH", logPath)

	mgr, seam := loadLifecyclePlugin(t, logPath)
	defer mgr.Shutdown()

	err := agent.RunTurn(context.Background(), &scriptedClient{errOn: 1}, "test-model", "sys", "hi", &strings.Builder{}, &strings.Builder{}, nil, nil, nil, "", agent.WithHooks(seam))
	if err == nil {
		t.Fatal("RunTurn should fail when the stream cannot open")
	}

	logs := readLifecycleLog(t, logPath)
	if !contains(eventNames(logs), "turn_error") {
		t.Fatalf("turn_error not logged: %v", eventNames(logs))
	}
	for _, l := range logs {
		if l.Event == "turn_error" && !strings.Contains(l.Error, "stream-open failure") {
			t.Errorf("turn_error should carry the failing error, got %q", l.Error)
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
