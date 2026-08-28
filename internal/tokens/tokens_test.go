package tokens

import (
	"testing"

	"github.com/ron2111/omnitoken"
)

// TestCountUsesTokenizer asserts that a model with a registered omnitoken
// adapter is tokenized exactly rather than approximated: the Counter's answer
// matches omnitoken's own engine for the same input.
func TestCountUsesTokenizer(t *testing.T) {
	c := New()
	model := "gpt-4o"
	text := "the quick brown fox jumps over the lazy dog"
	n := c.Count(model, text)
	eng, err := omnitoken.ForModel(model)
	if err != nil {
		t.Fatalf("omnitoken.ForModel(%q): %v", model, err)
	}
	want := eng.CountTokens(text)
	if n != want {
		t.Errorf("Count(%q) = %d, want omnitoken adapter count %d", model, n, want)
	}
}

// TestCountFallbackHeuristic asserts that a model without an adapter
// falls back to the len/4 heuristic approximation and never errors.
func TestCountFallbackHeuristic(t *testing.T) {
	c := New()
	for _, model := range []string{"claude-3-5-sonnet", "gemini-2.0-flash", "big-pickle", "unknown-model-x"} {
		text := "hello world, this is some text that will be counted"
		n := c.Count(model, text)
		want := len(text) / 4
		if n != want {
			t.Errorf("Count(%q) = %d, want heuristic %d", model, n, want)
		}
	}
}

// TestCountDeterministic asserts that counting the same text under the same
// model is stable across calls (the per-model engine is reused from cache).
func TestCountDeterministic(t *testing.T) {
	c := New()
	text := "repeatable input string for the token counter"
	first := c.Count("gpt-4o", text)
	for i := 0; i < 3; i++ {
		if got := c.Count("gpt-4o", text); got != first {
			t.Fatalf("Count not deterministic: call %d = %d, first = %d", i, got, first)
		}
	}
}
