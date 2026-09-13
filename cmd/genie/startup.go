package main

import (
	"fmt"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/llm"
)

// registryFromConfig bridges the parsed [providers.*] config tables into the
// llm registry's spec shape. It lives here rather than in internal/llm
// because of the package layering: config imports modelinfo, which imports
// llm, so llm cannot reference config types without an import cycle.
func registryFromConfig(cfg *config.Config) *llm.Registry {
	specs := make(map[string]llm.ProviderSpec, len(cfg.Providers))
	for name, p := range cfg.Providers {
		specs[name] = llm.ProviderSpec{
			Wire:      p.Wire,
			BaseURL:   p.BaseURL,
			APIKeyEnv: p.APIKeyEnv,
			Model:     p.Model,
			Models:    p.Models,
			Opts:      p.Opts,
		}
	}
	return llm.NewRegistry(specs)
}

// resolveStartup resolves what the harness boots on: the provider named by
// the selection key, its client built through the registry, and its default
// model as the first model. An unset selection or an unknown provider is a
// startup error that names the requirement — there is no global model to fall
// back on, so the "runModel mismatch" bug cannot recur.
func resolveStartup(reg *llm.Registry, provider string) (llm.Client, string, error) {
	if provider == "" {
		return nil, "", fmt.Errorf("no active provider: set provider = \"<name>\" in config or GENIE_PROVIDER")
	}
	model, err := reg.DefaultModel(provider)
	if err != nil {
		return nil, "", fmt.Errorf("active provider: %w", err)
	}
	client, err := reg.Client(provider)
	if err != nil {
		return nil, "", fmt.Errorf("active provider: %w", err)
	}
	return client, model, nil
}
