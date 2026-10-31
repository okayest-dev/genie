package run

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/okayest-dev/genie/internal/tools"
)

// SlashCommandTool is a tool that invokes a slash command via the run.Handle.
type SlashCommandTool struct {
	name        string
	description string
	handler     func(ctx context.Context, args string, h *Handle) (string, error)
	h           *Handle
}

// Name returns the tool name.
func (t *SlashCommandTool) Name() string {
	return t.name
}

// Description returns the tool description.
func (t *SlashCommandTool) Description() string {
	return t.description
}

// Parameters returns the JSON Schema for the tool arguments.
func (t *SlashCommandTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"args": map[string]any{
				"type":        "string",
				"description": "Arguments to pass to the slash command (everything after the command name)",
			},
		},
		"required": []string{"args"},
	}
}

// Execute runs the slash command with the given arguments.
func (t *SlashCommandTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Args string `json:"args"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	return t.handler(context.Background(), params.Args, t.h)
}

// RegisterSlashCommands registers all slash command tools with the given handle.
func RegisterSlashCommands(reg *tools.Registry, h *Handle) {
	// /new
	reg.Register(&SlashCommandTool{
		name:        "slash_new",
		description: "Start a new session with optional agent, model, handoff, and prompt. Usage: slash_new {args: \"[--agent <name>] [--model <id>] [--handoff [<path>]] [<prompt>]\"}",
		h:           h,
		handler: func(ctx context.Context, args string, h *Handle) (string, error) {
			sess, err := h.NewSession()
			if err != nil {
				return "", fmt.Errorf("create session: %w", err)
			}
			return fmt.Sprintf("new session: %s", sess.ID), nil
		},
	})

	// /model
	reg.Register(&SlashCommandTool{
		name:        "slash_model",
		description: "List available models or switch model. Usage: slash_model {args: \"[<id>]\"}",
		h:           h,
		handler: func(ctx context.Context, args string, h *Handle) (string, error) {
			if strings.TrimSpace(args) == "" {
				// List models
				models, err := h.ProviderCatalog(ctx)
				if err != nil {
					return "", fmt.Errorf("get model catalog: %w", err)
				}
				var out strings.Builder
				for _, m := range models {
					out.WriteString(fmt.Sprintf("%s\n", m.ID))
				}
				return out.String(), nil
			}
			// Switch model
			h.SetModel(strings.TrimSpace(args))
			return fmt.Sprintf("model set to %s", args), nil
		},
	})

	// /agent
	reg.Register(&SlashCommandTool{
		name:        "slash_agent",
		description: "List available agents or switch agent. Usage: slash_agent {args: \"[<name>]\"}",
		h:           h,
		handler: func(ctx context.Context, args string, h *Handle) (string, error) {
			if strings.TrimSpace(args) == "" {
				// List agents
				if h.AgentReg() == nil {
					return "no agents configured", nil
				}
				agents := h.AgentReg().List()
				var out strings.Builder
				for _, a := range agents {
					out.WriteString(fmt.Sprintf("%s\n", a))
				}
				return out.String(), nil
			}
			// Switch agent
			_, err := h.SwitchAgent(strings.TrimSpace(args))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("agent set to %s", args), nil
		},
	})

	// /provider
	reg.Register(&SlashCommandTool{
		name:        "slash_provider",
		description: "List providers or switch provider. Usage: slash_provider {args: \"[<name>]\"}",
		h:           h,
		handler: func(ctx context.Context, args string, h *Handle) (string, error) {
			if strings.TrimSpace(args) == "" {
				// List providers
				names := h.ProviderNames()
				return strings.Join(names, "\n"), nil
			}
			// Switch provider
			return "", h.SwitchProvider(strings.TrimSpace(args))
		},
	})

	// /summarize
	reg.Register(&SlashCommandTool{
		name:        "slash_summarize",
		description: "Write a handoff brief for the current session. Usage: slash_summarize {args: \"\"}",
		h:           h,
		handler: func(ctx context.Context, args string, h *Handle) (string, error) {
			path, err := h.Summarize(ctx)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("wrote %s", path), nil
		},
	})

	// /changes
	reg.Register(&SlashCommandTool{
		name:        "slash_changes",
		description: "List change batches or show details. Usage: slash_changes {args: \"[<id>]\"}",
		h:           h,
		handler: func(ctx context.Context, args string, h *Handle) (string, error) {
			return "not fully implemented", nil
		},
	})

	// /task
	reg.Register(&SlashCommandTool{
		name:        "slash_task",
		description: "Show the active task and its lifecycle record. Usage: slash_task {args: \"\"}",
		h:           h,
		handler: func(ctx context.Context, args string, h *Handle) (string, error) {
			state := h.TaskState()
			active := state.Get()
			if active == nil {
				return "no active task", nil
			}
			return fmt.Sprintf("Active Task: %s\nTitle: %s\nType: %s\nStatus: %s", active.Task.ID, active.Task.Title, active.Task.Type, active.Task.Status), nil
		},
	})
}