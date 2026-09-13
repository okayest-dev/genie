package main

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/llm"
)

// captureStartupClient is a fake wire client that records the parameters its
// factory was built with, so tests can prove the client came from the active
// provider's own spec and not a global default.
type captureStartupClient struct {
	baseURL string
	apiKey  string
	opts    map[string]any
}

func (c *captureStartupClient) Stream(_ context.Context, _ llm.Request) (iter.Seq[llm.Event], error) {
	return func(yield func(llm.Event) bool) {}, nil
}

func (c *captureStartupClient) ListModels(_ context.Context) ([]llm.Model, error) {
	return nil, nil
}

// registerStartupCaptureWire registers a wire whose builds record their
// (baseURL, apiKey, opts) parameters, one entry per built client. Names must
// be test-scoped: registration lands in the shared package-global registry.
func registerStartupCaptureWire(name string) *[]llm.Client {
	var built []llm.Client
	llm.RegisterWire(name, func(baseURL, apiKey string, opts map[string]any) llm.Client {
		c := &captureStartupClient{baseURL: baseURL, apiKey: apiKey, opts: opts}
		built = append(built, c)
		return c
	})
	return &built
}

// firstCapture returns the single recorded client, failing the test when the
// count is not exactly one.
func firstCapture(t *testing.T, built *[]llm.Client) *captureStartupClient {
	t.Helper()
	if len(*built) != 1 {
		t.Fatalf("built %d clients, want exactly 1", len(*built))
	}
	c, ok := (*built)[0].(*captureStartupClient)
	if !ok {
		t.Fatalf("built client has type %T, want *captureStartupClient", (*built)[0])
	}
	return c
}

// TestResolveStartupBuildsActiveProviderClient pins the core slice: startup
// resolves the provider named by the selection key, and the client plus the
// first model come from that provider's own base_url, api_key_env and default
// model — not from a global model or another provider.
func TestResolveStartupBuildsActiveProviderClient(t *testing.T) {
	built := registerStartupCaptureWire("startuptest-capture")
	r := llm.NewRegistry(map[string]llm.ProviderSpec{
		"zen": {
			Wire: "startuptest-capture", BaseURL: "https://zen.example/v1",
			APIKeyEnv: "ZEN_KEY", Model: "zen-model",
		},
		"other": {
			Wire: "startuptest-capture", BaseURL: "https://other.example/v1",
			APIKeyEnv: "OTHER_KEY", Model: "other-model",
		},
	})
	t.Setenv("ZEN_KEY", "key-zen")
	t.Setenv("OTHER_KEY", "key-other")

	client, model, err := resolveStartup(r, "other")
	if err != nil {
		t.Fatalf("resolveStartup: %v", err)
	}
	if model != "other-model" {
		t.Errorf("model = %q, want the active provider's default %q", model, "other-model")
	}
	if client == nil {
		t.Fatal("resolveStartup returned a nil client")
	}
	got := firstCapture(t, built)
	if got.baseURL != "https://other.example/v1" {
		t.Errorf("client baseURL = %q, want the active provider's", got.baseURL)
	}
	if got.apiKey != "key-other" {
		t.Errorf("client apiKey = %q, want the active provider's api_key_env value", got.apiKey)
	}
}

// TestResolveStartupErrorsAsTable: every startup-resolution failure must
// surface as a clean error naming its cause — an unset selection names the
// provider requirement, an unknown provider and an unregistered wire name
// themselves. There is no silent global fallback (og-z1m.3).
func TestResolveStartupErrorsAsTable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		provider   string
		specs      map[string]llm.ProviderSpec
		wantStderr string
	}{
		{
			name:       "unset provider names the requirement",
			provider:   "",
			specs:      map[string]llm.ProviderSpec{"zen": {Wire: "openai", Model: "big-pickle"}},
			wantStderr: "no active provider",
		},
		{
			name:       "unknown provider names itself",
			provider:   "nothing",
			specs:      map[string]llm.ProviderSpec{"zen": {Wire: "openai", Model: "big-pickle"}},
			wantStderr: "nothing",
		},
		{
			name:       "unregistered wire names itself",
			provider:   "p",
			specs:      map[string]llm.ProviderSpec{"p": {Wire: "no-such-wire", Model: "m"}},
			wantStderr: "no-such-wire",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := resolveStartup(llm.NewRegistry(tc.specs), tc.provider)
			if err == nil {
				t.Fatal("resolveStartup should error")
			}
			if !strings.Contains(err.Error(), tc.wantStderr) {
				t.Errorf("error should name %q, got %v", tc.wantStderr, err)
			}
		})
	}
}

// TestRegistryFromConfigResolvesDefaults is the integration seam: the parsed
// config tables (shipped defaults) bridge into the registry and boot the
// named provider on its own default model.
func TestRegistryFromConfigResolvesDefaults(t *testing.T) {
	cfg, err := config.Parse(nil, "/home/u", nil)
	if err != nil {
		t.Fatalf("config.Parse: %v", err)
	}
	r := registryFromConfig(cfg)
	client, model, err := resolveStartup(r, "zen")
	if err != nil {
		t.Fatalf("resolveStartup(zen): %v", err)
	}
	if model != "big-pickle" {
		t.Errorf("model = %q, want the shipped zen default %q", model, "big-pickle")
	}
	if client == nil {
		t.Fatal("resolveStartup returned a nil client")
	}
}
