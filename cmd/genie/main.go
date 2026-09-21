package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/okayest-dev/genie/internal/agent"
	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/contextmgr"
	"github.com/okayest-dev/genie/internal/instruct"
	"github.com/okayest-dev/genie/internal/ledger"
	"github.com/okayest-dev/genie/internal/llm"
	_ "github.com/okayest-dev/genie/internal/llm/anthropic"
	_ "github.com/okayest-dev/genie/internal/llm/bedrock"
	_ "github.com/okayest-dev/genie/internal/llm/copilot"
	_ "github.com/okayest-dev/genie/internal/llm/google"
	_ "github.com/okayest-dev/genie/internal/llm/openai"
	_ "github.com/okayest-dev/genie/internal/llm/responses"
	"github.com/okayest-dev/genie/internal/modelinfo"
	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/plugin"
	"github.com/okayest-dev/genie/internal/repl"
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

const usage = `usage: genie [-v] [-d] [-a agent] [-p prompt]

genie is a minimal terminal agent harness.

Flags:
  -a agent      load a named agent definition for this run
  -p prompt     run a single prompt, print the reply to stdout, and exit
  -approve-all  in headless (-p) mode, approve every escalation instead of
                auto-denying; refused otherwise, never persists to config
  -v            verbose output: high-level flow to stderr
  -d            debug output: low-level detail to stderr (implies -v)

Environment:
  GENIE_DEBUG    enable debug mode (true/1/yes)

Without -p, genie starts an interactive REPL.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	// Go's flag package treats the token after -p as its value, so
	// "-p --approve-all <prompt>" would swallow the boolean flag as the
	// prompt. Hoist the approval flag (single- or double-dash) above -p so it
	// parses and -p still takes the value that follows (og-uy5.6).
	args = hoistApproveAllAfterPrompt(args)

	// Pre-scan for -p without a value (e.g. "genie -p" or "genie -p -").
	// Go's flag package requires a value after -p, so we detect the
	// stdin-reading cases before handing off to flag.Parse.
	pFlag := ""
	pSeen := false
	var cleanArgs []string
	skipNext := false
	for i, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if a == "-p" || a == "--prompt" {
			pSeen = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				pFlag = args[i+1]
				cleanArgs = append(cleanArgs, a, args[i+1])
				skipNext = true
			}
			// else: -p with no value — don't add to cleanArgs
			continue
		}
		if strings.HasPrefix(a, "-p=") || strings.HasPrefix(a, "--prompt=") {
			pSeen = true
			pFlag = strings.SplitN(a, "=", 2)[1]
			cleanArgs = append(cleanArgs, a)
			continue
		}
		cleanArgs = append(cleanArgs, a)
	}

	// If -p was seen but no value, read from stdin.
	var stdinPrompt string
	if pSeen && pFlag == "" {
		var ok bool
		stdinPrompt, ok = readStdinPrompt(stderr)
		if !ok {
			return 3
		}
	}

	fs := flag.NewFlagSet("genie", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	prompt := fs.String("p", "", "run a single prompt")
	agentFlag := fs.String("a", "", "agent definition to load for this run")
	verbose := fs.Bool("v", false, "verbose output")
	debug := fs.Bool("d", false, "debug output (implies -v)")
	approveAll := fs.Bool("approve-all", false, "approve all escalations for a headless (-p) run")
	if err := fs.Parse(cleanArgs); err != nil {
		return 3
	}

	// If stdinPrompt was set, use it as the prompt.
	if stdinPrompt != "" {
		*prompt = stdinPrompt
	}

	// --approve-all is headless-only: in the interactive REPL there is a user
	// to ask, and a blanket approval has no single-turn scope to expire in.
	// Refuse rather than silently approve everything (og-uy5.6).
	if *approveAll && *prompt == "" {
		fmt.Fprintln(stderr, "Error: --approve-all requires headless mode; pass a prompt with -p")
		return 3
	}
	// The headless negotiator: auto-deny by default, blanket-approve under
	// --approve-all. Either way it routes through the same single-turn,
	// in-memory policy store, so nothing is ever persisted.
	headlessNeg := headlessNegotiator(*approveAll)

	debugEnv := isTruthy(os.Getenv("GENIE_DEBUG"))
	debug = boolPtr(*debug || debugEnv)
	verbose = boolPtr(*verbose || *debug)

	configureSlog(stderr, *verbose, *debug)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	// Build the effective-policy store from the resolved config surface and
	// the permanent grants. Writes/net/run/env start uncovered under the
	// restrictive default, so they escalate at the deny point.
	permStore, err := buildPermissionStore(cwd, cfg.Permissions)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	// Permanent grants persist to the config file; if the path cannot be
	// resolved they stay in-memory for the run.
	var permSink permissions.PermanentSink
	if path, err := config.Path(); err == nil {
		permSink = func(g permissions.Grant) error {
			_, err := config.ApplyPermanentGrant(path, config.PermanentGrant{
				Permission: string(g.Axis),
				Scope:      g.Scope,
				Granted:    g.Granted,
			})
			return err
		}
	} else {
		slog.Warn("permanent grants will not persist", "error", err)
	}

	// Build the tool registry from config. request_permission is registered
	// always-on (a negotiation channel, not a capability) and seeded with the
	// headless negotiator; the REPL swaps in the interactive one.
	registry := buildRegistry(cwd, cfg.Tools, cfg.BashTimeout, permStore, permSink, headlessNeg)

	// Resolve the skill pool once so agent resolution validates explicit
	// skills lists against real discovered skills (and inheritance binds the
	// whole pool rather than an empty list), then bind the same pool into the
	// instruction layer below.
	skillPool, poolWarns, err := skill.FilteredPool(cfg.Skills.Dirs, cfg.Skills.Enable, cfg.Skills.Disable)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	for _, w := range poolWarns {
		fmt.Fprintf(stderr, "warning: %s\n", w.Message)
	}
	skillNames := skill.Names(skillPool)

	// Resolve the agent for this run.
	var runAgent *config.ResolvedAgent
	agentName := *agentFlag
	if agentName == "" && cfg.DefaultAgent != "" {
		agentName = cfg.DefaultAgent
	}

	if agentName != "" {
		agentReg := resolveAgentReg(cwd)
		resolved, err := agentReg.GetResolved(agentName, cfg, skillNames)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 3
		}
		if err := registry.ValidateTools(resolved.Tools); err != nil {
			fmt.Fprintf(stderr, "Error: agent %q: %v\n", agentName, err)
			return 3
		}
		runAgent = resolved
	}

	// A per-agent [permissions] section replaces the global base wholly for
	// this run (og-uy5.7); the deny-point gate and the instruction snapshot
	// both read the store, so the active agent's base lands before either is
	// consulted. An agent without a section inherits the global base.
	permStore.SetBaseFromConfig(runAgent.EffectiveBase(cfg.Permissions.Base))

	// Resolve the boot client and first model from the active provider through
	// the registry. With the provider key unset, an interactive run prompts to
	// pick from the declared set and a one-shot -p run falls back to the first
	// declared provider (with a warning); zero declared providers is a startup
	// error (og-z1m.4). The startup client never comes from flat-key wire/base_url
	// selection, from a plugin, or from a model-prefix route: the resolved
	// provider is the only path.
	reg := registryFromConfig(cfg)
	provider, err := selectStartupProvider(reg, cfg.Provider, *prompt == "", os.Stdin, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	client, runModel, err := resolveStartup(reg, provider)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	// A per-agent model override still wins, but only when the agent declares
	// one outright: there is no config global, so an agent with no model of its
	// own starts the run on the active provider's default.
	if runAgent.HasExplicitModel() {
		runModel = runAgent.Model
	}

	// Bind the discovered pool to the resolved agent and build the layer.
	agentSkills := []string(nil)
	if runAgent != nil {
		agentSkills = runAgent.Skills
	}
	bound, err := skill.BindToAgent(skillPool, agentSkills, agentName)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	skillLayer := skill.BuildSkillLayer(bound)

	// Assemble instruction with agent context, appending the base-policy
	// snapshot and negotiation mechanism paragraph (og-uy5.5).
	instruction, err := instruct.LoadWithAgentAndPermissions(cfg, runAgent, skillLayer, cwd, permStore.BaseSnapshot())
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	// Agent-scoped registry.
	runRegistry := registry
	if runAgent != nil && runAgent.Tools != nil {
		runRegistry = registry.Subset(runAgent.Tools)
	}

	// Load plugins.
	pluginMgr := plugin.NewManager(cfg.PluginDir, cfg.PluginEnable, cfg.PluginDisable, runRegistry)
	if err := pluginMgr.LoadPlugins(); err != nil {
		fmt.Fprintf(stderr, "Error loading plugins: %v\n", err)
		return 1
	}
	defer pluginMgr.Shutdown()

	// Build the plugin context seam from loaded plugins + [context.plugins]. A
	// single-active conflict (multiple plugins claiming compact/condense without
	// an explicit active_compact/active_condense choice) is a hard startup error;
	// hook failures later degrade gracefully with a visible terminal message.
	ctxSeam, err := plugin.NewContextSeam(pluginMgr.PluginsInOrder(), plugin.ContextConfig{
		Order:          cfg.Context.PluginsOrder,
		ActiveCompact:  cfg.Context.ActiveCompact,
		ActiveCondense: cfg.Context.ActiveCondense,
	}, func(msg string) { fmt.Fprintf(stderr, "context degraded: %s\n", msg) })
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	// Build the plugin lifecycle seam from loaded plugins + [lifecycle.plugins].
	// All lifecycle events degrade by default; a plugin-declared fatal escalation
	// aborts the turn. The seam implements agent.Hooks and is delivered via the
	// WithHooks option on both REPL and -p paths.
	lifecycleSeam := plugin.NewLifecycleSeam(pluginMgr.PluginsInOrder(), plugin.LifecycleConfig{
		Order: cfg.Lifecycle.PluginsOrder,
	}, func(msg string) { fmt.Fprintf(stderr, "lifecycle degraded: %s\n", msg) })

	// Context window & budget: per-model config overrides first, then
	// authoritative provider data via the optional ModelInfo probe (lazily
	// probed once per model and cached for the process lifetime).
	counter := tokens.New()

	// buildContextOpts returns the ContextManager options for a given base
	// client, sourcing modelinfo from that client: a /provider switch hands
	// the freshly built client back in, so the resolver is re-sourced against
	// the new provider rather than serving the old one's model windows
	// (ADR-0004).
	buildContextOpts := func(base llm.Client) []contextmgr.Option {
		var infoSource modelinfo.Source
		if p, ok := base.(llm.ModelInfoProvider); ok {
			infoSource = p
		}
		resolver := modelinfo.New(infoSource, cfg.Context.Windows, modelinfo.Options{
			BudgetTokens:  cfg.Context.BudgetTokens,
			BudgetPercent: cfg.Context.BudgetPercent,
		})
		return []contextmgr.Option{
			contextmgr.WithTurns(cfg.Context.Turns),
			contextmgr.WithCounter(counter),
			contextmgr.WithResolver(resolver),
			contextmgr.WithHooks(ctxSeam),
			contextmgr.WithOnDegrade(func(msg string) { fmt.Fprintf(stderr, "context degraded: %s\n", msg) }),
			contextmgr.WithCondenseSize(cfg.Context.CondenseSize),
			contextmgr.WithNetDrop(cfg.Context.NetDrop),
		}
	}
	ctxOpts := buildContextOpts(client)

	// No -p flag: start the interactive REPL.
	if *prompt == "" {
		var agentReg *config.AgentReg
		if runAgent != nil || cfg.DefaultAgent != "" {
			agentReg = resolveAgentReg(cwd)
		}
		replCfg := &repl.Config{
			Client:          client,
			Model:           runModel,
			Provider:        provider,
			Providers:       reg,
			Instruction:     instruction,
			SessionDir:      cfg.SessionDir,
			Registry:        runRegistry,
			Cwd:             cwd,
			Cfg:             cfg,
			AgentReg:        agentReg,
			DefaultAgent:    runAgent,
			BashTimeout:     cfg.BashTimeout,
			CtxOpts:         ctxOpts,
			RebuildOpts:     buildContextOpts,
			AgentOpts:       []agent.Option{agent.WithHooks(lifecycleSeam)},
			Commands:        &plugin.ManagerCommands{Manager: pluginMgr},
			Stdin:           os.Stdin,
			Stdout:          stdout,
			Stderr:          stderr,
			PermissionStore: permStore,
			PermanentSink:   permSink,
		}
		err := repl.Run(context.Background(), replCfg)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	// -p flag: run a single prompt and exit.
	sess, err := session.New(cfg.SessionDir)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	// Create a change ledger for -p mode.
	ldg := ledger.New(cfg.SessionDir, sess.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle SIGINT for exit code 2.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	var turnOpts []agent.Option
	if runAgent != nil {
		turnOpts = append(turnOpts, agent.WithAgentName(runAgent.Name))
	}
	turnOpts = append(turnOpts, agent.WithHooks(lifecycleSeam))
	// Non-interactive: there is no one to ask, so every uncovered requirement
	// is resolved by the headless negotiator — auto-denied and fed back to the
	// model by default, blanket-approved for this single run under --approve-all.
	turnOpts = append(turnOpts, agent.WithPermissions(permissions.NewGate(permStore, headlessNeg, permSink)))
	ctxClient := contextmgr.New(client, sess, ctxOpts...)
	err = agent.RunTurn(ctx, ctxClient, runModel, instruction, *prompt, stdout, stderr, sess, runRegistry, ldg, cwd, turnOpts...)

	// Close the ledger to flush any recorded mutations.
	if closeErr := ldg.Close(); closeErr != nil {
		slog.Error("failed to close ledger", "error", closeErr)
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 2
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stderr, "session: %s\n", sess.ID)
	return 0
}

// buildPermissionStore seeds the effective-policy store from config: the base
// scopes plus every persisted permanent grant.
func buildPermissionStore(cwd string, p config.Permissions) (*permissions.Store, error) {
	store := permissions.New(cwd)
	store.SetBaseFromConfig(p.Base)
	for _, g := range p.Permanent {
		if err := store.GrantPermanent(permissions.Grant{
			Axis:    permissions.Axis(g.Permission),
			Scope:   g.Scope,
			Tier:    permissions.TierPermanent,
			Granted: g.Granted,
		}); err != nil {
			return nil, fmt.Errorf("config: permanent grant: %w", err)
		}
	}
	return store, nil
}

// resolveAgentReg creates an AgentReg from the standard directories.
func resolveAgentReg(cwd string) *config.AgentReg {
	globalDir := filepath.Join(os.Getenv("GENIE_CONFIG_DIR"), "genie", "agents")
	if os.Getenv("GENIE_CONFIG_DIR") == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			globalDir = filepath.Join(dir, "genie", "agents")
		}
	}
	localDir := filepath.Join(cwd, ".genie", "agents")
	return config.NewAgentReg(globalDir, localDir)
}

// buildRegistry creates the tool registry, registering available tools and
// disabling any that are turned off in config. request_permission is always-on
// — it is a negotiation channel, not a capability — and is seeded with the
// headless negotiator (auto-deny, or blanket-approve under --approve-all); the
// REPL swaps in the interactive one.
func buildRegistry(cwd string, cfgTools config.Tools, bashTimeout time.Duration, store *permissions.Store, sink permissions.PermanentSink, headless permissions.Negotiator) *tools.Registry {
	reg := tools.NewRegistry()

	// Register tools.
	reg.Register(readtool.New(cwd))
	reg.Register(writetool.New(cwd))
	reg.Register(edittool.New(cwd))
	reg.Register(bashtool.New(cwd, bashTimeout))
	reqTool := requesttool.New(store, sink)
	reqTool.SetNegotiator(headless)
	reg.Register(reqTool)

	// Disable tools turned off in config.
	if !cfgTools.Read {
		reg.Disable("read")
	}
	if !cfgTools.Write {
		reg.Disable("write")
	}
	if !cfgTools.Edit {
		reg.Disable("edit")
	}
	if !cfgTools.Bash {
		reg.Disable("bash")
	}

	return reg
}

// headlessNegotiator returns the headless negotiator for a -p run: DenyAll by
// default, ApproveAll under --approve-all. Either way grants are in-memory and
// never persisted to config.
func headlessNegotiator(approveAll bool) permissions.Negotiator {
	if approveAll {
		return permissions.ApproveAll{}
	}
	return permissions.DenyAll{}
}

// hoistApproveAllAfterPrompt rewrites "-p --approve-all <prompt>" (and the
// same with the single-dash approval flag, or --prompt) so the approval flag
// parses first and -p keeps its value. Other flag forms pass through
// unchanged.
func hoistApproveAllAfterPrompt(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if (a == "-p" || a == "--prompt") && i+1 < len(args) &&
			(args[i+1] == "-approve-all" || args[i+1] == "--approve-all") {
			out = append(out, args[i+1], a)
			i++ // skip the hoisted flag
			continue
		}
		out = append(out, a)
	}
	return out
}

// readStdinPrompt reads all of stdin, trims whitespace, and returns the
// content as a prompt string. If stdin is empty, it prints usage and returns
// ("", false). On success it returns (prompt, true).
func readStdinPrompt(stderr io.Writer) (string, bool) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(stderr, "Error: reading stdin: %v\n", err)
		return "", false
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		fmt.Fprint(stderr, usage)
		return "", false
	}
	return prompt, true
}

// configureSlog sets up the global slog handler. LevelWarn means silent (no
// info or debug messages appear); LevelInfo means verbose; LevelDebug means
// debug (which implies verbose).
func configureSlog(w io.Writer, verbose, debug bool) {
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelInfo
	}
	if debug {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == "time" {
				return slog.Attr{}
			}
			return a
		},
	})
	slog.SetDefault(slog.New(handler))

	if debug {
		slog.Debug("debug mode enabled")
	} else if verbose {
		slog.Info("verbose mode enabled")
	}
}

// isTruthy returns true for "true", "1", "yes" (case-insensitive).
func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func boolPtr(b bool) *bool { return &b }
