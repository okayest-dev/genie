// Package contextmgr implements ContextManager: a transparent llm.Client
// wrapper that sits outermost in the client chain (around RoutingClient) and
// owns history injection and token accounting. From the caller's perspective
// it is indistinguishable from any other llm.Client; on Stream it injects
// prior-turn history from the session so a later turn's request carries
// earlier turns' messages, and it computes the outgoing request's token count
// for the active model via its Counter, against the resolver's authoritative
// context window and budget.
package contextmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"

	"github.com/okayest-dev/og/internal/llm"
	"github.com/okayest-dev/og/internal/modelinfo"
	"github.com/okayest-dev/og/internal/session"
	"github.com/okayest-dev/og/internal/tokens"
)

// Hooks is the plugin context seam: external plugins hook into context
// processing around each Stream. A nil Hooks (or one attached via WithHooks
// whose operations are all inert) is a no-op; every operation may be absent,
// in which case it must return its input unchanged.
type Hooks interface {
	// BeforeRequest rewrites the (already history-injected) request before it
	// is forwarded. Registered hooks run as a deterministic ordered filter
	// chain (Go http-middleware style), each seeing the previous hook's result.
	// before_request may rewrite the message list on top of injected history.
	BeforeRequest(ctx context.Context, req llm.Request) (llm.Request, error)

	// AfterResponse observes a completed turn. It may return a narrow usage/
	// session delta but never rewrites history.
	AfterResponse(ctx context.Context, req llm.Request, usage llm.Usage) error

	// Compact rewrites history (summarise/evict). It is single-active: exactly
	// one implementation runs, from a config-explicit choice defaulting to a
	// built-in registrant. Return req unchanged when no implementation is
	// active.
	Compact(ctx context.Context, req llm.Request) (llm.Request, error)

	// Condense narrows history (e.g. tool-output reduction); it is
	// single-active like Compact. Return req unchanged when not active.
	Condense(ctx context.Context, req llm.Request) (llm.Request, error)
}

// ContextManager is a pure llm.Client decorator holding a session reference.
// It implements exactly the llm.Client surface (Stream + ListModels) and
// nothing more, so callers cannot distinguish it from any other client.
type ContextManager struct {
	inner llm.Client
	sess  *session.Session
	// turns is the number of prior turns of history injected into each new
	// turn's request. Zero means unlimited (the whole conversation).
	turns int
	// counter computes token counts for the active model. Nil disables
	// token accounting.
	counter tokens.Counter
	// resolver supplies the authoritative context window and budget for the
	// active model. Nil means no window is known.
	resolver *modelinfo.Resolver
	// hooks is the optional plugin context seam invoked around each Stream.
	hooks Hooks
	// onDegrade surfaces a visible degradation message to the terminal when a
	// context hook fails; the request still proceeds. Nil silences it (slog
	// always logs).
	onDegrade func(msg string)
	// lastTokens is the token count of the most recent outgoing request.
	lastTokens int
}

// Option configures a ContextManager at construction time.
type Option func(*ContextManager)

// WithTurns limits history injection to the `turns` most recent prior turns.
// Zero means unlimited (the default).
func WithTurns(turns int) Option {
	return func(m *ContextManager) { m.turns = turns }
}

// WithCounter attaches the token Counter used to compute the outgoing
// request's token count for the active model.
func WithCounter(c tokens.Counter) Option {
	return func(m *ContextManager) { m.counter = c }
}

// WithResolver attaches the modelinfo Resolver that supplies the
// authoritative context window and budget for the active model.
func WithResolver(r *modelinfo.Resolver) Option {
	return func(m *ContextManager) { m.resolver = r }
}

// WithHooks attaches the plugin context seam. Nil disables the seam.
func WithHooks(h Hooks) Option {
	return func(m *ContextManager) { m.hooks = h }
}

// WithOnDegrade sets the callback that surfaces a visible context-degradation
// message to the terminal (typically stderr). Called whenever a context hook
// fails and its contribution is skipped while the request still proceeds.
func WithOnDegrade(fn func(msg string)) Option {
	return func(m *ContextManager) { m.onDegrade = fn }
}

// New wraps inner so that Stream requests gain the session's prior history.
func New(inner llm.Client, sess *session.Session, opts ...Option) *ContextManager {
	m := &ContextManager{inner: inner, sess: sess}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Tokens returns the token count of the most recent outgoing request,
// computed with the attached Counter. Zero when no Counter is attached or no
// request has been streamed yet.
func (m *ContextManager) Tokens() int {
	return m.lastTokens
}

// Stream injects prior-turn history from the session into the request, runs
// the plugin context seam (before_request chain, then the active single-active
// compact/condense), forwards to the inner client, and runs the after_response
// chain once the stream drains. It returns a wrapping iterator that re-yields
// inner events unchanged, so the caller observes a transparent stream. Session
// state updates on usage/tool/finish events are owned by the agent loop (which
// persists assistant and tool messages); this decorator only re-serials the
// request so callers stay unaware of context management.
func (m *ContextManager) Stream(ctx context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	req = m.injectHistory(req)
	req = m.runChain(ctx, req, "before_request", func(r llm.Request) (llm.Request, error) {
		return m.hooks.BeforeRequest(ctx, r)
	})
	req = m.runChain(ctx, req, "compact", func(r llm.Request) (llm.Request, error) {
		return m.hooks.Compact(ctx, r)
	})
	req = m.runChain(ctx, req, "condense", func(r llm.Request) (llm.Request, error) {
		return m.hooks.Condense(ctx, r)
	})
	m.recordUsage(ctx, req)

	stream, err := m.inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	usage := llm.Usage{}
	return func(yield func(llm.Event) bool) {
		for ev := range stream {
			if ev.Kind == llm.EventUsage {
				usage = ev.Usage
			}
			if !yield(ev) {
				return
			}
		}
		if m.hooks != nil {
			if err := m.hooks.AfterResponse(ctx, req, usage); err != nil {
				m.degrade("context after_response hook: %v", err)
			}
		}
	}, nil
}

// runChain invokes a single plugin hook on a request, degrading gracefully when
// the hook carries no implementation (nil hooks) or fails. A failing hook's
// contribution is skipped (its output is discarded and the request it received
// is kept, so earlier chain contributions are preserved) and a visible
// degradation is surfaced to the terminal; the request still proceeds.
func (m *ContextManager) runChain(ctx context.Context, req llm.Request, name string, fn func(llm.Request) (llm.Request, error)) llm.Request {
	if m.hooks == nil {
		return req
	}
	out, err := fn(req)
	if err != nil {
		m.degrade("context %s hook: %v", name, err)
		return req
	}
	return out
}

// degrade logs a context-degradation warning and surfaces it to the terminal
// via the optional onDegrade callback. A failed hook is skipped; the request
// still proceeds.
func (m *ContextManager) degrade(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	slog.Warn("context degradation", "detail", msg)
	if m.onDegrade != nil {
		m.onDegrade(msg)
	}
}

// ListModels passes through to the inner client untouched; it is never
// intercepted.
func (m *ContextManager) ListModels(ctx context.Context) ([]llm.Model, error) {
	return m.inner.ListModels(ctx)
}

// injectHistory builds the message list the inner client receives: the current
// turn's agent instruction, followed by prior turns' messages from the
// session, followed by the rest of the current turn.
//
// The session is the source of truth for the conversation. Every turn appends
// exactly one agent-instruction (system-role) message as its first message, so
// the last system-role message in the session history marks the start of the
// current turn. Everything before it is prior-turn history (its system-role
// messages dropped); everything from it onward is the current turn, which the
// agent loop has already appended (its instruction + user before streaming,
// then assistant and tool results as the tool loop runs). Reconstructing from
// the session rather than req.Messages keeps the tool loop correct: an
// assistant tool-call message lives in the session but is never placed on
// req.Messages, so matching the request tail against history would miscount
// and duplicate the current turn.
func (m *ContextManager) injectHistory(req llm.Request) llm.Request {
	if m.sess == nil {
		return req
	}
	history := m.sess.History()

	start := -1
	for i, msg := range history {
		if msg.Role == llm.RoleSystem {
			start = i
		}
	}
	// No system message yet (fresh session awaiting its first append): pass the
	// request through untouched.
	if start < 0 {
		return req
	}

	prior := m.windowPrior(history[:start])

	current := history[start:]
	messages := make([]llm.Message, 0, len(prior)+len(current))
	messages = append(messages, current[0:1]...)
	messages = append(messages, prior...)
	messages = append(messages, current[1:]...)

	req.Messages = messages
	return req
}

// windowPrior returns the prior-history region flattened into a message list
// with the leading system-role instruction messages dropped. A turn always
// begins with a system-role instruction message, so the region can be split
// into turns at those boundaries. When m.turns is positive it keeps only the
// most recent m.turns turns; zero or negative means unlimited.
func (m *ContextManager) windowPrior(raw []llm.Message) []llm.Message {
	var turns [][]llm.Message
	var cur []llm.Message
	for _, msg := range raw {
		if msg.Role == llm.RoleSystem {
			if len(cur) > 0 {
				turns = append(turns, cur)
			}
			cur = nil
			continue
		}
		cur = append(cur, msg)
	}
	if len(cur) > 0 {
		turns = append(turns, cur)
	}

	if m.turns > 0 && len(turns) > m.turns {
		turns = turns[len(turns)-m.turns:]
	}

	var out []llm.Message
	for _, t := range turns {
		out = append(out, t...)
	}
	return out
}

// recordUsage computes the token count of the outgoing request for the
// active model via the attached Counter and records it as the latest usage.
// With a resolver attached, the count is logged against the authoritative
// context window and budget so operators can see the headroom. Counting is
// skipped when no Counter is attached.
func (m *ContextManager) recordUsage(ctx context.Context, req llm.Request) {
	if m.counter == nil {
		return
	}
	n := 0
	for _, msg := range req.Messages {
		n += m.counter.Count(req.Model, msg.Content)
		for _, tc := range msg.ToolCalls {
			n += m.counter.Count(req.Model, tc.Arguments)
		}
	}
	// Tool definitions are part of the prompt context; count each.
	for _, td := range req.Tools {
		// Marshal the tool def to approximate its token cost in the prompt.
		// Exact schema counting is provider-specific; this approximation
		// uses the same counter as the rest of the request.
		if data, err := json.Marshal(td); err == nil {
			n += m.counter.Count(req.Model, string(data))
		}
	}
	m.lastTokens = n

	if m.resolver == nil {
		slog.Debug("context usage", "model", req.Model, "tokens", n)
		return
	}
	window := m.resolver.ContextWindow(ctx, req.Model)
	budget := m.resolver.Budget(ctx, req.Model)
	slog.Debug("context usage", "model", req.Model, "tokens", n, "window", window, "budget", budget)
}
