package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/okayest-dev/genie/internal/agent"
	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/contextmgr"
	"github.com/okayest-dev/genie/internal/instruct"
	"github.com/okayest-dev/genie/internal/ledger"
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/modelinfo"
	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/plugin"
	"github.com/okayest-dev/genie/internal/session"
	"github.com/okayest-dev/genie/internal/skill"
	"github.com/okayest-dev/genie/internal/tokens"
	"github.com/okayest-dev/genie/internal/tools"
	"github.com/okayest-dev/genie/internal/tools/bashtool"
	"github.com/okayest-dev/genie/internal/tools/edittool"
	"github.com/okayest-dev/genie/internal/tools/readtool"
	"github.com/okayest-dev/genie/internal/tools/requesttool"
	"github.com/okayest-dev/genie/internal/tools/writetool"
)

// ProviderSource is the port through which the run resolves providers: the
// declared names, a provider's default model, and a freshly built client. The
// prod *llm.Registry satisfies it; tests script a fake.
type ProviderSource interface {
	// Names returns the declared provider names in deterministic order.
	Names() []string
	// Catalog returns a provider's model catalog in order.
	Catalog(ctx context.Context, name string) ([]llm.Model, error)
	// DefaultModel returns a provider's default model.
	DefaultModel(name string) (string, error)
	// Client builds the provider's client.
	Client(name string) (llm.Client, error)
}

// Options bundles the mode-shaped pieces an entry point injects. Everything a
// turn touches that is derivable from Config + Cwd — tools, skills, plugins,
// sessions, and the permission gate — is assembled inside New.
type Options struct {
	// Config is the harness config. A nil config is tolerated as a zero
	// configuration (unit-test handles).
	Config *config.Config
	// Cwd is the working directory for AGENTS.md lookup and the tool cwd.
	Cwd string
	// Stdin/Stdout/Stderr are the assembly-context streams. Signals and
	// interactive negotiation are owned by the entry point, so the run itself
	// writes only session notices (and no-provider warnings) to Stderr; the
	// others are carried for the mode-shaped pieces an entry point wires.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Provider is the provider the run boots on. When empty and exactly one
	// provider is declared, that provider is picked. A nil ProviderSource and
	// an empty Provider yield a provider-less handle (no client) usable for
	// non-turn surfaces (e.g. /changes in unit tests).
	Provider       string
	ProviderSource ProviderSource
	// Agent is the resolved agent for this run, or nil for the default flow.
	Agent *config.ResolvedAgent
	// AgentReg resolves named agents at switch time. Nil disables /agent.
	AgentReg *config.AgentReg
	// Negotiator drives permission escalation (auto-deny headless, interactive
	// in the REPL). DenyAll is the safe default.
	Negotiator permissions.Negotiator
	// PermanentSink persists permanent-tier grants. Nil keeps them in-memory.
	PermanentSink permissions.PermanentSink
	// OnUsageDegrade receives plugin seam and context degradation messages.
	// Nil silences them.
	OnUsageDegrade func(string)
	// WithLedger enables a change ledger over the run's session, flushed on
	// Close.
	WithLedger bool
}

// env is the mutable slice of handle state a turn reads, snapshotted under the
// handle mutex. Reconfiguration mutates it in place from the (single) looper
// goroutine; turns read the snapshot so a mid-turn switch never rewrites an
// in-flight request.
type env struct {
	provider   string
	baseClient llm.Client
	client     llm.Client
	model      string
	agent      *config.ResolvedAgent
	registry   *tools.Registry
	gate       *permissions.Gate
}

// Handle is a fully assembled, ready-to-turn genie run. It owns the session
// (and optionally a ledger) and every component a turn touches, and exposes
// the locked read surface plus granular reconfiguration methods.
type Handle struct {
	opts Options

	store      *permissions.Store
	sink       permissions.PermanentSink
	globalBase map[string][]string

	full       *tools.Registry
	reqT       *requesttool.Tool
	skillNames []string

	plug         *plugin.Manager
	ctxSeam      *plugin.ContextSeam
	life         *plugin.LifecycleSeam
	counter      tokens.Counter
	buildCtxOpts func(base llm.Client) []contextmgr.Option

	sess   *session.Session
	ledger *ledger.Ledger

	degrade func(string)

	mu  sync.RWMutex
	cur env
}

// New assembles a complete run. It builds the effective-policy store, tool
// registry, skill pool, plugin manager, context/lifecycle seams, token
// counter, modelinfo resolver, session, and (optionally) ledger, then wires a
// client for the boot provider. Mode-shaped pieces — negotiator, sink,
// streams — come from Options; signals stay in the entry point.
func New(opts Options) (*Handle, error) {
	if opts.Config == nil {
		opts.Config = &config.Config{}
	}
	if opts.Negotiator == nil {
		opts.Negotiator = permissions.DenyAll{}
	}

	h := &Handle{
		opts:       opts,
		sink:       opts.PermanentSink,
		globalBase: opts.Config.Permissions.Base,
		degrade:    func(string) {},
	}

	// Effective-policy store: an agent-declared [permissions] section replaces
	// the global base wholly, then every persisted permanent grant is seeded.
	store := permissions.New(opts.Cwd)
	store.SetBaseFromConfig(opts.Agent.EffectiveBase(h.globalBase))
	for _, g := range opts.Config.Permissions.Permanent {
		if err := store.GrantPermanent(permissions.Grant{
			Axis: permissions.Axis(g.Permission), Scope: g.Scope,
			Tier: permissions.TierPermanent, Granted: g.Granted,
		}); err != nil {
			return nil, fmt.Errorf("config: permanent grant: %w", err)
		}
	}
	h.store = store

	// Full tool registry; request_permission is always-on (a negotiation
	// channel, not a capability) and seeded with the run's negotiator.
	full := tools.NewRegistry()
	full.Register(readtool.New(opts.Cwd))
	full.Register(writetool.New(opts.Cwd))
	full.Register(edittool.New(opts.Cwd))
	full.Register(bashtool.New(opts.Cwd, opts.Config.BashTimeout))
	reqT := requesttool.New(store, h.sink)
	reqT.SetNegotiator(opts.Negotiator)
	full.Register(reqT)
	if !opts.Config.Tools.Read {
		full.Disable("read")
	}
	if !opts.Config.Tools.Write {
		full.Disable("write")
	}
	if !opts.Config.Tools.Edit {
		full.Disable("edit")
	}
	if !opts.Config.Tools.Bash {
		full.Disable("bash")
	}
	h.full = full
	h.reqT = reqT

	// Resolve the skill pool once so agent validation checks explicit skills
	// lists against the discovered pool; dubious directories surface now.
	pool, warns, err := skill.FilteredPool(opts.Config.Skills.Dirs, opts.Config.Skills.Enable, opts.Config.Skills.Disable)
	if err != nil {
		return nil, err
	}
	for _, w := range warns {
		if opts.Stderr != nil {
			fmt.Fprintf(opts.Stderr, "warning: %s\n", w.Message)
		}
	}
	h.skillNames = skill.Names(pool)

	// Validate the boot agent against the full tool set.
	if opts.Agent != nil {
		if err := full.ValidateTools(opts.Agent.Tools); err != nil {
			return nil, fmt.Errorf("agent %q: %w", opts.Agent.Name, err)
		}
	}

	// The agent-scoped registry the plugin manager and every turn sees.
	scoped := full
	if opts.Agent != nil && opts.Agent.Tools != nil {
		scoped = full.Subset(opts.Agent.Tools)
	}

	// Load plugins over the scoped registry and build the context/lifecycle
	// seams. On any hard seam error the manager is shut down before returning.
	plug := plugin.NewManager(opts.Config.PluginDir, opts.Config.PluginEnable, opts.Config.PluginDisable, scoped)
	if err := plug.LoadPlugins(); err != nil {
		return nil, err
	}
	h.plug = plug

	if opts.OnUsageDegrade != nil {
		h.degrade = opts.OnUsageDegrade
	}
	ctxDegrade := func(msg string) { h.degrade("context degraded: " + msg) }
	ctxSeam, err := plugin.NewContextSeam(plug.PluginsInOrder(), plugin.ContextConfig{
		Order:          opts.Config.Context.PluginsOrder,
		ActiveCompact:  opts.Config.Context.ActiveCompact,
		ActiveCondense: opts.Config.Context.ActiveCondense,
	}, ctxDegrade)
	if err != nil {
		plug.Shutdown()
		return nil, err
	}
	h.ctxSeam = ctxSeam
	h.life = plugin.NewLifecycleSeam(plug.PluginsInOrder(), plugin.LifecycleConfig{
		Order: opts.Config.Lifecycle.PluginsOrder,
	}, func(msg string) { h.degrade("lifecycle degraded: " + msg) })
	h.counter = tokens.New()

	// Context-window & budget options are built per base client so a provider
	// switch re-sources modelinfo from the fresh client (ADR-0004).
	h.buildCtxOpts = func(base llm.Client) []contextmgr.Option {
		var infoSource modelinfo.Source
		if p, ok := base.(llm.ModelInfoProvider); ok {
			infoSource = p
		}
		resolver := modelinfo.New(infoSource, opts.Config.Context.Windows, modelinfo.Options{
			BudgetTokens:  opts.Config.Context.BudgetTokens,
			BudgetPercent: opts.Config.Context.BudgetPercent,
		})
		return []contextmgr.Option{
			contextmgr.WithTurns(opts.Config.Context.Turns),
			contextmgr.WithCounter(h.counter),
			contextmgr.WithResolver(resolver),
			contextmgr.WithHooks(ctxSeam),
			contextmgr.WithOnDegrade(ctxDegrade),
			contextmgr.WithCondenseSize(opts.Config.Context.CondenseSize),
			contextmgr.WithNetDrop(opts.Config.Context.NetDrop),
		}
	}

	// Session and (optionally) ledger. The session notice goes to Stderr so
	// both entry points echo the same line without owning session creation.
	sess, err := session.New(opts.Config.SessionDir)
	if err != nil {
		plug.Shutdown()
		return nil, fmt.Errorf("create session: %w", err)
	}
	h.sess = sess
	if opts.WithLedger {
		h.ledger = ledger.New(opts.Config.SessionDir, sess.ID)
	}
	if opts.Stderr != nil {
		fmt.Fprintf(opts.Stderr, "session: %s\n", sess.ID)
	}

	// Boot provider. Without a source or a name the handle is provider-less.
	provider := opts.Provider
	if provider == "" && opts.ProviderSource != nil {
		if names := opts.ProviderSource.Names(); len(names) > 0 {
			provider = names[0]
		}
	}
	var baseClient llm.Client
	var model string
	if provider != "" && opts.ProviderSource != nil {
		model, err = opts.ProviderSource.DefaultModel(provider)
		if err != nil {
			plug.Shutdown()
			return nil, err
		}
		baseClient, err = opts.ProviderSource.Client(provider)
		if err != nil {
			plug.Shutdown()
			return nil, err
		}
	}

	h.cur = env{
		provider:   provider,
		baseClient: baseClient,
		client:     h.wrap(baseClient, sess),
		model:      model,
		agent:      opts.Agent,
		registry:   scoped,
		gate:       permissions.NewGate(store, opts.Negotiator, opts.PermanentSink),
	}
	return h, nil
}

// wrap builds the context-wrapped client for a base client and session,
// sourcing modelinfo from the base client per ADR-0004.
func (h *Handle) wrap(base llm.Client, sess *session.Session) llm.Client {
	if base == nil {
		return nil
	}
	return contextmgr.New(base, sess, h.buildCtxOpts(base)...)
}

// resolveInstruction assembles the instruction for the given agent,
// re-running the skill pipeline (discover → filter → bind → build) on every
// read so SKILL.md edits are picked up without a config reload, and appending
// the live base-policy snapshot.
func (h *Handle) resolveInstruction(agent *config.ResolvedAgent) (string, error) {
	var agentSkills []string
	agentName := ""
	if agent != nil {
		agentSkills = agent.Skills
		agentName = agent.Name
	}
	layer, warns, err := skill.Pipeline(
		h.opts.Config.Skills.Dirs, h.opts.Config.Skills.Enable, h.opts.Config.Skills.Disable,
		agentSkills, agentName,
	)
	if err != nil {
		slog.Error("skill pipeline failed", "error", err)
	}
	for _, w := range warns {
		slog.Warn(w.Message)
	}
	s, err := instruct.LoadWithAgentAndPermissions(h.opts.Config, agent, layer, h.opts.Cwd, h.store.BaseSnapshot())
	if err != nil {
		return "", err
	}
	return s, nil
}

// snapshot returns a consistent view of the handle's mutable state.
func (h *Handle) snapshot() (env, *session.Session) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cur, h.sess
}

// Instruction is the assembled instruction for the current agent, resolved
// fresh on every call. Assembly failures degrade to the empty instruction
// rather than failing the turn.
func (h *Handle) Instruction() string {
	cur, _ := h.snapshot()
	s, err := h.resolveInstruction(cur.agent)
	if err != nil {
		slog.Error("failed to resolve instruction", "error", err)
		return ""
	}
	return s
}

// LoadInstruction resolves the current agent's instruction and returns any
// assembly error. Entry points use it to fail a run at startup when the
// instruction cannot be built (e.g. a missing instruction file).
func (h *Handle) LoadInstruction() error {
	cur, _ := h.snapshot()
	_, err := h.resolveInstruction(cur.agent)
	return err
}

// Client returns the context-wrapped client bound to the current provider,
// model-information source, and session.
func (h *Handle) Client() llm.Client {
	cur, _ := h.snapshot()
	return cur.client
}

// BaseClient returns the raw (unwrapped) client of the active provider.
func (h *Handle) BaseClient() llm.Client {
	cur, _ := h.snapshot()
	return cur.baseClient
}

// Model returns the effective model: an agent-declared model wins outright,
// otherwise the requested (requested/run or provider-default) model. This is
// the single precedence computation every entry point reads.
func (h *Handle) Model() string {
	cur, _ := h.snapshot()
	return runModel(cur)
}

// Registry returns the tool registry scoped to the current agent.
func (h *Handle) Registry() *tools.Registry {
	cur, _ := h.snapshot()
	if cur.registry == nil {
		return h.full
	}
	return cur.registry
}

// Session returns the current session.
func (h *Handle) Session() *session.Session {
	_, sess := h.snapshot()
	return sess
}

// Ledger returns the run's change ledger, or nil when WithLedger was not set.
func (h *Handle) Ledger() *ledger.Ledger {
	return h.ledger
}

// CurrentAgent returns the active agent, or nil for the default flow.
func (h *Handle) CurrentAgent() *config.ResolvedAgent {
	cur, _ := h.snapshot()
	return cur.agent
}

// Provider returns the active provider name.
func (h *Handle) Provider() string {
	cur, _ := h.snapshot()
	return cur.provider
}

// ProviderNames returns the declared provider names in order. A nil source
// reports none.
func (h *Handle) ProviderNames() []string {
	if h.opts.ProviderSource == nil {
		return nil
	}
	return h.opts.ProviderSource.Names()
}

// ProviderCatalog returns the active provider's model catalog.
func (h *Handle) ProviderCatalog(ctx context.Context) ([]llm.Model, error) {
	if h.opts.ProviderSource == nil {
		return nil, errors.New("no provider source")
	}
	return h.opts.ProviderSource.Catalog(ctx, h.Provider())
}

// AgentReg returns the agent registry, or nil when none is configured.
func (h *Handle) AgentReg() *config.AgentReg {
	return h.opts.AgentReg
}

// Commands returns the plugin command source over the loaded plugins.
func (h *Handle) Commands() plugin.CommandSource {
	if h.plug == nil {
		return nil
	}
	return &plugin.ManagerCommands{Manager: h.plug}
}

// Close flushes the ledger and shuts down the plugin manager. It returns the
// first error encountered.
func (h *Handle) Close() error {
	var first error
	if h.ledger != nil {
		if err := h.ledger.Close(); err != nil {
			first = err
		}
	}
	if h.plug != nil {
		h.plug.Shutdown()
	}
	return first
}

// Turn runs one assembled agent turn. It snapshots the handle under a read
// lock, re-resolves the instruction, and streams the reply to out with
// tool framing to errOut.
func (h *Handle) Turn(ctx context.Context, prompt string, out, errOut io.Writer) error {
	cur, sess := h.snapshot()
	if cur.client == nil {
		return errors.New("no provider selected")
	}
	registry := cur.registry
	if registry == nil {
		registry = h.full
	}
	model := runModel(cur)

	var opts []agent.Option
	opts = append(opts, agent.WithHooks(h.life))
	if cur.agent != nil {
		opts = append(opts, agent.WithAgentName(cur.agent.Name))
	}
	opts = append(opts, agent.WithPermissions(cur.gate))

	instruction, ierr := h.resolveInstruction(cur.agent)
	if ierr != nil {
		slog.Error("failed to resolve instruction", "error", ierr)
	}
	return agent.RunTurn(ctx, cur.client, model, instruction, prompt, out, errOut,
		sess, registry, h.ledger, h.opts.Cwd, opts...)
}

// SetModel switches the requested/run model. An agent-declared model continues
// to win where one is active.
func (h *Handle) SetModel(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cur.model = id
}

// NewSession starts a fresh session and rebinds the context client to it. The
// provider and its model are untouched.
func (h *Handle) NewSession() (*session.Session, error) {
	sess, err := session.New(h.opts.Config.SessionDir)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	h.mu.Lock()
	if h.ledger != nil {
		if err := h.ledger.Close(); err != nil {
			slog.Error("failed to flush ledger for previous session", "error", err)
		}
		h.ledger = ledger.New(h.opts.Config.SessionDir, sess.ID)
	}
	h.sess = sess
	h.cur.client = h.wrap(h.cur.baseClient, sess)
	h.mu.Unlock()
	if h.opts.Stderr != nil {
		fmt.Fprintf(h.opts.Stderr, "session: %s\n", sess.ID)
	}
	return sess, nil
}

// SwitchProvider switches the session to the named provider, rebuilding its
// client, re-wrapping it over the same session, and resetting the session
// model to the new provider's default. An unknown name returns an error
// naming the available set and leaves the session untouched.
func (h *Handle) SwitchProvider(name string) error {
	if h.opts.ProviderSource == nil {
		return errors.New("no providers configured")
	}
	names := h.opts.ProviderSource.Names()
	found := false
	for _, n := range names {
		if n == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no such provider: %s (available: %s)", name, strings.Join(names, ", "))
	}
	model, err := h.opts.ProviderSource.DefaultModel(name)
	if err != nil {
		return err
	}
	client, err := h.opts.ProviderSource.Client(name)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.cur.provider = name
	h.cur.baseClient = client
	h.cur.model = model
	h.cur.client = h.wrap(client, h.sess)
	h.mu.Unlock()
	return nil
}

// SwitchAgent resolves and activates the named agent against the discovered
// skill pool: its tool subset becomes the active registry and its
// [permissions] base replaces the global base wholly. Model and instruction
// follow the agent on the next Model()/Instruction() read.
func (h *Handle) SwitchAgent(name string) (*config.ResolvedAgent, error) {
	if h.opts.AgentReg == nil {
		return nil, errors.New("no agents configured")
	}
	resolved, err := h.opts.AgentReg.GetResolved(name, h.opts.Config, h.skillNames)
	if err != nil {
		return nil, err
	}
	if err := h.full.ValidateTools(resolved.Tools); err != nil {
		return nil, fmt.Errorf("agent %q: %w", name, err)
	}
	h.store.SetBaseFromConfig(resolved.EffectiveBase(h.globalBase))
	registry := h.full
	if resolved.Tools != nil {
		registry = h.full.Subset(resolved.Tools)
	}
	h.mu.Lock()
	h.cur.agent = resolved
	h.cur.registry = registry
	h.mu.Unlock()
	return resolved, nil
}

// ResetAgent returns the run to the default flow: no agent, the full tool
// registry, and the global base policy restored. Used by the REPL to end a
// one-shot @name turn.
func (h *Handle) ResetAgent() {
	h.store.SetBaseFromConfig(h.globalBase)
	h.mu.Lock()
	h.cur.agent = nil
	h.cur.registry = h.full
	h.mu.Unlock()
}

// SetNegotiator swaps the permission negotiator behind the gate and the
// request_permission tool. The REPL calls this when its interactive negotiator
// comes up.
func (h *Handle) SetNegotiator(neg permissions.Negotiator) {
	if neg == nil {
		return
	}
	gate := permissions.NewGate(h.store, neg, h.sink)
	h.mu.Lock()
	h.cur.gate = gate
	h.mu.Unlock()
	if h.reqT != nil {
		h.reqT.SetNegotiator(neg)
	}
}

// runModel resolves the effective model from a snapshot: an agent-explicit
// model wins, else the run's current model.
func runModel(cur env) string {
	if cur.agent != nil && cur.agent.HasExplicitModel() {
		return cur.agent.Model
	}
	return cur.model
}
