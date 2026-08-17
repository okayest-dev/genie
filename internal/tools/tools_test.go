package tools

import (
	"encoding/json"
	"testing"

	"github.com/okayest-dev/og/internal/llm"
)

type fakeTool struct {
	name        string
	description string
	params      map[string]any
	executeFunc func(json.RawMessage) (string, error)
}

func (f *fakeTool) Name() string            { return f.name }
func (f *fakeTool) Description() string     { return f.description }
func (f *fakeTool) Parameters() map[string]any { return f.params }
func (f *fakeTool) Execute(args json.RawMessage) (string, error) {
	if f.executeFunc != nil {
		return f.executeFunc(args)
	}
	return "ok", nil
}

func TestRegistryLookup(t *testing.T) {
	r := NewRegistry()
	tool := &fakeTool{name: "read", description: "read a file"}
	r.Register(tool)

	got, ok := r.Get("read")
	if !ok {
		t.Fatal("Get('read') returned false")
	}
	if got.Name() != "read" {
		t.Errorf("Name() = %q, want %q", got.Name(), "read")
	}
}

func TestRegistryMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("Get('nonexistent') returned true, want false")
	}
}

func TestRegistryToolDefs(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{
		name:        "read",
		description: "read a file",
		params:      map[string]any{"type": "object"},
	})
	r.Register(&fakeTool{
		name:        "write",
		description: "write a file",
		params:      map[string]any{"type": "object"},
	})

	defs := r.ToolDefs()
	if len(defs) != 2 {
		t.Fatalf("ToolDefs() returned %d tools, want 2", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
		if d.Description == "" {
			t.Errorf("tool %q has empty description", d.Name)
		}
		if d.Parameters == nil {
			t.Errorf("tool %q has nil parameters", d.Name)
		}
	}
	if !names["read"] || !names["write"] {
		t.Errorf("ToolDefs() missing tools: %v", names)
	}
}

func TestRegistryDisabledTool(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "bash", description: "run bash"})
	r.Disable("bash")

	_, ok := r.Get("bash")
	if ok {
		t.Error("Get('bash') returned true for disabled tool, want false")
	}

	_, err := r.Execute("bash", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("Execute on disabled tool returned nil, want error")
	}
	if !IsDisabled(err) {
		t.Errorf("error is not DisabledError: %v", err)
	}
}

func TestRegistryExecute(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{
		name: "echo",
		executeFunc: func(args json.RawMessage) (string, error) {
			return "echoed", nil
		},
	})

	result, err := r.Execute("echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result != "echoed" {
		t.Errorf("result = %q, want %q", result, "echoed")
	}
}

func TestRegistryExecuteMissingTool(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute("nonexistent", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("Execute missing tool returned nil, want error")
	}
}

func TestValidateArgs(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
	}

	tests := []struct {
		name    string
		args    string
		wantErr bool
	}{
		{"valid", `{"path":"foo.go"}`, false},
		{"missing required", `{}`, true},
		{"wrong type", `{"path":123}`, true},
		{"invalid json", `{bad`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateArgs(json.RawMessage(tc.args), schema)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateArgs(%s) error = %v, wantErr %v", tc.args, err, tc.wantErr)
			}
		})
	}
}

func TestDisabledError(t *testing.T) {
	err := DisabledError("bash")
	if err.Error() != "tool 'bash' is disabled" {
		t.Errorf("Error() = %q", err.Error())
	}
	if !IsDisabled(err) {
		t.Error("IsDisabled returned false, want true")
	}
}

func TestRegistryToolDefsOrder(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "z-tool", description: "z"})
	r.Register(&fakeTool{name: "a-tool", description: "a"})

	defs := r.ToolDefs()
	if len(defs) != 2 {
		t.Fatalf("ToolDefs() returned %d tools, want 2", len(defs))
	}
	// Order should match registration order.
	if defs[0].Name != "z-tool" {
		t.Errorf("first tool = %q, want %q", defs[0].Name, "z-tool")
	}
	if defs[1].Name != "a-tool" {
		t.Errorf("second tool = %q, want %q", defs[1].Name, "a-tool")
	}
}

func TestToolDefMatchesLLMType(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{
		name:        "read",
		description: "Read a file",
		params:      map[string]any{"type": "object"},
	})

	defs := r.ToolDefs()
	if len(defs) != 1 {
		t.Fatalf("ToolDefs() returned %d tools, want 1", len(defs))
	}

	// Verify it matches the llm.ToolDef type.
	var _ llm.ToolDef = defs[0]
}
