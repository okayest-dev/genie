// Package tools provides the tool registry, JSON Schema validation, and
// the disabled-tool error surface. Each tool implements the Tool interface;
// the Registry maps names to tools and produces the ToolDefs the client
// sends to the provider.
package tools

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/okayest-dev/og/internal/llm"
)

// Tool is the interface every tool must implement.
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any // JSON Schema object
	Execute(args json.RawMessage) (string, error)
}

// Registry maps tool names to Tool implementations.
type Registry struct {
	tools    []Tool
	index    map[string]Tool
	disabled map[string]bool
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		index:    make(map[string]Tool),
		disabled: make(map[string]bool),
	}
}

// Register adds a tool. Later registrations of the same name overwrite earlier ones.
func (r *Registry) Register(t Tool) {
	name := t.Name()
	r.index[name] = t
	// Preserve insertion order; replace if already present.
	for i, existing := range r.tools {
		if existing.Name() == name {
			r.tools[i] = t
			return
		}
	}
	r.tools = append(r.tools, t)
}

// Disable marks a tool as unavailable. A disabled tool's Execute returns a
// DisabledError and its Get returns false.
func (r *Registry) Disable(name string) {
	r.disabled[name] = true
}

// Get returns the tool by name, or false if not found or disabled.
func (r *Registry) Get(name string) (Tool, bool) {
	if r.disabled[name] {
		return nil, false
	}
	t, ok := r.index[name]
	return t, ok
}

// IsDisabled reports whether the named tool is registered but disabled.
func (r *Registry) IsDisabled(name string) bool {
	return r.disabled[name] && r.index[name] != nil
}

// Execute runs the named tool with the given JSON arguments. It returns a
// DisabledError for disabled tools and an error for missing tools.
func (r *Registry) Execute(name string, args json.RawMessage) (string, error) {
	if r.disabled[name] {
		return "", DisabledError(name)
	}
	t, ok := r.index[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return t.Execute(args)
}

// ToolDefs returns the tool definitions in registration order, suitable
// for sending to the provider.
func (r *Registry) ToolDefs() []llm.ToolDef {
	defs := make([]llm.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		if r.disabled[t.Name()] {
			continue
		}
		defs = append(defs, llm.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return defs
}

// ValidateArgs checks that args is valid JSON and satisfies the given JSON
// Schema (basic: checks required fields and types). It returns a human-readable
// error on failure.
func ValidateArgs(args json.RawMessage, schema map[string]any) error {
	var parsed any
	if err := json.Unmarshal(args, &parsed); err != nil {
		return fmt.Errorf("malformed JSON arguments: %v", err)
	}

	obj, ok := parsed.(map[string]any)
	if !ok {
		return fmt.Errorf("arguments must be a JSON object, got %T", parsed)
	}

	// Check required fields.
	if req, ok := schema["required"]; ok {
		if reqList, ok := req.([]any); ok {
			for _, field := range reqList {
				name, ok := field.(string)
				if !ok {
					continue
				}
				if _, found := obj[name]; !found {
					return fmt.Errorf("missing required argument: %s", name)
				}
			}
		}
	}

	// Check types of provided fields against properties.
	if props, ok := schema["properties"].(map[string]any); ok {
		for key, val := range obj {
			prop, ok := props[key].(map[string]any)
			if !ok {
				continue
			}
			wantType, ok := prop["type"].(string)
			if !ok {
				continue
			}
			if !typeMatches(val, wantType) {
				return fmt.Errorf("argument %q: expected %s, got %T", key, wantType, val)
			}
		}
	}

	return nil
}

// typeMatches checks if a JSON-decoded value matches the expected JSON Schema type.
func typeMatches(val any, want string) bool {
	switch want {
	case "string":
		_, ok := val.(string)
		return ok
	case "number":
		switch val.(type) {
		case float64, json.Number:
			return true
		}
		return false
	case "integer":
		if f, ok := val.(float64); ok {
			return f == float64(int(f))
		}
		return false
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "array":
		_, ok := val.([]any)
		return ok
	case "object":
		_, ok := val.(map[string]any)
		return ok
	case "null":
		return val == nil
	default:
		return true // unknown type, don't reject
	}
}

// DisabledError is the error returned when a tool call targets a disabled tool.
type DisabledError string

func (e DisabledError) Error() string {
	return fmt.Sprintf("tool '%s' is disabled", string(e))
}

// IsDisabled reports whether err is a DisabledError.
func IsDisabled(err error) bool {
	var de DisabledError
	if errors.As(err, &de) {
		return true
	}
	return false
}
