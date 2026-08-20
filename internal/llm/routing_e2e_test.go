package llm_test

import (
	"context"
	"iter"
	"testing"

	"github.com/okayest-dev/og/internal/llm"
)

// testPluginClient is a mock that tracks which models it handled.
type testPluginClient struct {
	handled []string
}

func (c *testPluginClient) Stream(_ context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	c.handled = append(c.handled, req.Model)
	return func(yield func(llm.Event) bool) {
		yield(llm.Event{Kind: llm.EventText, Text: "plugin response"})
		yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishStop})
	}, nil
}

func (c *testPluginClient) ListModels(_ context.Context) ([]llm.Model, error) {
	return []llm.Model{
		{ID: "gpt-4o"},
		{ID: "claude-sonnet-4-5"},
	}, nil
}

// testBuiltinClient is a mock for the built-in provider.
type testBuiltinClient struct {
	handled []string
}

func (c *testBuiltinClient) Stream(_ context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	c.handled = append(c.handled, req.Model)
	return func(yield func(llm.Event) bool) {
		yield(llm.Event{Kind: llm.EventText, Text: "builtin response"})
		yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishStop})
	}, nil
}

func (c *testBuiltinClient) ListModels(_ context.Context) ([]llm.Model, error) {
	return []llm.Model{{ID: "big-pickle"}}, nil
}

func TestRoutingPluginModelRoutesToPlugin(t *testing.T) {
	plugin := &testPluginClient{}
	builtin := &testBuiltinClient{}
	r := llm.NewRoutingClient(builtin, map[string]llm.Client{
		"gpt-4o":           plugin,
		"claude-sonnet-4-5": plugin,
	})

	stream, err := r.Stream(context.Background(), llm.Request{Model: "claude-sonnet-4-5"})
	if err != nil {
		t.Fatal(err)
	}

	var text string
	for ev := range stream {
		if ev.Kind == llm.EventText {
			text = ev.Text
		}
	}
	if text != "plugin response" {
		t.Errorf("expected plugin response, got %q", text)
	}
	if len(plugin.handled) != 1 || plugin.handled[0] != "claude-sonnet-4-5" {
		t.Errorf("plugin should have handled claude-sonnet-4-5, got %v", plugin.handled)
	}
	if len(builtin.handled) != 0 {
		t.Errorf("builtin should not have been called, got %v", builtin.handled)
	}
}

func TestRoutingBuiltinModelRoutesToDefault(t *testing.T) {
	plugin := &testPluginClient{}
	builtin := &testBuiltinClient{}
	r := llm.NewRoutingClient(builtin, map[string]llm.Client{
		"gpt-4o": plugin,
	})

	stream, err := r.Stream(context.Background(), llm.Request{Model: "big-pickle"})
	if err != nil {
		t.Fatal(err)
	}

	var text string
	for ev := range stream {
		if ev.Kind == llm.EventText {
			text = ev.Text
		}
	}
	if text != "builtin response" {
		t.Errorf("expected builtin response, got %q", text)
	}
	if len(builtin.handled) != 1 {
		t.Errorf("builtin should have handled big-pickle, got %v", builtin.handled)
	}
}

func TestRoutingListModelsIncludesBoth(t *testing.T) {
	plugin := &testPluginClient{}
	builtin := &testBuiltinClient{}
	r := llm.NewRoutingClient(builtin, map[string]llm.Client{
		"gpt-4o":           plugin,
		"claude-sonnet-4-5": plugin,
	})

	models, err := r.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	ids := make(map[string]bool)
	for _, m := range models {
		ids[m.ID] = true
	}

	for _, want := range []string{"big-pickle", "gpt-4o", "claude-sonnet-4-5"} {
		if !ids[want] {
			t.Errorf("missing model %q in merged list", want)
		}
	}
}
