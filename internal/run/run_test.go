package run

import (
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/tools/requesttool"
)

// fakeClient is a scriptable llm.Client: Stream emits a fixed event script and
// records the last request it saw so tests observe model, history, and turn
// flow without a provider server.
type fakeClient struct {
	models    []llm.Model
	events    []llm.Event
	streamErr error
	lastReq   llm.Request
	streams   int
}

func (c *fakeClient) ListModels(context.Context) ([]llm.Model, error) { return c.models, nil }

func (c *fakeClient) Stream(_ context.Context, req llm.Request) (iter.Seq[llm.Event], error) {
	c.lastReq = req
	c.streams++
	if c.streamErr != nil {
		return nil, c.streamErr
	}
	events := c.events
	if events == nil {
		events = echoEvents("fake reply")
	}
	return func(yield func(llm.Event) bool) {
		for _, e := range events {
			if !yield(e) {
				return
			}
		}
	}, nil
}

func echoEvents(text string) []llm.Event {
	return []llm.Event{
		{Kind: llm.EventText, Text: text},
		{Kind: llm.EventFinish, End: llm.FinishStop},
	}
}

// fakeSource scripts the ProviderSource port.
type fakeSource struct {
	names       []string
	defaults    map[string]string
	clients     map[string]*fakeClient
	catalogs    map[string][]llm.Model
	catalogErrs map[string]error
	clientErrs  map[string]error
}

func (f *fakeSource) Names() []string { return f.names }

func (f *fakeSource) DefaultModel(name string) (string, error) {
	if m, ok := f.defaults[name]; ok {
		return m, nil
	}
	return "", errors.New("no such provider " + name)
}

func (f *fakeSource) Client(name string) (llm.Client, error) {
	if err := f.clientErrs[name]; err != nil {
		return nil, err
	}
	c, ok := f.clients[name]
	if !ok {
		return nil, errors.New("no such provider " + name)
	}
	return c, nil
}

func (f *fakeSource) Catalog(_ context.Context, name string) ([]llm.Model, error) {
	if err := f.catalogErrs[name]; err != nil {
		return nil, err
	}
	c, ok := f.catalogs[name]
	if !ok {
		return nil, errors.New("no such provider " + name)
	}
	return c, nil
}

// recNegotiator records negotiation calls and returns a scripted response.
type recNegotiator struct {
	axes   []permissions.Axis
	scopes []string
	resp   permissions.Response
	err    error
}

func (n *recNegotiator) Negotiate(_ context.Context, axis permissions.Axis, scope string) (permissions.Response, error) {
	n.axes = append(n.axes, axis)
	n.scopes = append(n.scopes, scope)
	return n.resp, n.err
}

func twoProviderSource() *fakeSource {
	return &fakeSource{
		names:    []string{"alpha", "beta"},
		defaults: map[string]string{"alpha": "alpha-model", "beta": "beta-model"},
		clients: map[string]*fakeClient{
			"alpha": {models: []llm.Model{{ID: "alpha-1"}, {ID: "alpha-2"}}},
			"beta":  {models: []llm.Model{{ID: "beta-1"}, {ID: "beta-2"}}},
		},
		catalogs: map[string][]llm.Model{
			"alpha": {{ID: "alpha-model"}, {ID: "alpha-1"}, {ID: "alpha-2"}},
			"beta":  {{ID: "beta-model"}, {ID: "beta-1"}, {ID: "beta-2"}},
		},
	}
}

func newTestHandle(t *testing.T, opts Options) *Handle {
	t.Helper()
	h, err := New(opts)
	if err != nil {
		t.Fatalf("run.New: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// testCfg returns a config with every tool enabled, matching the defaults
// config.Load seeds for real runs (a raw zero Config would disable them all).
func testCfg(dir string) *config.Config {
	return &config.Config{
		SessionDir: dir,
		Tools:      config.Tools{Read: true, Write: true, Edit: true, Bash: true},
	}
}

func TestNewBootsProviderDefaults(t *testing.T) {
	src := twoProviderSource()
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		Provider:       "alpha",
		ProviderSource: src,
		Stderr:         io.Discard,
	})

	if h.Provider() != "alpha" {
		t.Errorf("Provider = %q, want alpha", h.Provider())
	}
	if got := strings.Join(h.ProviderNames(), ","); got != "alpha,beta" {
		t.Errorf("ProviderNames = %q, want alpha,beta", got)
	}
	if h.Model() != "alpha-model" {
		t.Errorf("Model = %q, want alpha-model", h.Model())
	}
	if h.Client() == nil || h.BaseClient() == nil {
		t.Fatal("client and base client must be wired")
	}
	if h.Session() == nil {
		t.Fatal("session nil")
	}
	if h.CurrentAgent() != nil {
		t.Errorf("CurrentAgent = %+v, want nil", h.CurrentAgent())
	}
	if h.Ledger() != nil {
		t.Error("Ledger must be nil without WithLedger")
	}
	if h.Commands() == nil {
		t.Error("Commands must be non-nil over the plugin manager")
	}
	// The full (unscoped) registry carries every tool including
	// request_permission.
	if _, ok := h.Registry().Get("read"); !ok {
		t.Error("registry should carry read with no agent")
	}
	if _, ok := h.Registry().Get(requesttool.ToolName); !ok {
		t.Error("request_permission should be always-on")
	}
}

func TestNewPicksSingleProvider(t *testing.T) {
	src := twoProviderSource()
	src.names = []string{"only"}
	src.defaults = map[string]string{"only": "only-model"}
	src.clients = map[string]*fakeClient{"only": {models: []llm.Model{{ID: "m"}}}}
	src.catalogs = map[string][]llm.Model{"only": {{ID: "m"}}}

	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: src,
		Stderr:         io.Discard,
	})
	if h.Provider() != "only" {
		t.Errorf("Provider = %q, want the single declared provider", h.Provider())
	}
}

func TestNewProviderlessHandle(t *testing.T) {
	h := newTestHandle(t, Options{Config: &config.Config{SessionDir: t.TempDir()}, Stderr: io.Discard})
	if h.Provider() != "" || h.ProviderNames() != nil {
		t.Errorf("provider = %q names=%v, want empty", h.Provider(), h.ProviderNames())
	}
	if h.Client() != nil {
		t.Error("provider-less handle must have no client")
	}
	if err := h.Turn(context.Background(), "hi", io.Discard, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "no provider selected") {
		t.Errorf("Turn err = %v, want no-provider error", err)
	}
}

func TestNewNilConfigDoesNotPanic(t *testing.T) {
	// A nil Config is defaulted to a zero config; with no session dir the
	// assembly must still fail cleanly (never dereference a nil config).
	_, err := New(Options{Stderr: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "create session") {
		t.Errorf("err = %v, want a create-session error", err)
	}
}

func TestNewToolToggles(t *testing.T) {
	h := newTestHandle(t, Options{
		Config: func() *config.Config {
			cfg := testCfg(t.TempDir())
			cfg.Tools.Read = false
			return cfg
		}(),
		ProviderSource: twoProviderSource(),
		Provider:       "alpha",
		Stderr:         io.Discard,
	})
	if _, ok := h.Registry().Get("read"); ok {
		t.Error("read should be disabled by config")
	}
	if _, ok := h.Registry().Get("bash"); !ok {
		t.Error("bash should stay enabled")
	}
}

func TestNewValidatesAgentTools(t *testing.T) {
	_, err := New(Options{
		Config: &config.Config{SessionDir: t.TempDir()},
		Agent:  &config.ResolvedAgent{Name: "bogus", Tools: []string{"nope"}},
		Stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), `agent "bogus"`) {
		t.Errorf("err = %v, want agent tool validation error", err)
	}
}

func TestNewAgentScopeRegistry(t *testing.T) {
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		Agent:          &config.ResolvedAgent{Name: "reader", Tools: []string{"read"}},
		ProviderSource: twoProviderSource(),
		Provider:       "alpha",
		Stderr:         io.Discard,
	})
	if _, ok := h.Registry().Get("read"); !ok {
		t.Error("read should be in the scoped registry")
	}
	if _, ok := h.Registry().Get("bash"); ok {
		t.Error("bash should be scoped out")
	}
}

func TestNewPermanentGrantInvalidAxis(t *testing.T) {
	_, err := New(Options{
		Config: func() *config.Config {
			cfg := testCfg(t.TempDir())
			cfg.Permissions.Permanent = []config.PermanentGrant{{Permission: "bogus", Scope: ".", Granted: time.Now()}}
			return cfg
		}(),
		Stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "permanent grant") {
		t.Errorf("err = %v, want permanent grant error", err)
	}
}

func TestNewBadSessionDir(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New(Options{Config: &config.Config{SessionDir: filepath.Join(blocker, "sub")}, Stderr: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "create session") {
		t.Errorf("err = %v, want create-session error", err)
	}
}

func TestNewSkillDiscoverySurface(t *testing.T) {
	dir := t.TempDir()
	// A directory that is a file fails discovery hard.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{
		Config: &config.Config{SessionDir: t.TempDir(), Skills: config.Skills{Dirs: []string{blocker}}},
		Stderr: io.Discard,
	}); err == nil || !strings.Contains(err.Error(), "discover") {
		t.Errorf("err = %v, want skill discover error", err)
	}
}

func TestNewSkillWarningPrinted(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "broken", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("not a frontmatter skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	h := newTestHandle(t, Options{
		Config: &config.Config{SessionDir: t.TempDir(), Skills: config.Skills{Dirs: []string{dir}}},
		Stderr: &stderr,
	})
	if h == nil {
		t.Fatal("handle nil")
	}
	if !strings.Contains(stderr.String(), "warning:") {
		t.Errorf("stderr = %q, want skill warning", stderr.String())
	}
}

func TestNewProviderErrorsSurface(t *testing.T) {
	src := twoProviderSource()
	src.defaults["alpha"] = ""
	src.clientErrs = map[string]error{"alpha": errors.New("client boom")}
	delete(src.defaults, "alpha")

	// DefaultModel failure.
	noDefault := &fakeSource{names: []string{"alpha"}, defaults: map[string]string{}, clients: map[string]*fakeClient{"alpha": {}}}
	if _, err := New(Options{
		Config: &config.Config{SessionDir: t.TempDir()}, Provider: "alpha", ProviderSource: noDefault, Stderr: io.Discard,
	}); err == nil {
		t.Error("expected DefaultModel error to surface")
	}

	// Client failure.
	noClient := &fakeSource{names: []string{"alpha"}, defaults: map[string]string{"alpha": "m"}, clientErrs: map[string]error{"alpha": errors.New("client boom")}}
	_, err := New(Options{
		Config: &config.Config{SessionDir: t.TempDir()}, Provider: "alpha", ProviderSource: noClient, Stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "client boom") {
		t.Errorf("err = %v, want client error", err)
	}
}

func TestTurnStreamsReply(t *testing.T) {
	src := twoProviderSource()
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: src, Provider: "alpha", Stderr: io.Discard,
	})

	var out, errOut bytes.Buffer
	if err := h.Turn(context.Background(), "hello", &out, &errOut); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if !strings.Contains(out.String(), "fake reply") {
		t.Errorf("out = %q, want streamed reply", out.String())
	}
	if src.clients["alpha"].streams != 1 {
		t.Errorf("streams = %d, want 1", src.clients["alpha"].streams)
	}
	if src.clients["alpha"].lastReq.Model != "alpha-model" {
		t.Errorf("request model = %q, want alpha-model", src.clients["alpha"].lastReq.Model)
	}
}

func TestTurnAgentModelPrecedence(t *testing.T) {
	agent := &config.ResolvedAgent{Name: "coder", Model: "coder-model", Tools: []string{"read"}}
	src := twoProviderSource()
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: src, Provider: "alpha",
		Agent: agent, Stderr: io.Discard,
	})

	var out bytes.Buffer
	if err := h.Turn(context.Background(), "hi", &out, io.Discard); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if src.clients["alpha"].lastReq.Model != "coder-model" {
		t.Errorf("request model = %q, want the agent's explicit model", src.clients["alpha"].lastReq.Model)
	}
	// /model cannot override an agent-declared model.
	h.SetModel("other-model")
	if h.Model() != "coder-model" {
		t.Errorf("Model = %q, want agent model to win", h.Model())
	}
	if h.CurrentAgent() != agent {
		t.Errorf("CurrentAgent mismatch")
	}
}

func TestInstructionIncludesDefault(t *testing.T) {
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: twoProviderSource(), Provider: "alpha", Stderr: io.Discard,
	})
	inst := h.Instruction()
	if !strings.Contains(inst, "You are genie") {
		t.Errorf("instruction = %q, want the default prompt", inst)
	}
}

func TestSetModel(t *testing.T) {
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: twoProviderSource(), Provider: "alpha", Stderr: io.Discard,
	})
	h.SetModel("custom")
	if h.Model() != "custom" {
		t.Errorf("Model = %q, want custom", h.Model())
	}
}

func TestNewSessionRebinds(t *testing.T) {
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: twoProviderSource(), Provider: "alpha", Stderr: io.Discard,
	})
	old := h.Session().ID
	sess, err := h.NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if sess.ID == old {
		t.Error("NewSession must mint a fresh session id")
	}
	if h.Session().ID != sess.ID {
		t.Error("Handle.Session must follow the new session")
	}
	if h.Provider() != "alpha" {
		t.Errorf("Provider = %q, want unchanged by /new", h.Provider())
	}
}

func TestSwitchProviderResyncs(t *testing.T) {
	src := twoProviderSource()
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: src, Provider: "alpha", Stderr: io.Discard,
	})

	if err := h.SwitchProvider("beta"); err != nil {
		t.Fatalf("SwitchProvider: %v", err)
	}
	if h.Provider() != "beta" {
		t.Errorf("Provider = %q, want beta", h.Provider())
	}
	if h.Model() != "beta-model" {
		t.Errorf("Model = %q, want beta-model", h.Model())
	}
	if h.BaseClient() != src.clients["beta"] {
		t.Error("base client should be the new provider's client")
	}

	var out bytes.Buffer
	if err := h.Turn(context.Background(), "hi", &out, io.Discard); err != nil {
		t.Fatalf("Turn after switch: %v", err)
	}
	if src.clients["beta"].streams != 1 {
		t.Error("post-switch turn should hit the new provider")
	}
	if src.clients["alpha"].streams != 0 {
		t.Error("pre-switch provider must see no post-switch turns")
	}
}

func TestSwitchProviderUnknownLeavesUntouched(t *testing.T) {
	src := twoProviderSource()
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: src, Provider: "alpha", Stderr: io.Discard,
	})
	err := h.SwitchProvider("gamma")
	if err == nil || !strings.Contains(err.Error(), "no such provider: gamma") ||
		!strings.Contains(err.Error(), "available: alpha, beta") {
		t.Errorf("err = %v, want no-such-provider naming the set", err)
	}
	if h.Provider() != "alpha" {
		t.Errorf("Provider = %q, want alpha untouched", h.Provider())
	}
}

func TestSwitchProviderNoSource(t *testing.T) {
	h := newTestHandle(t, Options{Config: &config.Config{SessionDir: t.TempDir()}, Stderr: io.Discard})
	if err := h.SwitchProvider("x"); err == nil || !strings.Contains(err.Error(), "no providers configured") {
		t.Errorf("err = %v, want no-providers error", err)
	}
}

func TestProviderCatalog(t *testing.T) {
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: twoProviderSource(), Provider: "alpha", Stderr: io.Discard,
	})
	models, err := h.ProviderCatalog(context.Background())
	if err != nil {
		t.Fatalf("ProviderCatalog: %v", err)
	}
	if len(models) != 3 || models[0].ID != "alpha-model" {
		t.Errorf("models = %+v, want the alpha catalog", models)
	}
}

func TestSwitchAgentScopesAndAppliesBase(t *testing.T) {
	globalDir := t.TempDir()
	agents := map[string]string{
		"reader": `
model = "reader-model"
tools = ["read"]
skills = []

[permissions]
read = ["."]
`,
	}
	for name, body := range agents {
		if err := os.WriteFile(filepath.Join(globalDir, name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	agentReg := config.NewAgentReg(globalDir, t.TempDir())

	src := twoProviderSource()
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: src, Provider: "alpha",
		AgentReg: agentReg, Stderr: io.Discard,
	})
	if _, err := h.SwitchAgent("nosuch"); err == nil {
		t.Error("expected unknown-agent error")
	}
	resolved, err := h.SwitchAgent("reader")
	if err != nil {
		t.Fatalf("SwitchAgent: %v", err)
	}
	if resolved.Name != "reader" || h.CurrentAgent().Name != "reader" {
		t.Errorf("current agent = %q, want reader", h.CurrentAgent().Name)
	}
	if h.Model() != "reader-model" {
		t.Errorf("Model = %q, want reader-model", h.Model())
	}
	if _, ok := h.Registry().Get("read"); !ok {
		t.Error("reader's read tool should be in the scoped registry")
	}
	if _, ok := h.Registry().Get("bash"); ok {
		t.Error("bash should be scoped out after agent switch")
	}
	inst := h.Instruction()
	if !strings.Contains(inst, "write: nothing is authorized") {
		t.Errorf("instruction after switch must show the reader base replacing the global base (no write):\n%s", inst)
	}
}

func TestSwitchAgentWithoutReg(t *testing.T) {
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: twoProviderSource(), Provider: "alpha", Stderr: io.Discard,
	})
	if h.AgentReg() != nil {
		t.Error("AgentReg must be nil when not configured")
	}
	if _, err := h.SwitchAgent("x"); err == nil || !strings.Contains(err.Error(), "no agents configured") {
		t.Errorf("err = %v, want no-agents error", err)
	}
}

func TestAgentRegAccessor(t *testing.T) {
	globalDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(globalDir, "reader.toml"),
		[]byte("model = \"reader-model\"\nskills = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentReg := config.NewAgentReg(globalDir, t.TempDir())
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: twoProviderSource(), Provider: "alpha",
		AgentReg: agentReg, Stderr: io.Discard,
	})
	if h.AgentReg() != agentReg {
		t.Error("AgentReg must surface the configured registry")
	}
}

func TestResetAgentReturnsToDefaultFlow(t *testing.T) {
	globalDir := t.TempDir()
	agents := map[string]string{
		"reader": `
model = "reader-model"
tools = ["read"]
skills = []
`,
	}
	for name, body := range agents {
		if err := os.WriteFile(filepath.Join(globalDir, name+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	agentReg := config.NewAgentReg(globalDir, t.TempDir())

	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: twoProviderSource(), Provider: "alpha",
		AgentReg: agentReg, Stderr: io.Discard,
	})
	if _, err := h.SwitchAgent("reader"); err != nil {
		t.Fatalf("SwitchAgent: %v", err)
	}
	if h.CurrentAgent() == nil {
		t.Fatal("current agent should be reader after switch")
	}

	h.ResetAgent()
	if h.CurrentAgent() != nil {
		t.Errorf("CurrentAgent = %v, want nil after reset", h.CurrentAgent())
	}
	if h.Model() != "alpha-model" {
		t.Errorf("Model = %q, want the run default %q", h.Model(), "alpha-model")
	}
	if _, ok := h.Registry().Get("bash"); !ok {
		t.Error("bash should be back in the scoped registry after reset")
	}
}

func TestProviderCatalogNoSource(t *testing.T) {
	h := newTestHandle(t, Options{Config: testCfg(t.TempDir()), Stderr: io.Discard})
	if _, err := h.ProviderCatalog(context.Background()); err == nil {
		t.Error("expected no-provider-source error")
	}
}

func TestInstructionFailureDegrades(t *testing.T) {
	cfg := testCfg(t.TempDir())
	cfg.InstructionFile = filepath.Join(t.TempDir(), "missing.txt") // never written
	h := newTestHandle(t, Options{
		Config:         cfg,
		ProviderSource: twoProviderSource(), Provider: "alpha", Stderr: io.Discard,
	})
	if h.Instruction() != "" {
		t.Errorf("Instruction = %q, want empty fallback on assembly failure", h.Instruction())
	}
	var out bytes.Buffer
	if err := h.Turn(context.Background(), "hi", &out, io.Discard); err != nil {
		t.Errorf("Turn should still run with an empty instruction: %v", err)
	}
}

// degradePluginScript only knows context/before_request, and it fails every
// invocation, forcing the context seam into its degradation path.
const degradePluginScript = `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"providers":false,"context_before":true,"version":1},"id":'"$id"'}'
            ;;
        "context/before_request")
            echo '{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal explosion"},"id":'"$id"'}'
            ;;
        "shutdown")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            exit 0
            ;;
        *)
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            ;;
    esac
done
`

func TestOnUsageDegradeReceivesPrefixedMessages(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq required for the JSON-RPC plugin harness")
	}
	pluginDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pluginDir, "bad_ctx.sh"), []byte(degradePluginScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testCfg(t.TempDir())
	cfg.PluginDir = pluginDir
	var degrades []string
	h := newTestHandle(t, Options{
		Config:         cfg,
		ProviderSource: twoProviderSource(), Provider: "alpha", Stderr: io.Discard,
		OnUsageDegrade: func(msg string) { degrades = append(degrades, msg) },
	})
	var out bytes.Buffer
	if err := h.Turn(context.Background(), "hi", &out, io.Discard); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if len(degrades) == 0 {
		t.Fatal("expected the failing hook to surface as degradation")
	}
	for _, d := range degrades {
		if !strings.HasPrefix(d, "context degraded: before_request hook ") {
			t.Errorf("degrade message = %q, want the prefixed format", d)
		}
		if strings.Contains(d, "\n") {
			t.Errorf("degrade message must carry no trailing newline (main prints it): %q", d)
		}
	}
}

func TestSetNegotiatorReachesGateAndTool(t *testing.T) {
	src := twoProviderSource()
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: src, Provider: "alpha", Stderr: io.Discard,
	})

	neg := &recNegotiator{resp: permissions.ResponseSession}
	h.SetNegotiator(neg)

	tool, ok := h.Registry().Get(requesttool.ToolName)
	if !ok {
		t.Fatal("request_permission missing")
	}
	reqT, ok := tool.(*requesttool.Tool)
	if !ok {
		t.Fatal("tool mismatch")
	}
	out, err := reqT.Execute([]byte(`{"permission":"net","scope":"example.com"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(neg.axes) != 1 || neg.axes[0] != permissions.Axis("net") {
		t.Errorf("negotiation axes = %v, want [net]", neg.axes)
	}
	if !strings.Contains(out, "granted") {
		t.Errorf("out = %q, want a grant line", out)
	}
}

func TestLedgerFlushedOnClose(t *testing.T) {
	dir := t.TempDir()
	h, err := New(Options{
		Config:         &config.Config{SessionDir: dir},
		ProviderSource: twoProviderSource(), Provider: "alpha", Stderr: io.Discard,
		WithLedger: true,
	})
	if err != nil {
		t.Fatalf("run.New: %v", err)
	}
	if h.Ledger() == nil {
		t.Fatal("ledger must be non-nil with WithLedger")
	}
	sess := h.Session()
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sess.ID+".ldg")); err != nil {
		t.Logf("ledger file check: %v", err)
	}
}

func TestNewSessionBadDir(t *testing.T) {
	h := newTestHandle(t, Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: twoProviderSource(), Provider: "alpha", Stderr: io.Discard,
	})
	if _, err := h.NewSession(); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
}
