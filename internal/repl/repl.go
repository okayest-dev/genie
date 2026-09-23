// Package repl implements the interactive REPL (Read-Eval-Print Loop) for genie.
// It provides a canonical-mode line reader with live streaming, Ctrl+C handling
// across three zones, slash commands, and interactive confirm prompts. All
// turn assembly — session, tools, skills, plugins, permission gate, providers,
// agents — lives in run.Handle; the REPL is a thin reader that routes lines,
// signals, and prompts through it.
package repl

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/ledger"
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/plugin"
	"github.com/okayest-dev/genie/internal/run"
)

const prompt = "genie> "

// Config is the thin entry-point surface a REPL needs. The run.Handle owns
// everything a turn touches; the stream fields route the REPL's own IO.
type Config struct {
	// Run is the fully assembled run. It must be non-nil.
	Run *run.Handle
	// Cfg is the harness config the run was assembled from.
	Cfg *config.Config
	// SessionDir is the harness session directory (/changes reads ledgers).
	SessionDir string
	// Cwd is the working directory the run uses.
	Cwd    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run starts the interactive REPL loop. It reads user input, runs agent
// turns, and handles slash commands. The REPL exits on /quit or EOF.
func Run(ctx context.Context, cfg *Config) error {
	if cfg.Run == nil {
		return errors.New("repl: no run handle")
	}

	// One goroutine owns stdin and fans lines out over a channel. Sharing the
	// buffered reader with the escalation prompt is what lets ^C interrupt a
	// prompt without racing a blocked Scan.
	lines := make(chan string)
	go func() {
		scanner := bufio.NewScanner(cfg.Stdin)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	// Set up SIGINT routing. A single consumer drains os.Interrupt and hands
	// it to whichever of idle/turn/prompt is active, so the escalation prompt
	// owns delivery while it is live.
	router := newInterruptRouter()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		for range sigCh {
			router.deliver()
		}
	}()

	// Swap in the interactive negotiator: the deny-point gate and the
	// request_permission tool both follow, so inline escalation prompts are
	// byte-identical to pre-negotiation prompts.
	cfg.Run.SetNegotiator(&interactiveNegotiator{lines: lines, out: cfg.Stdout, router: router})

	for {
		fmt.Fprint(cfg.Stdout, prompt)
		select {
		case <-router.idleCh:
			// Ctrl+C at idle: exit.
			fmt.Fprintln(cfg.Stderr)
			return nil
		case line, ok := <-lines:
			if !ok {
				// EOF or read error.
				fmt.Fprintln(cfg.Stderr)
				return nil
			}

			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// 1. Parse @name one-shot (before slash commands).
			if agentName, rest, ok := parseInlineAgent(line); ok {
				handleInlineAgent(ctx, agentName, rest, cfg, router)
				continue
			}

			// 2. Handle slash commands.
			if strings.HasPrefix(line, "/") {
				if handleSlashCommand(ctx, line, cfg) {
					return nil
				}
				continue
			}

			// 3. Normal turn.
			runTurn(ctx, cfg, line, router)
		}
	}
}

// inlineAgentName matches a valid agent name at the start of a line.
var inlineAgentName = regexp.MustCompile(`^@([a-z0-9-]+)(?:\s|$)`)

// parseInlineAgent checks if a line starts with @name and extracts the
// agent name and remaining prompt. Returns ("", "", false) if not an inline agent.
func parseInlineAgent(line string) (agentName, prompt string, ok bool) {
	m := inlineAgentName.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimSpace(line[len(m[0]):]), true
}

// handleInlineAgent runs a one-shot agent turn, then reverts to the previous
// agent (or the default flow).
func handleInlineAgent(ctx context.Context, agentName, prompt string, cfg *Config, router *interruptRouter) {
	if prompt == "" {
		fmt.Fprintf(cfg.Stderr, "genie: @%s requires a prompt\n", agentName)
		return
	}

	previous := cfg.Run.CurrentAgent()
	if _, err := cfg.Run.SwitchAgent(agentName); err != nil {
		fmt.Fprintf(cfg.Stderr, "genie: %v\n", err)
		return
	}

	runTurn(ctx, cfg, prompt, router)

	if previous != nil {
		_, _ = cfg.Run.SwitchAgent(previous.Name)
	} else {
		cfg.Run.ResetAgent()
	}
}

// runTurn executes a single agent turn. The run handle owns instruction
// resolution, model precedence, registry scoping, and the session; the repl
// only frames the turn so ^C can cancel it.
func runTurn(ctx context.Context, cfg *Config, prompt string, router *interruptRouter) {
	router.drainTurn()
	router.set(modeTurn)
	turnCtx, cancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() {
		errCh <- cfg.Run.Turn(turnCtx, prompt, cfg.Stdout, cfg.Stderr)
	}()

	select {
	case <-router.turnCh:
		cancel()
		fmt.Fprintln(cfg.Stderr, "\n[turn cancelled]")
	case err := <-errCh:
		cancel()
		if err != nil {
			fmt.Fprintf(cfg.Stderr, "Error: %v\n", err)
		}
	}
	router.set(modeIdle)
}

// handleSlashCommand processes a slash command and returns true if the REPL
// should exit.
func handleSlashCommand(ctx context.Context, line string, cfg *Config) bool {
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/quit", "/exit":
		return true

	case "/help":
		fmt.Fprintln(cfg.Stdout, "Commands:")
		fmt.Fprintln(cfg.Stdout, "  /help             show this help")
		fmt.Fprintln(cfg.Stdout, "  /quit             exit the REPL")
		fmt.Fprintln(cfg.Stdout, "  /new              start a new session")
		fmt.Fprintln(cfg.Stdout, "  /changes          list change batches")
		fmt.Fprintln(cfg.Stdout, "  /changes <id>     show change details")
		fmt.Fprintln(cfg.Stdout, "  /provider         list available providers")
		fmt.Fprintln(cfg.Stdout, "  /provider <name>  switch to a named provider")
		fmt.Fprintln(cfg.Stdout, "  /model            list available models")
		fmt.Fprintln(cfg.Stdout, "  /model <id>       switch to a different model")
		fmt.Fprintln(cfg.Stdout, "  /agent            list available agents")
		fmt.Fprintln(cfg.Stdout, "  /agent <name>     switch to a named agent")
		fmt.Fprintln(cfg.Stdout, "")
		fmt.Fprintln(cfg.Stdout, "  @<name> <prompt>  one-shot agent switch")
		if cmds := cfg.Run.Commands(); cmds != nil {
			printPluginCommandsHelp(cmds, cfg.Stdout)
		}
		fmt.Fprintln(cfg.Stdout, "")
		fmt.Fprintln(cfg.Stdout, "Ctrl+C: quit at idle, cancel mid-turn")

	case "/new":
		if _, err := cfg.Run.NewSession(); err != nil {
			fmt.Fprintf(cfg.Stderr, "Error: %v\n", err)
		}

	case "/changes":
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}
		handleChanges(args, cfg, cfg.Run.Session().ID, cfg.Stdout)

	case "/provider":
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			listProviders(cfg)
		} else {
			switchProvider(strings.TrimSpace(parts[1]), cfg)
		}

	case "/model":
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			listModels(ctx, cfg)
		} else {
			setModel(ctx, strings.TrimSpace(parts[1]), cfg)
		}

	case "/agent":
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			listAgents(cfg)
		} else {
			switchAgent(strings.TrimSpace(parts[1]), cfg)
		}

	default:
		if cmds := cfg.Run.Commands(); cmds != nil && handlePluginCommand(line, cmds, cfg) {
			break
		}
		fmt.Fprintf(cfg.Stdout, "unknown command: %s (try /help)\n", cmd)
	}

	return false
}

// listProviders prints the declared providers and marks the session's current
// one.
func listProviders(cfg *Config) {
	names := cfg.Run.ProviderNames()
	if len(names) == 0 {
		fmt.Fprintln(cfg.Stdout, "no providers configured")
		return
	}
	cur := cfg.Run.Provider()
	fmt.Fprintln(cfg.Stdout, "Available providers:")
	for _, name := range names {
		marker := "  "
		if name == cur {
			marker = "* "
		}
		fmt.Fprintf(cfg.Stdout, "%s%s\n", marker, name)
	}
	fmt.Fprintf(cfg.Stdout, "\nCurrent: %s\n", cur)
}

// switchProvider switches the run to the named provider. The transcript
// continues (a switch never starts a new session) and the run model resets to
// the new provider's default. An unknown name prints the available set and
// leaves the session untouched.
func switchProvider(target string, cfg *Config) {
	if err := cfg.Run.SwitchProvider(target); err != nil {
		fmt.Fprintf(cfg.Stdout, "%s\n", err)
		return
	}
	fmt.Fprintf(cfg.Stdout, "provider: %s (model: %s)\n", cfg.Run.Provider(), cfg.Run.Model())
}

// listModels prints the active provider's model catalog, marking the current
// model. A catalog failure degrades to the active model alone.
func listModels(ctx context.Context, cfg *Config) {
	models, err := activeCatalog(ctx, cfg)
	if err != nil {
		// Degrade: show the active model. Streaming still runs on it.
		fmt.Fprintf(cfg.Stderr, "Error: fetching model catalog: %v\n", err)
		fmt.Fprintln(cfg.Stdout, "Available models:")
		fmt.Fprintf(cfg.Stdout, "* %s\n", cfg.Run.Model())
		fmt.Fprintf(cfg.Stdout, "\nCurrent: %s\n", cfg.Run.Model())
		return
	}
	fmt.Fprintln(cfg.Stdout, "Available models:")
	for _, m := range models {
		marker := "  "
		if m.ID == cfg.Run.Model() {
			marker = "* "
		}
		fmt.Fprintf(cfg.Stdout, "%s%s\n", marker, m.ID)
	}
	fmt.Fprintf(cfg.Stdout, "\nCurrent: %s\n", cfg.Run.Model())
}

// setModel switches the run model, validating it against the active
// provider's catalog.
func setModel(ctx context.Context, target string, cfg *Config) {
	models, err := activeCatalog(ctx, cfg)
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "Error: fetching model catalog: %v\n", err)
		return
	}
	for _, m := range models {
		if m.ID == target {
			cfg.Run.SetModel(target)
			fmt.Fprintf(cfg.Stdout, "model: %s\n", target)
			return
		}
	}
	fmt.Fprintf(cfg.Stdout, "genie: no such model: %s\n", target)
}

// activeCatalog returns the active provider's model catalog from the run's
// provider source.
func activeCatalog(ctx context.Context, cfg *Config) ([]llm.Model, error) {
	return cfg.Run.ProviderCatalog(ctx)
}

// listAgents prints the available agents and marks the current one.
func listAgents(cfg *Config) {
	reg := cfg.Run.AgentReg()
	if reg == nil {
		fmt.Fprintln(cfg.Stdout, "no agents configured")
		return
	}
	names := reg.List()
	if len(names) == 0 {
		fmt.Fprintln(cfg.Stdout, "no agents configured")
		return
	}
	cur := cfg.Run.CurrentAgent()
	fmt.Fprintln(cfg.Stdout, "Available agents:")
	for _, name := range names {
		marker := "  "
		if cur != nil && cur.Name == name {
			marker = "* "
		}
		def, _ := reg.Get(name)
		modelStr := ""
		if def != nil && def.Model != "" {
			modelStr = fmt.Sprintf("  model: %s", def.Model)
		}
		toolsStr := ""
		if def != nil && def.Tools != nil {
			toolsStr = fmt.Sprintf("  tools: %s", strings.Join(def.Tools, ", "))
		}
		fmt.Fprintf(cfg.Stdout, "%s%s%s%s\n", marker, name, modelStr, toolsStr)
	}
	if cur != nil {
		fmt.Fprintf(cfg.Stdout, "\nCurrent: %s\n", cur.Name)
	} else {
		fmt.Fprintln(cfg.Stdout, "\nCurrent: (default)")
	}
}

// switchAgent activates the named agent on the run: its tool subset becomes
// the active registry and its [permissions] base replaces the global base
// wholly.
func switchAgent(target string, cfg *Config) {
	if _, err := cfg.Run.SwitchAgent(target); err != nil {
		fmt.Fprintf(cfg.Stderr, "genie: %v\n", err)
		return
	}
	resolved := cfg.Run.CurrentAgent()
	toolsStr := ""
	if resolved.Tools != nil {
		toolsStr = fmt.Sprintf(", tools: %s", strings.Join(resolved.Tools, ", "))
	}
	modelStr := ""
	if resolved.Model != "" {
		modelStr = fmt.Sprintf(" (model: %s)", resolved.Model)
	}
	fmt.Fprintf(cfg.Stdout, "switched to %s%s%s\n", resolved.Name, modelStr, toolsStr)
}

// handlePluginCommand routes /<plugin> ... to the plugin command source. It
// returns true when the command was handled (hit or miss).
func handlePluginCommand(line string, cmds plugin.CommandSource, cfg *Config) bool {
	rest := strings.TrimPrefix(line, "/")
	pluginName, rest, _ := strings.Cut(rest, " ")
	pluginName = strings.ToLower(pluginName)
	rest = strings.TrimLeft(rest, " \t")

	if rest == "" {
		return handlePluginBare(pluginName, cmds, cfg)
	}

	command := rest
	var args string
	if idx := strings.IndexAny(command, " \t"); idx >= 0 {
		args = strings.TrimSpace(command[idx:])
		command = command[:idx]
	}

	result, err := cmds.RunCommand(pluginName, command, args)
	if err != nil {
		fmt.Fprintf(cfg.Stdout, "%s\n", formatPluginError(pluginName, command, err))
		return true
	}

	if result.Text != "" {
		fmt.Fprintln(cfg.Stdout, result.Text)
	} else if result.Data != nil {
		b, err := json.Marshal(result.Data)
		if err != nil {
			fmt.Fprintf(cfg.Stderr, "Error: %v\n", err)
		} else {
			fmt.Fprintln(cfg.Stdout, string(b))
		}
	}
	return true
}

// handlePluginBare handles a bare /<plugin> by showing curated help or listing
// the plugin's commands.
func handlePluginBare(pluginName string, cmds plugin.CommandSource, cfg *Config) bool {
	text, err := cmds.Help(pluginName, "")
	if err == nil && text != "" {
		fmt.Fprintln(cfg.Stdout, text)
		return true
	}

	cmds2, err := cmds.ListCommands(pluginName)
	if err != nil {
		fmt.Fprintf(cfg.Stdout, "%s\n", formatPluginError(pluginName, "", err))
		return true
	}
	if len(cmds2) == 0 {
		fmt.Fprintf(cfg.Stdout, "%s has no commands\n", pluginName)
		return true
	}
	fmt.Fprintf(cfg.Stdout, "%s commands:\n", pluginName)
	for _, c := range cmds2 {
		fmt.Fprintf(cfg.Stdout, "  %s  %s\n", c.Name, c.Description)
	}
	return true
}

// formatPluginError produces a user-facing message for plugin command errors.
func formatPluginError(pluginName, command string, err error) string {
	switch {
	case errors.Is(err, plugin.ErrUnknownPlugin):
		return fmt.Sprintf("unknown command: /%s (try /help)", pluginName)
	case errors.Is(err, plugin.ErrPluginInactive):
		return fmt.Sprintf("plugin %s is not active", pluginName)
	case errors.Is(err, plugin.ErrUnknownCommand):
		return fmt.Sprintf("%s: no such command: %s", pluginName, command)
	default:
		return fmt.Sprintf("%s: %v", pluginName, err)
	}
}

// printPluginCommandsHelp prints a flat plugin-commands section for /help,
// enumerating each plugin's command (name + one-line description).
func printPluginCommandsHelp(cmds plugin.CommandSource, out io.Writer) {
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Plugin commands:")
	for _, name := range cmds.Plugins() {
		list, err := cmds.ListCommands(name)
		if err != nil {
			continue
		}
		for _, c := range list {
			fmt.Fprintf(out, "  /%s %s  %s\n", name, c.Name, c.Description)
		}
	}
}

// fileNames returns a comma-separated list of file paths from a batch's files.
func fileNames(files []ledger.File) string {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Path
	}
	return strings.Join(names, ", ")
}

// handleChanges handles the /changes slash command.
func handleChanges(args string, cfg *Config, sessionID string, out io.Writer) {
	args = strings.TrimSpace(args)
	if args == "" {
		batches, err := ledger.LoadBatches(cfg.SessionDir, sessionID)
		if err != nil {
			fmt.Fprintf(cfg.Stderr, "Error: %v\n", err)
			return
		}
		if len(batches) == 0 {
			fmt.Fprintln(out, "no changes")
			return
		}
		for _, b := range batches {
			totalDelta := 0
			for _, f := range b.Files {
				totalDelta += f.Delta.Added + f.Delta.Removed
			}
			fmt.Fprintf(out, "%03d  %s  %d lines  %s\n",
				b.Seq, b.Time, totalDelta, fileNames(b.Files))
		}
		return
	}

	id, err := strconv.Atoi(args)
	if err != nil {
		fmt.Fprintf(out, "genie: invalid change id: %s\n", args)
		return
	}
	batch, err := ledger.LoadBatchByID(cfg.SessionDir, sessionID, id)
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "Error: %v\n", err)
		return
	}
	if batch == nil {
		fmt.Fprintf(out, "genie: no such change id: %d\n", id)
		return
	}
	for _, f := range batch.Files {
		fmt.Fprintf(out, "--- %s (%s)\n", f.Path, f.Ops)
		fmt.Fprintf(out, "+++ delta: +%d/-%d\n", f.Delta.Added, f.Delta.Removed)
		if strings.Contains(f.Diff, "[binary]") {
			fmt.Fprintln(out, "[binary file]")
		} else if strings.Contains(f.Diff, "[truncated") {
			fmt.Fprintf(out, "%s\n", f.Diff)
		} else {
			fmt.Fprint(out, f.Diff)
		}
		fmt.Fprintln(out)
	}
}
