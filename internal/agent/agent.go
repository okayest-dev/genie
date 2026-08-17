// Package agent runs the harness's agent loop. In the v1 tracer bullet the
// loop is a single no-tool turn: send the prompt, stream the reply to out.
// Tool calling and the multi-cycle loop land in later tickets.
package agent

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/okayest-dev/og/internal/llm"
	"github.com/okayest-dev/og/internal/session"
)

// RunTurn runs one no-tool agent turn against c: build the canonical
// conversation, stream the reply, and write text deltas to out as they
// arrive. instruction is the assembled system prompt from the three
// sources (default + config file + AGENTS.md). If sess is non-nil, the
// conversation is persisted to the session transcript. It returns nil on
// a completed turn and the provider failure otherwise.
func RunTurn(ctx context.Context, c llm.Client, model, instruction, prompt string, out io.Writer, sess *session.Session) error {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: instruction},
		{Role: llm.RoleUser, Content: prompt},
	}

	// Persist the system and user messages to the transcript.
	if sess != nil {
		for _, msg := range messages {
			if err := sess.Append(msg); err != nil {
				return err
			}
		}
	}

	slog.Info("turn started", "model", model, "prompt_length", len(prompt))

	stream, err := c.Stream(ctx, llm.Request{
		Model:    model,
		Messages: messages,
	})
	if err != nil {
		return err
	}

	var usage llm.Usage
	var finishReason llm.FinishReason
	var reply strings.Builder
	for ev := range stream {
		switch ev.Kind {
		case llm.EventText:
			if _, err := io.WriteString(out, ev.Text); err != nil {
				return err
			}
			reply.WriteString(ev.Text)
		case llm.EventFinish:
			finishReason = ev.End
		case llm.EventUsage:
			usage = ev.Usage
		case llm.EventError:
			return ev.Err
		}
	}
	if _, err := io.WriteString(out, "\n"); err != nil {
		return err
	}

	// Persist the assistant's reply to the transcript.
	if sess != nil {
		if err := sess.Append(llm.Message{
			Role:    llm.RoleAssistant,
			Content: reply.String(),
		}); err != nil {
			return err
		}
	}

	slog.Info("turn completed",
		"finish_reason", string(finishReason),
		"prompt_tokens", usage.PromptTokens,
		"completion_tokens", usage.CompletionTokens,
		"total_tokens", usage.TotalTokens,
	)
	return nil
}
