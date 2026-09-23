package repl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/ledger"
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/run"
)

func TestSlashHelp(t *testing.T) {
	h := newTestHandle(t, nil, "", t.TempDir())
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Run:        h,
		SessionDir: t.TempDir(),
		Stdin:      strings.NewReader("/help\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
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
	h := newTestHandle(t, nil, "", t.TempDir())
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Run:        h,
		SessionDir: t.TempDir(),
		Stdin:      strings.NewReader("/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSlashUnknown(t *testing.T) {
	h := newTestHandle(t, nil, "", t.TempDir())
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Run:        h,
		SessionDir: t.TempDir(),
		Stdin:      strings.NewReader("/foo\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "unknown command: /foo") {
		t.Errorf("stdout = %q, want unknown command message", stdout.String())
	}
}

func TestEmptyInputSkipped(t *testing.T) {
	h := newTestHandle(t, nil, "", t.TempDir())
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Run:        h,
		SessionDir: t.TempDir(),
		Stdin:      strings.NewReader("\n\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty lines should not produce any output except the prompt.
	if strings.Contains(stdout.String(), "Error:") {
		t.Errorf("stdout = %q, want no errors from empty input", stdout.String())
	}
}

func TestEOFExitsCleanly(t *testing.T) {
	h := newTestHandle(t, nil, "", t.TempDir())
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Run:        h,
		SessionDir: t.TempDir(),
		Stdin:      strings.NewReader(""),
		Stdout:     &stdout,
		Stderr:     &stderr,
	}
	if err := Run(context.Background(), cfg); err != nil {
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

// TestSlashChangesEmpty drives /changes inside the REPL loop against a fresh
// session (no ledgers) and expects the empty listing.
func TestSlashChangesEmpty(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandle(t, nil, "", dir)
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Run:        h,
		SessionDir: dir,
		Stdin:      strings.NewReader("/changes\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "no changes") {
		t.Errorf("stdout = %q, want 'no changes'", out)
	}
}

// TestSlashChangesListsBatches preseeded under the running session's ID so the
// REPL's /changes lists the real batch.
func TestSlashChangesListsBatches(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandle(t, nil, "", dir)
	writeTestLedger(t, dir, h.Session().ID)

	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Run:        h,
		SessionDir: dir,
		Stdin:      strings.NewReader("/changes\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "foo.txt") {
		t.Errorf("stdout = %q, want the preseeded batch listed", stdout.String())
	}
}

func TestSlashChangesIDNotFound(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandle(t, nil, "", dir)
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Run:        h,
		SessionDir: dir,
		Stdin:      strings.NewReader("/changes 99\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "no such change id: 99") {
		t.Errorf("stdout = %q, want 'no such change id: 99'", stdout.String())
	}
}

func TestSlashChangesInvalidID(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandle(t, nil, "", dir)
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Run:        h,
		SessionDir: dir,
		Stdin:      strings.NewReader("/changes abc\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
	}
	if err := Run(context.Background(), cfg); err != nil {
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

// testCfg returns a config every REPL test can boot a handle from (raw non-load
// configs disable every tool; enable them all like config.Load does).
func testCfg(dir string) *config.Config {
	return &config.Config{
		SessionDir: dir,
		Tools:      config.Tools{Read: true, Write: true, Edit: true, Bash: true},
	}
}

// newTestHandle assembles a run.Handle for a REPL test. A nil source and empty
// provider yield a provider-less handle (slash tests that don't touch
// providers).
func newTestHandle(t *testing.T, reg run.ProviderSource, provider, dir string) *run.Handle {
	t.Helper()
	h, err := run.New(run.Options{
		Config:         testCfg(dir),
		ProviderSource: reg,
		Provider:       provider,
		Stderr:         io.Discard,
	})
	if err != nil {
		t.Fatalf("run.New: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// newSlashCfg builds a Config whose Run satisfies the slash handlers; stdout
// and stderr buffers capture the command output. The boot provider/model are
// driven by reg's default (alpha-model for twoProviderReg).
func newSlashCfg(t *testing.T, reg run.ProviderSource, provider, dir string) (*Config, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return &Config{
		Run:        newTestHandle(t, reg, provider, dir),
		SessionDir: dir,
		Stdout:     &stdout,
		Stderr:     &stderr,
	}, &stdout, &stderr
}

// runSlash drives one slash command through the handler and asserts it did not
// request an exit.
func runSlash(t *testing.T, cfg *Config, line string) {
	t.Helper()
	if handleSlashCommand(context.Background(), line, cfg) {
		t.Fatalf("command %q requested exit", line)
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

// fakeRegistry scripts the provider surface the run handle consumes: the
// declared names, each provider's default model, its catalog, and the client a
// switch rebuilds. It satisfies run.ProviderSource.
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
	cfg, stdout, _ := newSlashCfg(t, twoProviderReg(), "alpha", t.TempDir())
	runSlash(t, cfg, "/provider")

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
	cfg, stdout, stderr := newSlashCfg(t, twoProviderReg(), "alpha", t.TempDir())
	oldClient := cfg.Run.Client()

	runSlash(t, cfg, "/provider beta")

	if cfg.Run.Provider() != "beta" {
		t.Errorf("provider = %q, want beta", cfg.Run.Provider())
	}
	if cfg.Run.Model() != "beta-model" {
		t.Errorf("model = %q, want beta-default %q", cfg.Run.Model(), "beta-model")
	}
	if cfg.Run.Client() == oldClient {
		t.Errorf("client not rebuilt after switch")
	}
	if !strings.Contains(stdout.String(), "provider: beta (model: beta-model)") {
		t.Errorf("stdout = %q, want switch message", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want clean switch", stderr.String())
	}

	// /model after the switch lists the new provider's catalog and marks
	// the active provider's model (there is no global to fall back to).
	stdout.Reset()
	runSlash(t, cfg, "/model")
	out := stdout.String()
	if !strings.Contains(out, "* beta-model") {
		t.Errorf("/model = %q, want asterisk on the beta default", out)
	}
	if strings.Contains(out, "alpha-") {
		t.Errorf("/model = %q, must not list the old provider's catalog", out)
	}
}

func TestProviderUnknownNamesTheAvailableSet(t *testing.T) {
	cfg, stdout, _ := newSlashCfg(t, twoProviderReg(), "alpha", t.TempDir())
	runSlash(t, cfg, "/provider gamma")

	out := stdout.String()
	if !strings.Contains(out, "no such provider: gamma") || !strings.Contains(out, "(available: alpha, beta)") {
		t.Errorf("stdout = %q, want 'no such provider: gamma (available: alpha, beta)'", out)
	}
	if cfg.Run.Provider() != "alpha" || cfg.Run.Model() != "alpha-model" {
		t.Errorf("state drifted on failed switch: provider=%q model=%q", cfg.Run.Provider(), cfg.Run.Model())
	}
}

func TestBareProviderListsWithoutSwitching(t *testing.T) {
	cfg, _, _ := newSlashCfg(t, twoProviderReg(), "alpha", t.TempDir())
	runSlash(t, cfg, "/provider beta")
	runSlash(t, cfg, "/provider  ")

	// A bare /provider with only spaces lists; it must not switch.
	if cfg.Run.Provider() != "beta" {
		t.Errorf("provider = %q, want beta (bare /provider must list, not switch)", cfg.Run.Provider())
	}
}

func TestModelListingUsesActiveProviderCatalog(t *testing.T) {
	cfg, stdout, _ := newSlashCfg(t, twoProviderReg(), "alpha", t.TempDir())

	runSlash(t, cfg, "/provider beta")
	stdout.Reset()
	runSlash(t, cfg, "/model")

	out := stdout.String()
	if !strings.Contains(out, "beta-1") || !strings.Contains(out, "beta-2") {
		t.Errorf("/model = %q, want the active (beta) provider's catalog", out)
	}
}

func TestModelSwitchValidatedWithinActiveProvider(t *testing.T) {
	cfg, stdout, _ := newSlashCfg(t, twoProviderReg(), "alpha", t.TempDir())

	// alpha's catalog does not contain beta-2: switching to it must fail.
	runSlash(t, cfg, "/model beta-2")
	if cfg.Run.Model() != "alpha-model" {
		t.Errorf("model = %q, want untouched after unknown model", cfg.Run.Model())
	}
	if !strings.Contains(stdout.String(), "no such model: beta-2") {
		t.Errorf("stdout = %q, want no-such-model", stdout.String())
	}

	stdout.Reset()
	runSlash(t, cfg, "/provider beta")
	runSlash(t, cfg, "/model beta-2")
	if cfg.Run.Model() != "beta-2" {
		t.Errorf("model = %q, want beta-2 after valid switch within beta", cfg.Run.Model())
	}
}

func TestProviderSurvivesNewAfterSwitch(t *testing.T) {
	cfg, _, _ := newSlashCfg(t, twoProviderReg(), "alpha", t.TempDir())
	runSlash(t, cfg, "/provider beta")
	if cfg.Run.Provider() != "beta" {
		t.Fatalf("provider = %q, want beta after switch", cfg.Run.Provider())
	}

	before := cfg.Run.BaseClient()
	runSlash(t, cfg, "/new")
	if cfg.Run.Model() != "beta-model" {
		t.Errorf("model = %q, want the switched provider's default preserved", cfg.Run.Model())
	}
	if cfg.Run.Provider() != "beta" {
		t.Errorf("provider = %q, want beta preserved across /new", cfg.Run.Provider())
	}
	if cfg.Run.BaseClient() != before {
		t.Errorf("/new rebuilt from a different client than the switched provider")
	}
}

func TestModelCatalogFailureDegradesToDefaultModel(t *testing.T) {
	reg := twoProviderReg()
	reg.catalogErrs = map[string]error{"alpha": fmt.Errorf("catalog down")}

	cfg, stdout, stderr := newSlashCfg(t, reg, "alpha", t.TempDir())
	runSlash(t, cfg, "/model")

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

// TestAgentSwitchReflectedOnRun drives the /agent surface against a real
// handle and pins the switch line the e2e suite relies on.
func TestAgentSwitchReflectedOnRun(t *testing.T) {
	agentsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentsDir, "reader.toml"),
		[]byte("model = \"reader-model\"\ntools = [\"read\"]\nskills = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := run.New(run.Options{
		Config:         testCfg(t.TempDir()),
		ProviderSource: twoProviderReg(),
		Provider:       "alpha",
		AgentReg:       config.NewAgentReg(agentsDir, t.TempDir()),
		Stderr:         io.Discard,
	})
	if err != nil {
		t.Fatalf("run.New: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	var stdout, stderr bytes.Buffer
	cfg := &Config{Run: h, Stdout: &stdout, Stderr: &stderr}

	runSlash(t, cfg, "/agent reader")
	if cmd := h.CurrentAgent(); cmd == nil || cmd.Name != "reader" {
		t.Errorf("current agent = %+v, want reader", cmd)
	}
	if !strings.Contains(stdout.String(), "switched to reader (model: reader-model), tools: read") {
		t.Errorf("stdout = %q, want the switch line", stdout.String())
	}
	if h.Model() != "reader-model" {
		t.Errorf("model = %q, want the agent-declared model", h.Model())
	}
}

// TestAgentUnknownPrintsErr pins the /agent error path: unknown names surface
// through the handle's resolution error, not a blank switch.
func TestAgentUnknownPrintsErr(t *testing.T) {
	agentsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentsDir, "reader.toml"),
		[]byte("model = \"reader-model\"\nskills = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newTestHandle(t, twoProviderReg(), "alpha", t.TempDir())
	t.Cleanup(func() { _ = h.Close() })

	var stdout, stderr bytes.Buffer
	cfg := &Config{Run: h, Stdout: &stdout, Stderr: &stderr}

	// The handle boots without an AgentReg, so /agent switch reports none.
	runSlash(t, cfg, "/agent nosuch")
	if !strings.Contains(stderr.String(), "no agents configured") {
		t.Errorf("stderr = %q, want 'no agents configured'", stderr.String())
	}
}
