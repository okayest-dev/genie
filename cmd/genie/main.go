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

	"github.com/okayest-dev/genie/internal/config"
	_ "github.com/okayest-dev/genie/internal/llm/anthropic"
	_ "github.com/okayest-dev/genie/internal/llm/bedrock"
	_ "github.com/okayest-dev/genie/internal/llm/copilot"
	_ "github.com/okayest-dev/genie/internal/llm/google"
	_ "github.com/okayest-dev/genie/internal/llm/openai"
	_ "github.com/okayest-dev/genie/internal/llm/responses"
	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/repl"
	runpkg "github.com/okayest-dev/genie/internal/run"
	"github.com/okayest-dev/genie/internal/skill"
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
	// in-memory policy store, so nothing is ever persisted. The REPL swaps in
	// the interactive negotiator after the run handle is assembled.
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

	// Permanent grants persist to the config file; if the path cannot be
	// resolved they stay in-memory for the run. The effective-policy store
	// itself lives in run.New.
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

	// Resolve the agent for this run. Agent resolution needs the discovered
	// skill pool to validate explicit skills lists, so the pool is filtered
	// here for that read; run.New derives its own pool for the agent-scoped
	// assembly and for switch-time resolution.
	skillPool, poolWarns, err := skill.FilteredPool(cfg.Skills.Dirs, cfg.Skills.Enable, cfg.Skills.Disable)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	for _, w := range poolWarns {
		fmt.Fprintf(stderr, "warning: %s\n", w.Message)
	}
	skillNames := skill.Names(skillPool)

	var runAgent *config.ResolvedAgent
	var agentReg *config.AgentReg
	agentName := *agentFlag
	if agentName == "" && cfg.DefaultAgent != "" {
		agentName = cfg.DefaultAgent
	}
	if agentName != "" {
		agentReg = resolveAgentReg(cwd)
		resolved, err := agentReg.GetResolved(agentName, cfg, skillNames)
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 3
		}
		runAgent = resolved
	}

	// Resolve and boot the provider. With the provider key unset, an
	// interactive run prompts to pick from the declared set and a one-shot -p
	// run falls back to the first declared provider (with a warning); zero
	// declared providers is a startup error (og-z1m.4). The boot client and
	// model are resolved inside run.New — the registry is only the named
	// provider surface here.
	reg := registryFromConfig(cfg)
	provider, err := selectStartupProvider(reg, cfg.Provider, *prompt == "", os.Stdin, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	// Assemble the run handle once; the REPL and -p share it. run.New owns
	// every derivable component (store, tools, skills, plugins, seams, tokens,
	// modelinfo, session, ledger, boot provider) and applies the agent's
	// permissions base and model precedence.
	h, err := runpkg.New(runpkg.Options{
		Config:         cfg,
		Cwd:            cwd,
		Stdin:          os.Stdin,
		Stdout:         stdout,
		Stderr:         stderr,
		Provider:       provider,
		ProviderSource: reg,
		Agent:          runAgent,
		AgentReg:       agentReg,
		Negotiator:     headlessNeg,
		PermanentSink:  permSink,
		OnUsageDegrade: func(msg string) { fmt.Fprintf(stderr, "%s\n", msg) },
		WithLedger:     true,
	})
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	// No -p flag: start the interactive REPL.
	if *prompt == "" {
		// Fail at startup when the boot instruction cannot be assembled
		// (matches the pre-run.Handle behavior).
		if ierr := h.LoadInstruction(); ierr != nil {
			if cerr := h.Close(); cerr != nil {
				slog.Error("failed to close ledger", "error", cerr)
			}
			fmt.Fprintf(stderr, "Error: %v\n", ierr)
			return 1
		}
		replCfg := &repl.Config{
			Run:        h,
			Cfg:        cfg,
			SessionDir: cfg.SessionDir,
			Cwd:        cwd,
			Stdin:      os.Stdin,
			Stdout:     stdout,
			Stderr:     stderr,
		}
		err := repl.Run(context.Background(), replCfg)
		if cerr := h.Close(); cerr != nil {
			slog.Error("failed to close ledger", "error", cerr)
		}
		if err != nil {
			fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	// -p flag: run a single prompt and exit.
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

	// Resolve the instruction up front so an unresolvable instruction file fails
	// the run at startup (matches the pre-run.Handle behavior).
	if ierr := h.LoadInstruction(); ierr != nil {
		if cerr := h.Close(); cerr != nil {
			slog.Error("failed to close ledger", "error", cerr)
		}
		fmt.Fprintf(stderr, "Error: %v\n", ierr)
		return 1
	}

	err = h.Turn(ctx, *prompt, stdout, stderr)

	// Close the ledger to flush any recorded mutations and shut down the
	// plugin manager regardless of the turn's outcome.
	if cerr := h.Close(); cerr != nil {
		slog.Error("failed to close ledger", "error", cerr)
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 2
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
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
