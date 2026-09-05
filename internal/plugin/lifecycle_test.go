package plugin

// Scripted lifecycle plugins for exercising the LifecycleSeam (og-cbu.5).
// Behavior is keyed off the plugin's own binary name (basename $0), so several
// plugins with different roles can share one manager and one seam:
//
//	tmpl-a / tmpl-b        marker plugins (append per-event suffixes)
//	tmpl-fatal             request_built AND turn_error declare fatal
//	tmpl-tafatal           tool_after declares fatal
//	tmpl-rrfatal           response_ready (final) declares fatal
//	tmpl-err               every hook returns a JSON-RPC error (degrades)
//	tmpl-suppress          tool_before suppresses the tool named "x"
//
// Marker convention: request_built appends [RB:<name>], tool_after [TA:<name>],
// response_ready [RR:<name>] (non-final deltas only), tool_before [TB:<name>].

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/agent"
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/tools"
)

const lifecycleScript = `#!/bin/bash
name=$(basename "$0")
fatal_req=false; fatal_ta=false; fatal_rr=false; err_all=false; suppress=""
case "$name" in
    *tafatal*)    fatal_ta=true;;
    *rrfatal*)    fatal_rr=true;;
    *fatal*)      fatal_req=true;;
    *err*)        err_all=true;;
    *suppress*)   suppress="x";;
esac

while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"lifecycle_request_built":true,"lifecycle_tool_before":true,"lifecycle_tool_after":true,"lifecycle_response_ready":true,"lifecycle_turn_error":true,"version":1},"id":'"$id"'}'
            ;;
        "lifecycle/request_built")
            if $err_all; then
                echo '{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal explosion"},"id":'"$id"'}'
            elif $fatal_req; then
                echo "$line" | jq -c --argjson id "$id" '
                    {jsonrpc:"2.0",result:{request:{model:.params.model,messages:.params.messages,tools:.params.tools},fatal:true},id:$id}'
            else
                echo "$line" | jq -c --argjson id "$id" --arg suf "$name" '
                    [.params.messages[] | if .role=="user" then .content=(.content + "[RB:" + $suf + "]") else . end] as $m
                    | {jsonrpc:"2.0",result:{request:{model:.params.model,messages:$m,tools:.params.tools}},id:$id}'
            fi
            ;;
        "lifecycle/tool_before")
            if $err_all; then
                echo '{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal explosion"},"id":'"$id"'}'
            elif [ -n "$suppress" ] && [ "$(echo "$line" | jq -r '.params.name')" = "$suppress" ]; then
                echo '{"jsonrpc":"2.0","result":{"arguments":"","suppress":true},"id":'"$id"'}'
            else
                echo "$line" | jq -c --argjson id "$id" --arg suf "$name" '
                    {jsonrpc:"2.0",result:{arguments:(.params.arguments + "[TB:" + $suf + "]")},id:$id}'
            fi
            ;;
        "lifecycle/tool_after")
            if $err_all; then
                echo '{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal explosion"},"id":'"$id"'}'
            elif $fatal_ta; then
                echo '{"jsonrpc":"2.0","result":{"result":"","fatal":true},"id":'"$id"'}'
            else
                echo "$line" | jq -c --argjson id "$id" --arg suf "$name" '
                    {jsonrpc:"2.0",result:{result:(.params.result + "[TA:" + $suf + "]")},id:$id}'
            fi
            ;;
        "lifecycle/response_ready")
            if $err_all; then
                echo '{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal explosion"},"id":'"$id"'}'
            elif $fatal_rr; then
                echo '{"jsonrpc":"2.0","result":{"chunk":"","fatal":true},"id":'"$id"'}'
            else
                echo "$line" | jq -c --argjson id "$id" --arg suf "$name" '
                    {jsonrpc:"2.0",result:{chunk:(.params.chunk + (if .params.final then "" else "[RR:" + $suf + "]" end))},id:$id}'
            fi
            ;;
        "lifecycle/turn_error")
            if $err_all; then
                echo '{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal explosion"},"id":'"$id"'}'
            elif $fatal_req; then
                echo '{"jsonrpc":"2.0","result":{"fatal":true},"id":'"$id"'}'
            else
                echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            fi
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

// loadLifecycleScripts writes each named script into one temp plugins dir and
// loads them all through a single manager, returning the manager and the
// successfully-loaded plugins in registration (discovery) order.
func loadLifecycleScripts(t *testing.T, names ...string) (*Manager, []*Plugin) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := writeExec(path, lifecycleScript); err != nil {
			t.Fatalf("write plugin script %s: %v", name, err)
		}
	}
	mgr := NewManager(dir, nil, nil, tools.NewRegistry())
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
	plug := mgr.PluginsInOrder()
	if len(plug) != len(names) {
		t.Fatalf("loaded %d plugins, want %d: %v", len(plug), len(names), names)
	}
	return mgr, plug
}

func userContent(req llm.Request) string {
	for _, m := range req.Messages {
		if m.Role == llm.RoleUser {
			return m.Content
		}
	}
	return ""
}

func TestLifecycleSeamChainsAndOnionOrdering(t *testing.T) {
	mgr, plugs := loadLifecycleScripts(t, "tmpl-a", "tmpl-b")
	defer mgr.Shutdown()
	seam := NewLifecycleSeam(plugs, LifecycleConfig{}, nil)

	req := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}
	// request_built is a request leg: listed order (a, b).
	out, err := seam.RequestBuilt(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestBuilt: %v", err)
	}
	if got := userContent(out); got != "hi[RB:tmpl-a][RB:tmpl-b]" {
		t.Errorf("request_built chain = %q, want hi[RB:tmpl-a][RB:tmpl-b]", got)
	}

	// tool_before request leg: listed order (a, b) — args accumulate.
	args, suppress, err := seam.ToolBefore(context.Background(), "y", "1", "{}")
	if err != nil {
		t.Fatalf("ToolBefore: %v", err)
	}
	if suppress {
		t.Error("ToolBefore should not suppress for tool y")
	}
	if args != "{}[TB:tmpl-a][TB:tmpl-b]" {
		t.Errorf("tool_before chain = %q, want {}[TB:tmpl-a][TB:tmpl-b]", args)
	}

	// tool_after is a response leg: onion-reversed (b, a) — outermost first.
	res, err := seam.ToolAfter(context.Background(), "y", "1", "{}", "out", "")
	if err != nil {
		t.Fatalf("ToolAfter: %v", err)
	}
	if res != "out[TA:tmpl-b][TA:tmpl-a]" {
		t.Errorf("tool_after (onion) = %q, want out[TA:tmpl-b][TA:tmpl-a]", res)
	}

	// response_ready response leg: onion-reversed (b, a).
	chunk, err := seam.ResponseReady(context.Background(), "delta", false, "", llm.Usage{})
	if err != nil {
		t.Fatalf("ResponseReady: %v", err)
	}
	if chunk != "delta[RR:tmpl-b][RR:tmpl-a]" {
		t.Errorf("response_ready (onion) = %q, want delta[RR:tmpl-b][RR:tmpl-a]", chunk)
	}

	// turn_error observe-only, runs (currently single-chain run order = listed).
	if err := seam.TurnError(context.Background(), "boom", "turn", ""); err != nil {
		t.Fatalf("TurnError: %v", err)
	}
}

func TestLifecycleSeamConfigOrderOverrides(t *testing.T) {
	mgr, plugs := loadLifecycleScripts(t, "tmpl-a", "tmpl-b")
	defer mgr.Shutdown()
	seam := NewLifecycleSeam(plugs, LifecycleConfig{Order: []string{"tmpl-b", "tmpl-a"}}, nil)

	req := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}
	out, err := seam.RequestBuilt(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestBuilt: %v", err)
	}
	if got := userContent(out); got != "hi[RB:tmpl-b][RB:tmpl-a]" {
		t.Errorf("config-order chain = %q, want hi[RB:tmpl-b][RB:tmpl-a]", got)
	}
	// onion reversal still applies to response legs: reversed([b,a]) = [a,b].
	res, err := seam.ToolAfter(context.Background(), "y", "1", "{}", "out", "")
	if err != nil {
		t.Fatalf("ToolAfter: %v", err)
	}
	if res != "out[TA:tmpl-a][TA:tmpl-b]" {
		t.Errorf("config-order tool_after = %q, want out[TA:tmpl-a][TA:tmpl-b]", res)
	}
}

func TestLifecycleSeamSuppressShortCircuits(t *testing.T) {
	mgr, plugs := loadLifecycleScripts(t, "tmpl-suppress")
	defer mgr.Shutdown()
	seam := NewLifecycleSeam(plugs, LifecycleConfig{}, nil)
	plug := plugs[0]

	// Sanity: declare the capability.
	if !plug.Capabilities.LifecycleToolBefore {
		t.Fatal("suppress plugin should declare lifecycle_tool_before")
	}

	args, suppress, err := seam.ToolBefore(context.Background(), "x", "1", "{}")
	if err != nil {
		t.Fatalf("ToolBefore: %v", err)
	}
	if !suppress {
		t.Error("ToolBefore should suppress tool x")
	}
	// The suppressing hook returned no rewrite, so args stay as accumulated;
	// the call is dead regardless (agent uses only the suppress verdict).
	if args != "{}" {
		t.Errorf("suppressed args = %q, want %q", args, "{}")
	}
}

func TestLifecycleSeamDegradeByDefault(t *testing.T) {
	mgr, plugs := loadLifecycleScripts(t, "tmpl-err", "tmpl-b")
	defer mgr.Shutdown()
	var degraded []string
	seam := NewLifecycleSeam(plugs, LifecycleConfig{}, func(msg string) { degraded = append(degraded, msg) })

	req := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}
	out, err := seam.RequestBuilt(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestBuilt should not hard-fail on a failing hook: %v", err)
	}
	if got := userContent(out); got != "hi[RB:tmpl-b]" {
		t.Errorf("failing hook dropped, prior contributions kept: got %q, want hi[RB:tmpl-b]", got)
	}
	if len(degraded) != 1 || !strings.Contains(degraded[0], "tmpl-err") {
		t.Errorf("expected one degradation naming tmpl-err, got %v", degraded)
	}

	// The same degrade-by-default holds for every event: the failing hook's
	// contribution is dropped, the smart hook's kept, no error surfaces.
	args, suppress, err := seam.ToolBefore(context.Background(), "y", "1", "{}")
	if err != nil || suppress {
		t.Fatalf("ToolBefore should degrade past the failing hook: err=%v suppress=%v", err, suppress)
	}
	if args != "{}[TB:tmpl-b]" {
		t.Errorf("tool_before degrade = %q, want {}[TB:tmpl-b]", args)
	}
	res, err := seam.ToolAfter(context.Background(), "y", "1", "{}", "out", "")
	if err != nil {
		t.Fatalf("ToolAfter should degrade past the failing hook: %v", err)
	}
	if res != "out[TA:tmpl-b]" {
		t.Errorf("tool_after degrade = %q, want out[TA:tmpl-b]", res)
	}
	chunk, err := seam.ResponseReady(context.Background(), "delta", false, "", llm.Usage{})
	if err != nil {
		t.Fatalf("ResponseReady should degrade past the failing hook: %v", err)
	}
	if chunk != "delta[RR:tmpl-b]" {
		t.Errorf("response_ready degrade = %q, want delta[RR:tmpl-b]", chunk)
	}
	if err := seam.TurnError(context.Background(), "boom", "phase", ""); err != nil {
		t.Fatalf("TurnError should degrade past the failing hook: %v", err)
	}
	if len(degraded) != 5 {
		t.Errorf("expected 5 degradations (one per event), got %d: %v", len(degraded), degraded)
	}
}

func TestLifecycleSeamFatalEscalationAborts(t *testing.T) {
	mgr, plugs := loadLifecycleScripts(t, "tmpl-fatal")
	defer mgr.Shutdown()
	seam := NewLifecycleSeam(plugs, LifecycleConfig{}, nil)

	req := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}
	_, err := seam.RequestBuilt(context.Background(), req)
	var fatalErr *agent.FatalHookError
	if !errors.As(err, &fatalErr) {
		t.Fatalf("expected agent.FatalHookError, got %v", err)
	}
	if fatalErr.Event != "lifecycle/request_built" || fatalErr.Plugin != "tmpl-fatal" {
		t.Errorf("fatal error = %+v, want request_built/tmpl-fatal", fatalErr)
	}

	// turn_error is observe-only but the fatal flag still escalates.
	if err := seam.TurnError(context.Background(), "boom", "phase", "partial"); !errors.As(err, &fatalErr) {
		t.Fatalf("expected fatal turn_error error, got %v", err)
	}
}

func TestLifecycleSeamFatalEscalationOnResponseEvents(t *testing.T) {
	mgr, plugs := loadLifecycleScripts(t, "tmpl-tafatal")
	defer mgr.Shutdown()
	seam := NewLifecycleSeam(plugs, LifecycleConfig{}, nil)

	_, err := seam.ToolAfter(context.Background(), "y", "1", "{}", "out", "")
	var fatalErr *agent.FatalHookError
	if !errors.As(err, &fatalErr) || fatalErr.Event != "lifecycle/tool_after" {
		t.Errorf("tool_after fatal = %v, want lifecycle/tool_after", err)
	}

	mgr2, plugs2 := loadLifecycleScripts(t, "tmpl-rrfatal")
	defer mgr2.Shutdown()
	seam2 := NewLifecycleSeam(plugs2, LifecycleConfig{}, nil)
	_, err = seam2.ResponseReady(context.Background(), "", true, llm.FinishStop, llm.Usage{})
	if !errors.As(err, &fatalErr) || fatalErr.Event != "lifecycle/response_ready" {
		t.Errorf("response_ready fatal = %v, want lifecycle/response_ready", err)
	}
}
