package main

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"

	"github.com/okayest-dev/og/internal/llm"
	"github.com/okayest-dev/og/internal/plugin"
)

// pluginWireClient adapts a plugin.Plugin's StreamWire method into an
// llm.Client. Streaming is degraded — the plugin accumulates SSE chunks
// internally and returns the final result — but routing is correct.
type pluginWireClient struct {
	plugin *plugin.Plugin
}

func newPluginWireClient(p *plugin.Plugin) *pluginWireClient {
	return &pluginWireClient{plugin: p}
}

func (c *pluginWireClient) Stream(_ context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	result, err := c.plugin.StreamWire(data)
	if err != nil {
		return nil, err
	}

	var wireResult struct {
		Text      string `json:"text"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(result, &wireResult); err != nil {
		return nil, fmt.Errorf("parse wire result: %w", err)
	}

	return func(yield func(llm.Event) bool) {
		if wireResult.Text != "" {
			yield(llm.Event{Kind: llm.EventText, Text: wireResult.Text})
		}
		if len(wireResult.ToolCalls) > 0 {
			tcs := make([]llm.ToolCall, 0, len(wireResult.ToolCalls))
			for _, tc := range wireResult.ToolCalls {
				tcs = append(tcs, llm.ToolCall{
					ID:       tc.ID,
					Name:     tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
			yield(llm.Event{Kind: llm.EventToolCall, ToolCalls: tcs})
		}
		if wireResult.Usage != nil {
			yield(llm.Event{
				Kind: llm.EventUsage,
				Usage: llm.Usage{
					PromptTokens:     wireResult.Usage.PromptTokens,
					CompletionTokens: wireResult.Usage.CompletionTokens,
					TotalTokens:      wireResult.Usage.TotalTokens,
				},
			})
		}
		yield(llm.Event{Kind: llm.EventFinish, End: llm.FinishStop})
	}, nil
}

func (c *pluginWireClient) ListModels(_ context.Context) ([]llm.Model, error) {
	models := make([]llm.Model, 0, len(c.plugin.Models))
	for _, m := range c.plugin.Models {
		models = append(models, llm.Model{ID: m.ID})
	}
	return models, nil
}
