package modelinfo

import (
	"context"
	"errors"
	"testing"

	"github.com/okayest-dev/genie/internal/llm"
)

// fakeSource is a scripted llm.ModelInfoProvider recording how many times
// each model was probed.
type fakeSource struct {
	infos  map[string]*llm.ModelInfo
	err    error
	probes map[string]int
}

func newFakeSource(infos map[string]*llm.ModelInfo) *fakeSource {
	return &fakeSource{infos: infos, probes: map[string]int{}}
}

func (f *fakeSource) ModelInfo(_ context.Context, modelID string) (*llm.ModelInfo, error) {
	f.probes[modelID]++
	if f.err != nil {
		return nil, f.err
	}
	if info, ok := f.infos[modelID]; ok {
		return info, nil
	}
	return &llm.ModelInfo{}, nil
}

func TestContextWindowOverrideBeatsProvider(t *testing.T) {
	src := newFakeSource(map[string]*llm.ModelInfo{
		"m": {ContextLength: 100000},
	})
	r := New(src, map[string]int{"m": 999}, Options{})
	got := r.ContextWindow(context.Background(), "m")
	if got != 999 {
		t.Errorf("ContextWindow = %d, want config override 999", got)
	}
	if src.probes["m"] != 0 {
		t.Errorf("source probed %d times, want 0 (override short-circuits the probe)", src.probes["m"])
	}
}

func TestContextWindowFromProviderData(t *testing.T) {
	src := newFakeSource(map[string]*llm.ModelInfo{
		"gpt-4o": {ContextLength: 128000},
	})
	r := New(src, nil, Options{})
	got := r.ContextWindow(context.Background(), "gpt-4o")
	if got != 128000 {
		t.Errorf("ContextWindow = %d, want 128000 from provider data", got)
	}
}

func TestContextWindowProbedOnceAndCached(t *testing.T) {
	src := newFakeSource(map[string]*llm.ModelInfo{
		"m": {ContextLength: 128000},
	})
	r := New(src, nil, Options{})
	for i := 0; i < 5; i++ {
		if got := r.ContextWindow(context.Background(), "m"); got != 128000 {
			t.Fatalf("call %d: ContextWindow = %d, want 128000", i, got)
		}
	}
	if src.probes["m"] != 1 {
		t.Errorf("source probed %d times, want exactly 1 (process-lifetime cache)", src.probes["m"])
	}
}

func TestContextWindowUnknownStaysZero(t *testing.T) {
	// The provider exposes no window for the model: the resolver must not
	// invent one.
	src := newFakeSource(nil)
	r := New(src, nil, Options{})
	if got := r.ContextWindow(context.Background(), "unknown-model"); got != 0 {
		t.Errorf("ContextWindow = %d, want 0 (no invented limits)", got)
	}
}

func TestContextWindowProviderErrorStaysZero(t *testing.T) {
	src := newFakeSource(nil)
	src.err = errors.New("network down")
	r := New(src, nil, Options{})
	if got := r.ContextWindow(context.Background(), "m"); got != 0 {
		t.Errorf("ContextWindow = %d, want 0 when the provider probe fails", got)
	}
}

func TestContextWindowRetriesAfterFailure(t *testing.T) {
	src := newFakeSource(map[string]*llm.ModelInfo{
		"m": {ContextLength: 128000},
	})
	src.err = errors.New("network down")
	r := New(src, nil, Options{})
	if got := r.ContextWindow(context.Background(), "m"); got != 0 {
		t.Fatalf("ContextWindow = %d during outage, want 0", got)
	}
	src.err = nil // provider recovers
	if got := r.ContextWindow(context.Background(), "m"); got != 128000 {
		t.Errorf("ContextWindow = %d after recovery, want 128000 (failures must not be cached)", got)
	}
	if src.probes["m"] != 2 {
		t.Errorf("source probed %d times, want 2 (retry after the failed probe)", src.probes["m"])
	}
}

func TestSeedSkipsProbe(t *testing.T) {
	src := newFakeSource(nil)
	r := New(src, nil, Options{})
	r.Seed("listed-model", 200000)
	if got := r.ContextWindow(context.Background(), "listed-model"); got != 200000 {
		t.Errorf("ContextWindow = %d, want seeded 200000", got)
	}
	if src.probes["listed-model"] != 0 {
		t.Errorf("source probed %d times, want 0 (seeded value used)", src.probes["listed-model"])
	}
}

func TestSeedDoesNotBeatOverride(t *testing.T) {
	src := newFakeSource(nil)
	r := New(src, map[string]int{"m": 111}, Options{})
	r.Seed("m", 222)
	if got := r.ContextWindow(context.Background(), "m"); got != 111 {
		t.Errorf("ContextWindow = %d, want override 111 over seeded 222", got)
	}
}

func TestBudgetAbsoluteTokensWin(t *testing.T) {
	src := newFakeSource(map[string]*llm.ModelInfo{
		"m": {ContextLength: 100000},
	})
	r := New(src, nil, Options{BudgetTokens: 50000})
	if got := r.Budget(context.Background(), "m"); got != 50000 {
		t.Errorf("Budget = %d, want absolute 50000", got)
	}
}

func TestBudgetDefaultPercentOfWindow(t *testing.T) {
	src := newFakeSource(map[string]*llm.ModelInfo{
		"m": {ContextLength: 100000},
	})
	r := New(src, nil, Options{}) // no explicit percent: default 75
	if got := r.Budget(context.Background(), "m"); got != 75000 {
		t.Errorf("Budget = %d, want 75%% of 100000 = 75000", got)
	}
}

func TestBudgetConfiguredPercentOfWindow(t *testing.T) {
	src := newFakeSource(map[string]*llm.ModelInfo{
		"m": {ContextLength: 100000},
	})
	r := New(src, nil, Options{BudgetPercent: 90})
	if got := r.Budget(context.Background(), "m"); got != 90000 {
		t.Errorf("Budget = %d, want 90%% of 100000 = 90000", got)
	}
}

func TestBudgetUsesOverrideWindow(t *testing.T) {
	src := newFakeSource(nil)
	r := New(src, map[string]int{"m": 100000}, Options{BudgetPercent: 50})
	if got := r.Budget(context.Background(), "m"); got != 50000 {
		t.Errorf("Budget = %d, want 50%% of override window 100000 = 50000", got)
	}
}

func TestBudgetUnknownWindowIsZero(t *testing.T) {
	// No window from any source and no absolute budget: there is nothing to
	// budget against, so the budget is zero (no invented limits).
	src := newFakeSource(nil)
	r := New(src, nil, Options{})
	if got := r.Budget(context.Background(), "m"); got != 0 {
		t.Errorf("Budget = %d, want 0 when the window is unknown", got)
	}
}

func TestBudgetAbsoluteWinsOverUnknownWindow(t *testing.T) {
	// An absolute budget does not need a window at all.
	src := newFakeSource(nil)
	r := New(src, nil, Options{BudgetTokens: 40000})
	if got := r.Budget(context.Background(), "m"); got != 40000 {
		t.Errorf("Budget = %d, want absolute 40000 even with an unknown window", got)
	}
}
