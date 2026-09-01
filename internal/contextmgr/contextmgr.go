// Package contextmgr implements ContextManager: a transparent llm.Client
// wrapper that sits outermost in the client chain (around RoutingClient) and
// owns history injection, token accounting, and the context-management
// policies of the layered context map (ADR-0001): per-message token-count
// caching keyed by line index, request-time tool-output condensation, and
// synchronous durable-intent compaction under a budget. From the caller's
// perspective it is indistinguishable from any other llm.Client; on Stream it
// injects prior-turn history from the session so a later turn's request carries
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
	"strings"
	"unicode/utf8"

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
	// built-in registrant supplied by the ContextManager. Return req unchanged
	// when no implementation is active.
	Compact(ctx context.Context, req llm.Request) (llm.Request, error)

	// Condense narrows history (e.g. tool-output reduction); it is
	// single-active like Compact. Return req unchanged when not active.
	Condense(ctx context.Context, req llm.Request) (llm.Request, error)
}

// singleActiveSeam is an optional capability the Hooks seam may expose so the
// ContextManager knows whether it must run its own built-in single-active
// compact/condense implementation, or delegate to the seam's (an external
// plugin selected in config). A Hooks value that does not implement it is
// treated as supplying its own implementations; a nil Hooks means the built-ins
// are the active registrants.
type singleActiveSeam interface {
	CompactBuiltin() bool
	CondenseBuiltin() bool
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
	// condenseSize is the per-call token threshold above which a prior-turn
	// tool result is narrowed (or, with netDrop, dropped) before a request is
	// forwarded. Zero or negative disables condensation.
	condenseSize int
	// netDrop drops oversized prior-turn tool results from requests entirely,
	// leaving their assistant tool-call so the model sees the call was invoked.
	// Off by default.
	netDrop bool
	// counts caches one per-message token count per stable line index, so
	// unchanged history is not re-counted across requests. Grown lazily on the
	// first Stream, never at map build.
	counts map[int]cachedCount
	// toolDefCounts caches marshalled tool-definition token costs by their
	// canonical JSON, so tool definitions are counted once per distinct def.
	toolDefCounts map[string]int
	// lastTokens is the token count of the most recent outgoing request.
	lastTokens int
}

// cachedCount is one per-message token count cached by line index under a
// model, with a fingerprint of the message fields that contribute to it so a
// hook-rewritten message is never charged a stale count.
type cachedCount struct {
	model string
	fp    string
	n     int
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

// WithCondenseSize sets the per-call token threshold above which a prior-turn
// tool result is condensed (or net-dropped) before a request is forwarded.
// Zero or negative disables condensation.
func WithCondenseSize(size int) Option {
	return func(m *ContextManager) { m.condenseSize = size }
}

// WithNetDrop opts oversized prior-turn tool results into being dropped from
// requests entirely (their assistant tool-call stays). Off by default.
func WithNetDrop(b bool) Option {
	return func(m *ContextManager) { m.netDrop = b }
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
// request so callers stay unaware of context management. The built-in
// compactor runs synchronously inside the assembly (before the request is
// forwarded), and the built-in condenser projects the prior tool-output layer.
func (m *ContextManager) Stream(ctx context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	req, sources := m.injectHistory(req)
	req = m.runChain(ctx, req, "before_request", func(r llm.Request) (llm.Request, error) {
		return m.hooks.BeforeRequest(ctx, r)
	})
	req, sources = m.compactStep(ctx, req, sources)
	req, sources = m.condenseStep(ctx, req, sources)
	m.recordUsage(ctx, req, sources)

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

// builtInCompactActive reports whether the harness's built-in compactor is the
// active single-active implementation: no hook seam is attached, or the seam
// selected the built-in over an external plugin.
func (m *ContextManager) builtInCompactActive() bool {
	if m.hooks == nil {
		return true
	}
	if s, ok := m.hooks.(singleActiveSeam); ok {
		return s.CompactBuiltin()
	}
	return false
}

// builtInCondenseActive reports whether the harness's built-in condenser is
// the active single-active implementation.
func (m *ContextManager) builtInCondenseActive() bool {
	if m.hooks == nil {
		return true
	}
	if s, ok := m.hooks.(singleActiveSeam); ok {
		return s.CondenseBuiltin()
	}
	return false
}

// compactStep runs the single-active compactor: the built-in when it is the
// active registrant (and the ContextManager owns that side of the seam), the
// seam's plugin implementation otherwise. It returns the rewritten request and
// the per-message source-line index slice kept aligned with it.
func (m *ContextManager) compactStep(ctx context.Context, req llm.Request, sources []int) (llm.Request, []int) {
	if m.builtInCompactActive() {
		return m.builtinCompact(ctx, req, sources)
	}
	return m.runChain(ctx, req, "compact", func(r llm.Request) (llm.Request, error) {
		return m.hooks.Compact(ctx, r)
	}), sources
}

// condenseStep runs the single-active condenser: the built-in when it is the
// active registrant, the seam's plugin implementation otherwise.
func (m *ContextManager) condenseStep(ctx context.Context, req llm.Request, sources []int) (llm.Request, []int) {
	if m.builtInCondenseActive() {
		return m.builtinCondense(req, sources)
	}
	return m.runChain(ctx, req, "condense", func(r llm.Request) (llm.Request, error) {
		return m.hooks.Condense(ctx, r)
	}), sources
}

// injectHistory builds the message list the inner client receives: the fixed
// cheap spine (agent instruction + current turn) followed by per-layer
// retention of prior turns, assembled from the context map. The second return
// value is a per-message source slice: the stable transcript line index each
// assembled message came from (or -1 for a synthetic summary message), which
// token accounting and condensation key against.
//
// The session transcript is the source of truth. Each request rebuilds the map
// from it and layers every message by role; the last instruction-layer entry
// marks the current turn's first line, so the turn boundary is structural
// rather than a scan for the last system message. Prior turns are retained by
// turn: durable intent (user + assistant) windowed by the turns config,
// tool-output riding its turn's window until the built-in condenser narrows it.
// A compaction marker naming a line range suppresses the lines it summarised
// and substitutes a synthetic durable-intent summary message, so a compacted
// (or resumed) session respects the persisted summary on every subsequent
// request. Prior instructions are never shipped; the current instruction
// appears exactly once. The wire still receives one full ordered message list,
// and marker lines (compaction summaries) are JSONL metadata that never ship.
//
// Reconstructing from the transcript rather than req.Messages keeps the tool
// loop correct: an assistant tool-call message lives in the session but is
// never placed on req.Messages, so matching the request tail against history
// would miscount and duplicate the current turn.
func (m *ContextManager) injectHistory(req llm.Request) (llm.Request, []int) {
	if m.sess == nil {
		return req, nil
	}
	lines, err := m.sess.Lines()
	if err != nil {
		m.degrade("context transcript read: %v", err)
		return req, nil
	}
	// Rebuild the map from the same lines assembly reads so line indices and
	// the flat list cannot drift.
	m.cm.rebuild(lines)

	instr := m.cm.Layer(LayerInstruction)
	if len(instr) == 0 {
		// No instruction yet (fresh session awaiting its first append): pass
		// the request through untouched.
		return req, nil
	}
	currentStart := instr[len(instr)-1].Line

	retained := m.retainPriorTurns(instr, currentStart)

	messages := make([]llm.Message, 0, len(lines))
	sources := make([]int, 0, len(lines))
	// Spine: the current instruction ships first and once. It is an indexed
	// instruction-layer entry, so it is a message with a known line.
	messages = append(messages, lines[currentStart].Message())
	sources = append(sources, currentStart)

	emit := func(line int) {
		msg, ok := messageOf(lines[line])
		if !ok || msg.Role == llm.RoleSystem {
			return
		}
		messages = append(messages, msg)
		sources = append(sources, line)
	}
	emitSummary := func(summary string) {
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: summary})
		sources = append(sources, -1)
	}

	// Prior per-layer retention in transcript order. Compaction markers part
	// the prior region: the lines a marker's range covers are suppressed and
	// replaced by the marker's summary at the same spot. Ranges are emitted in
	// marker order (the compactor always evicts the oldest remaining intent,
	// so ranges never overlap).
	cur := 0
	for _, comp := range m.cm.Compactions() {
		if !comp.RangeValid() || comp.To >= currentStart {
			continue
		}
		for ; cur < currentStart && cur < comp.From; cur++ {
			if retained[cur] {
				emit(cur)
			}
		}
		emitSummary(comp.Summary)
		if comp.To+1 > cur {
			cur = comp.To + 1
		}
	}
	for ; cur < currentStart; cur++ {
		if retained[cur] {
			emit(cur)
		}
	}
	// Rest of the current turn, unconditional.
	for line := currentStart + 1; line < len(lines); line++ {
		if msg, ok := messageOf(lines[line]); ok {
			messages = append(messages, msg)
			sources = append(sources, line)
		}
	}

	req.Messages = messages
	return req, sources
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

// currentTurnStart returns the transcript line where the current turn begins
// (the most recent instruction-layer entry), or -1 when the map has none.
func (m *ContextManager) currentTurnStart() int {
	instr := m.cm.Layer(LayerInstruction)
	if len(instr) == 0 {
		return -1
	}
	return instr[len(instr)-1].Line
}

// builtinCompact implements the harness's built-in compactor (ADR-0001). When
// the assembled request's token total crosses the model's budget, the oldest
// prior durable-intent turns are evicted from the outgoing request into a
// single synthetic summary message, keeping the most recent intent turns raw
// for continuity. The evicted range is persisted as a JSONL metadata marker
// (never a transcript rewrite), so a resumed or later request reconstructs the
// same summary via the context-map build; the tool-output lines of the evicted
// turns ride with them. Instruction and (surviving) tool-output layers are
// untouched. Compaction is synchronous, runs within Stream, and is best-effort:
// when no older intent remains it proceeds as assembled.
func (m *ContextManager) builtinCompact(ctx context.Context, req llm.Request, sources []int) (llm.Request, []int) {
	if m.counter == nil || m.resolver == nil {
		return req, sources
	}
	budget := m.resolver.Budget(ctx, req.Model)
	if budget <= 0 {
		return req, sources
	}
	total := m.countRequest(req, sources)
	if total <= budget {
		return req, sources
	}

	currentStart := m.currentTurnStart()
	if currentStart < 0 {
		return req, sources
	}

	// Candidate prior durable-intent messages, oldest first (the prior region
	// is in ascending transcript order, so message order is line order).
	var candidates []int
	for i := range req.Messages {
		if sources[i] < 0 || sources[i] >= currentStart {
			continue
		}
		switch req.Messages[i].Role {
		case llm.RoleUser, llm.RoleAssistant:
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return req, sources
	}

	removed := make(map[int]bool, len(candidates))
	var removedMsgs []llm.Message
	for _, mi := range candidates {
		if total <= budget {
			break
		}
		removed[mi] = true
		removedMsgs = append(removedMsgs, req.Messages[mi])
		total -= m.messageCount(req.Model, req.Messages[mi], sources[mi])
	}

	rangeLo, rangeHi := sources[candidates[0]], sources[candidates[0]]
	for _, mi := range candidates {
		if !removed[mi] {
			continue
		}
		rangeHi = sources[mi]
	}

	summary := buildSummary(removedMsgs)
	if err := m.sess.AppendCompaction(summary, rangeLo, rangeHi); err != nil {
		m.degrade("context compaction persist: %v", err)
	}

	// Rewrite the request: drop the evicted durable-intent messages and every
	// message whose source line falls inside the evicted range (their turn's
	// tool output rides with the summary), substituting the summary where the
	// oldest evicted message stood.
	insertAt := -1
	for _, mi := range candidates {
		if removed[mi] {
			insertAt = mi
			break
		}
	}
	out := make([]llm.Message, 0, len(req.Messages))
	outSrc := make([]int, 0, len(sources))
	for i := range req.Messages {
		if i == insertAt {
			out = append(out, llm.Message{Role: llm.RoleUser, Content: summary})
			outSrc = append(outSrc, -1)
		}
		src := sources[i]
		if removed[i] || (src >= rangeLo && src <= rangeHi) {
			continue
		}
		out = append(out, req.Messages[i])
		outSrc = append(outSrc, src)
	}
	req.Messages = out
	return req, outSrc
}

// builtinCondense implements the harness's built-in condenser (ADR-0001): a
// request-time projection over the tool-output layer. A prior-turn tool result
// whose token count exceeds the condense threshold is narrowed in the outgoing
// request only, addressed by that result's tool-call id; the full result stays
// in the transcript. With net-drop opted in, an oversized result is dropped
// from the request entirely while its assistant tool-call message remains, so
// the model sees the call was invoked. The in-flight current turn is never
// condensed: the tool loop must observe its own fresh result verbatim.
func (m *ContextManager) builtinCondense(req llm.Request, sources []int) (llm.Request, []int) {
	if m.counter == nil || m.condenseSize <= 0 {
		return req, sources
	}
	currentStart := m.currentTurnStart()
	out := make([]llm.Message, 0, len(req.Messages))
	outSrc := make([]int, 0, len(sources))
	for i := range req.Messages {
		msg := req.Messages[i]
		src := sources[i]
		if src >= 0 && src < currentStart && msg.ToolCallID != "" {
			if m.messageCount(req.Model, msg, src) > m.condenseSize {
				if m.netDrop {
					// Drop the result; its assistant tool-call stays.
					continue
				}
				msg.Content = condenseResult(msg.Content)
			}
		}
		out = append(out, msg)
		outSrc = append(outSrc, src)
	}
	req.Messages = out
	return req, outSrc
}

// condenseResult narrows one tool result for a single request: it keeps a
// bounded excerpt of the full form and notes that the complete result is
// retained in the session transcript.
func condenseResult(content string) string {
	const keep = 200
	return truncateRunes(content, keep) + "\n… [result condensed: full output retained in session transcript]"
}

// buildSummary deterministically compresses a set of evicted durable-intent
// messages into a single bounded summary string: each message contributes a
// role-labelled excerpt. The summary is deliberately mechanical (shaping what a
// summary preserves is a follow-up); it keeps the evicted turns' subject matter
// present without shipping their full text.
func buildSummary(removed []llm.Message) string {
	const (
		perMessage = 200
		totalRunes = 1500
	)
	var b strings.Builder
	b.WriteString("[compacted earlier turns]")
	for _, msg := range removed {
		label := ""
		switch msg.Role {
		case llm.RoleUser:
			label = "user"
		case llm.RoleAssistant:
			label = "assistant"
		default:
			continue
		}
		b.WriteString("; ")
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(truncateRunes(msg.Content, perMessage))
		if len(msg.ToolCalls) > 0 {
			b.WriteString(" [tool calls")
			for _, tc := range msg.ToolCalls {
				b.WriteString(" ")
				b.WriteString(tc.Name)
			}
			b.WriteString("]")
		}
	}
	return truncateRunes(b.String(), totalRunes)
}

// truncateRunes truncates s to at most n runes.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// countRequest totals the token cost of an assembled request from cached
// per-message and tool-definition counts, so unchanged history is not
// re-counted.
func (m *ContextManager) countRequest(req llm.Request, sources []int) int {
	if m.counter == nil {
		return 0
	}
	total := 0
	for i, msg := range req.Messages {
		line := -1
		if i < len(sources) {
			line = sources[i]
		}
		total += m.messageCount(req.Model, msg, line)
	}
	for _, td := range req.Tools {
		total += m.toolDefCount(req.Model, td)
	}
	return total
}

// recordUsage computes the token count of the outgoing request for the active
// model from the per-message count cache and records it as the latest usage.
// With a resolver attached, the count is logged against the authoritative
// context window and budget so operators see the headroom. Counting is skipped
// when no Counter is attached. The count cache is population is deferred here
// (and in the compact/condense steps) to the first Stream — never at map build.
func (m *ContextManager) recordUsage(ctx context.Context, req llm.Request, sources []int) {
	if m.counter == nil {
		return
	}
	total := 0
	for i, msg := range req.Messages {
		line := -1
		if i < len(sources) {
			line = sources[i]
		}
		total += m.messageCount(req.Model, msg, line)
	}
	for _, td := range req.Tools {
		total += m.toolDefCount(req.Model, td)
	}
	m.lastTokens = total

	if m.resolver == nil {
		slog.Debug("context usage", "model", req.Model, "tokens", total)
		return
	}
	window := m.resolver.ContextWindow(ctx, req.Model)
	budget := m.resolver.Budget(ctx, req.Model)
	slog.Debug("context usage", "model", req.Model, "tokens", total, "window", window, "budget", budget)
}

// messageCount returns the token count of one message, computed via the
// attached Counter and cached by stable line index (when the message is
// indexed). A cached entry is reused only when the model and a fingerprint of
// the message match, so a hook-rewritten message is never charged a stale
// count; unindexed (synthetic) messages are always counted fresh.
func (m *ContextManager) messageCount(model string, msg llm.Message, line int) int {
	if line >= 0 {
		if c, ok := m.counts[line]; ok && c.model == model {
			if c.fp == messageFingerprint(msg) {
				return c.n
			}
		}
	}
	fp := messageFingerprint(msg)
	n := countMessagePlain(m.counter, model, msg)
	if line >= 0 {
		if m.counts == nil {
			m.counts = make(map[int]cachedCount)
		}
		m.counts[line] = cachedCount{model: model, fp: fp, n: n}
	}
	return n
}

// toolDefCount returns the token cost of a tool definition in a request,
// computing each distinct definition once and caching by its canonical JSON.
func (m *ContextManager) toolDefCount(model string, td llm.ToolDef) int {
	data, err := json.Marshal(td)
	if err != nil {
		return 0
	}
	key := string(data)
	if n, ok := m.toolDefCounts[key]; ok {
		return n
	}
	n := m.counter.Count(model, key)
	if m.toolDefCounts == nil {
		m.toolDefCounts = make(map[string]int)
	}
	m.toolDefCounts[key] = n
	return n
}

// countMessagePlain counts one message via the Counter: content plus each tool
// call's arguments.
func countMessagePlain(c tokens.Counter, model string, msg llm.Message) int {
	n := c.Count(model, msg.Content)
	for _, tc := range msg.ToolCalls {
		n += c.Count(model, tc.Arguments)
	}
	return n
}

// messageFingerprint cheaply fingerprints a message over the fields that
// contribute to its token count, so a cached count can be verified without
// re-running the tokenizer.
func messageFingerprint(msg llm.Message) string {
	var b strings.Builder
	b.WriteString(msg.Role)
	b.WriteByte('|')
	b.WriteString(msg.Content)
	b.WriteByte('|')
	b.WriteString(msg.ToolCallID)
	b.WriteByte('|')
	for _, tc := range msg.ToolCalls {
		b.WriteString(tc.ID)
		b.WriteByte(':')
		b.WriteString(tc.Name)
		b.WriteByte(':')
		b.WriteString(tc.Arguments)
		b.WriteByte(';')
	}
	return b.String()
}