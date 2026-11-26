// Package genietools implements tools for Genie self-improvement.
package genietools

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/tools"
)

// GeniePermissionGrantTool grants a permanent permission.
type GeniePermissionGrantTool struct {
	config *config.Config
}

func NewGeniePermissionGrantTool(cfg *config.Config) *GeniePermissionGrantTool {
	return &GeniePermissionGrantTool{config: cfg}
}

func (t *GeniePermissionGrantTool) Name() string        { return "genie_permission_grant" }
func (t *GeniePermissionGrantTool) Description() string { return "Grant a permanent permission" }

func (t *GeniePermissionGrantTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"permission": map[string]any{
				"type":        "string",
				"enum":        []string{"read", "write", "net", "run", "env"},
				"description": "Permission axis",
			},
			"scope": map[string]any{
				"type":        "string",
				"description": "Scope (path, host:port, executable, or env var name). Empty for blanket.",
			},
		},
		"required": []any{"permission"},
	}
}

func (t *GeniePermissionGrantTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Permission string `json:"permission"`
		Scope      string `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	grant := config.PermanentGrant{
		Permission: params.Permission,
		Scope:      params.Scope,
		Granted:    time.Now().UTC(),
	}

	path, err := config.Path()
	if err != nil {
		return "", fmt.Errorf("config path: %w", err)
	}

	granted, err := config.ApplyPermanentGrant(path, grant)
	if err != nil {
		return "", fmt.Errorf("apply grant: %w", err)
	}

	// Also update in-memory config
	t.config.Permissions.Permanent = append(t.config.Permissions.Permanent, grant)

	scopeDesc := params.Scope
	if scopeDesc == "" {
		scopeDesc = "blanket"
	}
	return fmt.Sprintf("Granted permanent %s permission for %s at %s", params.Permission, scopeDesc, granted.Format(time.RFC3339)), nil
}

// GeniePermissionRevokeTool revokes a permanent permission.
type GeniePermissionRevokeTool struct {
	config *config.Config
}

func NewGeniePermissionRevokeTool(cfg *config.Config) *GeniePermissionRevokeTool {
	return &GeniePermissionRevokeTool{config: cfg}
}

func (t *GeniePermissionRevokeTool) Name() string        { return "genie_permission_revoke" }
func (t *GeniePermissionRevokeTool) Description() string { return "Revoke a permanent permission" }

func (t *GeniePermissionRevokeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"permission": map[string]any{
				"type":        "string",
				"enum":        []string{"read", "write", "net", "run", "env"},
				"description": "Permission axis",
			},
			"scope": map[string]any{
				"type":        "string",
				"description": "Scope to revoke (must match exactly). Empty for blanket.",
			},
		},
		"required": []any{"permission", "scope"},
	}
}

func (t *GeniePermissionRevokeTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Permission string `json:"permission"`
		Scope      string `json:"scope"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Read current config file
	path, err := config.Path()
	if err != nil {
		return "", fmt.Errorf("config path: %w", err)
	}

	file, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no config file to revoke from")
		}
		return "", fmt.Errorf("read config: %w", err)
	}

	// Parse as raw TOML
	var raw map[string]any
	if _, err := toml.Decode(string(file), &raw); err != nil {
		return "", fmt.Errorf("parse config: %w", err)
	}

	// Navigate to permissions.permanent
	permsPath := []string{"permissions", "permanent"}
	current := raw
	for _, key := range permsPath[:len(permsPath)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return "", fmt.Errorf("config structure not found")
		}
		current = next
	}

	permanentKey := permsPath[len(permsPath)-1]
	grants, ok := current[permanentKey].([]any)
	if !ok {
		return "", fmt.Errorf("no permanent grants found")
	}

	// Find and remove matching grant
	newGrants := make([]any, 0, len(grants))
	found := false
	for _, g := range grants {
		grantMap, ok := g.(map[string]any)
		if !ok {
			newGrants = append(newGrants, g)
			continue
		}
		if grantMap["permission"] == params.Permission && grantMap["scope"] == params.Scope {
			found = true
			continue
		}
		newGrants = append(newGrants, g)
	}

	if !found {
		return "", fmt.Errorf("grant not found: %s %s", params.Permission, params.Scope)
	}

	current[permanentKey] = newGrants

	// Write back entire config
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
	newInMem := make([]config.PermanentGrant, 0, len(t.config.Permissions.Permanent))
	for _, g := range t.config.Permissions.Permanent {
		if g.Permission == params.Permission && g.Scope == params.Scope {
			continue
		}
		newInMem = append(newInMem, g)
	}
	t.config.Permissions.Permanent = newInMem

	return fmt.Sprintf("Revoked permanent %s permission for %s", params.Permission, params.Scope), nil
}

// GeniePermissionListTool lists all permission grants.
type GeniePermissionListTool struct {
	config *config.Config
}

func NewGeniePermissionListTool(cfg *config.Config) *GeniePermissionListTool {
	return &GeniePermissionListTool{config: cfg}
}

func (t *GeniePermissionListTool) Name() string        { return "genie_permission_list" }
func (t *GeniePermissionListTool) Description() string { return "List all permission grants" }

func (t *GeniePermissionListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{},
	}
}

func (t *GeniePermissionListTool) Execute(args json.RawMessage) (string, error) {
	var output strings.Builder

	output.WriteString("Base permissions:\n")
	for axis, scopes := range t.config.Permissions.Base {
		if len(scopes) == 0 {
			output.WriteString(fmt.Sprintf("  %s: (none)\n", axis))
		} else {
			output.WriteString(fmt.Sprintf("  %s: %s\n", axis, strings.Join(scopes, ", ")))
		}
	}

	output.WriteString("\nPermanent grants:\n")
	if len(t.config.Permissions.Permanent) == 0 {
		output.WriteString("  (none)\n")
	} else {
		for _, g := range t.config.Permissions.Permanent {
			scope := g.Scope
			if scope == "" {
				scope = "blanket"
			}
			output.WriteString(fmt.Sprintf("  %s %s (granted %s)\n", g.Permission, scope, g.Granted.Format(time.RFC3339)))
		}
	}

	return output.String(), nil
}

// Ensure tools implement the tools.Tool interface
var _ tools.Tool = (*GeniePermissionGrantTool)(nil)
var _ tools.Tool = (*GeniePermissionRevokeTool)(nil)
var _ tools.Tool = (*GeniePermissionListTool)(nil)