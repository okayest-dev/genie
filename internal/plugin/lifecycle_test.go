package plugin

// Scripted lifecycle plugins for exercising the LifecycleSeam (og-cbu.5).
// Behavior is keyed off the plugin's own binary name (basename $0), so several
// plugins with different roles can share one manager and one seam:
//
//	tmpl-a / tmpl-b        marker plugins (append per-event suffixes)
//	tmpl-fatal             request_built AND turn_error declare fatal
//	tmpl-tafatal           tool_after declares fatal
//	tmpl-tbfatal           tool_before declares fatal
//	tmpl-rrfatal           response_ready (final) declares fatal
//	tmpl-err               every hook returns a JSON-RPC error (degrades)
//	tmpl-errcount          same, and appends each hook method to $GENIE_HOOK_LOG
//	tmpl-onlytbcount       tool_before alone errors, the rest are healthy
//	tmpl-flakycount        each hook errors its first $GENIE_FLAKY_FAILS calls
//	tmpl-fcount            every hook declares fatal, and logs each call
//	tmpl-diecount          kills its process on tool_before, and logs each call
//	tmpl-suppress          tool_before suppresses the tool named "x"
//	tmpl-wipe              tool_before wipes the arguments to empty (set_empty)
//	tmpl-pass              tool_before observes only, returns an empty result
//
// Marker convention: request_built appends [RB:<name>], tool_after [TA:<name>],
// response_ready [RR:<name>] (non-final deltas only), tool_before [TB:<name>].

import (
	"context"
	"errors"
	"os"
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
fatal_req=false; fatal_tb=false; fatal_ta=false; fatal_rr=false; err_all=false; err_tb=false
suppress=""; wipe=false; pass=false; count=false; die=false; flaky=false
case "$name" in
    *tbfatal*)    fatal_tb=true;;
    *tafatal*)    fatal_ta=true;;
    *rrfatal*)    fatal_rr=true;;
    *fcount*)     fatal_req=true; fatal_tb=true; fatal_ta=true; fatal_rr=true; count=true;;
    *fatal*)      fatal_req=true;;
    *diecount*)   die=true; count=true;;
    *flakycount*) flaky=true; count=true;;
    *errcount*)   err_all=true; count=true;;
    *onlytb*)     err_tb=true;;
    *err*)        err_all=true;;
    *suppress*)   suppress="x";;
    *wipe*)       wipe=true;;
    *pass*)       pass=true;;
esac
case "$name" in
    *count*)      count=true;;
esac

# flaky: fail the calls whose 1-based ordinal is listed in $GENIE_FLAKY_FAILS
# ("1,2,4,5"), so a test can interleave failures and successes on one event and
# drive the counter to a chosen shape.
flaky_fail() {
    local f="$GENIE_HOOK_LOG/flaky-$1"
    echo x >> "$f"
    case ",${GENIE_FLAKY_FAILS:-}," in *",$(wc -l < "$f"),"*) return 0;; esac
    return 1
}

while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    if $count && [ -n "$GENIE_HOOK_LOG" ]; then
        echo "$method" >> "$GENIE_HOOK_LOG/$name.log"
    fi
    if $flaky && [ "$method" != "ping" ] && [ "$method" != "shutdown" ] && [ "$method" != "capabilities/list" ] && flaky_fail "$(echo "$method" | tr / .)"; then
        echo '{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal explosion"},"id":'"$id"'}'
        continue
    fi
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"lifecycle_request_built":true,"lifecycle_tool_before":true,"lifecycle_tool_after":true,"lifecycle_response_ready":true,"lifecycle_turn_error":true,"version":1},"id":'"$id"'}'
            ;;
        "lifecycle/request_built")
            if $err_all; then
                echo '{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal explosion"},"id":'"$id"'}'
            elif $fatal_req; then
                echo "$line" | jq -c --argjson id "$id" '
                    [.params.messages[] | if .role=="user" then .content=(.content + "[RB:fatal]") else . end] as $m
                    | {jsonrpc:"2.0",result:{request:{model:.params.model,messages:$m,tools:.params.tools},fatal:true},id:$id}'
            else
                echo "$line" | jq -c --argjson id "$id" --arg suf "$name" '
                    [.params.messages[] | if .role=="user" then .content=(.content + "[RB:" + $suf + "]") else . end] as $m
                    | {jsonrpc:"2.0",result:{request:{model:.params.model,messages:$m,tools:.params.tools}},id:$id}'
            fi
            ;;
        "lifecycle/tool_before")
            if $die; then
                exit 0
            elif $err_all || $err_tb; then
                echo '{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal explosion"},"id":'"$id"'}'
            elif $fatal_tb; then
                echo '{"jsonrpc":"2.0","result":{"arguments":"","fatal":true},"id":'"$id"'}'
            elif $wipe; then
                echo '{"jsonrpc":"2.0","result":{"set_empty":true},"id":'"$id"'}'
            elif $pass; then
                echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
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
	mgr, plugs := loadLifecycleScriptsOpt(t, nil, nil, names...)
	return mgr, plugs
}

// loadLifecycleScriptsOpt is loadLifecycleScripts with the hook circuit-breaker
// policy and its notice sink, so a test can drive the cooldown and count
// notices.
func loadLifecycleScriptsOpt(t *testing.T, policy *HookBreakerPolicy, notify func(string), names ...string) (*Manager, []*Plugin) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := writeExec(path, lifecycleScript); err != nil {
			t.Fatalf("write plugin script %s: %v", name, err)
		}
	}
	var opts []Option
	if policy != nil {
		opts = append(opts, WithHookBreaker(*policy, notify))
	}
	mgr := NewManager(dir, nil, nil, tools.NewRegistry(), opts...)
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
	// Filter out built-in markdown tracker if present (auto-loaded by manager)
	filtered := make([]*Plugin, 0, len(plug))
	for _, p := range plug {
		if p.Name == "markdown-tracker" {
			continue
		}
		filtered = append(filtered, p)
	}
	if len(filtered) != len(names) {
		t.Fatalf("loaded %d plugins, want %d: %v", len(filtered), len(names), names)
	}
	return mgr, filtered
}

// hookLog turns on per-method call logging for the counting plugins and
// returns a function reporting how many times each hook method was called. The
// log is the counting fake: it asserts how many times the harness actually
// called a hook, not merely that no error surfaced.
func hookLog(t *testing.T) func() map[string]int {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GENIE_HOOK_LOG", dir)
	return func() map[string]int {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read hook log dir: %v", err)
		}
		counts := make(map[string]int)
		for _, e := range entries {
			// flaky-* files are the plugin's own per-method call counters, not
			// the harness's call log.
			if strings.HasPrefix(e.Name(), "flaky-") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("read hook log %s: %v", e.Name(), err)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if line != "" {
					counts[strings.TrimSuffix(e.Name(), ".log")+":"+line]++
				}
			}
		}
		return counts
	}
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

func TestLifecycleSeamToolBeforeWipesToEmpty(t *testing.T) {
	mgr, plugs := loadLifecycleScripts(t, "tmpl-wipe", "tmpl-a")
	defer mgr.Shutdown()
	// Discovery order is alphabetical (tmpl-a before tmpl-wipe); pin the chain
	// order explicitly so wipe runs before the marker append.
	seam := NewLifecycleSeam(plugs, LifecycleConfig{Order: []string{"tmpl-wipe", "tmpl-a"}}, nil)

	// A wipe (set_empty:true) is distinct from "no change": the arguments are
	// replaced with the empty string, and that empty value feeds the next hook
	// in the chain (tmpl-a appends its marker to what it received).
	args, suppress, err := seam.ToolBefore(context.Background(), "y", "1", `{"secret":"abc"}`)
	if err != nil {
		t.Fatalf("ToolBefore: %v", err)
	}
	if suppress {
		t.Error("ToolBefore should not suppress when wiping")
	}
	if args != "[TB:tmpl-a]" {
		t.Errorf("tool_before wipe = %q, want %q (the wipe reached the next hook)", args, "[TB:tmpl-a]")
	}
}

func TestLifecycleSeamToolBeforeEmptyResultIsNoChange(t *testing.T) {
	mgr, plugs := loadLifecycleScripts(t, "tmpl-pass", "tmpl-a")
	defer mgr.Shutdown()
	seam := NewLifecycleSeam(plugs, LifecycleConfig{Order: []string{"tmpl-pass", "tmpl-a"}}, nil)

	// An empty result (no arguments, no set_empty) means "no change": the next
	// hook still sees the original arguments, not a wipe.
	args, suppress, err := seam.ToolBefore(context.Background(), "y", "1", `{"secret":"abc"}`)
	if err != nil {
		t.Fatalf("ToolBefore: %v", err)
	}
	if suppress {
		t.Error("ToolBefore should not suppress")
	}
	if want := `{"secret":"abc"}[TB:tmpl-a]`; args != want {
		t.Errorf("tool_before no-change = %q, want %q", args, want)
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
	got, err := seam.RequestBuilt(context.Background(), req)
	var fatalErr *agent.FatalHookError
	if !errors.As(err, &fatalErr) {
		t.Fatalf("expected agent.FatalHookError, got %v", err)
	}
	if fatalErr.Event != "lifecycle/request_built" || fatalErr.Plugin != "tmpl-fatal" {
		t.Errorf("fatal error = %+v, want request_built/tmpl-fatal", fatalErr)
	}
	// Bug #3 guard: the aborting hook's own rewrite must NOT be committed into
	// the returned request — the abort can't leak the mutating plugin's content.
	if gotM := userContent(got); gotM != "hi" {
		t.Errorf("request_built fatal leaked the aborting hook's rewrite: content %q, want %q", gotM, "hi")
	}
	if fatalErr.Cause != nil {
		t.Errorf("mid-turn fatal should have no cause, got %v", fatalErr.Cause)
	}

	// turn_error is observe-only but the fatal flag still escalates, and must
	// wrap the original error (bug #2 guard: root cause is never masked).
	turnErr := seam.TurnError(context.Background(), "boom", "phase", "partial")
	if !errors.As(turnErr, &fatalErr) {
		t.Fatalf("expected fatal turn_error error, got %v", turnErr)
	}
	if fatalErr.Event != "lifecycle/turn_error" {
		t.Errorf("turn_error fatal event = %q, want lifecycle/turn_error", fatalErr.Event)
	}
	if err := errors.Unwrap(turnErr); err == nil || err.Error() != "boom" {
		t.Errorf("turn_error fatal unwrap = %v, want error wrapping %q", err, "boom")
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

// --- hook circuit breaker (og-9xd) -----------------------------------------

// newBrokenSeam loads one always-failing plugin wired to a breaker with the
// given threshold, plus a counting sink for the seam's own degrade output and a
// second sink for the breaker's trip/recovery notices.
func newBrokenSeam(t *testing.T, threshold int, recovery time.Duration, name string) (*LifecycleSeam, *Plugin, *[]string, *[]string) {
	t.Helper()
	var degraded, notices []string
	policy := HookBreakerPolicy{Threshold: threshold, Recovery: recovery}
	mgr, plugs := loadLifecycleScriptsOpt(t, &policy, func(msg string) {
		notices = append(notices, msg)
	}, name)
	t.Cleanup(mgr.Shutdown)
	seam := NewLifecycleSeam(plugs, LifecycleConfig{}, func(msg string) {
		degraded = append(degraded, msg)
	})
	return seam, plugs[0], &degraded, &notices
}

func TestLifecycleBreakerStopsCallingAfterThreshold(t *testing.T) {
	calls := hookLog(t)
	seam, plug, degraded, notices := newBrokenSeam(t, 3, time.Minute, "tmpl-errcount")

	for i := 0; i < 20; i++ {
		if _, _, err := seam.ToolBefore(context.Background(), "y", "1", "{}"); err != nil {
			t.Fatalf("ToolBefore %d should degrade, not fail: %v", i, err)
		}
	}
	if got := calls()["tmpl-errcount:lifecycle/tool_before"]; got != 3 {
		t.Errorf("tool_before called %d times, want exactly the threshold of 3", got)
	}
	if len(*degraded) != 2 {
		// The failure that trips is covered by the trip notice, not a degrade
		// line, so only the two below-threshold failures reach the sink.
		t.Errorf("degrade sink called %d times, want 2 (one per failure below the threshold)", len(*degraded))
	}
	if len(*notices) != 1 {
		t.Errorf("notices = %v, want exactly one trip notice", *notices)
	}
	if !plug.HookTripped(HookToolBefore) {
		t.Error("want tool_before tripped")
	}
}

func TestLifecycleBreakerEventsAreIndependent(t *testing.T) {
	calls := hookLog(t)
	var degraded []string
	policy := HookBreakerPolicy{Threshold: 2, Recovery: time.Minute}
	mgr, plugs := loadLifecycleScriptsOpt(t, &policy, nil, "tmpl-onlytbcount")
	defer mgr.Shutdown()
	seam := NewLifecycleSeam(plugs, LifecycleConfig{}, func(msg string) { degraded = append(degraded, msg) })

	// Break tool_before hard: threshold 2, then it is held out.
	for i := 0; i < 6; i++ {
		if _, _, err := seam.ToolBefore(context.Background(), "y", "1", "{}"); err != nil {
			t.Fatalf("ToolBefore: %v", err)
		}
	}
	if got := calls()["tmpl-onlytbcount:lifecycle/tool_before"]; got != 2 {
		t.Errorf("tool_before called %d times, want 2", got)
	}
	if !plugs[0].HookTripped(HookToolBefore) {
		t.Fatal("tool_before should be tripped")
	}

	// Every other event keeps participating: it is a different hook.
	for i := 0; i < 4; i++ {
		if _, err := seam.ToolAfter(context.Background(), "y", "1", "{}", "out", ""); err != nil {
			t.Fatalf("ToolAfter: %v", err)
		}
		if err := seam.TurnError(context.Background(), "boom", "turn", ""); err != nil {
			t.Fatalf("TurnError: %v", err)
		}
	}
	if got := calls()["tmpl-onlytbcount:lifecycle/tool_after"]; got != 4 {
		t.Errorf("tool_after called %d times, want 4 — a healthy event is unaffected", got)
	}
	if got := calls()["tmpl-onlytbcount:lifecycle/turn_error"]; got != 4 {
		t.Errorf("turn_error called %d times, want 4", got)
	}
}

func TestLifecycleBreakerSuccessResetsCounter(t *testing.T) {
	hookLog(t)
	// Ordinals 1, 2, 4 and 5 fail; 3 succeeds.
	t.Setenv("GENIE_FLAKY_FAILS", "1,2,4,5")
	policy := HookBreakerPolicy{Threshold: 3, Recovery: time.Minute}
	var degraded, notices []string
	mgr, plugs := loadLifecycleScriptsOpt(t, &policy, func(msg string) { notices = append(notices, msg) }, "tmpl-flakycount")
	defer mgr.Shutdown()
	seam := NewLifecycleSeam(plugs, LifecycleConfig{}, func(msg string) { degraded = append(degraded, msg) })

	// Two failures, a success, two more failures: five calls, no trip.
	for i := 0; i < 5; i++ {
		_, _, _ = seam.ToolBefore(context.Background(), "y", "1", "{}")
	}
	if plugs[0].HookTripped(HookToolBefore) {
		t.Error("two failures, a success, two more failures must not trip")
	}
	if len(degraded) != 4 {
		t.Errorf("degrade sink called %d times, want 4 (one per failure, none suppressed)", len(degraded))
	}
	if degraded[0] == "" {
		t.Error("every failure below the threshold should still degrade")
	}
	if len(notices) != 0 {
		t.Errorf("notices = %v, want none", notices)
	}
}

func TestLifecycleBreakerResponseReadyNoticesOncePerTurn(t *testing.T) {
	seam, _, degraded, notices := newBrokenSeam(t, 2, time.Minute, "tmpl-errcount")

	// response_ready fires per text delta, so 20 deltas is one turn.
	for i := 0; i < 20; i++ {
		if _, err := seam.ResponseReady(context.Background(), "d", false, "", llm.Usage{}); err != nil {
			t.Fatalf("ResponseReady %d: %v", i, err)
		}
	}
	if len(*degraded) != 1 {
		t.Errorf("degrade sink called %d times for 20 deltas, want 1 (the trip notice replaces the rest)", len(*degraded))
	}
	if len(*notices) != 1 {
		t.Errorf("notices = %v, want exactly one trip notice for the whole turn", *notices)
	}
}

func TestLifecycleBreakerFatalNeitherCountsNorResets(t *testing.T) {
	calls := hookLog(t)
	policy := HookBreakerPolicy{Threshold: 2, Recovery: time.Minute}
	var notices []string
	mgr, plugs := loadLifecycleScriptsOpt(t, &policy, func(msg string) { notices = append(notices, msg) }, "tmpl-fcount")
	defer mgr.Shutdown()
	seam := NewLifecycleSeam(plugs, LifecycleConfig{}, nil)

	// Every event declares fatal. A deliberate abort is policy, not breakage, so
	// the plugin is never tripped and never degraded.
	for i := 0; i < 5; i++ {
		seam.RequestBuilt(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}})
		_, _, _ = seam.ToolBefore(context.Background(), "y", "1", "{}")
		seam.ToolAfter(context.Background(), "y", "1", "{}", "out", "")
		seam.ResponseReady(context.Background(), "d", false, "", llm.Usage{})
		seam.TurnError(context.Background(), "boom", "turn", "")
	}
	for _, event := range []string{HookRequestBuilt, HookToolBefore, HookToolAfter, HookResponseReady, HookTurnError} {
		if plugs[0].HookTripped(event) {
			t.Errorf("event %s tripped on fatal declarations", event)
		}
	}
	if got := plugs[0].TrippedHooks(); len(got) != 0 {
		t.Errorf("TrippedHooks = %v, want none", got)
	}
	if len(notices) != 0 {
		t.Errorf("notices = %v, want none", notices)
	}
	if got := calls()["tmpl-fcount:lifecycle/tool_before"]; got != 5 {
		t.Errorf("tool_before called %d times, want 5 — a fatal plugin keeps being called", got)
	}
}

func TestLifecycleBreakerProbeAfterCooldown(t *testing.T) {
	calls := hookLog(t)
	seam, plug, _, notices := newBrokenSeam(t, 1, 30*time.Millisecond, "tmpl-errcount")

	// Fail once, which trips.
	_, _, _ = seam.ToolBefore(context.Background(), "y", "1", "{}")
	if !plug.HookTripped(HookToolBefore) {
		t.Fatal("want tripped")
	}
	tripCount := len(*notices)

	// Inside the cooldown nothing is called, however many times we ask.
	for i := 0; i < 10; i++ {
		_, _, _ = seam.ToolBefore(context.Background(), "y", "1", "{}")
	}
	if got := calls()["tmpl-errcount:lifecycle/tool_before"]; got != 1 {
		t.Fatalf("tool_before called %d times inside the cooldown, want 1", got)
	}

	// After it, exactly one probe is admitted — response_ready-style
	// single-flight, asserted on the call count.
	time.Sleep(60 * time.Millisecond)
	for i := 0; i < 10; i++ {
		_, _, _ = seam.ToolBefore(context.Background(), "y", "1", "{}")
	}
	if got := calls()["tmpl-errcount:lifecycle/tool_before"]; got != 2 {
		t.Errorf("tool_before called %d times after the cooldown, want 2 (one probe)", got)
	}
	if len(*notices) != tripCount {
		t.Errorf("a re-tripping probe emitted a new notice: %v", *notices)
	}
}

func TestLifecycleBreakerZeroRecoveryIsOneWayDoor(t *testing.T) {
	calls := hookLog(t)
	seam, plug, _, _ := newBrokenSeam(t, 1, 0, "tmpl-errcount")

	_, _, _ = seam.ToolBefore(context.Background(), "y", "1", "{}")
	for i := 0; i < 10; i++ {
		_, _, _ = seam.ToolBefore(context.Background(), "y", "1", "{}")
	}
	if got := calls()["tmpl-errcount:lifecycle/tool_before"]; got != 1 {
		t.Errorf("tool_before called %d times, want 1 — hook_recovery_seconds = 0 is a one-way door", got)
	}
	if !plug.HookTripped(HookToolBefore) {
		t.Error("want tripped")
	}
}

func TestLifecycleBreakerThresholdOneTripsImmediately(t *testing.T) {
	calls := hookLog(t)
	seam, plug, degraded, notices := newBrokenSeam(t, 1, time.Minute, "tmpl-errcount")

	_, _, _ = seam.ToolBefore(context.Background(), "y", "1", "{}")
	if got := calls()["tmpl-errcount:lifecycle/tool_before"]; got != 1 {
		t.Errorf("tool_before called %d times, want 1", got)
	}
	if !plug.HookTripped(HookToolBefore) {
		t.Error("want tripped on the first failure")
	}
	if len(*degraded) != 0 {
		t.Errorf("degrade sink called %d times, want 0 — the trip notice replaces it", len(*degraded))
	}
	if len(*notices) != 1 {
		t.Errorf("notices = %v, want one trip notice", *notices)
	}
}

func TestLifecycleBreakerLivenessIsNotHealth(t *testing.T) {
	calls := hookLog(t)
	policy := HookBreakerPolicy{Threshold: 1, Recovery: time.Minute}
	var notices []string
	mgr, plugs := loadLifecycleScriptsOpt(t, &policy, func(msg string) {
		notices = append(notices, msg)
	}, "tmpl-diecount")
	defer mgr.Shutdown()
	seam := NewLifecycleSeam(plugs, LifecycleConfig{}, nil)

	for i := 0; i < 5; i++ {
		if _, _, err := seam.ToolBefore(context.Background(), "y", "1", "{}"); err != nil {
			t.Fatalf("ToolBefore %d should degrade past a dead plugin: %v", i, err)
		}
	}
	// The process died on the first call; the other four never reached it.
	if got := calls()["tmpl-diecount:lifecycle/tool_before"]; got != 1 {
		t.Errorf("tool_before reached the plugin %d times, want 1", got)
	}
	if plugs[0].HookTripped(HookToolBefore) {
		t.Error("a liveness failure must not trip the breaker")
	}
	if len(notices) != 1 {
		t.Fatalf("notices = %v, want exactly one liveness notice", notices)
	}
	if !strings.Contains(notices[0], "killed") || !strings.Contains(notices[0], "tool_before") {
		t.Errorf("liveness notice %q should name the plugin being killed and the event", notices[0])
	}
}
