package llm

import (
	"context"
	"fmt"
	"os"
	"sort"
)

// ProviderSpec is one declared provider in the shape the registry builds from.
// It mirrors the [providers.*] config tables but is deliberately free of an
// internal/config import: the config package sits downstream of llm (via
// modelinfo), so the registry cannot reference its types without an import
// cycle. Seeding is a mechanical bridge over the parsed tables.
type ProviderSpec struct {
	// Wire names the bundled in-process wire that serves the provider.
	Wire string
	// BaseURL is the provider's endpoint for that wire.
	BaseURL string
	// APIKeyEnv names the env var the provider's API key lives in. Empty
	// means the wire authenticates some other way and builds unauthenticated.
	APIKeyEnv string
	// Model is the provider's default model.
	Model string
	// Models optionally overrides the provider's catalog. Empty means the
	// catalog comes from the wire's model listing.
	Models []string
	// Opts passes wire-specific settings.
	Opts map[string]any
}

// Registry is the module that owns provider to client. Seeded once from the
// parsed [providers.*] tables, it builds a client for any declared provider
// name and resolves that provider's model catalog. Each client is
// parameterised from its own provider's base_url, api_key_env and opts — there
// are no global defaults to fall back on.
type Registry struct {
	providers map[string]ProviderSpec
}

// NewRegistry seeds a Registry with the declared providers, keyed by name.
func NewRegistry(providers map[string]ProviderSpec) *Registry {
	return &Registry{providers: providers}
}

// Client builds the client for a declared provider. The wire factory is
// parameterised from the provider's own base_url, its api_key_env (resolved
// from the environment at build time) and its opts. An unknown provider name
// or a provider naming an unregistered wire is an error.
func (r *Registry) Client(name string) (Client, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("registry: no such provider %q", name)
	}
	f, ok := registry[p.Wire]
	if !ok {
		return nil, fmt.Errorf("registry: provider %q names unregistered wire %q", name, p.Wire)
	}
	return f(p.BaseURL, os.Getenv(p.APIKeyEnv), p.Opts), nil
}

// Catalog returns the provider's model catalog. A provider with a declared
// Models list returns exactly that, in order; without one, the catalog comes
// from the wire's model listing.
func (r *Registry) Catalog(ctx context.Context, name string) ([]Model, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("registry: no such provider %q", name)
	}
	if len(p.Models) > 0 {
		out := make([]Model, 0, len(p.Models))
		for _, id := range p.Models {
			out = append(out, Model{ID: id})
		}
		return out, nil
	}
	c, err := r.Client(name)
	if err != nil {
		return nil, fmt.Errorf("registry: provider %q: %w", name, err)
	}
	return c.ListModels(ctx)
}

// DefaultModel returns the provider's default model. An unknown provider name
// is an error.
func (r *Registry) DefaultModel(name string) (string, error) {
	p, ok := r.providers[name]
	if !ok {
		return "", fmt.Errorf("registry: no such provider %q", name)
	}
	return p.Model, nil
}

// Names returns the declared provider names in sorted order, so callers get a
// deterministic listing.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
