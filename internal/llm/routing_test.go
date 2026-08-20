package llm

import (
	"context"
	"iter"
	"testing"
)

type mockClient struct {
	models    []Model
	streamErr error
}

func (m *mockClient) Stream(_ context.Context, req Request) (iter.Seq[Event], error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	return func(yield func(Event) bool) {
		yield(Event{Kind: EventText, Text: req.Model})
		yield(Event{Kind: EventFinish, End: FinishStop})
	}, nil
}

func (m *mockClient) ListModels(_ context.Context) ([]Model, error) {
	return m.models, nil
}

func TestRoutingClientDelegatesToRoutedModel(t *testing.T) {
	routed := &mockClient{models: []Model{{ID: "gpt-4o"}}}
	fallback := &mockClient{models: []Model{{ID: "big-pickle"}}}

	r := NewRoutingClient(fallback, map[string]Client{"gpt-4o": routed})

	stream, err := r.Stream(context.Background(), Request{Model: "gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}

	var text string
	for ev := range stream {
		if ev.Kind == EventText {
			text = ev.Text
		}
	}
	if text != "gpt-4o" {
		t.Errorf("expected routed model text, got %q", text)
	}
}

func TestRoutingClientFallsBackToDefault(t *testing.T) {
	routed := &mockClient{models: []Model{{ID: "gpt-4o"}}}
	fallback := &mockClient{models: []Model{{ID: "big-pickle"}}}

	r := NewRoutingClient(fallback, map[string]Client{"gpt-4o": routed})

	stream, err := r.Stream(context.Background(), Request{Model: "big-pickle"})
	if err != nil {
		t.Fatal(err)
	}

	var text string
	for ev := range stream {
		if ev.Kind == EventText {
			text = ev.Text
		}
	}
	if text != "big-pickle" {
		t.Errorf("expected fallback model text, got %q", text)
	}
}

func TestRoutingClientListModelsMerges(t *testing.T) {
	routed := &mockClient{models: []Model{{ID: "gpt-4o"}}}
	fallback := &mockClient{models: []Model{{ID: "big-pickle"}}}

	r := NewRoutingClient(fallback, map[string]Client{
		"gpt-4o":           routed,
		"claude-sonnet-4-5": routed,
	})

	models, err := r.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(models))
	}

	ids := make(map[string]bool)
	for _, m := range models {
		ids[m.ID] = true
	}
	for _, want := range []string{"big-pickle", "gpt-4o", "claude-sonnet-4-5"} {
		if !ids[want] {
			t.Errorf("missing model %q", want)
		}
	}
}
