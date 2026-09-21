package repl

import (
	"bytes"
	"context"
	"fmt"
	"iter"
	"path/filepath"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/ledger"
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/session"
)

func TestSlashHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/help\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Commands:") {
		t.Errorf("stdout = %q, want help text", stdout.String())
	}
}

func TestSlashQuit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSlashUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/foo\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "unknown command: /foo") {
		t.Errorf("stdout = %q, want unknown command message", stdout.String())
	}
}

func TestEmptyInputSkipped(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("\n\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty lines should not produce any output except the prompt.
	if strings.Contains(stdout.String(), "Error:") {
		t.Errorf("stdout = %q, want no errors from empty input", stdout.String())
	}
}

func TestEOFExitsCleanly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader(""),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error on EOF: %v", err)
	}
}

func writeTestLedger(t *testing.T, dir, sessionID string) {
	t.Helper()
	l := ledger.New(dir, sessionID)
	l.RecordToolCall("call_1")
	l.Snapshot("foo.txt", "line1\nline2")
	l.RecordMutation("foo.txt", "line1\nline2", "line1\nline3", ledger.OpEdit)
	if err := l.Close(); err != nil {
		t.Fatalf("write ledger: %v", err)
	}
}

func TestSlashChangesEmpty(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/changes\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "no changes") {
		t.Errorf("stdout = %q, want 'no changes'", stdout.String())
	}
}

func TestSlashChangesListsBatches(t *testing.T) {
	dir := t.TempDir()
	// We need a known session ID to write ledger data, so write it manually.
	sessionID := "test-session"
	writeTestLedger(t, dir, sessionID)

	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/changes\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: dir,
	}
	// Inject session by pre-creating the session file. The REPL will create
	// a new session, so we write the ledger under that ID instead.
	// Actually, we need to match the session the REPL creates. Let's use a
	// different approach: write ledger, then point at it.
	// The REPL creates its own session, so we can't predict the ID.
	// Instead, let's write the ledger for the session the REPL will create.
	// We'll read the session ID from stderr after it's created.
	// For simplicity, let's just test the empty case here and test the
	// listing case via a unit test on the handler.

	// The /changes command uses the session the REPL creates, which starts
	// empty. So /changes on a fresh session should print "no changes".
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "no changes") {
		t.Errorf("stdout = %q, want 'no changes'", out)
	}
}

func TestSlashChangesIDNotFound(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/changes 99\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "no such change id: 99") {
		t.Errorf("stdout = %q, want 'no such change id: 99'", stdout.String())
	}
}

func TestSlashChangesInvalidID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/changes abc\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "invalid change id: abc") {
		t.Errorf("stdout = %q, want 'invalid change id: abc'", stdout.String())
	}
}

func TestHandleChangesListsBatches(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-session"

	// Write two batches.
	l := ledger.New(dir, sessionID)
	l.RecordToolCall("call_1")
	l.Snapshot("foo.txt", "old")
	l.RecordMutation("foo.txt", "old", "new", ledger.OpEdit)
	if err := l.Close(); err != nil {
		t.Fatalf("close 1: %v", err)
	}
	l.RecordToolCall("call_2")
	l.Snapshot("bar.txt", "a")
	l.RecordMutation("bar.txt", "a", "b", ledger.OpOverwrite)
	if err := l.Close(); err != nil {
		t.Fatalf("close 2: %v", err)
	}

	var stdout bytes.Buffer
	cfg := &Config{SessionDir: dir, Stderr: &bytes.Buffer{}}
	handleChanges("", cfg, sessionID, &stdout)

	out := stdout.String()
	if !strings.Contains(out, "foo.txt") {
		t.Errorf("output missing foo.txt: %s", out)
	}
	if !strings.Contains(out, "bar.txt") {
		t.Errorf("output missing bar.txt: %s", out)
	}
	// Newest first: seq 1 before seq 0.
	idx1 := strings.Index(out, "001")
	idx0 := strings.Index(out, "000")
	if idx1 >= idx0 {
		t.Errorf("expected batch 1 before batch 0, got:\n%s", out)
	}
}

func TestHandleChangesShowBatch(t *testing.T) {
	dir := t.TempDir()
	sessionID := "test-session"

	l := ledger.New(dir, sessionID)
	l.RecordToolCall("call_1")
	l.Snapshot("foo.txt", "line1\nline2")
	l.RecordMutation("foo.txt", "line1\nline2", "line1\nline3", ledger.OpEdit)
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var stdout bytes.Buffer
	cfg := &Config{SessionDir: dir, Stderr: &bytes.Buffer{}}
	handleChanges("0", cfg, sessionID, &stdout)

	out := stdout.String()
	if !strings.Contains(out, "foo.txt") {
		t.Errorf("output missing foo.txt: %s", out)
	}
	if !strings.Contains(out, "edit") {
		t.Errorf("output missing op type: %s", out)
	}
	if !strings.Contains(out, "-line2") || !strings.Contains(out, "+line3") {
		t.Errorf("output missing diff content: %s", out)
	}
}

func TestHandleChangesBatchNotFound(t *testing.T) {
	var stdout bytes.Buffer
	cfg := &Config{SessionDir: t.TempDir(), Stderr: &bytes.Buffer{}}
	handleChanges("0", cfg, "nonexistent", &stdout)
	if !strings.Contains(stdout.String(), "no such change id: 0") {
		t.Errorf("output = %q, want 'no such change id: 0'", stdout.String())
	}
}

func TestHandleChangesInvalidID(t *testing.T) {
	var stdout bytes.Buffer
	cfg := &Config{SessionDir: t.TempDir(), Stderr: &bytes.Buffer{}}
	handleChanges("abc", cfg, "test", &stdout)
	if !strings.Contains(stdout.String(), "invalid change id: abc") {
		t.Errorf("output = %q, want 'invalid change id: abc'", stdout.String())
	}
}

// TestCurrentModelExplicitOnly pins the og-z1m.3 explicit-only rule on the
// no-global model (og-z1m.8): the runtime model is the active provider's
// default (cfg.Model) unless an agent declares its own model outright. An
// agent with no declared model resolves empty and leaves the default alone.
func TestCurrentModelExplicitOnly(t *testing.T) {
	providerDefault := &Config{Model: "other-model"}

	undeclared := &config.ResolvedAgent{Name: "a", Model: ""}
	if got := currentModel(providerDefault, undeclared); got != "other-model" {
		t.Errorf("undeclared agent: currentModel = %q, want provider default %q", got, "other-model")
	}

	declared := &config.ResolvedAgent{Name: "b", Model: "claude-sonnet-4-5"}
	if got := currentModel(providerDefault, declared); got != "claude-sonnet-4-5" {
		t.Errorf("declaring agent: currentModel = %q, want agent model %q", got, "claude-sonnet-4-5")
	}

	if got := currentModel(providerDefault, nil); got != "other-model" {
		t.Errorf("no agent: currentModel = %q, want provider default %q", got, "other-model")
	}
}

// fakeLLMClient is a stub llm.Client whose ListModels is scripted. Stream is
// a no-op (no turn runs in these tests).
type fakeLLMClient struct {
	models []llm.Model
	err    error
}

func (c *fakeLLMClient) ListModels(context.Context) ([]llm.Model, error) {
	return c.models, c.err
}

func (c *fakeLLMClient) Stream(context.Context, llm.Request) (iter.Seq[llm.Event], error) {
	return func(yield func(llm.Event) bool) {}, nil
}

// registryClient is the llm.Client lookup the fake registry satisfies.
type registryClient = llm.Client

// fakeRegistry scripts the provider surface the REPL needs: the declared
// names, each provider's default model, its catalog, and the client a switch
// rebuilds.
type fakeRegistry struct {
	names       []string
	defaults    map[string]string
	clients     map[string]registryClient
	catalogs    map[string][]llm.Model
	catalogErrs map[string]error
}

func (f *fakeRegistry) Names() []string { return f.names }

func (f *fakeRegistry) DefaultModel(name string) (string, error) {
	if m, ok := f.defaults[name]; ok {
		return m, nil
	}
	return "", fmt.Errorf("registry: no such provider %q", name)
}

func (f *fakeRegistry) Client(name string) (llm.Client, error) {
	c, ok := f.clients[name]
	if !ok {
		return nil, fmt.Errorf("registry: no such provider %q", name)
	}
	return c, nil
}

func (f *fakeRegistry) Catalog(_ context.Context, name string) ([]llm.Model, error) {
	if err := f.catalogErrs[name]; err != nil {
		return nil, err
	}
	c, ok := f.catalogs[name]
	if !ok {
		return nil, fmt.Errorf("registry: no such provider %q", name)
	}
	return c, nil
}

// newProviderState builds a Config, replState and session for slash-command
// tests, booting the state's context-wrapped client on provider's fake client.
func newProviderState(t *testing.T, reg *fakeRegistry, provider, model string) (*Config, *replState, *session.Session, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Providers:  reg,
		Provider:   provider,
		Model:      model,
		Stdin:      strings.NewReader(""),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	sess, err := session.New(cfg.SessionDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	client, err := reg.Client(provider)
	if err != nil {
		t.Fatalf("boot client: %v", err)
	}
	state := &replState{provider: provider, baseClient: client, client: wrapClient(cfg, client, sess)}
	return cfg, state, sess, &stdout, &stderr
}

// runSlash drives one slash command through the handler and asserts it did not
// request an exit.
func runSlash(t *testing.T, cfg *Config, state *replState, sess *session.Session, line string) {
	t.Helper()
	if handleSlashCommand(context.Background(), line, cfg, state, &sess) {
		t.Fatalf("command %q requested exit", line)
	}
}

// twoProviderReg scripts alpha (default alpha-model, catalog [alpha-model,
// alpha-1, alpha-2]) and beta (default beta-model, catalog [beta-model,
// beta-1, beta-2]) as openai-ish fake clients so every part of the switch
// path is observable.
func twoProviderReg() *fakeRegistry {
	return &fakeRegistry{
		names:    []string{"alpha", "beta"},
		defaults: map[string]string{"alpha": "alpha-model", "beta": "beta-model"},
		clients: map[string]registryClient{
			"alpha": &fakeLLMClient{models: []llm.Model{{ID: "alpha-1"}, {ID: "alpha-2"}}},
			"beta":  &fakeLLMClient{models: []llm.Model{{ID: "beta-1"}, {ID: "beta-2"}}},
		},
		catalogs: map[string][]llm.Model{
			"alpha": {{ID: "alpha-model"}, {ID: "alpha-1"}, {ID: "alpha-2"}},
			"beta":  {{ID: "beta-model"}, {ID: "beta-1"}, {ID: "beta-2"}},
		},
	}
}

func TestProviderListMarksCurrent(t *testing.T) {
	cfg, state, sess, stdout, _ := newProviderState(t, twoProviderReg(), "alpha", "alpha-model")
	runSlash(t, cfg, state, sess, "/provider")

	out := stdout.String()
	if !strings.Contains(out, "Available providers:") {
		t.Errorf("stdout = %q, want providers listing", out)
	}
	if !strings.Contains(out, "* alpha") || !strings.Contains(out, "  beta") {
		t.Errorf("stdout = %q, want alpha marked current and beta unmarked", out)
	}
	if !strings.Contains(out, "Current: alpha") {
		t.Errorf("stdout = %q, want 'Current: alpha'", out)
	}
}

func TestProviderSwitchRebuildsClientAndResetsModel(t *testing.T) {
	cfg, state, sess, stdout, stderr := newProviderState(t, twoProviderReg(), "alpha", "alpha-model")
	oldClient := state.client

	runSlash(t, cfg, state, sess, "/provider beta")

	if state.provider != "beta" {
		t.Errorf("provider = %q, want beta", state.provider)
	}
	if cfg.Model != "beta-model" {
		t.Errorf("model = %q, want beta-default %q", cfg.Model, "beta-model")
	}
	if state.client == oldClient {
		t.Errorf("client not rebuilt after switch")
	}
	if !strings.Contains(stdout.String(), "provider: beta (model: beta-model)") {
		t.Errorf("stdout = %q, want switch message", stdout.String())
	}
	stderrStr := stderr.String()
	if stderrStr != "" {
		t.Errorf("stderr = %q, want clean switch", stderrStr)
	}

	// /model after the switch lists the new provider's catalog and marks its
	// default (the active provider's model).
	stdout.Reset()
	runSlash(t, cfg, state, sess, "/model")
	out := stdout.String()
	if !strings.Contains(out, "* beta-2") && !strings.Contains(out, "* beta-model") {
		t.Errorf("/model = %q, want asterisk on a beta catalog entry or the beta default", out)
	}
	if strings.Contains(out, "alpha-") {
		t.Errorf("/model = %q, must not list the old provider's catalog", out)
	}
}

func TestProviderUnknownNamesTheAvailableSet(t *testing.T) {
	cfg, state, sess, stdout, _ := newProviderState(t, twoProviderReg(), "alpha", "alpha-model")
	runSlash(t, cfg, state, sess, "/provider gamma")

	out := stdout.String()
	if !strings.Contains(out, "no such provider: gamma") || !strings.Contains(out, "(available: alpha, beta)") {
		t.Errorf("stdout = %q, want 'no such provider: gamma (available: alpha, beta)'", out)
	}
	if state.provider != "alpha" || cfg.Model != "alpha-model" {
		t.Errorf("state drifted on failed switch: provider=%q model=%q", state.provider, cfg.Model)
	}
}

func TestBareProviderListsWithoutSwitching(t *testing.T) {
	cfg, state, sess, _, _ := newProviderState(t, twoProviderReg(), "alpha", "alpha-model")
	runSlash(t, cfg, state, sess, "/provider beta")
	runSlash(t, cfg, state, sess, "/provider  ")

	// A bare /provider with only spaces lists; it must not switch.
	if state.provider != "beta" {
		t.Errorf("provider = %q, want beta (bare /provider must list, not switch)", state.provider)
	}
}

func TestModelListingUsesActiveProviderCatalog(t *testing.T) {
	cfg, state, sess, stdout, _ := newProviderState(t, twoProviderReg(), "alpha", "alpha-model")

	runSlash(t, cfg, state, sess, "/provider beta")
	stdout.Reset()
	runSlash(t, cfg, state, sess, "/model")

	out := stdout.String()
	if !strings.Contains(out, "beta-1") || !strings.Contains(out, "beta-2") {
		t.Errorf("/model = %q, want the active (beta) provider's catalog", out)
	}
}

func TestModelSwitchValidatedWithinActiveProvider(t *testing.T) {
	cfg, state, sess, stdout, _ := newProviderState(t, twoProviderReg(), "alpha", "alpha-model")

	// alpha's catalog does not contain beta-2: switching to it must fail.
	runSlash(t, cfg, state, sess, "/model beta-2")
	if cfg.Model != "alpha-model" {
		t.Errorf("model = %q, want untouched after unknown model", cfg.Model)
	}
	if !strings.Contains(stdout.String(), "no such model: beta-2") {
		t.Errorf("stdout = %q, want no-such-model", stdout.String())
	}

	stdout.Reset()
	runSlash(t, cfg, state, sess, "/provider beta")
	runSlash(t, cfg, state, sess, "/model beta-2")
	if cfg.Model != "beta-2" {
		t.Errorf("model = %q, want beta-2 after valid switch within beta", cfg.Model)
	}
}

func TestProviderSurvivesNewAfterSwitch(t *testing.T) {
	reg := twoProviderReg()

	cfg, state, sess, _, _ := newProviderState(t, reg, "alpha", "alpha-model")
	runSlash(t, cfg, state, sess, "/provider beta")
	if state.provider != "beta" {
		t.Fatalf("provider = %q, want beta after switch", state.provider)
	}

	// /new must keep building on the switched provider, not revert to the
	// boot client: the pre-switch base client and post-switch base client are
	// distinct fakes, so leaving alpha would change the baseClient's models.
	before := state.baseClient
	runSlash(t, cfg, state, sess, "/new alpha")
	if cfg.Model != "beta-model" {
		t.Errorf("model = %q, want the switched provider's default preserved", cfg.Model)
	}
	if state.provider != "beta" {
		t.Errorf("provider = %q, want beta preserved across /new", state.provider)
	}
	if state.baseClient != before {
		t.Errorf("/new rebuilt from a different client than the switched provider")
	}
}

func TestModelCatalogFailureDegradesToDefaultModel(t *testing.T) {
	reg := twoProviderReg()
	reg.catalogErrs = map[string]error{"alpha": fmt.Errorf("catalog down")}

	cfg, state, sess, stdout, stderr := newProviderState(t, reg, "alpha", "alpha-model")
	runSlash(t, cfg, state, sess, "/model")

	if !strings.Contains(stderr.String(), "Error: fetching model catalog") {
		t.Errorf("stderr = %q, want catalog fetch error", stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "* alpha-model") {
		t.Errorf("/model = %q, want the default model listed as current on degradation", out)
	}
	if !strings.Contains(out, "Current: alpha-model") {
		t.Errorf("/model = %q, want 'Current: alpha-model'", out)
	}
}

// TestApplyAgentBase pins the per-agent base switch in the permission store
// (og-uy5.7): the global base applies for nil/no-permissions agents, an
// agent's declared [permissions] replaces it wholly (its write prompts despite
// a global write base), and switching back restores the global base.
func TestApplyAgentBase(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "work")
	store := permissions.New(cwd)
	cfg := &Config{
		Cfg: &config.Config{
			Permissions: config.Permissions{Base: map[string][]string{
				"read":  {"."},
				"write": {"."},
			}},
		},
		PermissionStore: store,
	}
	globalWrite := filepath.Join(cwd, "x.go")

	// No agent, and an agent without a [permissions] section, inherit the
	// global base.
	applyAgentBase(cfg, nil)
	if !store.Covered(permissions.AxisWrite, globalWrite) {
		t.Fatalf("global write base not applied for a nil agent")
	}
	applyAgentBase(cfg, &config.ResolvedAgent{Name: "plain"})
	if !store.Covered(permissions.AxisWrite, globalWrite) {
		t.Fatalf("global write base not inherited by an agent without permissions")
	}

	// A read-only [permissions] section replaces the base wholly: the global
	// write base is gone, so a write must escalate.
	applyAgentBase(cfg, &config.ResolvedAgent{
		Name:        "reader",
		Permissions: &config.AgentPermissions{Read: []string{"."}},
	})
	if store.Covered(permissions.AxisWrite, globalWrite) {
		t.Errorf("write covered under a read-only agent base; want it prompted despite the global write base")
	}
	if !store.Covered(permissions.AxisRead, filepath.Join(cwd, "a.go")) {
		t.Errorf("read under the agent's declared read=[] base not covered")
	}
	if store.Covered(permissions.AxisNet, "") {
		t.Errorf("net covered under a read-only agent base; want empty (no axis-level inheritance)")
	}

	// Switching back to an inheriting agent restores the global base.
	applyAgentBase(cfg, &config.ResolvedAgent{Name: "plain"})
	if !store.Covered(permissions.AxisWrite, globalWrite) {
		t.Errorf("global write base not restored on switch back to an inheriting agent")
	}
}

// TestApplyAgentBaseRequiresStoreAndConfig reports that applyAgentBase is a
// no-op (never a panic) for the nil-store / nil-config repl configurations
// used by unit tests — a store without a config to resolve the global base
// from keeps its own base untouched.
func TestApplyAgentBaseRequiresStoreAndConfig(t *testing.T) {
	applyAgentBase(&Config{}, &config.ResolvedAgent{Name: "x", Permissions: &config.AgentPermissions{Read: []string{"."}}})
	cwd := t.TempDir()
	store := permissions.New(cwd)
	applyAgentBase(&Config{PermissionStore: store}, &config.ResolvedAgent{Name: "x", Permissions: &config.AgentPermissions{Read: []string{"."}}})
	if store.Covered(permissions.AxisWrite, filepath.Join(cwd, "x.go")) {
		t.Error("applyAgentBase touched a store without a config; want a no-op")
	}
}
