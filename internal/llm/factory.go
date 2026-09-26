package llm

// Factory creates a Client from a provider's baseURL, apiKey and opts. The
// opts carry wire-specific settings per provider; a wire that takes none
// ignores them.
type Factory func(baseURL, apiKey string, opts map[string]any) Client

// wireFactories maps a wire name to its factory. [Registry] is the only
// client-builder: it looks a provider's wire up here and parameterises the
// factory from that provider's own base_url, api_key_env and opts.
var wireFactories = map[Wire]Factory{}

// RegisterWire registers a wire factory. Call from wire subpackage init.
func RegisterWire(name Wire, f Factory) {
	wireFactories[name] = f
}
