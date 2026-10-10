package plugin

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/contextmgr"
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/modelinfo"
	"github.com/okayest-dev/genie/internal/session"
	"github.com/okayest-dev/genie/internal/tools"
)

const ctxPluginScript = `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"context_before":true,"context_after":true,"context_compact":true,"context_condense":true,"lifecycle_tool_before":true,"lifecycle_turn_error":true,"version":1},"id":'"$id"'}'
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
            echo '{"jsonrpc":"2.0","result":{"tools":false,"context_before":true,"version":1},"id":'"$id"'}'
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

// --- hook circuit breaker (og-9xd) -----------------------------------------

// ctxBrokenPluginScript errors on every context hook and logs each call, so a
// test can assert how many times the harness reached it.
const ctxBrokenPluginScript = `#!/bin/bash
name=$(basename "$0")
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    if [ -n "$GENIE_HOOK_LOG" ]; then
        echo "$method" >> "$GENIE_HOOK_LOG/$name.log"
    fi
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"context_before":true,"context_after":true,"context_compact":true,"context_condense":true,"lifecycle_tool_before":true,"lifecycle_turn_error":true,"version":1},"id":'"$id"'}'
            ;;
        "ping")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            ;;
        "shutdown")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            exit 0
            ;;
        *)
            echo '{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal explosion"},"id":'"$id"'}'
            ;;
    esac
done
`

// loadBrokenCtx loads one always-failing context plugin with the breaker policy
// and a notice sink, and returns it with a hook-call counter.
func loadBrokenCtx(t *testing.T, threshold int, recovery time.Duration) (*Plugin, *Manager, func() map[string]int, *[]string, *[]string) {
	t.Helper()
	calls := hookLog(t)
	var notices, degraded []string
	policy := HookBreakerPolicy{Threshold: threshold, Recovery: recovery}
	dir := t.TempDir()
	path := filepath.Join(dir, "ctx-broken")
	if err := writeExec(path, ctxBrokenPluginScript); err != nil {
		t.Fatalf("write plugin script: %v", err)
	}
	mgr := NewManager(dir, nil, nil, tools.NewRegistry(), WithHookBreaker(policy, func(msg string) {
		notices = append(notices, msg)
	}))
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
	t.Cleanup(mgr.Shutdown)
	p, ok := mgr.GetPlugins()["ctx-broken"]
	if !ok {
		t.Fatal("plugin ctx-broken not loaded")
	}
	// The seam's own sink is wired by each test; expose a shared slice so the
	// tests can point it at both.
	return p, mgr, calls, &notices, &degraded
}

func TestContextBreakerStopsCallingAfterThreshold(t *testing.T) {
	p, _, calls, notices, degraded := loadBrokenCtx(t, 2, time.Minute)
	seam, err := NewContextSeam([]*Plugin{p}, ContextConfig{}, func(msg string) {
		*degraded = append(*degraded, msg)
	})
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}
	req := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}

	for i := 0; i < 20; i++ {
		if _, err := seam.BeforeRequest(context.Background(), req); err != nil {
			t.Fatalf("BeforeRequest %d: %v", i, err)
		}
		if err := seam.AfterResponse(context.Background(), req, llm.Usage{}); err != nil {
			t.Fatalf("AfterResponse %d: %v", i, err)
		}
	}
	if got := calls()["ctx-broken:context/before_request"]; got != 2 {
		t.Errorf("before_request called %d times, want the threshold of 2", got)
	}
	if got := calls()["ctx-broken:context/after_response"]; got != 2 {
		t.Errorf("after_response called %d times, want the threshold of 2 — the two events are counted separately", got)
	}
	if len(*degraded) != 2 {
		t.Errorf("degrade sink called %d times, want 2 (one per event, the tripping failure covered by its notice)", len(*degraded))
	}
	if len(*notices) != 2 {
		t.Errorf("notices = %v, want one trip notice per event", *notices)
	}
}

func TestContextBreakerTrippedCompactFallsBackToBuiltin(t *testing.T) {
	p, _, calls, _, _ := loadBrokenCtx(t, 1, time.Minute)
	seam, err := NewContextSeam([]*Plugin{p}, ContextConfig{}, nil)
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}
	if seam.compact != p {
		t.Fatalf("compact should start as the plugin, got %v", seam.compact)
	}
	if seam.CompactBuiltin() {
		t.Error("CompactBuiltin should be false while the plugin is healthy")
	}

	req := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}
	// One call trips compact. The error still surfaces — the call really did
	// fail — but the trip notice, not a degrade line per occurrence, is what
	// carries it from here on.
	if _, err := seam.Compact(context.Background(), req); err == nil {
		t.Fatal("the failing compact call should still return its error")
	}
	if !seam.CompactBuiltin() {
		t.Fatal("a tripped single-active compact plugin must let the built-in take over")
	}
	if seam.CondenseBuiltin() {
		t.Error("condense has not tripped, so its own implementation stays active")
	}
	if _, err := seam.Condense(context.Background(), req); err == nil {
		t.Fatal("the failing condense call should still return its error")
	}
	if !seam.CondenseBuiltin() {
		t.Error("a tripped single-active condense plugin must let the built-in take over too")
	}
	// The built-in keeps managing the context window: the seam is not asked to
	// compact, and the ContextManager runs its own compactor.
	if _, err := seam.Compact(context.Background(), req); err != nil {
		t.Fatalf("Compact while tripped: %v", err)
	}
	if got := calls()["ctx-broken:context/compact"]; got != 1 {
		t.Errorf("compact reached the plugin %d times, want 1", got)
	}
}

func TestContextBreakerSharesStateWithLifecycleSeam(t *testing.T) {
	p, _, _, notices, _ := loadBrokenCtx(t, 1, time.Minute)
	ctxSeam, err := NewContextSeam([]*Plugin{p}, ContextConfig{}, nil)
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}
	lifeSeam := NewLifecycleSeam([]*Plugin{p}, LifecycleConfig{}, nil)
	req := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}

	// Trip one event in each seam.
	if _, err := ctxSeam.BeforeRequest(context.Background(), req); err != nil {
		t.Fatalf("BeforeRequest: %v", err)
	}
	if _, _, err := lifeSeam.ToolBefore(context.Background(), "y", "1", "{}"); err != nil {
		t.Fatalf("ToolBefore: %v", err)
	}

	// One plugin, one tripped set, reported across the seam boundary.
	got := p.TrippedHooks()
	want := []string{HookBeforeRequest, HookToolBefore}
	if len(got) != len(want) {
		t.Fatalf("TrippedHooks = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("TrippedHooks[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// The second notice names the whole set, so the user is not told the two
	// are unrelated problems.
	last := (*notices)[len(*notices)-1]
	if !strings.Contains(last, HookBeforeRequest) || !strings.Contains(last, HookToolBefore) {
		t.Errorf("trip notice %q should name both seams' events", last)
	}
}

// stubClient is a minimal llm.Client that records the request it was handed, so
// a test can see whether the built-in compactor ran.
type stubClient struct{ got llm.Request }

func (c *stubClient) Stream(_ context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	c.got = req
	return func(func(llm.Event) bool) {}, nil
}
func (c *stubClient) ListModels(context.Context) ([]llm.Model, error) { return nil, nil }

// lenCounter counts a message's bytes, so a small budget is easy to blow.
type lenCounter struct{}

func (lenCounter) Count(_, text string) int { return len(text) }

// TestContextBreakerTrippedCompactStillCompactsE2E is the acceptance test for
// the single-active fallback: while the compact plugin is tripped, a request
// that exceeds the budget is still compacted — by the built-in — so the context
// window keeps being managed instead of quietly going unmanaged for the length
// of the cooldown.
func TestContextBreakerTrippedCompactStillCompactsE2E(t *testing.T) {
	p, _, calls, notices, _ := loadBrokenCtx(t, 1, time.Minute)
	seam, err := NewContextSeam([]*Plugin{p}, ContextConfig{}, nil)
	if err != nil {
		t.Fatalf("NewContextSeam: %v", err)
	}

	sess, err := session.New(t.TempDir())
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	inner := &stubClient{}
	resolver := modelinfo.New(nil, map[string]int{"m": 100}, modelinfo.Options{BudgetTokens: 30})
	mgr := contextmgr.New(inner, sess,
		contextmgr.WithCounter(lenCounter{}),
		contextmgr.WithResolver(resolver),
		contextmgr.WithHooks(seam),
	)

	// Enough prior history to blow the budget.
	for i := 1; i <= 6; i++ {
		for _, msg := range []llm.Message{
			{Role: llm.RoleSystem, Content: "sys"},
			{Role: llm.RoleUser, Content: fmt.Sprintf("question %d", i)},
		} {
			if err := sess.Append(msg); err != nil {
				t.Fatal(err)
			}
		}
		if err := sess.Append(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	req := llm.Request{Model: "m", Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "current question"},
	}}
	for _, msg := range req.Messages {
		if err := sess.Append(msg); err != nil {
			t.Fatal(err)
		}
	}

	// The first stream trips compact; the second must still compact.
	for i := 1; i <= 2; i++ {
		if _, err := mgr.Stream(context.Background(), req); err != nil {
			t.Fatalf("Stream %d: %v", i, err)
		}
	}

	if !p.HookTripped(HookCompact) {
		t.Fatalf("compact never tripped; plugin calls = %v", calls())
	}
	if got := calls()["ctx-broken:context/compact"]; got != 1 {
		t.Errorf("compact reached the plugin %d times, want 1", got)
	}
	lines, err := sess.Lines()
	if err != nil {
		t.Fatalf("Lines: %v", err)
	}
	markers := 0
	for _, ln := range lines {
		if ln.Role == session.RoleCompaction {
			markers++
		}
	}
	if markers == 0 {
		t.Error("the built-in compactor did not take over: the context window is unmanaged")
	}
	if len(*notices) == 0 {
		t.Error("the trip should have been announced, so a pinned active_compact is never silently overridden")
	}
}
