// Lifecycle seam: turns loaded plugins that declare granular lifecycle-hook
// capabilities (og-cbu.3 event model) into an agent.Hooks the agent loop
// invokes around each turn. All five events are sync ordered chains (og-cbu.4):
// request-side events fire in config-listed order, response-side events in
// reversed (onion) order so inverse pairs pack/expand nest. turn_error is
// observe-only and runs in listed order. Every hook degrades by default (skip,
// keep prior contributions); a plugin may opt-in to a per-event fatal escalation
// that aborts the turn (agent.FatalHookError).
package plugin

import (
	"context"
	"errors"
	"log/slog"

	"github.com/okayest-dev/genie/internal/agent"
	"github.com/okayest-dev/genie/internal/llm"
)

// LifecycleConfig is the resolved [lifecycle.plugins] selection passed to the
// seam builder.
type LifecycleConfig struct {
	// Order is the deterministic chain order shared by all lifecycle events
	// (plugin names first, in order; any plugin not listed is appended in
	// registration order). Empty means registration order for all. Request-side
	// events fire in this order; response-side events fire reversed (onion).
	Order []string
}

// LifecycleSeam implements agent.Hooks by dispatching to loaded plugins. It is
// built once at startup and is safe for concurrent use.
type LifecycleSeam struct {
	requestBuilt  []*Plugin
	toolBefore    []*Plugin
	toolAfter     []*Plugin // onion-reversed
	responseReady []*Plugin // onion-reversed
	turnError     []*Plugin
	onDegrade     func(msg string)
}

var _ agent.Hooks = (*LifecycleSeam)(nil)

// NewLifecycleSeam resolves the loaded plugins against the [lifecycle.plugins]
// selection and returns a LifecycleSeam. plugins must be in registration
// (discovery) order, which is the default chain order. onDegrade surfaces a
// visible degradation message to the terminal when a hook fails; it may be nil.
func NewLifecycleSeam(plugins []*Plugin, cfg LifecycleConfig, onDegrade func(msg string)) *LifecycleSeam {
	byName := make(map[string]*Plugin, len(plugins))
	for _, p := range plugins {
		byName[p.Name] = p
	}

	s := &LifecycleSeam{onDegrade: onDegrade}
	s.requestBuilt = orderedPlugins(byName, plugins, cfg.Order, func(p *Plugin) bool { return p.Capabilities.LifecycleRequestBuilt })
	s.toolBefore = orderedPlugins(byName, plugins, cfg.Order, func(p *Plugin) bool { return p.Capabilities.LifecycleToolBefore })
	// Response-side legs reverse the shared order (onion): outermost first.
	s.toolAfter = reversed(orderedPlugins(byName, plugins, cfg.Order, func(p *Plugin) bool { return p.Capabilities.LifecycleToolAfter }))
	s.responseReady = reversed(orderedPlugins(byName, plugins, cfg.Order, func(p *Plugin) bool { return p.Capabilities.LifecycleResponseReady }))
	s.turnError = orderedPlugins(byName, plugins, cfg.Order, func(p *Plugin) bool { return p.Capabilities.LifecycleTurnError })
	return s
}

func reversed(ps []*Plugin) []*Plugin {
	out := make([]*Plugin, len(ps))
	for i, p := range ps {
		out[len(ps)-1-i] = p
	}
	return out
}

// RequestBuilt runs the request_built chain over the assembled turn request. A
// failing hook is skipped (contribution dropped); a fatal declaration aborts
// with agent.FatalHookError.
func (s *LifecycleSeam) RequestBuilt(ctx context.Context, req llm.Request) (llm.Request, error) {
	cur := req
	for _, p := range s.requestBuilt {
		if !p.AdmitHook(HookRequestBuilt) {
			continue
		}
		out, fatal, err := p.CallLifecycleRequestBuilt(ctx, cur)
		if err != nil {
			s.degradeHook(p, HookRequestBuilt, err)
			continue
		}
		if fatal {
			// A fatal declaration aborts the turn: do NOT commit the aborting
			// hook's own rewrite into the request. Return the request as it was
			// before this hook so nothing its plugin mutated leaks into the
			// (aborted) turn. A declaration is policy, not breakage, so it
			// neither counts as a failure nor clears the event's counter.
			return cur, s.fatalNoCause(p.Name, "lifecycle/request_built")
		}
		p.HookSucceeded(HookRequestBuilt)
		cur = out
	}
	return cur, nil
}

// ToolBefore runs the tool_before guardrail chain. The first hook to suppress
// short-circuits — the call is dead, remaining hooks are not consulted, and
// suppress=true is returned. A fatal declaration aborts the turn.
func (s *LifecycleSeam) ToolBefore(ctx context.Context, name, id, args string) (string, bool, error) {
	cur := args
	for _, p := range s.toolBefore {
		if !p.AdmitHook(HookToolBefore) {
			continue
		}
		out, fatal, err := p.CallLifecycleToolBefore(ctx, name, id, cur)
		if err != nil {
			s.degradeHook(p, HookToolBefore, err)
			continue
		}
		// An explicit set_empty (distinct from an empty arguments, which means
		// "no change") wipes the args to the empty string, taking precedence
		// over a non-empty arguments; the wipe still flows through the
		// agent-side JSON validation, so a call wiped to "" fails closed unless
		// "" is valid for the tool.
		if out.SetEmpty {
			cur = ""
		} else if out.Arguments != "" {
			cur = out.Arguments
		}
		if fatal {
			return cur, false, s.fatalNoCause(p.Name, "lifecycle/tool_before")
		}
		p.HookSucceeded(HookToolBefore)
		if out.Suppress {
			slog.Info("lifecycle: tool suppressed", "plugin", p.Name, "tool", name)
			return cur, true, nil
		}
	}
	return cur, false, nil
}

// ToolAfter runs the tool_after chain over a completed tool call (onion order).
func (s *LifecycleSeam) ToolAfter(ctx context.Context, name, id, args, result, errText string) (string, error) {
	cur := result
	for _, p := range s.toolAfter {
		if !p.AdmitHook(HookToolAfter) {
			continue
		}
		out, fatal, err := p.CallLifecycleToolAfter(ctx, name, id, args, cur, errText)
		if err != nil {
			s.degradeHook(p, HookToolAfter, err)
			continue
		}
		cur = out.Result
		if fatal {
			return cur, s.fatalNoCause(p.Name, "lifecycle/tool_after")
		}
		p.HookSucceeded(HookToolAfter)
	}
	return cur, nil
}

// ResponseReady runs the response_ready chain over one streaming delta (or the
// final release) in onion order. It fires per delta, so it is also the event
// the circuit breaker has the most to say about.
func (s *LifecycleSeam) ResponseReady(ctx context.Context, chunk string, final bool, finish llm.FinishReason, usage llm.Usage) (string, error) {
	cur := chunk
	for _, p := range s.responseReady {
		if !p.AdmitHook(HookResponseReady) {
			continue
		}
		out, fatal, err := p.CallLifecycleResponseReady(ctx, cur, final, finish, usage)
		if err != nil {
			s.degradeHook(p, HookResponseReady, err)
			continue
		}
		cur = out.Chunk
		if fatal {
			return cur, s.fatalNoCause(p.Name, "lifecycle/response_ready")
		}
		p.HookSucceeded(HookResponseReady)
	}
	return cur, nil
}

// TurnError runs the observe-only turn_error chain once at a failing turn exit.
// A fatal declaration aborts, wrapping the original error it observed so the
// turn error is never masked by the escalation.
func (s *LifecycleSeam) TurnError(ctx context.Context, errText, phase, partial string) error {
	for _, p := range s.turnError {
		if !p.AdmitHook(HookTurnError) {
			continue
		}
		fatal, err := p.CallLifecycleTurnError(ctx, errText, phase, partial)
		if err != nil {
			s.degradeHook(p, HookTurnError, err)
			continue
		}
		if fatal {
			return s.fatal(p.Name, "lifecycle/turn_error", errText)
		}
		p.HookSucceeded(HookTurnError)
	}
	return nil
}

// fatalNoCause builds a turn-scoped abort for a mid-turn event that has no
// underlying error to preserve (e.g. request_built, tool_*).
func (s *LifecycleSeam) fatalNoCause(pluginName, event string) error {
	return agent.NewFatalHookError(pluginName, event, nil)
}

// fatal builds the turn-scoped abort error naming the responsible plugin and
// event. cause, when non-empty, is wrapped so the root error survives.
func (s *LifecycleSeam) fatal(pluginName, event, causeText string) error {
	var cause error
	if causeText != "" {
		cause = errors.New(causeText)
	}
	return agent.NewFatalHookError(pluginName, event, cause)
}

// degrade surfaces a visible degradation message to the terminal (via onDegrade)
// and logs it. A failed hook never fails the turn.
func (s *LifecycleSeam) degrade(msg string) {
	slog.Warn("lifecycle degradation", "detail", msg)
	if s.onDegrade != nil {
		s.onDegrade(msg)
	}
}

// degradeHook records a failed hook call against the plugin's per-(plugin,
// event) circuit breaker and surfaces the per-occurrence message the breaker
// asks for. A tripped event returns no message: its one trip notice already
// covers the event, and repeating it per occurrence is the noise this exists to
// stop. The breaker, not this seam, owns the counter and the suppression, so
// both seams behave identically for the same (plugin, event).
func (s *LifecycleSeam) degradeHook(p *Plugin, event string, err error) {
	degradeHook(s.degrade, p, event, err)
}
