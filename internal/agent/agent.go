// Package agent runs the harness's agent loop. In the v1 tracer bullet the
// loop is a single no-tool turn: send the prompt, stream the reply to out.
// Tool calling and the multi-cycle loop land in later tickets.
package agent

import (
	"context"
	"io"

	"github.com/okayest-dev/og/internal/llm"
)

// RunTurn runs one no-tool agent turn against c: build the canonical
// conversation, stream the reply, and write text deltas to out as they
// arrive. It returns nil on a completed turn and the provider failure
// otherwise.
func RunTurn(ctx context.Context, c llm.Client, model, prompt string, out io.Writer) error {
	stream, err := c.Stream(ctx, llm.Request{
		Model: model,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return err
	}

	for ev := range stream {
		switch ev.Kind {
		case llm.EventText:
			if _, err := io.WriteString(out, ev.Text); err != nil {
				return err
			}
		case llm.EventError:
			return ev.Err
		}
	}
	if _, err := io.WriteString(out, "\n"); err != nil {
		return err
	}
	return nil
}
