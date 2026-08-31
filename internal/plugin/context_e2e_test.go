package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/tools"
)

const ctxPluginScript = `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"wires":false,"providers":false,"context_before":true,"context_after":true,"context_compact":true,"context_condense":true,"version":1},"id":'"$id"'}'
            ;;
        "context/before_request")
            # Append a marker to each user message to prove the hook ran.
            echo "$line" | jq -c --argjson id "$id" '
                [.params.messages[] | if .role=="user" then .content=(.content + " [before]") else . end] as $m
                | {jsonrpc:"2.0",result:{request:{model:.params.model,messages:$m,tools:.params.tools}},id:$id}'
            ;;
        "context/after_response")
            echo '{"jsonrpc":"2.0","result":{"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}},"id":'"$id"'}'
            ;;
        "context/compact")
            echo '{"jsonrpc":"2.0","result":{"request":{"model":"m","messages":[],"tools":[]}},"id":'"$id"'}'
            ;;
        "context/condense")
            echo '{"jsonrpc":"2.0","result":{"request":null},"id":'"$id"'}'
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

const ctxBadPluginScript = `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"wires":false,"providers":false,"context_before":true,"version":1},"id":'"$id"'}'
            ;;
        "context/before_request")
            echo '{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal explosion"},"id":'"$id"'}'
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

func loadScriptPlugin(t *testing.T, name, script string) (*Plugin, *Manager) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := writeExec(path, script); err != nil {
		t.Fatalf("write plugin script: %v", err)
	}
	mgr := NewManager(dir, nil, nil, tools.NewRegistry())
	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}

	p, ok := mgr.GetPlugins()[name]
	if !ok {
		t.Fatalf("plugin %q not loaded", name)
	}
	return p, mgr
}

// TestContextSeamInvokesHooksE2E loads a subprocess plugin that declares all
// four context hooks and verifies the seam invokes them: before_request rewrites
// the request, after_response reports usage, and compact/condense run.
func TestContextSeamInvokesHooksE2E(t *testing.T) {
	p, mgr := loadScriptPlugin(t, "ctx", ctxPluginScript)
	defer mgr.Shutdown()
	seam, err := NewContextSeam([]*Plugin{p}, ContextConfig{}, nil)
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}

	req := llm.Request{
		Model: "m",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "sys"},
			{Role: llm.RoleUser, Content: "hello"},
		},
	}

	// before_request rewrites.
	out, err := seam.BeforeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("BeforeRequest: %v", err)
	}
	if !strings.Contains(out.Messages[1].Content, "[before]") {
		t.Errorf("before_request did not rewrite user message, got %q", out.Messages[1].Content)
	}

	// after_response observes usage.
	if err := seam.AfterResponse(context.Background(), req, llm.Usage{PromptTokens: 1, CompletionTokens: 1}); err != nil {
		t.Fatalf("AfterResponse: %v", err)
	}

	// compact is single-active on plugin p.
	if seam.compact != p {
		t.Fatalf("compact active = %v, want ctx", seam.compact)
	}
	if _, err := seam.Compact(context.Background(), req); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if _, err := seam.Condense(context.Background(), req); err != nil {
		t.Fatalf("Condense: %v", err)
	}
}

// TestContextSeamDegradesGracefullyOnHookFailureE2E verifies that when a
// plugin's before_request hook fails, the seam surfaces a visible degradation
// but the request still proceeds unchanged (contribution skipped).
func TestContextSeamDegradesGracefullyOnHookFailureE2E(t *testing.T) {
	p, mgr := loadScriptPlugin(t, "ctx-bad", ctxBadPluginScript)
	defer mgr.Shutdown()
	var degraded []string
	seam, err := NewContextSeam([]*Plugin{p}, ContextConfig{}, func(msg string) {
		degraded = append(degraded, msg)
	})
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}

	req := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}
	out, err := seam.BeforeRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("BeforeRequest should not hard-fail: %v", err)
	}
	if len(degraded) != 1 {
		t.Fatalf("expected one degradation message, got %d", len(degraded))
	}
	if !strings.Contains(degraded[0], "ctx-bad") {
		t.Errorf("degradation %q should name the failing plugin", degraded[0])
	}
	if len(out.Messages) != 1 || out.Messages[0].Content != "hi" {
		t.Errorf("request should proceed unchanged on hook failure, got %+v", out.Messages)
	}
}

func writeExec(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o755)
}
