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

	"github.com/okayest-dev/genie/internal/ledger"
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/session"
	"github.com/okayest-dev/genie/internal/tools"
)

// Option configures a RunTurn call.
type Option func(*turnOptions)

type turnOptions struct {
	agentName string
	hooks     Hooks
}

// WithAgentName attaches an agent name to the user message in the session log.
func WithAgentName(name string) Option {
	return func(o *turnOptions) { o.agentName = name }
}

// WithHooks attaches the lifecycle-hooks seam (a LifecycleSeam in the plugin
// package). Nil disables it.
func WithHooks(h Hooks) Option {
	return func(o *turnOptions) { o.hooks = h }
}

// Hooks is the lifecycle-hooks seam interface the agent loop invokes around a
// turn. It mirrors the og-cbu.3 event model: all five events are synchronous
// and fire inside RunTurn. The interface leaves the agent package plugin-free;
// the plugin package implements it (LifecycleSeam), wired once in main.go.
//
// Contract: non-fatal hook failures never surface here — the implementation
// degrades (skips the hook, keeps prior contributions). A method returns a
// non-nil error only for a plugin-declared fatal escalation, which aborts the
// turn (the session survives; the harness returns to the REPL).
type Hooks interface {
	// RequestBuilt rewrites the fully-assembled request once per turn, before
	// the first Stream call. The rewritten request is what gets sent.
	RequestBuilt(ctx context.Context, req llm.Request) (llm.Request, error)

	// ToolBefore runs before a tool executes: it may rewrite the arguments JSON
	// and may suppress the call. Suppress short-circuits the chain — the call is
	// dead and the harness moves to the next tool call.
	ToolBefore(ctx context.Context, name, id, args string) (argsOut string, suppress bool, err error)

	// ToolAfter runs after a tool call completes (including failures; errText is
	// a field, per og-cbu.3). It may rewrite the result text.
	ToolAfter(ctx context.Context, name, id, args, result, errText string) (resultOut string, err error)

	// ResponseReady observes/rewrites each streaming text delta; the call with
	// final=true is the last of the stream and carries the finish reason and
	// usage the turn ended with.
	ResponseReady(ctx context.Context, chunk string, final bool, finish llm.FinishReason, usage llm.Usage) (chunkOut string, err error)

	// TurnError observes a hard Go-error turn exit (stream-open, mid-stream
	// EventError, session/append failure). obtain-only; fires exactly once at the
	// failing exit.
	TurnError(ctx context.Context, errText, phase, partial string) error
}

// FatalHookError is the error a Hooks method returns when a plugin declared the
// event fatal (turn-scoped abort). It names the plugin and event responsible.
type FatalHookError struct {
	Plugin string
	Event  string
}

func (e *FatalHookError) Error() string {
	return fmt.Sprintf("lifecycle hook %s (plugin %q) declared fatal", e.Event, e.Plugin)
}

// RunTurn runs the agent loop against c: build the canonical conversation for
// the current turn, stream the reply, and when the model returns tool calls,
// execute them serially and feed results back. instruction is the assembled
// agent instruction. out receives text deltas; errOut
// receives tool framing headers. If sess is non-nil, the conversation is
// persisted. If registry is nil, no tools are sent and tool calls are not
// processed. If ldg is non-nil, file mutations are captured in the change
// ledger. cwd is the working directory for resolving relative file paths.
// Prior-turn history is not threaded here: the client wrapping c owns history
// injection from the session. opts configures optional behaviour (e.g.
// WithAgentName for session logging).
func RunTurn(ctx context.Context, c llm.Client, model, instruction, prompt string, out, errOut io.Writer, sess *session.Session, registry *tools.Registry, ldg *ledger.Ledger, cwd string, opts ...Option) (err error) {
	var to turnOptions
	for _, o := range opts {
		o(&to)
	}
	var reply strings.Builder
	// turn_error fires exactly once on any hard Go-error exit, with the reply
	// text accumulated before the failure. All other lifecycle hooks fire inline
	// at their natural loop position.
	defer func() {
		if err != nil && to.hooks != nil {
			if herr := to.hooks.TurnError(ctx, err.Error(), "turn", reply.String()); herr != nil {
				err = herr
			}
		}
	}()
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: instruction},
		{Role: llm.RoleUser, Content: prompt},
	}

	// Persist the system and user messages to the transcript.
	if sess != nil {
		for _, msg := range messages {
			// Attach agent name metadata to the user message.
			if to.agentName != "" && msg.Role == llm.RoleUser {
				if err := sess.AppendWithMeta(msg, map[string]string{"agent": to.agentName}); err != nil {
					return err
				}
				continue
			}
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

	// request_built: the pack slot — rewrite the fully-assembled request once
	// per turn before anything is streamed. A non-nil error is a fatal abort.
	if to.hooks != nil {
		var herr error
		req, herr = to.hooks.RequestBuilt(ctx, req)
		if herr != nil {
			return herr
		}
	}

	// Track whether we've retried without tools to avoid infinite loops.
	retriedNoTools := false

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
				chunk := ev.Text
				if to.hooks != nil {
					var herr error
					chunk, herr = to.hooks.ResponseReady(ctx, ev.Text, false, "", llm.Usage{})
					if herr != nil {
						return herr
					}
				}
				if _, err := io.WriteString(out, chunk); err != nil {
					return err
				}
				reply.WriteString(chunk)
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
			// response_ready final release: exposes the finish reason + usage the
			// turn ended with, so plugins can alert/log the turn-end summary.
			if to.hooks != nil {
				if _, herr := to.hooks.ResponseReady(ctx, "", true, finishReason, usage); herr != nil {
					return herr
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

		// Execute tool calls serially and feed results back.
		slog.Info("tool calls received", "count", len(toolCalls))
		for _, tc := range toolCalls {
			// Frame the tool run on stderr.
			if errOut != nil {
				fmt.Fprintf(errOut, "── %s %s ──\n", tc.Name, truncateArgs(tc.Arguments, 120))
			}

			args := tc.Arguments

			// tool_before: guardrail slot — a hook may rewrite the arguments and/or
			// suppress the call. Suppress short-circuits the chain: the call is dead,
			// and a well-formed tool message keeps the conversation sound (a bare
			// unanswered tool call would otherwise loop).
			if to.hooks != nil {
				var suppress bool
				var herr error
				args, suppress, herr = to.hooks.ToolBefore(ctx, tc.Name, tc.ID, tc.Arguments)
				if herr != nil {
					return herr
				}
				if suppress {
					toolMsg := llm.Message{
						Role:       llm.RoleTool,
						Content:    "Tool call suppressed by lifecycle hook.",
						ToolCallID: tc.ID,
					}
					req.Messages = append(req.Messages, toolMsg)
					if sess != nil {
						if err := sess.Append(toolMsg); err != nil {
							return err
						}
					}
					continue
				}
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
					if err := tools.ValidateArgs(json.RawMessage(args), tool.Parameters()); err != nil {
						execErr = fmt.Errorf("invalid arguments for %s: %v", tc.Name, err)
					} else {
						// Snapshot pre-mutation content for write/edit tools.
						if ldg != nil && (tc.Name == "write" || tc.Name == "edit") {
							var largs struct {
								Path string `json:"path"`
							}
							if json.Unmarshal([]byte(args), &largs) == nil && largs.Path != "" {
								absPath := resolvePath(largs.Path, cwd)
								if data, err := os.ReadFile(absPath); err == nil {
									ldg.Snapshot(absPath, string(data))
								} else {
									ldg.Snapshot(absPath, "") // New file
								}
								ldg.RecordToolCall(tc.ID)
							}
						}
						result, execErr = tool.Execute(json.RawMessage(args))
						// Record successful mutations in ledger.
						if ldg != nil && execErr == nil && (tc.Name == "write" || tc.Name == "edit") {
							var largs struct {
								Path    string `json:"path"`
								Content string `json:"content"`
							}
							if json.Unmarshal([]byte(args), &largs) == nil && largs.Path != "" {
								absPath := resolvePath(largs.Path, cwd)
								oldContent := ldg.GetSnapshot(absPath)
								newContent := largs.Content
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

			// tool_after: every completed call is observed (err is a field); a hook
			// may rewrite the result text.
			if to.hooks != nil {
				errText := ""
				if execErr != nil {
					errText = execErr.Error()
				}
				var herr error
				result, herr = to.hooks.ToolAfter(ctx, tc.Name, tc.ID, args, result, errText)
				if herr != nil {
					return herr
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
