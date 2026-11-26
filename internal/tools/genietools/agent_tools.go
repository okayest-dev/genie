// Package genietools implements tools for Genie self-improvement.
package genietools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/okayest-dev/genie/internal/tools"
)

// GenieAgentCreateTool creates a new agent definition.
type GenieAgentCreateTool struct {
	configDir string
	cwd       string
}

func NewGenieAgentCreateTool(configDir, cwd string) *GenieAgentCreateTool {
	return &GenieAgentCreateTool{configDir: configDir, cwd: cwd}
}

func (t *GenieAgentCreateTool) Name() string        { return "genie_agent_create" }
func (t *GenieAgentCreateTool) Description() string { return "Create a new agent definition" }

func (t *GenieAgentCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Agent name (kebab-case)",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Model to use (optional, defaults to provider default)",
			},
			"tools": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Explicit tool allowlist (optional)",
			},
			"skills": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Explicit skill allowlist (optional)",
			},
			"permissions": map[string]any{
				"type":        "object",
				"description": "Per-agent permission base (optional, replaces global)",
			},
			"instruction_file": map[string]any{
				"type":        "string",
				"description": "Path to agent instruction file (optional)",
			},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"project", "global"},
				"description": "Where to create the agent (default: project)",
			},
		},
		"required": []any{"name"},
	}
}

func (t *GenieAgentCreateTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Name            string   `json:"name"`
		Model           string   `json:"model,omitempty"`
		Tools           []string `json:"tools,omitempty"`
		Skills          []string `json:"skills,omitempty"`
		Permissions     map[string]any `json:"permissions,omitempty"`
		InstructionFile string   `json:"instruction_file,omitempty"`
		Scope           string   `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.Scope == "" {
		params.Scope = "project"
	}

	var agentsDir string
	if params.Scope == "project" {
		agentsDir = filepath.Join(t.cwd, ".genie", "agents")
	} else {
		agentsDir = filepath.Join(t.configDir, "genie", "agents")
	}

	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return "", fmt.Errorf("create agents dir: %w", err)
	}

	agentPath := filepath.Join(agentsDir, params.Name+".toml")
	if _, err := os.Stat(agentPath); err == nil {
		return "", fmt.Errorf("agent already exists: %s", params.Name)
	}

	// Build agent TOML
	agent := map[string]any{}
	if params.Model != "" {
		agent["model"] = params.Model
	}
	if len(params.Tools) > 0 {
		agent["tools"] = params.Tools
	}
	if len(params.Skills) > 0 {
		agent["skills"] = params.Skills
	}
	if params.Permissions != nil {
		agent["permissions"] = params.Permissions
	}
	if params.InstructionFile != "" {
		agent["instruction_file"] = params.InstructionFile
	}

	f, err := os.Create(agentPath)
	if err != nil {
		return "", fmt.Errorf("create agent file: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(agent); err != nil {
		return "", fmt.Errorf("encode agent: %w", err)
	}

	return fmt.Sprintf("Agent created at %s", agentPath), nil
}

// GenieAgentUpdateTool updates an agent definition.
type GenieAgentUpdateTool struct {
	configDir string
	cwd       string
}

func NewGenieAgentUpdateTool(configDir, cwd string) *GenieAgentUpdateTool {
	return &GenieAgentUpdateTool{configDir: configDir, cwd: cwd}
}

func (t *GenieAgentUpdateTool) Name() string        { return "genie_agent_update" }
func (t *GenieAgentUpdateTool) Description() string { return "Update an agent definition" }

func (t *GenieAgentUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Agent name to update",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "New model (optional)",
			},
			"tools": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "New tool allowlist (optional)",
			},
			"skills": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "New skill allowlist (optional)",
			},
			"permissions": map[string]any{
				"type":        "object",
				"description": "New permissions (optional)",
			},
			"instruction_file": map[string]any{
				"type":        "string",
				"description": "New instruction file path (optional)",
			},
		},
		"required": []any{"name"},
	}
}

func (t *GenieAgentUpdateTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Name            string         `json:"name"`
		Model           string         `json:"model,omitempty"`
		Tools           []string       `json:"tools,omitempty"`
		Skills          []string       `json:"skills,omitempty"`
		Permissions     map[string]any `json:"permissions,omitempty"`
		InstructionFile string         `json:"instruction_file,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Find agent file
	var agentPath string
	projectPath := filepath.Join(t.cwd, ".genie", "agents", params.Name+".toml")
	globalPath := filepath.Join(t.configDir, "genie", "agents", params.Name+".toml")

	if _, err := os.Stat(projectPath); err == nil {
		agentPath = projectPath
	} else if _, err := os.Stat(globalPath); err == nil {
		agentPath = globalPath
	} else {
		return "", fmt.Errorf("agent not found: %s", params.Name)
	}

	// Read existing
	var agent map[string]any
	if _, err := toml.DecodeFile(agentPath, &agent); err != nil {
		return "", fmt.Errorf("read agent: %w", err)
	}

	// Update fields
	if params.Model != "" {
		agent["model"] = params.Model
	}
	if params.Tools != nil {
		agent["tools"] = params.Tools
	}
	if params.Skills != nil {
		agent["skills"] = params.Skills
	}
	if params.Permissions != nil {
		agent["permissions"] = params.Permissions
	}
	if params.InstructionFile != "" {
		agent["instruction_file"] = params.InstructionFile
	}

	// Write back
	f, err := os.Create(agentPath)
	if err != nil {
		return "", fmt.Errorf("write agent: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(agent); err != nil {
		return "", fmt.Errorf("encode agent: %w", err)
	}

	return fmt.Sprintf("Agent %s updated at %s", params.Name, agentPath), nil
}

// GenieAgentRemoveTool removes an agent definition.
type GenieAgentRemoveTool struct {
	configDir string
	cwd       string
}

func NewGenieAgentRemoveTool(configDir, cwd string) *GenieAgentRemoveTool {
	return &GenieAgentRemoveTool{configDir: configDir, cwd: cwd}
}

func (t *GenieAgentRemoveTool) Name() string        { return "genie_agent_remove" }
func (t *GenieAgentRemoveTool) Description() string { return "Remove an agent definition" }

func (t *GenieAgentRemoveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Agent name to remove",
			},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"project", "global", "all"},
				"description": "Which scope to remove from (default: all)",
			},
		},
		"required": []any{"name"},
	}
}

func (t *GenieAgentRemoveTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Name  string `json:"name"`
		Scope string `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.Scope == "" {
		params.Scope = "all"
	}

	removed := false
	if params.Scope == "project" || params.Scope == "all" {
		path := filepath.Join(t.cwd, ".genie", "agents", params.Name+".toml")
		if err := os.Remove(path); err == nil {
			removed = true
		}
	}
	if params.Scope == "global" || params.Scope == "all" {
		path := filepath.Join(t.configDir, "genie", "agents", params.Name+".toml")
		if err := os.Remove(path); err == nil {
			removed = true
		}
	}

	if !removed {
		return "", fmt.Errorf("agent not found: %s", params.Name)
	}

	return fmt.Sprintf("Agent %s removed", params.Name), nil
}

// GenieAgentListTool lists agent definitions.
type GenieAgentListTool struct {
	configDir string
	cwd       string
}

func NewGenieAgentListTool(configDir, cwd string) *GenieAgentListTool {
	return &GenieAgentListTool{configDir: configDir, cwd: cwd}
}

func (t *GenieAgentListTool) Name() string        { return "genie_agent_list" }
func (t *GenieAgentListTool) Description() string { return "List agent definitions" }

func (t *GenieAgentListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"project", "global", "all"},
				"description": "Which scope to list (default: all)",
			},
		},
	}
}

func (t *GenieAgentListTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Scope string `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.Scope == "" {
		params.Scope = "all"
	}

	var agents []string

	listDir := func(dir, label string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
				name := strings.TrimSuffix(e.Name(), ".toml")
				agents = append(agents, fmt.Sprintf("  %s%s", name, label))
			}
		}
	}

	if params.Scope == "project" || params.Scope == "all" {
		listDir(filepath.Join(t.cwd, ".genie", "agents"), " (project)")
	}
	if params.Scope == "global" || params.Scope == "all" {
		listDir(filepath.Join(t.configDir, "genie", "agents"), " (global)")
	}

	if len(agents) == 0 {
		return "No agents found", nil
	}
	return "Available agents:\n" + strings.Join(agents, "\n"), nil
}

// Ensure tools implement the tools.Tool interface
var _ tools.Tool = (*GenieAgentCreateTool)(nil)
var _ tools.Tool = (*GenieAgentUpdateTool)(nil)
var _ tools.Tool = (*GenieAgentRemoveTool)(nil)
var _ tools.Tool = (*GenieAgentListTool)(nil)