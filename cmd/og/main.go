package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/okayest-dev/og/internal/agent"
	"github.com/okayest-dev/og/internal/config"
	"github.com/okayest-dev/og/internal/instruct"
	"github.com/okayest-dev/og/internal/llm"
	"github.com/okayest-dev/og/internal/plugin"
	_ "github.com/okayest-dev/og/internal/llm/anthropic"
	_ "github.com/okayest-dev/og/internal/llm/google"
	_ "github.com/okayest-dev/og/internal/llm/openai"
	_ "github.com/okayest-dev/og/internal/llm/responses"
	"github.com/okayest-dev/og/internal/repl"
	"github.com/okayest-dev/og/internal/session"
	"github.com/okayest-dev/og/internal/tools"
	"github.com/okayest-dev/og/internal/tools/bashtool"
	"github.com/okayest-dev/og/internal/tools/edittool"
	"github.com/okayest-dev/og/internal/tools/readtool"
	"github.com/okayest-dev/og/internal/tools/writetool"
)

const usage = `usage: og [-v] [-d] [-p prompt]

og is a minimal terminal agent harness.

Flags:
  -p prompt   run a single prompt, print the reply to stdout, and exit
  -v          verbose output: high-level flow to stderr
  -d          debug output: low-level detail to stderr (implies -v)

Environment:
  OG_DEBUG    enable debug mode (true/1/yes)

Without -p, og starts an interactive REPL.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("og", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	prompt := fs.String("p", "", "run a single prompt")
	verbose := fs.Bool("v", false, "verbose output")
	debug := fs.Bool("d", false, "debug output (implies -v)")
	if err := fs.Parse(args); err != nil {
		return 3
	}

	debugEnv := isTruthy(os.Getenv("OG_DEBUG"))
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
	instruction, err := instruct.Load(cfg, cwd)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	wire := cfg.Wire
	if wire == "" {
		wire = llm.DetectWire(cfg.Model)
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

	// Build the tool registry from config.
	registry := buildRegistry(cwd, cfg.Tools, cfg.BashTimeout)

	// Load plugins.
	pluginMgr := plugin.NewManager(cfg.PluginDir, cfg.PluginEnable, cfg.PluginDisable, registry)
	if err := pluginMgr.LoadPlugins(); err != nil {
		fmt.Fprintf(stderr, "Error loading plugins: %v\n", err)
		return 1
	}
	defer pluginMgr.Shutdown()

	// Build model→client route table from wire plugins.
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

	// No -p flag: start the interactive REPL.
	if *prompt == "" {
		replCfg := &repl.Config{
			Client:      client,
			Model:       cfg.Model,
			Instruction: instruction,
			SessionDir:  cfg.SessionDir,
			Registry:    registry,
			Stdin:       os.Stdin,
			Stdout:      stdout,
			Stderr:      stderr,
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

	if err := agent.RunTurn(context.Background(), client, cfg.Model, instruction, *prompt, stdout, stderr, sess, registry, nil, cwd); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "session: %s\n", sess.ID)
	return 0
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
