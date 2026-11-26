// Package genietools implements tools for Genie self-improvement.
package genietools

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/tools"
)

// GenieProviderAddTool adds or updates a provider configuration.
type GenieProviderAddTool struct {
	config *config.Config
}

func NewGenieProviderAddTool(cfg *config.Config) *GenieProviderAddTool {
	return &GenieProviderAddTool{config: cfg}
}

func (t *GenieProviderAddTool) Name() string        { return "genie_provider_add" }
func (t *GenieProviderAddTool) Description() string { return "Add or update a provider configuration" }

func (t *GenieProviderAddTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Provider name",
			},
			"wire": map[string]any{
				"type":        "string",
				"enum":        []string{"openai", "anthropic", "google", "copilot", "bedrock", "openai_responses"},
				"description": "Wire protocol",
			},
			"base_url": map[string]any{
				"type":        "string",
				"description": "API base URL",
			},
			"api_key_env": map[string]any{
				"type":        "string",
				"description": "Environment variable for API key",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Default model",
			},
			"models": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Model catalog (optional)",
			},
			"opts": map[string]any{
				"type":        "object",
				"description": "Wire-specific options (optional)",
			},
		},
		"required": []any{"name", "wire", "model"},
	}
}

func (t *GenieProviderAddTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Name       string         `json:"name"`
		Wire       string           `json:"wire"`
		BaseURL    string           `json:"base_url,omitempty"`
		APIKeyEnv  string           `json:"api_key_env,omitempty"`
		Model      string           `json:"model"`
		Models     []string         `json:"models,omitempty"`
		Opts       map[string]any   `json:"opts,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	path, err := config.Path()
	if err != nil {
		return "", fmt.Errorf("config path: %w", err)
	}

	file, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read config: %w", err)
	}

	var raw map[string]any
	if len(file) > 0 {
		if _, err := toml.Decode(string(file), &raw); err != nil {
			return "", fmt.Errorf("parse config: %w", err)
		}
	} else {
		raw = make(map[string]any)
	}

	// Ensure providers map exists
	providers, _ := raw["providers"].(map[string]any)
	if providers == nil {
		providers = make(map[string]any)
		raw["providers"] = providers
	}

	// Build provider config
	provider := map[string]any{
		"wire":  params.Wire,
		"model": params.Model,
	}
	if params.BaseURL != "" {
		provider["base_url"] = params.BaseURL
	}
	if params.APIKeyEnv != "" {
		provider["api_key_env"] = params.APIKeyEnv
	}
	if len(params.Models) > 0 {
		provider["models"] = params.Models
	}
	if len(params.Opts) > 0 {
		provider["opts"] = params.Opts
	}

	providers[params.Name] = provider

	// Write back
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(raw); err != nil {
		return "", fmt.Errorf("encode config: %w", err)
	}

	// Update in-memory config
	t.config.Providers[params.Name] = config.Provider{
		Wire:       params.Wire,
		BaseURL:    params.BaseURL,
		APIKeyEnv:  params.APIKeyEnv,
		Model:      params.Model,
		Models:     params.Models,
		Opts:       params.Opts,
	}

	return fmt.Sprintf("Provider %s added/updated", params.Name), nil
}

// GenieProviderRemoveTool removes a provider configuration.
type GenieProviderRemoveTool struct {
	config *config.Config
}

func NewGenieProviderRemoveTool(cfg *config.Config) *GenieProviderRemoveTool {
	return &GenieProviderRemoveTool{config: cfg}
}

func (t *GenieProviderRemoveTool) Name() string        { return "genie_provider_remove" }
func (t *GenieProviderRemoveTool) Description() string { return "Remove a provider configuration" }

func (t *GenieProviderRemoveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Provider name to remove",
			},
		},
		"required": []any{"name"},
	}
}

func (t *GenieProviderRemoveTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Check if it's a default provider
	defaultProviders := []string{"zen", "openai", "anthropic", "responses", "google", "copilot", "bedrock", "openrouter"}
	for _, dp := range defaultProviders {
		if dp == params.Name {
			return "", fmt.Errorf("cannot remove default provider %s", params.Name)
		}
	}

	path, err := config.Path()
	if err != nil {
		return "", fmt.Errorf("config path: %w", err)
	}

	file, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no config file")
		}
		return "", fmt.Errorf("read config: %w", err)
	}

	var raw map[string]any
	if _, err := toml.Decode(string(file), &raw); err != nil {
		return "", fmt.Errorf("parse config: %w", err)
	}

	providers, ok := raw["providers"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("no providers configured")
	}

	if _, ok := providers[params.Name]; !ok {
		return "", fmt.Errorf("provider not found: %s", params.Name)
	}

	delete(providers, params.Name)

	// Write back
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(raw); err != nil {
		return "", fmt.Errorf("encode config: %w", err)
	}

	// Update in-memory config
	delete(t.config.Providers, params.Name)

	return fmt.Sprintf("Provider %s removed", params.Name), nil
}

// GenieProviderListTool lists all configured providers.
type GenieProviderListTool struct {
	config *config.Config
}

func NewGenieProviderListTool(cfg *config.Config) *GenieProviderListTool {
	return &GenieProviderListTool{config: cfg}
}

func (t *GenieProviderListTool) Name() string        { return "genie_provider_list" }
func (t *GenieProviderListTool) Description() string { return "List all configured providers" }

func (t *GenieProviderListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{},
	}
}

func (t *GenieProviderListTool) Execute(args json.RawMessage) (string, error) {
	var output strings.Builder

	output.WriteString("Configured providers:\n")
	for name, p := range t.config.Providers {
		defaultMark := ""
		defaultProviders := []string{"zen", "openai", "anthropic", "responses", "google", "copilot", "bedrock", "openrouter"}
		for _, dp := range defaultProviders {
			if dp == name {
				defaultMark = " (default)"
				break
			}
		}
		activeMark := ""
		if t.config.Provider == name {
			activeMark = " *"
		}
		output.WriteString(fmt.Sprintf("  %s%s%s - wire: %s, model: %s", name, defaultMark, activeMark, p.Wire, p.Model))
		if p.BaseURL != "" {
			output.WriteString(fmt.Sprintf(", base_url: %s", p.BaseURL))
		}
		if p.APIKeyEnv != "" {
			output.WriteString(fmt.Sprintf(", api_key_env: %s", p.APIKeyEnv))
		}
		if len(p.Models) > 0 {
			output.WriteString(fmt.Sprintf(", models: %s", strings.Join(p.Models, ", ")))
		}
		output.WriteString("\n")
	}

	return output.String(), nil
}

// Ensure tools implement the tools.Tool interface
var _ tools.Tool = (*GenieProviderAddTool)(nil)
var _ tools.Tool = (*GenieProviderRemoveTool)(nil)
var _ tools.Tool = (*GenieProviderListTool)(nil)