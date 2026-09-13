package llm

import "fmt"

// Factory creates a Client from a provider's baseURL, apiKey and opts. The
// opts carry wire-specific settings per provider; a wire that takes none
// ignores them.
type Factory func(baseURL, apiKey string, opts map[string]any) Client

var registry = map[Wire]Factory{}

// RegisterWire registers a wire factory. Call from wire subpackage init.
func RegisterWire(name Wire, f Factory) {
	registry[name] = f
	ValidWires[name] = true
}

// ValidateWire checks that a wire name is registered. Returns an error
// for unregistered wires.
func ValidateWire(wire Wire) error {
	if wire == "" {
		return nil // empty means auto-detect, always valid
	}
	if !ValidWires[wire] {
		return fmt.Errorf("llm: wire %q not registered", wire)
	}
	return nil
}

// NewClient creates an llm.Client for the given wire. An empty wire
// falls back to openai. Unknown wires return an error.
func NewClient(wire Wire, baseURL, apiKey string) (Client, error) {
	if wire == "" {
		wire = WireOpenAI
	}
	f, ok := registry[wire]
	if !ok {
		return nil, fmt.Errorf("llm: wire %q not registered", wire)
	}
	return f(baseURL, apiKey, nil), nil
}
