package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/plugin"
)

// fakeWirePlugin simulates a wire plugin process: it reads wire/stream
// requests over the JSON-RPC pipe and answers each with a fixed raw result,
// mirroring a plugin's onStream returning the final provider response.
func fakeWirePlugin(result json.RawMessage) *plugin.Plugin {
	pluginToHarnessR, pluginToHarnessW := io.Pipe() // plugin stdout -> harness
	harnessToPluginR, harnessToPluginW := io.Pipe() // harness -> plugin stdin

	p := &plugin.Plugin{
		Name:         "fake",
		Capabilities: plugin.Capabilities{Version: 1, Wires: true},
		Active:       true,
		Codec:        plugin.NewCodec(pluginToHarnessR, harnessToPluginW),
	}

	go func() {
		defer pluginToHarnessW.Close()
		pc := plugin.NewCodec(harnessToPluginR, pluginToHarnessW)
		for {
			req, err := pc.ReadRequest()
			if err != nil {
				return
			}
			if req.Method != plugin.MethodWireStream {
				continue
			}
			pc.WriteResponse(&plugin.Response{JSONRPC: "2.0", Result: result, ID: req.ID})
		}
	}()
	return p
}

func collectEvents(c llm.Client) ([]llm.Event, error) {
	seq, err := c.Stream(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		return nil, err
	}
	var got []llm.Event
	for ev := range seq {
		got = append(got, ev)
	}
	return got, nil
}

// TestPluginWireClientTextOnlyRepro characterises the issue #10 repro: when the
// wire plugin replies with text alone (the model's intended tool invocation
// lives inside a fenced code block), the adapter emits only a text event and a
// stop finish — never an EventToolCall — so the agent loop has nothing to
// execute and the turn dies.
func TestPluginWireClientTextOnlyRepro(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"text": "Let me look at the repo.\n\n```bash\nfind . -name '*.go'\n```\n",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	c := newPluginWireClient(fakeWirePlugin(payload))

	evs, werr := collectEvents(c)
	if werr != nil {
		t.Fatalf("Stream: %v", werr)
	}

	var text string
	var toolCalls int
	var finishes int
	for _, ev := range evs {
		switch ev.Kind {
		case llm.EventText:
			text += ev.Text
		case llm.EventToolCall:
			toolCalls += len(ev.ToolCalls)
		case llm.EventFinish:
			finishes++
		}
	}
	if !strings.Contains(text, "```bash") {
		t.Errorf("text event missing fenced block:\n%s", text)
	}
	if toolCalls != 0 {
		t.Errorf("adapter emitted %d tool calls for a text-only reply; the fence must not be promoted", toolCalls)
	}
	if finishes != 1 {
		t.Errorf("got %d finish events, want 1", finishes)
	}
	if evs[len(evs)-1].End != llm.FinishStop {
		t.Errorf("final finish = %q, want %q", evs[len(evs)-1].End, llm.FinishStop)
	}
}

// TestPluginWireClientFlattensFinishReason characterises the adapter defect
// that contributes to the bug: a plugin-returned finish_reason (here
// tool_calls) is dropped — the adapter always yields a bare stop finish, so
// the agent loop can never branch on the provider's real end reason.
func TestPluginWireClientFlattensFinishReason(t *testing.T) {
	c := newPluginWireClient(fakeWirePlugin(json.RawMessage(
		`{"text":"hi","finish_reason":"tool_calls"}`,
	)))

	evs, err := collectEvents(c)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var finish *llm.Event
	for _, ev := range evs {
		if ev.Kind == llm.EventFinish {
			e := ev
			finish = &e
		}
	}
	if finish == nil {
		t.Fatal("no finish event emitted")
	}
	if finish.End != llm.FinishStop {
		t.Errorf("finish = %q, want %q (plugin's tool_calls reason must currently be flattened)", finish.End, llm.FinishStop)
	}
}

// TestPluginWireClientPassesNativeToolCalls is the happy-path control: when a
// wire plugin does return OpenAI-shaped tool_calls, the adapter forwards them
// as an EventToolCall (native-tool wires still work; only text-only wires are
// stuck).
func TestPluginWireClientPassesNativeToolCalls(t *testing.T) {
	c := newPluginWireClient(fakeWirePlugin(json.RawMessage(
		`{"text":"calling","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"cmd\":\"ls\"}"}}]}`,
	)))

	seq, err := c.Stream(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var got llm.ToolCall
	for ev := range seq {
		if ev.Kind == llm.EventToolCall && len(ev.ToolCalls) > 0 {
			got = ev.ToolCalls[0]
		}
	}
	if got.Name != "bash" || got.Arguments != `{"cmd":"ls"}` {
		t.Errorf("tool call = %+v, want name=bash args={\"cmd\":\"ls\"}", got)
	}
}

// TestPluginWireClientForwardsUsage confirms usage passthrough: when the
// plugin reports usage, the adapter surfaces it as an EventUsage. The issue
// repro's zeroed tokens therefore come from the plugin omitting usage, not
// from the adapter dropping it.
func TestPluginWireClientForwardsUsage(t *testing.T) {
	c := newPluginWireClient(fakeWirePlugin(json.RawMessage(
		`{"text":"hi","usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
	)))

	evs, err := collectEvents(c)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var got llm.Usage
	for _, ev := range evs {
		if ev.Kind == llm.EventUsage {
			got = ev.Usage
		}
	}
	if got.TotalTokens != 10 || got.PromptTokens != 7 || got.CompletionTokens != 3 {
		t.Errorf("usage = %+v, want prompt=7 completion=3 total=10", got)
	}
}