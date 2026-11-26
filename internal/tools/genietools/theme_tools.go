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

// GenieThemeCreateTool creates a new theme.
type GenieThemeCreateTool struct {
	configDir string
	cwd       string
}

func NewGenieThemeCreateTool(configDir, cwd string) *GenieThemeCreateTool {
	return &GenieThemeCreateTool{configDir: configDir, cwd: cwd}
}

func (t *GenieThemeCreateTool) Name() string        { return "genie_theme_create" }
func (t *GenieThemeCreateTool) Description() string { return "Create a new theme" }

func (t *GenieThemeCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Theme name (kebab-case)",
			},
			"extends": map[string]any{
				"type":        "string",
				"description": "Base theme to extend (classic, lean, or another theme name)",
			},
			"roles": map[string]any{
				"type":        "object",
				"description": "Role styling overrides (optional)",
			},
			"glyphs": map[string]any{
				"type":        "object",
				"description": "Glyph overrides (optional)",
			},
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"project", "global"},
				"description": "Where to create the theme (default: global)",
			},
		},
		"required": []any{"name"},
	}
}

func (t *GenieThemeCreateTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Name   string         `json:"name"`
		Extends string        `json:"extends,omitempty"`
		Roles  map[string]any `json:"roles,omitempty"`
		Glyphs map[string]any `json:"glyphs,omitempty"`
		Scope  string         `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.Scope == "" {
		params.Scope = "global"
	}

	var themesDir string
	if params.Scope == "project" {
		themesDir = filepath.Join(t.cwd, ".genie", "themes")
	} else {
		themesDir = filepath.Join(t.configDir, "genie", "themes")
	}

	if err := os.MkdirAll(themesDir, 0o755); err != nil {
		return "", fmt.Errorf("create themes dir: %w", err)
	}

	themePath := filepath.Join(themesDir, params.Name+".toml")
	if _, err := os.Stat(themePath); err == nil {
		return "", fmt.Errorf("theme already exists: %s", params.Name)
	}

	theme := map[string]any{}
	if params.Extends != "" {
		theme["extends"] = params.Extends
	}
	if params.Roles != nil {
		theme["roles"] = params.Roles
	}
	if params.Glyphs != nil {
		theme["glyphs"] = params.Glyphs
	}

	f, err := os.Create(themePath)
	if err != nil {
		return "", fmt.Errorf("create theme file: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(theme); err != nil {
		return "", fmt.Errorf("encode theme: %w", err)
	}

	return fmt.Sprintf("Theme created at %s", themePath), nil
}

// GenieThemeUpdateTool updates a theme.
type GenieThemeUpdateTool struct {
	configDir string
	cwd       string
}

func NewGenieThemeUpdateTool(configDir, cwd string) *GenieThemeUpdateTool {
	return &GenieThemeUpdateTool{configDir: configDir, cwd: cwd}
}

func (t *GenieThemeUpdateTool) Name() string        { return "genie_theme_update" }
func (t *GenieThemeUpdateTool) Description() string { return "Update a theme" }

func (t *GenieThemeUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Theme name to update",
			},
			"extends": map[string]any{
				"type":        "string",
				"description": "New base theme (optional)",
			},
			"roles": map[string]any{
				"type":        "object",
				"description": "New role styling (optional)",
			},
			"glyphs": map[string]any{
				"type":        "object",
				"description": "New glyph overrides (optional)",
			},
		},
		"required": []any{"name"},
	}
}

func (t *GenieThemeUpdateTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Name   string         `json:"name"`
		Extends string        `json:"extends,omitempty"`
		Roles  map[string]any `json:"roles,omitempty"`
		Glyphs map[string]any `json:"glyphs,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Find theme file
	var themePath string
	projectPath := filepath.Join(t.cwd, ".genie", "themes", params.Name+".toml")
	globalPath := filepath.Join(t.configDir, "genie", "themes", params.Name+".toml")

	if _, err := os.Stat(projectPath); err == nil {
		themePath = projectPath
	} else if _, err := os.Stat(globalPath); err == nil {
		themePath = globalPath
	} else {
		return "", fmt.Errorf("theme not found: %s", params.Name)
	}

	// Read existing
	var theme map[string]any
	if _, err := toml.DecodeFile(themePath, &theme); err != nil {
		return "", fmt.Errorf("read theme: %w", err)
	}

	// Update fields
	if params.Extends != "" {
		theme["extends"] = params.Extends
	}
	if params.Roles != nil {
		theme["roles"] = params.Roles
	}
	if params.Glyphs != nil {
		theme["glyphs"] = params.Glyphs
	}

	// Write back
	f, err := os.Create(themePath)
	if err != nil {
		return "", fmt.Errorf("write theme: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(theme); err != nil {
		return "", fmt.Errorf("encode theme: %w", err)
	}

	return fmt.Sprintf("Theme %s updated at %s", params.Name, themePath), nil
}

// GenieThemeRemoveTool removes a theme.
type GenieThemeRemoveTool struct {
	configDir string
	cwd       string
}

func NewGenieThemeRemoveTool(configDir, cwd string) *GenieThemeRemoveTool {
	return &GenieThemeRemoveTool{configDir: configDir, cwd: cwd}
}

func (t *GenieThemeRemoveTool) Name() string        { return "genie_theme_remove" }
func (t *GenieThemeRemoveTool) Description() string { return "Remove a theme" }

func (t *GenieThemeRemoveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Theme name to remove",
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

func (t *GenieThemeRemoveTool) Execute(args json.RawMessage) (string, error) {
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
		path := filepath.Join(t.cwd, ".genie", "themes", params.Name+".toml")
		if err := os.Remove(path); err == nil {
			removed = true
		}
	}
	if params.Scope == "global" || params.Scope == "all" {
		path := filepath.Join(t.configDir, "genie", "themes", params.Name+".toml")
		if err := os.Remove(path); err == nil {
			removed = true
		}
	}

	if !removed {
		return "", fmt.Errorf("theme not found: %s", params.Name)
	}

	return fmt.Sprintf("Theme %s removed", params.Name), nil
}

// GenieThemeListTool lists available themes.
type GenieThemeListTool struct {
	configDir string
	cwd       string
}

func NewGenieThemeListTool(configDir, cwd string) *GenieThemeListTool {
	return &GenieThemeListTool{configDir: configDir, cwd: cwd}
}

func (t *GenieThemeListTool) Name() string        { return "genie_theme_list" }
func (t *GenieThemeListTool) Description() string { return "List available themes" }

func (t *GenieThemeListTool) Parameters() map[string]any {
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

func (t *GenieThemeListTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Scope string `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.Scope == "" {
		params.Scope = "all"
	}

	var themes []string

	listDir := func(dir, label string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
				name := strings.TrimSuffix(e.Name(), ".toml")
				themes = append(themes, fmt.Sprintf("  %s%s", name, label))
			}
		}
	}

	// Built-in themes
	themes = append(themes, "  classic (builtin)")
	themes = append(themes, "  lean (builtin)")

	if params.Scope == "project" || params.Scope == "all" {
		listDir(filepath.Join(t.cwd, ".genie", "themes"), " (project)")
	}
	if params.Scope == "global" || params.Scope == "all" {
		listDir(filepath.Join(t.configDir, "genie", "themes"), " (global)")
	}

	return "Available themes:\n" + strings.Join(themes, "\n"), nil
}

// Ensure tools implement the tools.Tool interface
var _ tools.Tool = (*GenieThemeCreateTool)(nil)
var _ tools.Tool = (*GenieThemeUpdateTool)(nil)
var _ tools.Tool = (*GenieThemeRemoveTool)(nil)
var _ tools.Tool = (*GenieThemeListTool)(nil)