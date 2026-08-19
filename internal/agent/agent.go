// Package agent runs the harness's agent loop: send the prompt, stream the
// reply, and when the model requests tool calls, execute them serially and
// feed results back until the model stops calling tools.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/okayest-dev/og/internal/ledger"
	"github.com/okayest-dev/og/internal/llm"
	"github.com/okayest-dev/og/internal/session"
	"github.com/okayest-dev/og/internal/tools"
)

// RunTurn runs the agent loop against c: build the canonical conversation,
// stream the reply, and when the model returns tool calls, execute them
// serially and feed results back. instruction is the assembled system
// prompt. out receives text deltas; errOut receives tool framing headers.
// If sess is non-nil, the conversation is persisted. If registry is nil,
// no tools are sent and tool calls are not processed. If ldg is non-nil,
// file mutations are captured in the change ledger. cwd is the working
// directory for resolving relative file paths.
func RunTurn(ctx context.Context, c llm.Client, model, instruction, prompt string, out, errOut io.Writer, sess *session.Session, registry *tools.Registry, ldg *ledger.Ledger, cwd string) error {
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

	// Build the request with tools if available.
	req := llm.Request{
		Model:    model,
		Messages: messages,
	}
	if registry != nil {
		req.Tools = registry.ToolDefs()
	}

	// Track whether we've retried without tools to avoid infinite loops.
	retriedNoTools := false
	var reply strings.Builder

	for {
		stream, err := c.Stream(ctx, req)
		if err != nil {
			// Check for invalid_request on tools array — retry without tools.
			if !retriedNoTools && registry != nil {
				var pe *llm.ProviderError
				if errors.As(err, &pe) && pe.Kind == llm.KindInvalidRequest {
					slog.Info("provider rejected tools, retrying without", "error", pe.Message)
					req.Tools = nil
					retriedNoTools = true
					if reply.Len() > 0 && sess != nil {
						if err := sess.Append(llm.Message{
							Role:    llm.RoleAssistant,
							Content: reply.String(),
						}); err != nil {
							return err
						}
					}
					reply.Reset()
					continue
				}
			}
			return err
		}

		var usage llm.Usage
		var finishReason llm.FinishReason
		var toolCalls []llm.ToolCall
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
			case llm.EventToolCall:
				toolCalls = ev.ToolCalls
			case llm.EventError:
				return ev.Err
			}
		}

		// If we got a text reply, print the trailing newline and persist.
		if reply.Len() > 0 {
			if _, err := io.WriteString(out, "\n"); err != nil {
				return err
			}
		}

		// Persist the assistant's reply to the transcript.
		if reply.Len() > 0 && sess != nil {
			assistantMsg := llm.Message{
				Role:    llm.RoleAssistant,
				Content: reply.String(),
			}
			if len(toolCalls) > 0 {
				assistantMsg.ToolCalls = toolCalls
			}
			if err := sess.Append(assistantMsg); err != nil {
				return err
			}
		} else if len(toolCalls) > 0 && reply.Len() == 0 && sess != nil {
			// Tool-call-only message (no text content).
			if err := sess.Append(llm.Message{
				Role:      llm.RoleAssistant,
				ToolCalls: toolCalls,
			}); err != nil {
				return err
			}
		}

		// No tool calls — turn is complete.
		if len(toolCalls) == 0 {
			slog.Info("turn completed",
				"finish_reason", string(finishReason),
				"prompt_tokens", usage.PromptTokens,
				"completion_tokens", usage.CompletionTokens,
				"total_tokens", usage.TotalTokens,
			)
			return nil
		}

		// Execute tool calls serially and feed results back.
		slog.Info("tool calls received", "count", len(toolCalls))
		for _, tc := range toolCalls {
			// Frame the tool run on stderr.
			if errOut != nil {
				fmt.Fprintf(errOut, "── %s %s ──\n", tc.Name, truncateArgs(tc.Arguments, 120))
			}

			var result string
			var execErr error

			if registry == nil {
				execErr = fmt.Errorf("no tools registered")
			} else {
				tool, ok := registry.Get(tc.Name)
				if !ok {
					// Check if it's disabled or just unknown.
					if registry.IsDisabled(tc.Name) {
						execErr = tools.DisabledError(tc.Name)
					} else {
						execErr = fmt.Errorf("unknown tool '%s'", tc.Name)
					}
				} else {
					// Validate arguments before execution.
					if err := tools.ValidateArgs(json.RawMessage(tc.Arguments), tool.Parameters()); err != nil {
						execErr = fmt.Errorf("invalid arguments for %s: %v", tc.Name, err)
					} else {
						// Snapshot pre-mutation content for write/edit tools.
						if ldg != nil && (tc.Name == "write" || tc.Name == "edit") {
							var args struct {
								Path string `json:"path"`
							}
							if json.Unmarshal([]byte(tc.Arguments), &args) == nil && args.Path != "" {
								absPath := resolvePath(args.Path, cwd)
								if data, err := os.ReadFile(absPath); err == nil {
									ldg.Snapshot(absPath, string(data))
								} else {
									ldg.Snapshot(absPath, "") // New file
								}
								ldg.RecordToolCall(tc.ID)
							}
						}
						result, execErr = tool.Execute(json.RawMessage(tc.Arguments))
						// Record successful mutations in ledger.
						if ldg != nil && execErr == nil && (tc.Name == "write" || tc.Name == "edit") {
							var args struct {
								Path    string `json:"path"`
								Content string `json:"content"`
							}
							if json.Unmarshal([]byte(tc.Arguments), &args) == nil && args.Path != "" {
								absPath := resolvePath(args.Path, cwd)
								oldContent := ldg.GetSnapshot(absPath)
								newContent := args.Content
								if tc.Name == "edit" {
									// For edits, read the new content from the file.
									if data, err := os.ReadFile(absPath); err == nil {
										newContent = string(data)
									}
								}
								op := ledger.OpOverwrite
								if oldContent == "" {
									op = ledger.OpCreate
								} else if newContent == "" {
									op = ledger.OpDelete
								} else {
									op = ledger.OpEdit
								}
								ldg.RecordMutation(absPath, oldContent, newContent, op)
							}
						}
					}
				}
			}

			// Format the result or error as a tool message.
			var toolContent string
			if execErr != nil {
				toolContent = fmt.Sprintf("Error: %v", execErr)
				slog.Info("tool error", "tool", tc.Name, "error", execErr)
			} else {
				toolContent = result
				slog.Info("tool completed", "tool", tc.Name, "result_length", len(result))
			}

			// Add the tool result to the conversation.
			toolMsg := llm.Message{
				Role:       llm.RoleTool,
				Content:    toolContent,
				ToolCallID: tc.ID,
			}
			req.Messages = append(req.Messages, toolMsg)

			// Persist the tool result.
			if sess != nil {
				if err := sess.Append(toolMsg); err != nil {
					return err
				}
			}
		}

		// Loop back to stream the next response with tool results.
	}
}

// truncateArgs returns a shortened version of the arguments string for framing.
func truncateArgs(args string, max int) string {
	if len(args) <= max {
		return args
	}
	return args[:max] + "..."
}

// resolvePath resolves a file path relative to cwd if it's not already absolute.
func resolvePath(path, cwd string) string {
	if strings.HasPrefix(path, "/") {
		return path
	}
	return cwd + "/" + path
}
