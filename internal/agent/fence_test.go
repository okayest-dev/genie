package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/tools"
)

// fenceRegistry mirrors the agent-test tool set: bash, read, write, edit with
// the same schemas' required-property shapes.
func fenceRegistry() *tools.Registry {
	reg := tools.NewRegistry()
	reg.Register(&countingBashStub{calls: new(int)})
	reg.Register(&readtoolStub{})
	reg.Register(&writetoolStub{})
	reg.Register(&edittoolStub{})
	return reg
}

type writetoolStub struct{}

func (w *writetoolStub) Name() string        { return "write" }
func (w *writetoolStub) Description() string { return "Write a file" }
func (w *writetoolStub) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
		},
		"required": []any{"path", "content"},
	}
}
func (w *writetoolStub) Execute(_ json.RawMessage) (string, error) { return "wrote", nil }

type edittoolStub struct{}

func (e *edittoolStub) Name() string        { return "edit" }
func (e *edittoolStub) Description() string { return "Edit a file" }
func (e *edittoolStub) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string"},
			"oldText": map[string]any{"type": "string"},
			"newText": map[string]any{"type": "string"},
		},
		"required": []any{"path", "oldText", "newText"},
	}
}
func (e *edittoolStub) Execute(_ json.RawMessage) (string, error) { return "edited", nil }

func TestParseFencedToolCalls(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		disable  string
		want     []llmToolCall
	}{
		{
			name: "no fences",
			text: "just text, no code blocks",
			want: nil,
		},
		{
			name: "bash fence wraps command",
			text: "Let me check.\n\n```bash\nfind . -name '*.go'\n```\n\nDone.",
			want: []llmToolCall{{Name: "bash", Arguments: `{"command":"find . -name '*.go'"}`}},
		},
		{
			name: "tool prefix spelling",
			text: "``` tool bash\nls\n```",
			want: []llmToolCall{{Name: "bash", Arguments: `{"command":"ls"}`}},
		},
		{
			name: "read fence wraps single required path",
			text: "```read\ngo.mod\n```",
			want: []llmToolCall{{Name: "read", Arguments: `{"path":"go.mod"}`}},
		},
		{
			name: "json object content used verbatim",
			text: "```read\n{\"path\":\"Makefile\",\"limit\":5}\n```",
			want: []llmToolCall{{Name: "read", Arguments: `{"path":"Makefile","limit":5}`}},
		},
		{
			name: "multiple fences in one reply",
			text: "```bash\ngo vet ./...\n```\nthen\n```bash\ngo test ./...\n```",
			want: []llmToolCall{
				{Name: "bash", Arguments: `{"command":"go vet ./..."}`},
				{Name: "bash", Arguments: `{"command":"go test ./..."}`},
			},
		},
		{
			name: "non-tool language ignored",
			text: "```python\nprint('hi')\n```",
			want: nil,
		},
		{
			name: "unknown tool name ignored",
			text: "```rm -rf\n/somewhere\n```",
			want: nil,
		},
		{
			name: "empty fence content ignored",
			text: "```bash\n```",
			want: nil,
		},
		{
			name: "multi-required tool needs json",
			text: "```write\njust some text\n```",
			want: nil,
		},
		{
			name: "multi-required tool with json object",
			text: "```write\n{\"path\":\"a.txt\",\"content\":\"hi\"}\n```",
			want: []llmToolCall{{Name: "write", Arguments: `{"path":"a.txt","content":"hi"}`}},
		},
		{
			name:    "disabled tool ignored",
			text:    "```bash\nls\n```",
			disable: "bash",
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := fenceRegistry()
			if tt.disable != "" {
				reg.Disable(tt.disable)
			}
			got := parseFencedToolCalls(tt.text, reg)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d calls, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, w := range tt.want {
				if got[i].Name != w.Name || got[i].Arguments != w.Arguments {
					t.Errorf("call %d = %+v, want %+v", i, got[i], w)
				}
				if !strings.HasPrefix(got[i].ID, "fence-") {
					t.Errorf("call %d id %q, want fence-N", i, got[i].ID)
				}
			}
		})
	}
}

// llmToolCall is the projection of a synthesized call the test asserts on.
type llmToolCall struct {
	Name      string
	Arguments string
}