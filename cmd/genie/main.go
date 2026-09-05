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
	_ "github.com/okayest-dev/genie/internal/llm/google"
	_ "github.com/okayest-dev/genie/internal/llm/openai"
	_ "github.com/okayest-dev/genie/internal/llm/responses"
	"github.com/okayest-dev/genie/internal/modelinfo"
	"github.com/okayest-dev/genie/internal/plugin"
	"github.com/okayest-dev/genie/internal/repl"
	"github.com/okayest-dev/genie/internal/session"
	"github.com/okayest-dev/genie/internal/tokens"
	"github.com/okayest-dev/genie/internal/tools"
	"github.com/okayest-dev/genie/internal/tools/bashtool"
	"github.com/okayest-dev/genie/internal/tools/edittool"
	"github.com/okayest-dev/genie/internal/tools/readtool"
	"github.com/okayest-dev/genie/internal/tools/writetool"
)

const usage = `usage: genie [-v] [-d] [-a agent] [-p prompt]

genie is a minimal terminal agent harness.

Flags:
  -a agent   load a named agent definition for this run
  -p prompt  run a single prompt, print the reply to stdout, and exit
  -v         verbose output: high-level flow to stderr
  -d         debug output: low-level detail to stderr (implies -v)

Environment:
  GENIE_DEBUG    enable debug mode (true/1/yes)

Without -p, genie starts an interactive REPL.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
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
	if err := fs.Parse(cleanArgs); err != nil {
		return 3
	}

	// If stdinPrompt was set, use it as the prompt.
	if stdinPrompt != "" {
		*prompt = stdinPrompt
	}

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

	// Build the tool registry from config.
	registry := buildRegistry(cwd, cfg.Tools, cfg.BashTimeout)

	// Resolve the agent for this run.
	var runAgent *config.ResolvedAgent
	agentName := *agentFlag
	if agentName == "" && cfg.DefaultAgent != "" {
		agentName = cfg.DefaultAgent
	}

	if agentName != "" {
		agentReg := resolveAgentReg(cwd)
		resolved, err := agentReg.GetResolved(agentName, cfg)
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

	// Apply agent overrides to model.
	runModel := cfg.Model
	if runAgent != nil && runAgent.Model != "" {
		runModel = runAgent.Model
	}

	// Assemble instruction with agent context.
	instruction, err := instruct.LoadWithAgent(cfg, runAgent, cwd)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	// Agent-scoped registry.
	runRegistry := registry
	if runAgent != nil && runAgent.Tools != nil {
		runRegistry = registry.Subset(runAgent.Tools)
	}

	wire := cfg.Wire
	if wire == "" {
		wire = llm.DetectWire(runModel)
	}
	baseURL := cfg.BaseURL
	if cfg.Gateway != "" {
		baseURL = cfg.Gateway
	}
	client, err := llm.NewClient(wire, baseURL, cfg.APIKey)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	// Load plugins.
	pluginMgr := plugin.NewManager(cfg.PluginDir, cfg.PluginEnable, cfg.PluginDisable, runRegistry)
	if err := pluginMgr.LoadPlugins(); err != nil {
		fmt.Fprintf(stderr, "Error loading plugins: %v\n", err)
		return 1
	}
	defer pluginMgr.Shutdown()

	// Route through an explicit provider or build a model-based route table.
	if cfg.Provider != "" {
		p, ok := pluginMgr.GetPlugins()[cfg.Provider]
		if !ok {
			fmt.Fprintf(stderr, "Error: provider %q not found (loaded plugins: %s)\n", cfg.Provider, pluginNames(pluginMgr))
			return 1
		}
		if !p.Capabilities.Wires {
			fmt.Fprintf(stderr, "Error: provider %q does not support wires\n", cfg.Provider)
			return 1
		}
		client = newPluginWireClient(p)
	} else {
		modelRoutes := make(map[string]llm.Client)
		for _, p := range pluginMgr.GetPlugins() {
			if !p.Capabilities.Wires {
				continue
			}
			pc := newPluginWireClient(p)
			for _, m := range p.Models {
				modelRoutes[m.ID] = pc
			}
		}
		if len(modelRoutes) > 0 {
			client = llm.NewRoutingClient(client, modelRoutes)
		}
	}

	// Context window & budget: per-model config overrides first, then
	// authoritative provider data via the optional ModelInfo probe (lazily
	// probed once per model and cached for the process lifetime).
	var infoSource modelinfo.Source
	if p, ok := client.(llm.ModelInfoProvider); ok {
		infoSource = p
	}
	resolver := modelinfo.New(infoSource, cfg.Context.Windows, modelinfo.Options{
		BudgetTokens:  cfg.Context.BudgetTokens,
		BudgetPercent: cfg.Context.BudgetPercent,
	})
	// Plugin-reported windows are already in hand; seed without probing.
	for _, p := range pluginMgr.GetPlugins() {
		for _, m := range p.Models {
			resolver.Seed(m.ID, m.ContextWindow)
		}
	}

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

	counter := tokens.New()
	ctxOpts := []contextmgr.Option{
		contextmgr.WithTurns(cfg.Context.Turns),
		contextmgr.WithCounter(counter),
		contextmgr.WithResolver(resolver),
		contextmgr.WithHooks(ctxSeam),
		contextmgr.WithOnDegrade(func(msg string) { fmt.Fprintf(stderr, "context degraded: %s\n", msg) }),
		contextmgr.WithCondenseSize(cfg.Context.CondenseSize),
		contextmgr.WithNetDrop(cfg.Context.NetDrop),
	}

	// No -p flag: start the interactive REPL.
	if *prompt == "" {
		var agentReg *config.AgentReg
		if runAgent != nil || cfg.DefaultAgent != "" {
			agentReg = resolveAgentReg(cwd)
		}
		replCfg := &repl.Config{
			Client:       client,
			Model:        runModel,
			Instruction:  instruction,
			SessionDir:   cfg.SessionDir,
			Registry:     runRegistry,
			Cwd:          cwd,
			Cfg:          cfg,
			AgentReg:     agentReg,
			DefaultAgent: runAgent,
			BashTimeout:  cfg.BashTimeout,
			CtxOpts:      ctxOpts,
			AgentOpts:    []agent.Option{agent.WithHooks(lifecycleSeam)},
			Stdin:        os.Stdin,
			Stdout:       stdout,
			Stderr:       stderr,
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
// disabling any that are turned off in config.
func buildRegistry(cwd string, cfgTools config.Tools, bashTimeout time.Duration) *tools.Registry {
	reg := tools.NewRegistry()

	// Register tools.
	reg.Register(readtool.New(cwd))
	reg.Register(writetool.New(cwd, tools.AutoDeny{}))
	reg.Register(edittool.New(cwd))
	reg.Register(bashtool.New(cwd, tools.AutoDeny{}, bashTimeout))

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

func pluginNames(mgr *plugin.Manager) string {
	names := make([]string, 0)
	for name := range mgr.GetPlugins() {
		names = append(names, name)
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
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
