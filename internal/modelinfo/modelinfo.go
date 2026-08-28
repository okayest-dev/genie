// Package modelinfo resolves per-model context windows from authoritative
// sources — an explicit config override first, then provider data via the
// optional llm.ModelInfoProvider probe — and derives the context budget from
// the resolved window. Windows are probed lazily, once per model, and cached
// for the process lifetime. The resolver never invents a window: a model
// with no override and no provider-reported window resolves to zero.
package modelinfo

import (
	"context"
	"log/slog"
	"sync"

	"github.com/okayest-dev/og/internal/llm"
)

// DefaultBudgetPercent is the proportion of the context window budgeted for
// a conversation when no explicit budget is configured, keeping headroom so
// a request cannot silently blow the window.
const DefaultBudgetPercent = 75

// Source supplies authoritative per-model metadata. llm.ModelInfoProvider
// implementations satisfy it.
type Source interface {
	ModelInfo(ctx context.Context, modelID string) (*llm.ModelInfo, error)
}

// Options carries the [context] budget config: an absolute token budget
// takes precedence over a percentage of the resolved window.
type Options struct {
	// BudgetTokens, when > 0, is the absolute context budget in tokens.
	BudgetTokens int
	// BudgetPercent is the budget as a percentage of the context window.
	// Zero or negative falls back to DefaultBudgetPercent.
	BudgetPercent float64
}

// Resolver resolves context windows and budgets for models.
type Resolver struct {
	source    Source
	overrides map[string]int
	opts      Options

	mu      sync.Mutex
	windows map[string]int // model ID → resolved window; 0 = known unknown
}

// New builds a Resolver. source may be nil (no provider data available);
// overrides maps model IDs to explicit context windows from config and take
// precedence over the source.
func New(source Source, overrides map[string]int, opts Options) *Resolver {
	return &Resolver{
		source:    source,
		overrides: overrides,
		opts:      opts,
		windows:   make(map[string]int),
	}
}

// Seed records an already-known authoritative window for modelID (e.g. from
// a plugin's model catalog), avoiding a later probe. Values <= 0 are ignored.
// A config override still takes precedence over a seeded value.
func (r *Resolver) Seed(modelID string, contextLength int) {
	if contextLength <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.windows[modelID]; !ok {
		r.windows[modelID] = contextLength
	}
}

// ContextWindow returns the model's context window in tokens: the config
// override when present, otherwise the authoritative provider value probed
// once per model and cached. Returns 0 when no authoritative data exists.
//
// The probe runs outside the cache lock so a slow provider does not
// serialise lookups for other models; under concurrent first-access the
// probe may run more than once, which is harmless (it is a read-only
// lookup) and the last write wins.
func (r *Resolver) ContextWindow(ctx context.Context, modelID string) int {
	if w, ok := r.overrides[modelID]; ok && w > 0 {
		return w
	}

	r.mu.Lock()
	w, ok := r.windows[modelID]
	r.mu.Unlock()
	if ok {
		return w
	}

	pw, ok := r.probe(ctx, modelID)

	r.mu.Lock()
	defer r.mu.Unlock()
	// A failed probe is not cached: a transient provider outage must not
	// become a permanent zero window for the process. A definitive
	// "provider exposes no window" (success, zero) is cached as 0 — that is
	// the authoritative answer, never a guessed one.
	if ok {
		if _, raced := r.windows[modelID]; !raced {
			r.windows[modelID] = pw
		}
	}
	return pw
}

// probe consults the source once. It reports whether the source answered:
// on a failed probe the window is zero and ok is false, so the failure is
// not cached as if it were authoritative data. A provider that exposes no
// window resolves to a cached zero — never an invented window.
func (r *Resolver) probe(ctx context.Context, modelID string) (int, bool) {
	if r.source == nil {
		return 0, false
	}
	info, err := r.source.ModelInfo(ctx, modelID)
	if err != nil {
		slog.Warn("model info probe failed; no context window for model", "model", modelID, "error", err)
		return 0, false
	}
	if info == nil || info.ContextLength <= 0 {
		slog.Debug("provider exposes no context window for model", "model", modelID)
		return 0, true
	}
	return info.ContextLength, true
}

// Budget returns the token budget for modelID: the configured absolute
// budget when set, otherwise the configured percentage (default 75) of the
// resolved context window. Returns 0 when the window is unknown and no
// absolute budget is configured.
func (r *Resolver) Budget(ctx context.Context, modelID string) int {
	if r.opts.BudgetTokens > 0 {
		return r.opts.BudgetTokens
	}
	window := r.ContextWindow(ctx, modelID)
	if window <= 0 {
		return 0
	}
	percent := r.opts.BudgetPercent
	if percent <= 0 {
		percent = DefaultBudgetPercent
	}
	return int(float64(window) * percent / 100)
}
