package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/okayest-dev/genie/internal/agent"
	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/contextmgr"
	"github.com/okayest-dev/genie/internal/gates"
	"github.com/okayest-dev/genie/internal/instruct"
	"github.com/okayest-dev/genie/internal/ledger"
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/modelinfo"
	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/plugin"
	"github.com/okayest-dev/genie/internal/session"
	"github.com/okayest-dev/genie/internal/skill"
	"github.com/okayest-dev/genie/internal/style"
	"github.com/okayest-dev/genie/internal/tasktools"
	"github.com/okayest-dev/genie/internal/theme"
	"github.com/okayest-dev/genie/internal/tokens"
	"github.com/okayest-dev/genie/internal/tools"
	"github.com/okayest-dev/genie/internal/tools/bashtool"
	"github.com/okayest-dev/genie/internal/tools/codetool"
	"github.com/okayest-dev/genie/internal/tools/edittool"
	"github.com/okayest-dev/genie/internal/tools/readtool"
	"github.com/okayest-dev/genie/internal/tools/requesttool"
	"github.com/okayest-dev/genie/internal/tools/skilltool"
	"github.com/okayest-dev/genie/internal/tools/writetool"
	"github.com/okayest-dev/genie/internal/worktree"
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
	// SkillPool is the pre-discovered, config-filtered skill pool. When nil,
	// New will discover it. Pass a pre-computed pool to avoid redundant discovery.
	SkillPool []skill.ParsedSkill
	// Theme and render profiles power live role styling of the turn surface
	// (og-2s0): the model stream and the tool-frame lines render through their
	// roles wherever the profile can display them. All nil leaves the turn
	// output byte-identical plain, so callers that never wired styling get
	// today's bytes.
	Theme         *theme.Theme
	StdoutProfile *style.RenderProfile
	StderrProfile *style.RenderProfile
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

// PromptStatus is the exit status of the last completed turn.
type PromptStatus int

const (
	// PromptStatusUnknown means no turn has completed yet.
	PromptStatusUnknown PromptStatus = iota
	// PromptStatusOK means the last turn completed successfully.
	PromptStatusOK
	// PromptStatusError means the last turn ended with an error.
	PromptStatusError
	// PromptStatusCancelled means the last turn was cancelled by the user.
	PromptStatusCancelled
)

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

	// bound is the agent's bound pool as of the last resolveInstruction — the
	// pool the skill tool resolves names against. Guarded by mu.
	bound []skill.ParsedSkill

	plug         *plugin.Manager
	ctxSeam      *plugin.ContextSeam
	life         *plugin.LifecycleSeam
	counter      tokens.Counter
	buildCtxOpts func(base llm.Client, modelID string) []contextmgr.Option

	taskState *tasktools.TaskState
	tracker   tasktools.Tracker
	// baseCwd is the run's original working root, restored after a task's
	// worktree is torn down. Guarded by mu.
	baseCwd         string
	worktreeManager worktree.ManagerInterface
	gateRunner      *gates.GateRunnerImpl
	evidenceLedger  gates.EvidenceLedger

	claimTool   *tasktools.TaskClaimTool
	resolveTool *tasktools.TaskResolveTool
	resetTool   *tasktools.SessionResetTool

	sess   *session.Session
	ledger *ledger.Ledger

	degrade func(string)

	// Token accumulation and prompt status for the idle prompt bar.
	// Guarded by mu.
	totalTokens      int
	lastPromptStatus PromptStatus

	// Git gatherer for the prompt git segment.
	gitGatherer *gitGatherer

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
	// The active-task state is created before the tools so the guarded bash
	// tool can share it.
	h.baseCwd = opts.Cwd
	h.taskState = tasktools.NewTaskState()
	full := tools.NewRegistry()
	h.installCwdTools(full, opts.Cwd)
	reqT := requesttool.New(store, h.sink)
	reqT.SetNegotiator(opts.Negotiator)
	full.Register(reqT)
	full.Register(skilltool.New(h))

	// Task tools: register if enabled in config
	if opts.Config.TaskTools.Enable {
		// Get tracker from plugin manager
		var tracker tasktools.Tracker
		if h.plug != nil {
			tracker = h.plug.GetTrackerSource()
		}
		if tracker == nil {
			// No tracker available (no plugin manager or no tracker plugin)
			if opts.Stdin != nil && opts.Stdout != nil {
				// For non-test runs without plugin manager, use fake tracker
				tracker = tasktools.NewFakeTracker(nil)
			} else {
				tracker = tasktools.NewFakeTracker(nil)
			}
		}
		h.tracker = tracker
		slog.Info("tracker resolved", "isolation", tracker.Isolation().String())

		// Create evidence ledger
		ledgerPath := filepath.Join(opts.Config.SessionDir, "gates")
		var evidenceLedger gates.EvidenceLedger
		fileLedger, err := gates.NewFileEvidenceLedger(ledgerPath)
		if err != nil {
			// Fall back to in-memory ledger
			evidenceLedger = gates.NewInMemoryEvidenceLedger()
		} else {
			evidenceLedger = fileLedger
		}
		h.evidenceLedger = evidenceLedger

		// Create follow-up engine for dedup guard and priority shaping (og-hd8)
		followupMetrics := &tasktools.FollowupMetrics{}
		followupEngine := tasktools.NewFollowupEngine(tracker, opts.Config.TaskTools.Followup, followupMetrics)

		// Create ticket creator for gate deferral with follow-up engine
		ticketCreator := NewTrackerTicketCreatorWithFollowup(tracker, followupEngine)

		// Create deferred finding store
		deferredFindingStore := gates.NewInMemoryDeferredFindingStore()

		claimTool := tasktools.NewTaskClaimTool(tracker, h.taskState, true, opts.Config.TaskTools.CommandTimeout)
		h.claimTool = claimTool
		full.Register(claimTool)

		// Worktree isolation (og-wm7.3): when policy asks for it and the run is
		// in a git repo, claim allocates a task worktree and rebinds the session
		// cwd. Non-git repos are exempt (allocation is skipped). Configure this
		// before building the gate runner so the resolve-time git gate (og-wm7.3.3)
		// receives the manager.
		h.configureWorktree(claimTool, opts.Config.Worktree.Policy)

		// Create gate registry and runner
		gateRegistry := gates.NewDefaultGateRegistry()

		gateRunner := gates.NewGateRunner(gateRegistry, evidenceLedger, opts.Config.Gates, h.taskState.EvidenceTracker().Observed(), ticketCreator, deferredFindingStore, h.worktreeManager)
		h.gateRunner = gateRunner

		// Task resolve tool with gate runner
		resolveTool := tasktools.NewTaskResolveTool(tracker, h.taskState, gateRunner, true, opts.Config.TaskTools.CommandTimeout)
		h.resolveTool = resolveTool
		full.Register(resolveTool)
		resolveTool.SetRestoreCwd(h.RestoreCwd)
		resolveTool.SetWorktreeTeardown(h.TeardownWorktree)

		// Task create tool with follow-up engine
		createTool := tasktools.NewTaskCreateToolWithFollowup(tracker, followupEngine, true, opts.Config.TaskTools.CommandTimeout)
		full.Register(createTool)

		// Task progress tool
		progressTool := tasktools.NewTaskProgressTool(tracker, h.taskState, true, opts.Config.TaskTools.CommandTimeout)
		full.Register(progressTool)

		// Task defer finding tool
		deferFindingTool := tasktools.NewTaskDeferFindingTool(h.taskState, true, opts.Config.TaskTools.CommandTimeout)
		full.Register(deferFindingTool)

		// Task review tool (review gate findings channel)
		reviewTool := tasktools.NewTaskReviewTool(h.taskState, true, opts.Config.TaskTools.CommandTimeout)
		full.Register(reviewTool)

		// Session reset tool
		resetTool := tasktools.NewSessionResetTool(h.taskState, true, opts.Config.TaskTools.CommandTimeout, h)
		full.Register(resetTool)
	}

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

	// Code tool: register by default, but if Deno is absent, disable and warn.
	// The tool remains registered (so its description reflects runtime) but
	// disabled. The message additionally points at re-enabling bash if bash
	// is also disabled.
	if !opts.Config.Tools.Code {
		full.Disable("code")
	} else if !codetool.IsDenoAvailable() {
		full.Disable("code")
		msg := "code tool disabled: Deno not found on PATH"
		if !opts.Config.Tools.Bash {
			msg += "; re-enable bash via config tools.bash to retain an execution path"
		}
		if opts.Stderr != nil {
			fmt.Fprintf(opts.Stderr, "%s\n", msg)
		}
	}
	h.full = full
	h.reqT = reqT

	// Resolve the skill pool once so agent validation checks explicit skills
	// lists against the discovered pool; dubious directories surface now.
	// Use the pre-computed pool from Options if provided (passed from main
	// to avoid redundant discovery at startup).
	var pool []skill.ParsedSkill
	if len(opts.SkillPool) > 0 {
		pool = opts.SkillPool
	} else {
		var warns []skill.Warning
		var err error
		pool, warns, err = skill.FilteredPool(opts.Config.Skills.Dirs, opts.Config.Skills.Enable, opts.Config.Skills.Disable)
		if err != nil {
			return nil, err
		}
		for _, w := range warns {
			if opts.Stderr != nil && opts.Theme != nil && opts.StderrProfile != nil {
				warnOut := opts.Theme.RoleWriter(opts.Stderr, opts.StderrProfile, "warning")
				fmt.Fprintf(warnOut, "warning: %s\n", w.Message)
			} else if opts.Stderr != nil {
				fmt.Fprintf(opts.Stderr, "warning: %s\n", w.Message)
			}
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
	//
	// The hook circuit-breaker policy and its notice sink are installed here
	// rather than on either seam: a plugin's breaker state is per (plugin, event)
	// and spans both seams, so a trip noticed in one has to be reported through
	// one sink as one line.
	plug := plugin.NewManager(opts.Config.PluginDir, opts.Config.PluginEnable, opts.Config.PluginDisable, scoped,
		plugin.WithHookBreaker(plugin.HookBreakerPolicy{
			Threshold: opts.Config.Hooks.FailureThreshold,
			Recovery:  opts.Config.Hooks.Recovery,
		}, func(msg string) { h.degrade("plugin hooks degraded: " + msg) }))
	if err := plug.LoadPlugins(); err != nil {
		return nil, err
	}
	h.plug = plug

	if opts.OnUsageDegrade != nil {
		h.degrade = opts.OnUsageDegrade
	} else if opts.Theme != nil && opts.Stderr != nil && opts.StderrProfile != nil {
		// Default degradation sink uses the warning role
		warnOut := opts.Theme.RoleWriter(opts.Stderr, opts.StderrProfile, "warning")
		h.degrade = func(msg string) { fmt.Fprintln(warnOut, msg) }
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
	h.buildCtxOpts = func(base llm.Client, modelID string) []contextmgr.Option {
		var infoSource modelinfo.Source
		if p, ok := base.(llm.ModelInfoProvider); ok {
			infoSource = p
		}
		resolver := modelinfo.New(infoSource, opts.Config.Context.Windows, modelinfo.Options{
			BudgetTokens:  opts.Config.Context.BudgetTokens,
			BudgetPercent: opts.Config.Context.BudgetPercent,
		})
		// Seed the resolver with the default window for the current model if
		// no override exists and no provider window is available. This ensures
		// the budget is non-zero from turn one, enabling compaction.
		if modelID != "" && opts.Config.Context.DefaultWindowTokens > 0 {
			if _, hasOverride := opts.Config.Context.Windows[modelID]; !hasOverride {
				resolver.Seed(modelID, opts.Config.Context.DefaultWindowTokens)
			}
		}
		optsList := []contextmgr.Option{
			contextmgr.WithTurns(opts.Config.Context.Turns),
			contextmgr.WithCounter(h.counter),
			contextmgr.WithResolver(resolver),
			contextmgr.WithHooks(ctxSeam),
			contextmgr.WithOnDegrade(ctxDegrade),
			contextmgr.WithCondenseSize(opts.Config.Context.CondenseSize),
			contextmgr.WithNetDrop(opts.Config.Context.NetDrop),
		}
		// Attach summarizer when summary_model is configured.
		if opts.Config.Context.SummaryModel != "" {
			summarizer := makeSummarizer(base, opts.Config.Context.SummaryModel, resolver, h.counter, ctxDegrade)
			optsList = append(optsList, contextmgr.WithSummarizer(summarizer))
		}
		return optsList
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
		if opts.Theme != nil && opts.StderrProfile != nil {
			bannerOut := opts.Theme.RoleWriter(opts.Stderr, opts.StderrProfile, "banner")
			fmt.Fprintf(bannerOut, "session: %s\n", sess.ID)
		} else {
			fmt.Fprintf(opts.Stderr, "session: %s\n", sess.ID)
		}
	}

	// Initialize git gatherer for the prompt git segment.
	h.gitGatherer = newGitGatherer(opts.Cwd)

	// Provide session to task tools for boundary compaction tracking.
	if h.claimTool != nil {
		h.claimTool.SetSession(sess)
	}
	if h.resolveTool != nil {
		h.resolveTool.SetSession(sess)
	}

	// Task state transcript mirror: write task state changes as metadata markers.
	h.taskState.SetOnChange(func(data []byte) error {
		return h.sess.AppendWithMeta(llm.Message{}, map[string]string{"task_state": string(data)})
	})

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
		client:     h.wrap(baseClient, sess, model),
		model:      model,
		agent:      opts.Agent,
		registry:   scoped,
		gate:       permissions.NewGate(store, opts.Negotiator, opts.PermanentSink),
	}
	return h, nil
}

// installCwdTools (re)registers the working-directory-bound tools — read,
// write, edit, bash (guarded when task tools are enabled), and code — rooted at
// cwd. It is called at assembly and again on every cwd rebind so the tools that
// resolve relative paths track the session's working root.
func (h *Handle) installCwdTools(reg *tools.Registry, cwd string) {
	reg.Register(readtool.New(cwd))
	reg.Register(writetool.New(cwd))
	reg.Register(edittool.New(cwd))
	baseBash := bashtool.New(cwd, h.opts.Config.BashTimeout)
	if h.opts.Config.TaskTools.Enable {
		reg.Register(bashtool.NewGuardedBashTool(baseBash, h.taskState, h.opts.Config.TaskTools.TrackerGuardPolicy))
	} else {
		reg.Register(baseBash)
	}
	reg.Register(codetool.New(h.store, cwd, h.opts.Config.Tools.CodeTimeout))
}

// configureWorktree wires claim-time worktree allocation when the policy asks
// for it and opts.Cwd is inside a git repository. A non-git working directory
// leaves the claim tool with no allocator (the policy is exempt), matching the
// epic's "non-git repos are exempt" rule.
func (h *Handle) configureWorktree(claim *tasktools.TaskClaimTool, policy string) {
	if policy != worktree.PolicyRequired && policy != worktree.PolicyOptional {
		return
	}
	root, ok, err := worktree.DetectRoot(context.Background(), h.opts.Cwd)
	if err != nil {
		slog.Error("worktree: detect repo root failed", "error", err)
		return
	}
	if !ok {
		slog.Info("worktree.policy set but working directory is not a git repo; worktrees disabled", "cwd", h.opts.Cwd)
		return
	}
	manager := worktree.New(root, h.opts.Config.Worktree)
	h.worktreeManager = manager
	claim.ConfigureWorktree(policy, manager, h.RebindCwd)
	slog.Info("worktree policy active", "policy", policy, "repo", root, "path", h.opts.Config.Worktree.Path)
}

// RebindCwd moves the session's working root to root: the cwd-bound tools are
// re-registered against it, the agent-scoped registry is rebuilt, and the
// permission store re-roots so relative base scopes follow. It is the
// claim-time worktree hook (og-wm7.3).
func (h *Handle) RebindCwd(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve worktree path %s: %w", root, err)
	}
	h.mu.Lock()
	h.opts.Cwd = abs
	h.installCwdTools(h.full, abs)
	if cur := h.cur; cur.agent != nil && cur.agent.Tools != nil {
		h.cur.registry = h.full.Subset(cur.agent.Tools)
	} else {
		h.cur.registry = h.full
	}
	h.mu.Unlock()
	h.store.SetCwd(abs)
	// While the session lives in a task worktree, keep the original checkout
	// reachable for reads — tracker state, domain docs — even though writes
	// stay confined to the worktree (og-wm7.3). Restoring to the original
	// checkout drops the extra root again.
	if h.baseCwd != "" && abs != h.baseCwd {
		h.store.AddReadRoot(h.baseCwd)
	} else {
		h.store.ClearReadRoots()
	}
	slog.Info("session cwd rebound", "cwd", abs)
	return nil
}

// RestoreCwd moves the session's working root back to the run's original
// checkout. It is the resolve-time counterpart to RebindCwd.
func (h *Handle) RestoreCwd() error {
	return h.RebindCwd(h.baseCwd)
}

// TeardownWorktree tears down the task's worktree after check-in. It is called
// after the session cwd has been restored to the main checkout and the active
// task has been cleared, so the task id is passed in explicitly.
func (h *Handle) TeardownWorktree(taskID string) error {
	if h.worktreeManager == nil || taskID == "" {
		return nil
	}
	return h.worktreeManager.Teardown(context.Background(), taskID)
}

// wrap builds the context-wrapped client for a base client and session,
// sourcing modelinfo from the base client per ADR-0004.
func (h *Handle) wrap(base llm.Client, sess *session.Session, modelID string) llm.Client {
	if base == nil {
		return nil
	}
	return contextmgr.New(base, sess, h.buildCtxOpts(base, modelID)...)
}

// makeSummarizer creates a summarizer function that uses the base client
// (below the ContextManager) to avoid recursion. It disables tools and caps
// output tokens.
func makeSummarizer(base llm.Client, summaryModel string, resolver *modelinfo.Resolver, counter tokens.Counter, onDegrade func(string)) contextmgr.Summarizer {
	return func(ctx context.Context, in contextmgr.SummaryInput) (string, error) {
		// Resolve the summary model from the active provider's catalog.
		// If the configured model is not found, use the base client's model.
		model := in.Model
		if summaryModel != "" {
			model = summaryModel
		}
		// Build a request with no tools, no history, just the material.
		req := llm.Request{
			Model:    model,
			Messages: in.Material,
			Tools:    nil, // tools disabled for summarization
		}
		// Add a system prompt for the summarizer.
		var sysPrompt string
		switch in.Purpose {
		case contextmgr.SummaryPurposeCompact:
			sysPrompt = `Summarize the following evicted conversation turns into a compact summary that preserves: task/goal, decisions made, files/commands touched, current state, and what remains. Keep it under ` + fmt.Sprintf("%d", in.Target) + ` tokens. Output only the summary.`
		case contextmgr.SummaryPurposeBrief:
			// The brief prompt is already in the material (from buildBriefPrompt).
			// Just ensure we respect the target.
			sysPrompt = `Write the handoff brief. Output ONLY the markdown document.`
		}
		if sysPrompt != "" {
			req.Messages = append([]llm.Message{{Role: llm.RoleSystem, Content: sysPrompt}}, req.Messages...)
		}

		// Stream the response and collect text.
		stream, err := base.Stream(ctx, req)
		if err != nil {
			return "", err
		}
		var builder strings.Builder
		for ev := range stream {
			if ev.Kind == llm.EventText {
				builder.WriteString(ev.Text)
			}
		}
		output := builder.String()
		if output == "" {
			return "", fmt.Errorf("empty summarizer output")
		}
		return output, nil
	}
}

// Summarize generates a handoff brief for the current session and writes it
// to the session directory as brief-N.md. It returns the path to the written
// file. The brief includes the model-written body plus the Files Touched
// section populated from the change ledger.
func (h *Handle) Summarize(ctx context.Context) (string, error) {
	cur, sess := h.snapshot()
	if cur.client == nil {
		return "", errors.New("no provider selected")
	}
	// Get the context manager to call its Summarize method.
	cm, ok := cur.client.(*contextmgr.ContextManager)
	if !ok {
		return "", errors.New("client is not a ContextManager")
	}
	model := h.Model()
	body, err := cm.Summarize(ctx, model)
	if err != nil {
		return "", err
	}

	// Build the full brief with ledger section.
	var brief strings.Builder
	brief.WriteString(fmt.Sprintf("# Handoff brief — %s: ", sess.ID))
	// Extract one-line goal from the first user message (first turn).
	if h.ledger != nil {
		// We'll add the goal from the session
		lines, _ := sess.Lines()
		goal := "work session"
		for _, line := range lines {
			if line.Role == "user" && line.Content != "" {
				goal = line.Content
				if len(goal) > 80 {
					goal = goal[:80] + "..."
				}
				break
			}
		}
		brief.WriteString(goal)
	} else {
		brief.WriteString("work session")
	}
	brief.WriteString("\n\n")
	brief.WriteString(body)
	brief.WriteString("\n\n## Files touched\n")
	if h.ledger != nil {
		batches, err := ledger.LoadBatches(h.opts.Config.SessionDir, sess.ID)
		if err == nil && len(batches) > 0 {
			seen := make(map[string]bool)
			for _, batch := range batches {
				for _, f := range batch.Files {
					if !seen[f.Path] {
						brief.WriteString(fmt.Sprintf("- %s (%s, +%d/-%d)\n", f.Path, f.Ops, f.Delta.Added, f.Delta.Removed))
						seen[f.Path] = true
					}
				}
			}
		} else {
			brief.WriteString("none\n")
		}
	} else {
		brief.WriteString("none\n")
	}

	// Write to session dir as brief-N.md.
	sessionDir := h.opts.Config.SessionDir
	if sessionDir == "" {
		sessionDir = h.opts.Cwd
	}
	n := 1
	for {
		path := filepath.Join(sessionDir, fmt.Sprintf("brief-%d.md", n))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(brief.String()), 0o644); err != nil {
				return "", fmt.Errorf("write brief: %w", err)
			}
			return path, nil
		}
		n++
	}
}

// Bound implements skilltool.Pool over the agent's bound skill set. It is
// registered on the handle so the skill tool can validate a name without
// re-running discovery, and so a skill the agent did not bind is unreachable
// even if it is enabled in config.
func (h *Handle) Bound() []skill.ParsedSkill {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.bound
}

// codeToolInstruction is the paragraph injected into the agent instruction
// when the code tool is enabled. It tells the model how to request permissions,
// what happens on denial, and that the base-policy snapshot is the source of truth.
const codeToolInstruction = "\nWhen using the code tool, request the permission axes you need via the permissions field (read, write, net, run, env). Your requests are intersected with the effective policy — you will see what was granted. On a denial, do not retry the call; instead, call request_permission to ask in advance, or work within the granted scope. Do not re-attempt a permission you personally rejected. The current permissions snapshot above is the authoritative source."

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
	filtered, warns, err := skill.FilteredPool(h.opts.Config.Skills.Dirs, h.opts.Config.Skills.Enable, h.opts.Config.Skills.Disable)
	if err != nil {
		return "", err
	}
	bound, err := skill.BindToAgent(filtered, agentSkills, agentName)
	if err != nil {
		return "", err
	}
	// Publish the bound set so the skill tool resolves against what this agent
	// actually has, and build the layer from the same slice — the index and the
	// tool's pool then describe one set, never two that drifted.
	h.mu.Lock()
	h.bound = bound
	h.mu.Unlock()
	layer := skill.BuildSkillLayer(bound)
	for _, w := range warns {
		slog.Warn(w.Message)
	}
	s, err := instruct.LoadWithAgentAndPermissions(h.opts.Config, agent, layer, h.opts.Cwd, h.store.BaseSnapshot())
	if err != nil {
		return "", err
	}

	// Inject code tool instruction paragraph when the tool is enabled.
	// The paragraph tells the model to request needed axes in permissions;
	// know requests are intersected with effective policy and it sees what
	// was granted; on a denial, NOT retry but ask via request_permission or
	// work within the granted scope; and not re-attempt a personally rejected
	// permission. The existing base-policy snapshot stays the mechanism's
	// source of truth.
	if !h.full.IsDisabled("code") {
		s += codeToolInstruction
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

// TaskState returns the active task state for the session.
func (h *Handle) TaskState() *tasktools.TaskState {
	return h.taskState
}

// GateRunner returns the gate runner for this run.
func (h *Handle) GateRunner() gates.GateRunner {
	return h.gateRunner
}

// WorktreeManager returns the worktree manager for this run.
func (h *Handle) WorktreeManager() worktree.ManagerInterface {
	return h.worktreeManager
}

// Provider returns the active provider name.
func (h *Handle) Provider() string {
	cur, _ := h.snapshot()
	return cur.provider
}

// Cwd returns the working directory for this run.
func (h *Handle) Cwd() string {
	return h.opts.Cwd
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

	// Live role styling (og-2s0): wrap the model stream in its role and route
	// the tool-frame lines through the tool role. A theme that leaves a role
	// unmapped renders plain; a profile that cannot display colour drops the
	// bytes through verbatim. With no styling wired (nil theme/profiles), the
	// turn output is exactly today's plain text.
	if h.opts.Theme != nil && h.opts.StdoutProfile != nil {
		out = h.opts.Theme.RoleWriter(out, h.opts.StdoutProfile, "model")
	}
	if h.opts.Theme != nil && h.opts.StderrProfile != nil {
		opts = append(opts, agent.WithToolFrame(h.opts.Theme.RoleWriter(errOut, h.opts.StderrProfile, "tool")))
	}

	instruction, ierr := h.resolveInstruction(cur.agent)
	if ierr != nil {
		slog.Error("failed to resolve instruction", "error", ierr)
	}

	usage, err := agent.RunTurn(ctx, cur.client, model, instruction, prompt, out, errOut,
		sess, registry, h.ledger, h.opts.Cwd, opts...)

	// Update token accumulator and prompt status on turn completion.
	// Only completed turns count; in-flight turn is not counted.
	// On error or cancel, we still record the usage if the turn completed,
	// but the status reflects the outcome.
	h.mu.Lock()
	if err == nil {
		h.totalTokens += usage.TotalTokens
		h.lastPromptStatus = PromptStatusOK
	} else if errors.Is(err, context.Canceled) {
		h.lastPromptStatus = PromptStatusCancelled
	} else {
		h.lastPromptStatus = PromptStatusError
	}
	h.mu.Unlock()

	// Notify git gatherer that a turn completed (triggers degraded recovery).
	if h.gitGatherer != nil {
		h.gitGatherer.NotifyTurnCompleted()
	}

	return err
}

// TotalTokens returns the accumulated token count from completed turns.
func (h *Handle) TotalTokens() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.totalTokens
}

// LastPromptStatus returns the exit status of the last completed turn.
func (h *Handle) LastPromptStatus() PromptStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastPromptStatus
}

// GitInfo returns the current git branch and state for the prompt segment.
func (h *Handle) GitInfo() GitInfo {
	if h.gitGatherer == nil {
		return GitInfo{Valid: false}
	}
	return h.gitGatherer.Gather()
}

// NotifyGitTurnCompleted notifies the git gatherer that a turn or slash command
// has completed, triggering recovery from degraded mode.
func (h *Handle) NotifyGitTurnCompleted() {
	if h.gitGatherer != nil {
		h.gitGatherer.NotifyTurnCompleted()
	}
}

// SetModel switches the requested/run model. An agent-declared model continues
// to win where one is active.
func (h *Handle) SetModel(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cur.model == id {
		return
	}
	h.cur.model = id
	// Re-wrap the client with the new model to seed the resolver with the
	// default window for the new model.
	if h.cur.baseClient != nil {
		h.cur.client = h.wrap(h.cur.baseClient, h.sess, id)
	}
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
	h.cur.client = h.wrap(h.cur.baseClient, sess, h.cur.model)
	h.mu.Unlock()
	if h.opts.Stderr != nil {
		if h.opts.Theme != nil && h.opts.StderrProfile != nil {
			bannerOut := h.opts.Theme.RoleWriter(h.opts.Stderr, h.opts.StderrProfile, "banner")
			fmt.Fprintf(bannerOut, "session: %s\n", sess.ID)
		} else {
			fmt.Fprintf(h.opts.Stderr, "session: %s\n", sess.ID)
		}
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
	h.cur.client = h.wrap(client, h.sess, model)
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
	// The skill pool is derived from the resolved agent on the next turn, so
	// nothing about the previous agent's bindings needs tearing down here.
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
	// Same reasoning as SwitchAgent.
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

// gitGatherer handles gathering git branch and dirty state for the prompt segment.
// It implements the degraded path with timeout and recovery as per ADR-0011.
type gitGatherer struct {
	cwd           string
	mu            sync.Mutex
	degraded      bool
	lastTurnCount int
}

// MuLock locks the gatherer's mutex for testing.
func (g *gitGatherer) MuLock() {
	g.mu.Lock()
}

// MuUnlock unlocks the gatherer's mutex for testing.
func (g *gitGatherer) MuUnlock() {
	g.mu.Unlock()
}

// GitInfo holds the result of a git gather operation.
type GitInfo struct {
	Branch string
	State  string // "clean", "dirty", "unknown"
	Valid  bool   // false means collapse the segment
}

// newGitGatherer creates a new git gatherer for the given working directory.
func newGitGatherer(cwd string) *gitGatherer {
	return &gitGatherer{
		cwd: cwd,
	}
}

// gatherTimeout is the fixed internal deadline for the full git scan.
// Per ADR-0011, this is not configurable; initial value ~150ms.
const gatherTimeout = 150 * time.Millisecond

// Gather returns the current git branch and state for the prompt.
// It implements the healthy path, degraded path, and recovery logic.
func (g *gitGatherer) Gather() GitInfo {
	g.mu.Lock()
	defer g.mu.Unlock()

	// If degraded, try the cheap path (symbolic-ref)
	if g.degraded {
		branch, ok := g.gatherCheap()
		if !ok {
			// Cheap path failed - collapse segment
			return GitInfo{Valid: false}
		}
		// Cheap path succeeded - return branch with unknown state
		return GitInfo{
			Branch: branch,
			State:  "unknown",
			Valid:  true,
		}
	}

	// Healthy path: try full scan with timeout
	done := make(chan GitInfo, 1)
	go func() {
		done <- g.gatherFull()
	}()

	select {
	case info := <-done:
		return info
	case <-time.After(gatherTimeout):
		// Deadline exceeded - enter degraded mode
		g.degraded = true
		g.lastTurnCount = 0 // Will be incremented on next turn
		// Try cheap path immediately for this draw
		branch, ok := g.gatherCheap()
		if !ok {
			return GitInfo{Valid: false}
		}
		return GitInfo{
			Branch: branch,
			State:  "unknown",
			Valid:  true,
		}
	}
}

// gatherFull runs the full git status scan.
// Returns branch name and clean/dirty state.
func (g *gitGatherer) gatherFull() GitInfo {
	ctx, cancel := context.WithTimeout(context.Background(), gatherTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v1", "-b", "--untracked-files=all")
	cmd.Dir = g.cwd
	output, err := cmd.Output()
	if err != nil {
		// Git command failed - not a repo, git missing, permission error, etc.
		return GitInfo{Valid: false}
	}

	return g.parseGitStatus(string(output))
}

// gatherCheap runs the cheap git symbolic-ref --short HEAD path.
// Returns branch name (or "HEAD" for detached) and success flag.
func (g *gitGatherer) gatherCheap() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gatherTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = g.cwd
	output, err := cmd.Output()
	if err != nil {
		// symbolic-ref fails on detached HEAD (exit 128), non-repo, git absent, permission error
		return "", false
	}
	branch := strings.TrimSpace(string(output))
	if branch == "" {
		return "", false
	}
	return branch, true
}

// parseGitStatus parses the output of git status --porcelain=v1 -b --untracked-files=all.
// First line is ## <branch>...<remote> [ahead N] [behind N]
// Subsequent lines are entries (staged, unstaged, conflict, untracked).
func (g *gitGatherer) parseGitStatus(output string) GitInfo {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return GitInfo{Valid: false}
	}

	// Parse branch from first line
	firstLine := lines[0]
	if !strings.HasPrefix(firstLine, "## ") {
		return GitInfo{Valid: false}
	}

	branch := strings.TrimPrefix(firstLine, "## ")
	// Strip remote tracking info: <branch>...<remote> [ahead N] [behind N]
	if idx := strings.Index(branch, "..."); idx >= 0 {
		branch = branch[:idx]
	}
	// Strip [ahead N] [behind N] suffixes
	branch = stripAheadBehind(branch)
	branch = strings.TrimSpace(branch)

	// Detached HEAD shows as "HEAD (no branch)" or similar
	if branch == "HEAD (no branch)" || branch == "HEAD" {
		branch = "HEAD"
	}

	// Check for dirtiness: any line after the first is an entry
	dirty := len(lines) > 1

	state := "clean"
	if dirty {
		state = "dirty"
	}

	return GitInfo{
		Branch: branch,
		State:  state,
		Valid:  true,
	}
}

// stripAheadBehind removes [ahead N] and [behind N] suffixes from branch string.
func stripAheadBehind(s string) string {
	// Remove [ahead N]
	for {
		idx := strings.Index(s, "[ahead ")
		if idx < 0 {
			break
		}
		end := strings.Index(s[idx:], "]")
		if end < 0 {
			break
		}
		s = s[:idx] + s[idx+end+1:]
	}
	// Remove [behind N]
	for {
		idx := strings.Index(s, "[behind ")
		if idx < 0 {
			break
		}
		end := strings.Index(s[idx:], "]")
		if end < 0 {
			break
		}
		s = s[:idx] + s[idx+end+1:]
	}
	return strings.TrimSpace(s)
}

// NotifyTurnCompleted notifies the git gatherer that a turn has completed.
// This triggers recovery from degraded mode on the next Gather call.
func (g *gitGatherer) NotifyTurnCompleted() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.degraded {
		g.lastTurnCount++
		if g.lastTurnCount >= 1 {
			// First draw after a turn - exit degraded mode
			g.degraded = false
			g.lastTurnCount = 0
		}
	}
}
