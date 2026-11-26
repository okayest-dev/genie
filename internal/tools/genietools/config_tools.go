// Package genietools implements tools for Genie self-improvement:
// config patching, plugin/skill/agent/theme management, permissions, and providers.
package genietools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/tools"
)

// ConfigScope identifies which config file to operate on.
type ConfigScope string

const (
	ConfigScopeGlobal  ConfigScope = "global"
	ConfigScopeProject ConfigScope = "project"
	ConfigScopeAgent   ConfigScope = "agent"
)

// ConfigFileResolver resolves config file paths for a given scope.
type ConfigFileResolver struct {
	Cwd           string
	ConfigDir     string
	AgentName     string // For agent-scoped configs
}

// Resolve returns the path to the config file for the given scope.
func (r *ConfigFileResolver) Resolve(scope ConfigScope) (string, error) {
	switch scope {
	case ConfigScopeGlobal:
		return config.Path()
	case ConfigScopeProject:
		return filepath.Join(r.Cwd, ".genie", "config.toml"), nil
	case ConfigScopeAgent:
		if r.AgentName == "" {
			return "", fmt.Errorf("agent name required for agent scope")
		}
		// Try project-local first, then global
		projectPath := filepath.Join(r.Cwd, ".genie", "agents", r.AgentName+".toml")
		if _, err := os.Stat(projectPath); err == nil {
			return projectPath, nil
		}
		globalDir := filepath.Join(r.ConfigDir, "genie", "agents")
		return filepath.Join(globalDir, r.AgentName+".toml"), nil
	default:
		return "", fmt.Errorf("unknown config scope: %s", scope)
	}
}

// ReadConfig reads and parses a config file.
func ReadConfig(path string) (*config.Config, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found: %s", path)
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	// For project/agent configs, we need to parse as TOML and merge with defaults
	// For now, return the raw TOML as a map for patching
	var raw map[string]any
	if _, err := toml.Decode(string(file), &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return &config.Config{}, nil // Placeholder - we'll use raw TOML for patching
}

// ReadRawConfig reads a config file as raw TOML (map[string]any).
func ReadRawConfig(path string) (map[string]any, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]any), nil // Return empty map for non-existent
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var raw map[string]any
	if _, err := toml.Decode(string(file), &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return raw, nil
}

// WriteRawConfig writes a raw TOML map to a config file.
func WriteRawConfig(path string, data map[string]any) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(data); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return nil
}

// PatchOperation represents a single JSON Patch operation (RFC 6902).
type PatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
	From  string      `json:"from,omitempty"`
}

// ApplyJSONPatch applies a JSON Patch (RFC 6902) to a TOML document.
func ApplyJSONPatch(doc map[string]any, ops []PatchOperation) error {
	for _, op := range ops {
		if err := applyOp(doc, op); err != nil {
			return fmt.Errorf("patch op %s %s: %w", op.Op, op.Path, err)
		}
	}
	return nil
}

// applyOp applies a single patch operation.
func applyOp(doc map[string]any, op PatchOperation) error {
	switch op.Op {
	case "add":
		return addOp(doc, op.Path, op.Value)
	case "remove":
		return removeOp(doc, op.Path)
	case "replace":
		return replaceOp(doc, op.Path, op.Value)
	case "move":
		return moveOp(doc, op.From, op.Path)
	case "copy":
		return copyOp(doc, op.From, op.Path)
	case "test":
		return testOp(doc, op.Path, op.Value)
	default:
		return fmt.Errorf("unknown operation: %s", op.Op)
	}
}

// JSON Pointer helper: split path into components.
func splitPointer(path string) []string {
	if path == "" || path == "/" {
		return nil
	}
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return parts
}

// navigate navigates to the parent of the target key, creating intermediate maps if needed.
func navigate(doc map[string]any, parts []string, create bool) (map[string]any, string, error) {
	current := doc
	for i, part := range parts[:len(parts)-1] {
		next, ok := current[part]
		if !ok {
			if !create {
				return nil, "", fmt.Errorf("path not found: %s", strings.Join(parts[:i+1], "/"))
			}
			next = make(map[string]any)
			current[part] = next
		}
		m, ok := next.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("not an object at: %s", strings.Join(parts[:i+1], "/"))
		}
		current = m
	}
	return current, parts[len(parts)-1], nil
}

func addOp(doc map[string]any, path string, value interface{}) error {
	parts := splitPointer(path)
	if parts == nil {
		return fmt.Errorf("cannot add to root")
	}
	parent, key, err := navigate(doc, parts, true)
	if err != nil {
		return err
	}
	// Handle array indices
	if idx := parseArrayIndex(key); idx >= 0 {
		arr, ok := parent[key[:len(key)-len(fmt.Sprintf("[%d]", idx))]].([]any)
		if !ok {
			return fmt.Errorf("not an array")
		}
		if idx > len(arr) {
			return fmt.Errorf("array index out of bounds")
		}
		// Insert at index
		newArr := make([]any, len(arr)+1)
		copy(newArr, arr[:idx])
		newArr[idx] = value
		copy(newArr[idx+1:], arr[idx:])
		parent[key[:len(key)-len(fmt.Sprintf("[%d]", idx))]] = newArr
		return nil
	}
	parent[key] = value
	return nil
}

func removeOp(doc map[string]any, path string) error {
	parts := splitPointer(path)
	if parts == nil {
		return fmt.Errorf("cannot remove root")
	}
	parent, key, err := navigate(doc, parts, false)
	if err != nil {
		return err
	}
	if idx := parseArrayIndex(key); idx >= 0 {
		arrKey := key[:len(key)-len(fmt.Sprintf("[%d]", idx))]
		arr, ok := parent[arrKey].([]any)
		if !ok {
			return fmt.Errorf("not an array")
		}
		if idx >= len(arr) {
			return fmt.Errorf("array index out of bounds")
		}
		parent[arrKey] = append(arr[:idx], arr[idx+1:]...)
		return nil
	}
	delete(parent, key)
	return nil
}

func replaceOp(doc map[string]any, path string, value interface{}) error {
	parts := splitPointer(path)
	if parts == nil {
		return fmt.Errorf("cannot replace root")
	}
	parent, key, err := navigate(doc, parts, false)
	if err != nil {
		return err
	}
	if idx := parseArrayIndex(key); idx >= 0 {
		arrKey := key[:len(key)-len(fmt.Sprintf("[%d]", idx))]
		arr, ok := parent[arrKey].([]any)
		if !ok {
			return fmt.Errorf("not an array")
		}
		if idx >= len(arr) {
			return fmt.Errorf("array index out of bounds")
		}
		arr[idx] = value
		return nil
	}
	parent[key] = value
	return nil
}

func moveOp(doc map[string]any, from, path string) error {
	// Copy then remove
	if err := copyOp(doc, from, path); err != nil {
		return err
	}
	return removeOp(doc, from)
}

func copyOp(doc map[string]any, from, path string) error {
	fromParts := splitPointer(from)
	if fromParts == nil {
		return fmt.Errorf("cannot copy from root")
	}
	parent, key, err := navigate(doc, fromParts, false)
	if err != nil {
		return err
	}
	var value interface{}
	if idx := parseArrayIndex(key); idx >= 0 {
		arrKey := key[:len(key)-len(fmt.Sprintf("[%d]", idx))]
		arr, ok := parent[arrKey].([]any)
		if !ok || idx >= len(arr) {
			return fmt.Errorf("source not found")
		}
		value = arr[idx]
	} else {
		value = parent[key]
	}
	return addOp(doc, path, value)
}

func testOp(doc map[string]any, path string, value interface{}) error {
	parts := splitPointer(path)
	if parts == nil {
		return fmt.Errorf("cannot test root")
	}
	parent, key, err := navigate(doc, parts, false)
	if err != nil {
		return err
	}
	var actual interface{}
	if idx := parseArrayIndex(key); idx >= 0 {
		arrKey := key[:len(key)-len(fmt.Sprintf("[%d]", idx))]
		arr, ok := parent[arrKey].([]any)
		if !ok || idx >= len(arr) {
			return fmt.Errorf("test path not found")
		}
		actual = arr[idx]
	} else {
		actual = parent[key]
	}
	if !equal(actual, value) {
		return fmt.Errorf("test failed: expected %v, got %v", value, actual)
	}
	return nil
}

func parseArrayIndex(key string) int {
	if strings.HasSuffix(key, "]") {
		idxStr := strings.TrimSuffix(strings.TrimPrefix(key, "["), "]")
		var idx int
		if _, err := fmt.Sscanf(idxStr, "%d", &idx); err == nil {
			return idx
		}
	}
	return -1
}

func equal(a, b interface{}) bool {
	aBytes, _ := json.Marshal(a)
	bBytes, _ := json.Marshal(b)
	return string(aBytes) == string(bBytes)
}

// Tool implementations

// GenieConfigReadTool reads a Genie config file.
type GenieConfigReadTool struct {
	resolver *ConfigFileResolver
}

func NewGenieConfigReadTool(cwd, configDir, agentName string) *GenieConfigReadTool {
	return &GenieConfigReadTool{
		resolver: &ConfigFileResolver{Cwd: cwd, ConfigDir: configDir, AgentName: agentName},
	}
}

func (t *GenieConfigReadTool) Name() string        { return "genie_config_read" }
func (t *GenieConfigReadTool) Description() string { return "Read a Genie config file (global, project, or agent-scoped)" }

func (t *GenieConfigReadTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"global", "project", "agent"},
				"description": "Which config file to read",
			},
			"agent_name": map[string]any{
				"type":        "string",
				"description": "Agent name (required for agent scope)",
			},
		},
		"required": []any{"scope"},
	}
}

func (t *GenieConfigReadTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Scope      string `json:"scope"`
		AgentName  string `json:"agent_name,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	scope := ConfigScope(params.Scope)
	if scope == ConfigScopeAgent {
		t.resolver.AgentName = params.AgentName
	}

	path, err := t.resolver.Resolve(scope)
	if err != nil {
		return "", err
	}

	data, err := ReadRawConfig(path)
	if err != nil {
		return "", err
	}

	out, _ := toml.Marshal(data)
	return string(out), nil
}

// GenieConfigWriteTool writes a complete Genie config file.
type GenieConfigWriteTool struct {
	resolver *ConfigFileResolver
}

func NewGenieConfigWriteTool(cwd, configDir, agentName string) *GenieConfigWriteTool {
	return &GenieConfigWriteTool{
		resolver: &ConfigFileResolver{Cwd: cwd, ConfigDir: configDir, AgentName: agentName},
	}
}

func (t *GenieConfigWriteTool) Name() string        { return "genie_config_write" }
func (t *GenieConfigWriteTool) Description() string { return "Write a complete Genie config file (global, project, or agent-scoped)" }

func (t *GenieConfigWriteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"global", "project", "agent"},
				"description": "Which config file to write",
			},
			"agent_name": map[string]any{
				"type":        "string",
				"description": "Agent name (required for agent scope)",
			},
			"config": map[string]any{
				"type":        "object",
				"description": "Complete config as TOML-compatible object",
			},
		},
		"required": []any{"scope", "config"},
	}
}

func (t *GenieConfigWriteTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Scope     string         `json:"scope"`
		AgentName string         `json:"agent_name,omitempty"`
		Config    map[string]any `json:"config"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	scope := ConfigScope(params.Scope)
	if scope == ConfigScopeAgent {
		t.resolver.AgentName = params.AgentName
	}

	path, err := t.resolver.Resolve(scope)
	if err != nil {
		return "", err
	}

	if err := WriteRawConfig(path, params.Config); err != nil {
		return "", err
	}

	return fmt.Sprintf("Config written to %s", path), nil
}

// GenieConfigPatchTool applies a JSON Patch to a Genie config file.
type GenieConfigPatchTool struct {
	resolver *ConfigFileResolver
}

func NewGenieConfigPatchTool(cwd, configDir, agentName string) *GenieConfigPatchTool {
	return &GenieConfigPatchTool{
		resolver: &ConfigFileResolver{Cwd: cwd, ConfigDir: configDir, AgentName: agentName},
	}
}

func (t *GenieConfigPatchTool) Name() string        { return "genie_config_patch" }
func (t *GenieConfigPatchTool) Description() string { return "Apply a JSON Patch (RFC 6902) to a Genie config file" }

func (t *GenieConfigPatchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"scope": map[string]any{
				"type":        "string",
				"enum":        []string{"global", "project", "agent"},
				"description": "Which config file to patch",
			},
			"agent_name": map[string]any{
				"type":        "string",
				"description": "Agent name (required for agent scope)",
			},
			"patch": map[string]any{
				"type":        "array",
				"description": "JSON Patch operations (RFC 6902)",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"op": map[string]any{"type": "string", "enum": []string{"add", "remove", "replace", "move", "copy", "test"}},
						"path": map[string]any{"type": "string"},
						"value": map[string]any{},
						"from": map[string]any{"type": "string"},
					},
					"required": []any{"op", "path"},
				},
			},
		},
		"required": []any{"scope", "patch"},
	}
}

func (t *GenieConfigPatchTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Scope     string           `json:"scope"`
		AgentName string           `json:"agent_name,omitempty"`
		Patch     []PatchOperation `json:"patch"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	scope := ConfigScope(params.Scope)
	if scope == ConfigScopeAgent {
		t.resolver.AgentName = params.AgentName
	}

	path, err := t.resolver.Resolve(scope)
	if err != nil {
		return "", err
	}

	// Read existing config
	data, err := ReadRawConfig(path)
	if err != nil {
		return "", err
	}

	// Apply patch
	if err := ApplyJSONPatch(data, params.Patch); err != nil {
		return "", err
	}

	// Write back
	if err := WriteRawConfig(path, data); err != nil {
		return "", err
	}

	return fmt.Sprintf("Config patched at %s", path), nil
}

// Ensure tools implement the tools.Tool interface
var _ tools.Tool = (*GenieConfigReadTool)(nil)
var _ tools.Tool = (*GenieConfigWriteTool)(nil)
var _ tools.Tool = (*GenieConfigPatchTool)(nil)