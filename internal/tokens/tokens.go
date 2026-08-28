// Package tokens counts tokens in text for a given model. It wraps omnitoken
// behind an internal Counter interface so the token-counting library can be
// swapped without touching callers. Models without a registered adapter
// fall back to a len/4 heuristic approximation; the harness never
// fails to count.
package tokens

import (
	"sync"

	"github.com/ron2111/omnitoken"
)

// Counter counts tokens in text for a given model ID. Implementations are
// expected to be safe for concurrent use.
type Counter interface {
	// Count returns the number of tokens text consumes under model's
	// tokenizer. Models with no registered adapter fall back to a
	// len/4 approximation.
	Count(model, text string) int
}

// New returns a Counter backed by omnitoken with a len/4 heuristic fallback
// for models omnitoken has no adapter for.
func New() Counter {
	return &counter{engines: make(map[string]func(string) int)}
}

// counter is the omnitoken-backed Counter. Engines are resolved lazily per
// model and cached for the process lifetime.
type counter struct {
	mu      sync.Mutex
	engines map[string]func(string) int
}

// Count returns the token count for text under model, falling back to the
// len/4 heuristic when no adapter is registered.
func (c *counter) Count(model, text string) int {
	return c.engineFor(model)(text)
}

// engineFor returns a token-counting function for model. It consults the
// cache first; on a miss it tries omnitoken's model-aware adapter and falls
// back to the len/4 heuristic, caching whichever it resolved for the process
// lifetime.
func (c *counter) engineFor(model string) func(string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fn, ok := c.engines[model]; ok {
		return fn
	}
	fn := c.resolve(model)
	c.engines[model] = fn
	return fn
}

// resolve builds the token-counting function for model. omnitoken engines own
// an embedded vocabulary and compute exact (BPE) counts; any unsupported
// model falls back to the len/4 heuristic.
func (c *counter) resolve(model string) func(string) int {
	engine, err := omnitoken.ForModel(model)
	if err != nil {
		return heuristicCount
	}
	return engine.CountTokens
}

// heuristicCount approximates tokens as one per four bytes of UTF-8 text — a
// well-known rule of thumb for English-dominant LLM traffic, used only where
// no exact tokenizer adapter exists.
func heuristicCount(text string) int {
	return len(text) / 4
}
