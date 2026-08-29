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

	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/modelinfo"
	"github.com/okayest-dev/genie/internal/session"
	"github.com/okayest-dev/genie/internal/tokens"
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
	// cm is the derived context map over the session transcript. It is rebuilt
	// from the transcript on each request and supplies the structural layer
	// boundaries request assembly retains against.
	cm *ContextMap
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
	m := &ContextManager{inner: inner, sess: sess, cm: NewMap()}
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

// injectHistory builds the message list the inner client receives: the fixed
// cheap spine (agent instruction + current turn) followed by per-layer
// retention of prior turns, assembled from the context map.
//
// The session transcript is the source of truth. Each request rebuilds the map
// from it and layers every message by role; the last instruction-layer entry
// marks the current turn's first line, so the turn boundary is structural
// rather than a scan for the last system message. (Rebuilding per request
// keeps the derived index in step with appends the manager only sees at Stream
// time; the map stays read-only over the transcript, and token-level caching
// lands separately in og-8qu.6.) Prior turns are retained by turn: durable
// intent (user + assistant) windowed by the turns config, tool-output riding
// its turn's window until og-8qu.4 gives it a condensation layer rule keyed by
// tool-call-id. Prior instructions are never shipped; the current instruction
// appears exactly once. The wire still receives one full ordered message list.
// Marker lines (compaction summaries) are JSONL metadata, part of no layer,
// and are never shipped.
//
// Reconstructing from the transcript rather than req.Messages keeps the tool
// loop correct: an assistant tool-call message lives in the session but is
// never placed on req.Messages, so matching the request tail against history
// would miscount and duplicate the current turn.
func (m *ContextManager) injectHistory(req llm.Request) llm.Request {
	if m.sess == nil {
		return req
	}
	lines, err := m.sess.Lines()
	if err != nil {
		m.degrade("context transcript read: %v", err)
		return req
	}
	// Rebuild the map from the same lines assembly reads so line indices and
	// the flat list cannot drift.
	m.cm.rebuild(lines)

	instr := m.cm.Layer(LayerInstruction)
	if len(instr) == 0 {
		// No instruction yet (fresh session awaiting its first append): pass
		// the request through untouched.
		return req
	}
	currentStart := instr[len(instr)-1].Line

	retained := m.retainPriorTurns(instr, currentStart)

	messages := make([]llm.Message, 0, len(lines))
	// Spine: the current instruction ships first and once. It is an indexed
	// instruction-layer entry, so it is always a message.
	messages = append(messages, lines[currentStart].Message())
	// Prior per-layer retention in transcript order; prior instructions are
	// dropped and marker lines never ship.
	for i, ln := range lines[:currentStart] {
		if !retained[i] {
			continue
		}
		msg, ok := messageOf(ln)
		if !ok || msg.Role == llm.RoleSystem {
			continue
		}
		messages = append(messages, msg)
	}
	// Rest of the current turn.
	for _, ln := range lines[currentStart+1:] {
		if msg, ok := messageOf(ln); ok {
			messages = append(messages, msg)
		}
	}

	req.Messages = messages
	return req
}

// messageOf converts a transcript line to its canonical message. A marker line
// (compaction metadata, part of no layer) or an unrecognised role yields no
// message.
func messageOf(ln session.TranscriptLine) (llm.Message, bool) {
	if LayerOf(ln.Role) == "" {
		return llm.Message{}, false
	}
	return ln.Message(), true
}

// retainPriorTurns selects which transcript lines of the prior region are
// retained under the turns window. Turn boundaries come from the instruction
// layer: each turn begins at an instruction line, so the entries before
// currentStart delimit the prior turns and currentStart ends the last one.
// When m.turns is positive it keeps only the most recent m.turns prior turns;
// zero or negative means unlimited (every line before the current turn). The
// caller additionally drops prior instruction lines and marker lines.
func (m *ContextManager) retainPriorTurns(instr []Entry, currentStart int) []bool {
	retained := make([]bool, currentStart)

	var boundaries []int
	for _, e := range instr {
		if e.Line < currentStart {
			boundaries = append(boundaries, e.Line)
		}
	}
	priorTurns := len(boundaries)
	if m.turns <= 0 || priorTurns <= m.turns {
		for i := range retained {
			retained[i] = true
		}
		return retained
	}

	startTurn := priorTurns - m.turns
	for k := startTurn; k < priorTurns; k++ {
		end := currentStart
		if k+1 < priorTurns {
			end = boundaries[k+1]
		}
		for i := boundaries[k]; i < end; i++ {
			retained[i] = true
		}
	}
	return retained
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
