package llm_test

import (
	"context"
	"iter"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/llm"
	_ "github.com/okayest-dev/genie/internal/llm/anthropic"
	_ "github.com/okayest-dev/genie/internal/llm/bedrock"
	_ "github.com/okayest-dev/genie/internal/llm/copilot"
	_ "github.com/okayest-dev/genie/internal/llm/google"
	_ "github.com/okayest-dev/genie/internal/llm/openai"
	_ "github.com/okayest-dev/genie/internal/llm/responses"
)

// captureClient is a fake wire client that records the config its factory was
// parameterised with and serves a fixed catalog.
type captureClient struct {
	baseURL string
	apiKey  string
	opts    map[string]any
	models  []llm.Model
}

func (c *captureClient) Stream(_ context.Context, _ llm.Request) (iter.Seq[llm.Event], error) {
	return func(yield func(llm.Event) bool) {}, nil
}

func (c *captureClient) ListModels(_ context.Context) ([]llm.Model, error) {
	return c.models, nil
}

// registerCaptureWire registers a wire named name whose builds record their
// (baseURL, apiKey, opts) parameters, one entry per built client. Names must
// be unique per test and clearly test-scoped: registration lands in the
// package-global wire registry shared with production lookups.
func registerCaptureWire(name string, models []llm.Model) *[]llm.Client {
	var built []llm.Client
	llm.RegisterWire(name, func(baseURL, apiKey string, opts map[string]any) llm.Client {
		c := &captureClient{baseURL: baseURL, apiKey: apiKey, opts: opts, models: models}
		built = append(built, c)
		return c
	})
	return &built
}

// specsFromConfig bridges the parsed config tables into registry specs, the
// same shape main.go's startup will use.
func specsFromConfig(cfg *config.Config) map[string]llm.ProviderSpec {
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
	return specs
}

// sortedConfigProviderNames is the expected Names() output for a config's
// provider map.
func sortedConfigProviderNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func TestRegistryClientForEveryBundledWire(t *testing.T) {
	cfg, err := config.Parse(nil, "/home/u", nil)
	if err != nil {
		t.Fatalf("config.Parse: %v", err)
	}
	r := llm.NewRegistry(specsFromConfig(cfg))

	type tv struct{ name, wire string }
	var tests []tv
	for name, p := range cfg.Providers {
		tests = append(tests, tv{name, p.Wire})
	}
	for _, tc := range tests {
		t.Run(tc.name+"/"+tc.wire, func(t *testing.T) {
			c, err := r.Client(tc.name)
			if err != nil {
				t.Fatalf("Client(%q): %v", tc.name, err)
			}
			if c == nil {
				t.Fatalf("Client(%q) returned nil client", tc.name)
			}
		})
	}

	wires := map[string]bool{}
	for _, tc := range tests {
		wires[tc.wire] = true
	}
	for _, wire := range []string{"openai", "anthropic", "responses", "google", "copilot", "bedrock"} {
		if !wires[wire] {
			t.Errorf("no declared provider on bundled wire %q", wire)
		}
	}

	// The registry was seeded from the parsed config tables: its default model
	// and name listing must match what config resolved, so a client is proven
	// to come from that provider's own config, not a global default.
	for name, p := range cfg.Providers {
		if got, err := r.DefaultModel(name); err != nil || got != p.Model {
			t.Errorf("DefaultModel(%q) = %q, %v; want %q from config", name, got, err, p.Model)
		}
	}
	if got, want := r.Names(), sortedConfigProviderNames(cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("Names = %v, want config's sorted providers %v", got, want)
	}
}

func TestRegistryClientParameterisedPerProvider(t *testing.T) {
	built := registerCaptureWire("registrytest-capture", nil)
	r := llm.NewRegistry(map[string]llm.ProviderSpec{
		"one": {
			Wire: "registrytest-capture", BaseURL: "https://one.example/v1",
			APIKeyEnv: "REG_KEY_ONE", Model: "m1", Opts: map[string]any{"a": 1},
		},
		"two": {
			Wire: "registrytest-capture", BaseURL: "https://two.example/v1",
			APIKeyEnv: "REG_KEY_TWO", Model: "m2", Opts: map[string]any{"b": 2},
		},
	})
	t.Setenv("REG_KEY_ONE", "key-one")
	t.Setenv("REG_KEY_TWO", "key-two")

	for _, name := range []string{"one", "two"} {
		if _, err := r.Client(name); err != nil {
			t.Fatalf("Client(%q): %v", name, err)
		}
	}
	if len(*built) != 2 {
		t.Fatalf("built %d clients, want 2", len(*built))
	}

	want := []struct {
		baseURL, apiKey string
	}{
		{"https://one.example/v1", "key-one"},
		{"https://two.example/v1", "key-two"},
	}
	for i, w := range want {
		c := (*built)[i].(*captureClient)
		if c.baseURL != w.baseURL {
			t.Errorf("client %d baseURL = %q, want %q", i, c.baseURL, w.baseURL)
		}
		if c.apiKey != w.apiKey {
			t.Errorf("client %d apiKey = %q, want %q", i, c.apiKey, w.apiKey)
		}
	}
}

func TestRegistryClientUnknownProvider(t *testing.T) {
	r := llm.NewRegistry(map[string]llm.ProviderSpec{
		"zen": {Wire: "openai", Model: "big-pickle"},
	})
	_, err := r.Client("nothing")
	if err == nil {
		t.Fatal("Client(unknown) should error")
	}
	if !strings.Contains(err.Error(), "nothing") {
		t.Errorf("error should name the provider, got %v", err)
	}
}

func TestRegistryClientUnregisteredWire(t *testing.T) {
	r := llm.NewRegistry(map[string]llm.ProviderSpec{
		"p": {Wire: "no-such-wire", Model: "m"},
	})
	_, err := r.Client("p")
	if err == nil {
		t.Fatal("Client with unregistered wire should error")
	}
	if !strings.Contains(err.Error(), "no-such-wire") {
		t.Errorf("error should name the wire, got %v", err)
	}
}

func TestRegistryCatalogUsesDeclaredModels(t *testing.T) {
	r := llm.NewRegistry(map[string]llm.ProviderSpec{
		"p": {Wire: "openai", Model: "default", Models: []string{"a", "b", "c"}},
	})
	models, err := r.Catalog(context.Background(), "p")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	var got []string
	for _, m := range models {
		got = append(got, m.ID)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Catalog = %v, want %v", got, want)
	}
}

func TestRegistryCatalogFallsBackToWireListing(t *testing.T) {
	registerCaptureWire("registrytest-listing", []llm.Model{{ID: "w1"}, {ID: "w2"}})
	r := llm.NewRegistry(map[string]llm.ProviderSpec{
		"p": {Wire: "registrytest-listing", Model: "m"},
	})
	models, err := r.Catalog(context.Background(), "p")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	var got []string
	for _, m := range models {
		got = append(got, m.ID)
	}
	if want := []string{"w1", "w2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Catalog = %v, want %v", got, want)
	}
}

func TestRegistryCatalogUnknownProvider(t *testing.T) {
	r := llm.NewRegistry(map[string]llm.ProviderSpec{})
	if _, err := r.Catalog(context.Background(), "nothing"); err == nil {
		t.Fatal("Catalog(unknown) should error")
	}
}

func TestRegistryCatalogUnregisteredWire(t *testing.T) {
	r := llm.NewRegistry(map[string]llm.ProviderSpec{
		"p": {Wire: "no-such-wire", Model: "m"},
	})
	if _, err := r.Catalog(context.Background(), "p"); err == nil {
		t.Fatal("Catalog with unregistered wire should error")
	}
}

func TestRegistryNames(t *testing.T) {
	r := llm.NewRegistry(map[string]llm.ProviderSpec{
		"zen": {Wire: "openai", Model: "m"},
		"abc": {Wire: "openai", Model: "m"},
		"mid": {Wire: "openai", Model: "m"},
	})
	if got, want := r.Names(), []string{"abc", "mid", "zen"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Names = %v, want %v", got, want)
	}
}

func TestRegistryDefaultModel(t *testing.T) {
	r := llm.NewRegistry(map[string]llm.ProviderSpec{
		"p": {Wire: "openai", Model: "mm"},
	})
	m, err := r.DefaultModel("p")
	if err != nil {
		t.Fatalf("DefaultModel: %v", err)
	}
	if m != "mm" {
		t.Errorf("DefaultModel = %q, want %q", m, "mm")
	}
	if _, err := r.DefaultModel("nothing"); err == nil {
		t.Fatal("DefaultModel(unknown) should error")
	}
}
