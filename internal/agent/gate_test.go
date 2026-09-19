package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/tools"
)

// permissionedStub is a tool that declares requirements and counts executions.
type permissionedStub struct {
	reqs  []tools.Requirement
	calls int
}

func (p *permissionedStub) Name() string        { return "write" }
func (p *permissionedStub) Description() string { return "Write a file" }
func (p *permissionedStub) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (p *permissionedStub) RequiredPermissions(json.RawMessage) ([]tools.Requirement, error) {
	return p.reqs, nil
}
func (p *permissionedStub) Execute(json.RawMessage) (string, error) {
	p.calls++
	return "wrote file", nil
}

// scriptNegotiator replays a fixed response list.
type scriptNegotiator struct {
	responses []permissions.Response
}

func (s *scriptNegotiator) Negotiate(context.Context, permissions.Axis, string) (permissions.Response, error) {
	r := s.responses[0]
	s.responses = s.responses[1:]
	return r, nil
}

// gateRun drives one tool call through RunTurn and returns the requests the
// mock client saw.
func gateRun(t *testing.T, tool *permissionedStub, gate *permissions.Gate) []llm.Request {
	t.Helper()
	reg := tools.NewRegistry()
	reg.Register(tool)

	var reqs []llm.Request
	var calls int
	mock := &mockStreamClient{
		streamFunc: func(_ context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
			reqs = append(reqs, req)
			calls++
			if calls == 1 {
				return func(yield func(llm.Event) bool) {
					yield(llm.Event{Kind: llm.EventToolCall, ToolCalls: []llm.ToolCall{
						{ID: "call_1", Name: "write", Arguments: `{}`},
					}})
					yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishToolCalls})
				}, nil
			}
			return func(yield func(llm.Event) bool) {
				yield(llm.Event{Kind: llm.EventText, Text: "done"})
				yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishStop})
			}, nil
		},
	}

	var out bytes.Buffer
	opts := []Option{}
	if gate != nil {
		opts = append(opts, WithPermissions(gate))
	}
	if err := RunTurn(context.Background(), mock, "m", "sys", "hi", &out, nil, nil, reg, nil, "/work", opts...); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	return reqs
}

// lastToolMessage returns the final tool-role message of a request.
func lastToolMessage(t *testing.T, req llm.Request) llm.Message {
	t.Helper()
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == llm.RoleTool {
			return req.Messages[i]
		}
	}
	t.Fatal("no tool message in request")
	return llm.Message{}
}

func TestRunTurnGateRejectDoesNotExecute(t *testing.T) {
	tool := &permissionedStub{reqs: []tools.Requirement{{Axis: "write", Scope: "/work/x"}}}
	store := permissions.New("/work")
	gate := permissions.NewGate(store, &scriptNegotiator{responses: []permissions.Response{permissions.ResponseReject}}, nil)

	reqs := gateRun(t, tool, gate)
	if tool.calls != 0 {
		t.Fatalf("tool executed %d times on a rejected call, want 0", tool.calls)
	}
	msg := lastToolMessage(t, reqs[1])
	for _, want := range []string{
		"Permission rejected: write /work/x",
		"status: call not executed",
		"hint: granted axes remain available",
	} {
		if !strings.Contains(msg.Content, want) {
			t.Errorf("tool result missing %q:\n%s", want, msg.Content)
		}
	}
}

func TestRunTurnGateGrantExecutesWithGrantLine(t *testing.T) {
	tool := &permissionedStub{reqs: []tools.Requirement{{Axis: "write", Scope: "/work/x"}}}
	store := permissions.New("/work")
	gate := permissions.NewGate(store, &scriptNegotiator{responses: []permissions.Response{permissions.ResponseSession}}, nil)

	reqs := gateRun(t, tool, gate)
	if tool.calls != 1 {
		t.Fatalf("tool executed %d times, want 1", tool.calls)
	}
	want := "Permission granted: write /work/x\nwrote file"
	if msg := lastToolMessage(t, reqs[1]); msg.Content != want {
		t.Errorf("tool result = %q, want %q", msg.Content, want)
	}
}

func TestRunTurnGateOnceGrantSpentAfterRun(t *testing.T) {
	tool := &permissionedStub{reqs: []tools.Requirement{{Axis: "write", Scope: "/work/x"}}}
	store := permissions.New("/work")
	gate := permissions.NewGate(store, &scriptNegotiator{responses: []permissions.Response{permissions.ResponseOnce}}, nil)

	if reqs := gateRun(t, tool, gate); len(reqs) == 0 {
		t.Fatal("no requests recorded")
	}
	if store.Covered(permissions.AxisWrite, "/work/x") {
		t.Error("once grant survived the resolved call")
	}
}

func TestRunTurnNoGateIsUngated(t *testing.T) {
	tool := &permissionedStub{reqs: []tools.Requirement{{Axis: "write", Scope: "/work/x"}}}
	reqs := gateRun(t, tool, nil)
	if tool.calls != 1 {
		t.Fatalf("tool executed %d times, want 1", tool.calls)
	}
	if msg := lastToolMessage(t, reqs[1]); msg.Content != "wrote file" {
		t.Errorf("tool result = %q, want bare output", msg.Content)
	}
}
