// Package genietools implements tools for Genie self-improvement.
package genietools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/okayest-dev/genie/internal/skill"
	"github.com/okayest-dev/genie/internal/tools"
)

// GenieSkillCreateTool creates a new skill scaffold.
type GenieSkillCreateTool struct {
	skillDirs []string
}

func NewGenieSkillCreateTool(skillDirs []string) *GenieSkillCreateTool {
	return &GenieSkillCreateTool{skillDirs: skillDirs}
}

func (t *GenieSkillCreateTool) Name() string        { return "genie_skill_create" }
func (t *GenieSkillCreateTool) Description() string { return "Create a new skill scaffold" }

func (t *GenieSkillCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Skill name (kebab-case)",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Skill description",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "Skill body/instructions (markdown)",
			},
			"disable_model_invocation": map[string]any{
				"type":        "boolean",
				"description": "Whether to disable model invocation for this skill",
			},
			"argument_hint": map[string]any{
				"type":        "string",
				"description": "Hint for when to use this skill",
			},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"project", "user", "external"},
				"description": "Which skill directory to create in (default: project)",
			},
		},
		"required": []any{"name", "description"},
	}
}

func (t *GenieSkillCreateTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Name                   string `json:"name"`
		Description            string `json:"description"`
		Body                   string `json:"body,omitempty"`
		DisableModelInvocation bool   `json:"disable_model_invocation,omitempty"`
		ArgumentHint           string `json:"argument_hint,omitempty"`
		Scope                  string `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.Scope == "" {
		params.Scope = "project"
	}

	var targetDir string
	switch params.Scope {
	case "project":
		if len(t.skillDirs) > 0 {
			targetDir = t.skillDirs[0]
		} else {
			cwd, _ := os.Getwd()
			targetDir = filepath.Join(cwd, ".genie", "skills")
		}
	case "user":
		if len(t.skillDirs) > 2 {
			targetDir = t.skillDirs[2]
		} else {
			home, _ := os.UserHomeDir()
			targetDir = filepath.Join(home, ".config", "genie", "skills")
		}
	case "external":
		if len(t.skillDirs) > 1 {
			targetDir = t.skillDirs[1]
		} else {
			home, _ := os.UserHomeDir()
			targetDir = filepath.Join(home, ".agents", "skills")
		}
	default:
		return "", fmt.Errorf("unknown scope: %s", params.Scope)
	}

	skillPath := filepath.Join(targetDir, params.Name)
	if err := os.MkdirAll(skillPath, 0o755); err != nil {
		return "", fmt.Errorf("create skill dir: %w", err)
	}

	// Build frontmatter
	fm := map[string]any{
		"name":                       params.Name,
		"description":                params.Description,
		"disable-model-invocation":   params.DisableModelInvocation,
		"argument-hint":              params.ArgumentHint,
	}

	// Default body
	body := params.Body
	if body == "" {
		body = fmt.Sprintf("# Skill: %s\n\n%s\n\n<!-- Add your skill instructions here -->", params.Name, params.Description)
	}

	// Write SKILL.md
	skillFile := filepath.Join(skillPath, "SKILL.md")
	f, err := os.Create(skillFile)
	if err != nil {
		return "", fmt.Errorf("create skill file: %w", err)
	}
	defer f.Close()

	// Write frontmatter
	f.WriteString("---\n")
	for k, v := range fm {
		if v != nil && v != "" && v != false {
			fmt.Fprintf(f, "%s: %v\n", k, v)
		}
	}
	f.WriteString("---\n\n")
	f.WriteString(body)

	return fmt.Sprintf("Skill created at %s", skillFile), nil
}

// GenieSkillUpdateTool updates an existing skill.
type GenieSkillUpdateTool struct {
	skillDirs []string
}

func NewGenieSkillUpdateTool(skillDirs []string) *GenieSkillUpdateTool {
	return &GenieSkillUpdateTool{skillDirs: skillDirs}
}

func (t *GenieSkillUpdateTool) Name() string        { return "genie_skill_update" }
func (t *GenieSkillUpdateTool) Description() string { return "Update an existing skill's body or frontmatter" }

func (t *GenieSkillUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Skill name to update",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "New description (optional)",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "New body/instructions (optional)",
			},
			"disable_model_invocation": map[string]any{
				"type":        "boolean",
				"description": "New disable-model-invocation value (optional)",
			},
			"argument_hint": map[string]any{
				"type":        "string",
				"description": "New argument-hint (optional)",
			},
		},
		"required": []any{"name"},
	}
}

func (t *GenieSkillUpdateTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Name                   string `json:"name"`
		Description            string `json:"description,omitempty"`
		Body                   string `json:"body,omitempty"`
		DisableModelInvocation *bool  `json:"disable_model_invocation,omitempty"`
		ArgumentHint           string `json:"argument_hint,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Find the skill
	var skillPath string
	for _, dir := range t.skillDirs {
		path := filepath.Join(dir, params.Name, "SKILL.md")
		if _, err := os.Stat(path); err == nil {
			skillPath = path
			break
		}
	}
	if skillPath == "" {
		return "", fmt.Errorf("skill not found: %s", params.Name)
	}

	// Read existing
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		return "", fmt.Errorf("read skill: %w", err)
	}

	parsed, err := skill.Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("parse skill: %w", err)
	}

	// Update fields
	if params.Description != "" {
		parsed.Description = params.Description
	}
	if params.Body != "" {
		parsed.Body = params.Body
	}
	if params.DisableModelInvocation != nil {
		parsed.DisableModelInvocation = *params.DisableModelInvocation
	}
	if params.ArgumentHint != "" {
		parsed.ArgumentHint = params.ArgumentHint
	}

	// Write back
	f, err := os.Create(skillPath)
	if err != nil {
		return "", fmt.Errorf("write skill: %w", err)
	}
	defer f.Close()

	f.WriteString("---\n")
	fmt.Fprintf(f, "name: %s\n", parsed.Name)
	fmt.Fprintf(f, "description: %s\n", parsed.Description)
	if parsed.DisableModelInvocation {
		fmt.Fprintf(f, "disable-model-invocation: true\n")
	}
	if parsed.ArgumentHint != "" {
		fmt.Fprintf(f, "argument-hint: %s\n", parsed.ArgumentHint)
	}
	f.WriteString("---\n\n")
	f.WriteString(parsed.Body)

	return fmt.Sprintf("Skill %s updated at %s", params.Name, skillPath), nil
}

// GenieSkillRemoveTool removes a skill.
type GenieSkillRemoveTool struct {
	skillDirs []string
}

func NewGenieSkillRemoveTool(skillDirs []string) *GenieSkillRemoveTool {
	return &GenieSkillRemoveTool{skillDirs: skillDirs}
}

func (t *GenieSkillRemoveTool) Name() string        { return "genie_skill_remove" }
func (t *GenieSkillRemoveTool) Description() string { return "Remove a skill" }

func (t *GenieSkillRemoveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Skill name to remove",
			},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"project", "user", "external", "all"},
				"description": "Which scope to remove from (default: all)",
			},
		},
		"required": []any{"name"},
	}
}

func (t *GenieSkillRemoveTool) Execute(args json.RawMessage) (string, error) {
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

	var dirs []string
	switch params.Scope {
	case "project":
		if len(t.skillDirs) > 0 {
			dirs = []string{t.skillDirs[0]}
		}
	case "user":
		if len(t.skillDirs) > 2 {
			dirs = []string{t.skillDirs[2]}
		}
	case "external":
		if len(t.skillDirs) > 1 {
			dirs = []string{t.skillDirs[1]}
		}
	case "all":
		dirs = t.skillDirs
	default:
		return "", fmt.Errorf("unknown scope: %s", params.Scope)
	}

	removed := false
	for _, dir := range dirs {
		path := filepath.Join(dir, params.Name)
		if err := os.RemoveAll(path); err == nil {
			removed = true
		}
	}

	if !removed {
		return "", fmt.Errorf("skill not found: %s", params.Name)
	}

	return fmt.Sprintf("Skill %s removed", params.Name), nil
}

// GenieSkillListTool lists available skills.
type GenieSkillListTool struct {
	skillDirs []string
}

func NewGenieSkillListTool(skillDirs []string) *GenieSkillListTool {
	return &GenieSkillListTool{skillDirs: skillDirs}
}

func (t *GenieSkillListTool) Name() string        { return "genie_skill_list" }
func (t *GenieSkillListTool) Description() string { return "List available skills" }

func (t *GenieSkillListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"project", "user", "external", "all"},
				"description": "Which scope to list (default: all)",
			},
		},
	}
}

func (t *GenieSkillListTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Scope string `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.Scope == "" {
		params.Scope = "all"
	}

	var dirs []string
	switch params.Scope {
	case "project":
		if len(t.skillDirs) > 0 {
			dirs = []string{t.skillDirs[0]}
		}
	case "user":
		if len(t.skillDirs) > 2 {
			dirs = []string{t.skillDirs[2]}
		}
	case "external":
		if len(t.skillDirs) > 1 {
			dirs = []string{t.skillDirs[1]}
		}
	case "all":
		dirs = t.skillDirs
	default:
		return "", fmt.Errorf("unknown scope: %s", params.Scope)
	}

	var skills []string
	seen := make(map[string]bool)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && !seen[e.Name()] {
				skillFile := filepath.Join(dir, e.Name(), "SKILL.md")
				if _, err := os.Stat(skillFile); err == nil {
					raw, _ := os.ReadFile(skillFile)
					parsed, _ := skill.Parse(string(raw))
					scopeLabel := ""
					if dir == t.skillDirs[0] {
						scopeLabel = " (project)"
					} else if len(t.skillDirs) > 1 && dir == t.skillDirs[1] {
						scopeLabel = " (external)"
					} else if len(t.skillDirs) > 2 && dir == t.skillDirs[2] {
						scopeLabel = " (user)"
					}
					skills = append(skills, fmt.Sprintf("  %s%s - %s", parsed.Name, scopeLabel, parsed.Description))
					seen[e.Name()] = true
				}
			}
		}
	}

	if len(skills) == 0 {
		return "No skills found", nil
	}
	return "Available skills:\n" + strings.Join(skills, "\n"), nil
}

// Ensure tools implement the tools.Tool interface
var _ tools.Tool = (*GenieSkillCreateTool)(nil)
var _ tools.Tool = (*GenieSkillUpdateTool)(nil)
var _ tools.Tool = (*GenieSkillRemoveTool)(nil)
var _ tools.Tool = (*GenieSkillListTool)(nil)