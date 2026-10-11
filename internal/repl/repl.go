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
	"time"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/ledger"
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/llm/copilot"
	"github.com/okayest-dev/genie/internal/plugin"
	"github.com/okayest-dev/genie/internal/prompt"
	"github.com/okayest-dev/genie/internal/run"
	"github.com/okayest-dev/genie/internal/style"
	"github.com/okayest-dev/genie/internal/theme"
)

const defaultPrompt = "genie> "

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
	// PromptBar is the prompt bar renderer for the idle prompt.
	PromptBar *prompt.PromptBar
	// Theme is the loaded theme for prompt rendering.
	Theme *theme.Theme
	// Profile is the render profile for prompt rendering.
	Profile *style.RenderProfile
	// StderrProfile is the render profile for stderr, probed independently of
	// stdout so error and notice roles colour only where stderr is a display.
	StderrProfile *style.RenderProfile
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
		// Render the idle prompt bar
		if cfg.PromptBar != nil {
			ctx := prompt.BuildContext(cfg.Run, cfg.Cfg, cfg.Theme)
			barLines := cfg.PromptBar.Render(ctx)
			for _, line := range barLines {
				fmt.Fprint(cfg.Stdout, line)
			}
		} else {
			fmt.Fprint(cfg.Stdout, defaultPrompt)
		}

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
		cfg.printTurnCancelled()
	case err := <-errCh:
		cancel()
		if err != nil {
			cfg.printTurnError(err)
		}
	}
	router.set(modeIdle)
}

// roleWriter returns a writer that renders bytes through the given role on w.
// With no theme or profile wired it returns w unchanged, so themed output is
// purely additive: pipes and headerless callers keep today's plain bytes.
func (c *Config) roleWriter(w io.Writer, profile *style.RenderProfile, role string) io.Writer {
	if c.Theme == nil || profile == nil {
		return w
	}
	return c.Theme.RoleWriter(w, profile, role)
}

// printTurnError reports a turn's failure through the error role.
func (c *Config) printTurnError(err error) {
	fmt.Fprintf(c.roleWriter(c.Stderr, c.StderrProfile, "error"), "Error: %v\n", err)
}

// printTurnCancelled prints the [turn cancelled] notice through the
// turn_cancelled role.
func (c *Config) printTurnCancelled() {
	fmt.Fprintln(c.roleWriter(c.Stderr, c.StderrProfile, "turn_cancelled"), "\n[turn cancelled]")
}

// handleSlashCommand processes a slash command and returns true if the REPL
// should exit.
func handleSlashCommand(ctx context.Context, line string, cfg *Config) bool {
	// Parse colon-separated command hierarchy: /cmd:subcmd:subsubcmd [args...]
	// Everything after the first space is treated as arguments to the final subcommand.
	cmdParts := strings.SplitN(line, " ", 2)
	cmdPath := strings.ToLower(cmdParts[0])
	args := ""
	if len(cmdParts) > 1 {
		args = strings.TrimSpace(cmdParts[1])
	}

	// Split command path on colon
	pathParts := strings.Split(cmdPath, ":")
	cmd := pathParts[0]

	switch cmd {
	case "/quit", "/exit":
		return true

	case "/help":
		printHelp(cfg)
		return false

	case "/new":
		if _, err := cfg.Run.NewSession(); err != nil {
			fmt.Fprintf(cfg.Stderr, "Error: %v\n", err)
		}
		return false

	case "/changes":
		changeID := ""
		if len(pathParts) > 1 {
			changeID = pathParts[1]
		}
		handleChanges(changeID, cfg, cfg.Run.Session().ID, cfg.Stdout)
		return false

	case "/task":
		handleTask(cfg)
		return false

	case "/provider":
		if len(pathParts) < 2 || pathParts[1] == "" {
			listProviders(cfg)
		} else {
			switchProvider(pathParts[1], cfg)
		}
		return false

	case "/model":
		if len(pathParts) < 2 || pathParts[1] == "" {
			listModels(ctx, cfg)
		} else {
			setModel(ctx, pathParts[1], cfg)
		}
		return false

	case "/agent":
		if len(pathParts) < 2 || pathParts[1] == "" {
			listAgents(cfg)
		} else {
			switchAgent(pathParts[1], cfg)
		}
		return false

	case "/copilot":
		if len(pathParts) < 2 || pathParts[1] == "" {
			printCopilotHelp(cfg)
		} else {
			handleCopilotCommand(ctx, strings.Join(pathParts[1:], ":"), args, cfg)
		}
		return false

	default:
		if cmds := cfg.Run.Commands(); cmds != nil && handlePluginCommand(line, cmds, cfg) {
			return false
		}
		fmt.Fprintf(cfg.Stdout, "unknown command: %s (try /help)\n", cmd)
		return false
	}
}

// Notify git gatherer that a slash command completed (triggers degraded recovery per ADR-0011).
func notifySlashCommandCompleted(cmd string, cfg *Config) {
	if cmd != "/quit" && cmd != "/exit" {
		cfg.Run.NotifyGitTurnCompleted()
	}
}

// printHelp prints the main help text.
func printHelp(cfg *Config) {
	fmt.Fprintln(cfg.Stdout, "Commands:")
	fmt.Fprintln(cfg.Stdout, "  /help                    show this help")
	fmt.Fprintln(cfg.Stdout, "  /quit, /exit             exit the REPL")
	fmt.Fprintln(cfg.Stdout, "  /new                     start a new session")
	fmt.Fprintln(cfg.Stdout, "  /changes                 list change batches")
	fmt.Fprintln(cfg.Stdout, "  /changes:<id>            show change details")
	fmt.Fprintln(cfg.Stdout, "  /task                    show the active task and its lifecycle")
	fmt.Fprintln(cfg.Stdout, "  /provider                list available providers (numbered)")
	fmt.Fprintln(cfg.Stdout, "  /provider:<name|num>     switch to a provider by name or number")
	fmt.Fprintln(cfg.Stdout, "  /model                   list available models (numbered)")
	fmt.Fprintln(cfg.Stdout, "  /model:<id|num>          switch to a model by ID or number")
	fmt.Fprintln(cfg.Stdout, "  /agent                   list available agents (numbered)")
	fmt.Fprintln(cfg.Stdout, "  /agent:<name|num>        switch to an agent by name or number")
	fmt.Fprintln(cfg.Stdout, "  /copilot                 manage Copilot credentials")
	fmt.Fprintln(cfg.Stdout, "  /copilot:auth:login      start device-flow login")
	fmt.Fprintln(cfg.Stdout, "  /copilot:auth:status     show credential status")
	fmt.Fprintln(cfg.Stdout, "")
	fmt.Fprintln(cfg.Stdout, "  @<name> <prompt>         one-shot agent switch")
	if cmds := cfg.Run.Commands(); cmds != nil {
		printPluginCommandsHelp(cmds, cfg.Stdout)
	}
	fmt.Fprintln(cfg.Stdout, "")
	fmt.Fprintln(cfg.Stdout, "Ctrl+C: quit at idle, cancel mid-turn")
}

// printCopilotHelp prints the copilot command help.
func printCopilotHelp(cfg *Config) {
	fmt.Fprintln(cfg.Stdout, "Usage: /copilot:auth:login|status [--host <domain>]")
	fmt.Fprintln(cfg.Stdout, "  /copilot:auth:login [--host github.com]  Start device-flow login")
	fmt.Fprintln(cfg.Stdout, "  /copilot:auth:status [--host github.com] Show credential status")
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
	for i, name := range names {
		marker := "  "
		if name == cur {
			marker = "* "
		}
		fmt.Fprintf(cfg.Stdout, "%s%d: %s\n", marker, i+1, name)
	}
	fmt.Fprintf(cfg.Stdout, "\nCurrent: %s\n", cur)
}

// switchProvider switches the run to the named provider. The transcript
// continues (a switch never starts a new session) and the run model resets to
// the new provider's default. An unknown name prints the available set and
// leaves the session untouched.
func switchProvider(target string, cfg *Config) {
	names := cfg.Run.ProviderNames()
	resolved := resolveByNameOrIndex(target, names)
	if resolved == "" {
		fmt.Fprintf(cfg.Stdout, "genie: no such provider: %s (available: %s)\n", target, strings.Join(names, ", "))
		return
	}
	if err := cfg.Run.SwitchProvider(resolved); err != nil {
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
		fmt.Fprintf(cfg.Stdout, "  1: * %s\n", cfg.Run.Model())
		fmt.Fprintf(cfg.Stdout, "\nCurrent: %s\n", cfg.Run.Model())
		return
	}
	fmt.Fprintln(cfg.Stdout, "Available models:")
	modelIDs := make([]string, len(models))
	for i, m := range models {
		marker := "  "
		if m.ID == cfg.Run.Model() {
			marker = "* "
		}
		modelIDs[i] = m.ID
		fmt.Fprintf(cfg.Stdout, "%s%d: %s\n", marker, i+1, m.ID)
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
	modelIDs := make([]string, len(models))
	for i, m := range models {
		modelIDs[i] = m.ID
	}
	resolved := resolveByNameOrIndex(target, modelIDs)
	if resolved == "" {
		fmt.Fprintf(cfg.Stdout, "genie: no such model: %s\n", target)
		return
	}
	cfg.Run.SetModel(resolved)
	fmt.Fprintf(cfg.Stdout, "model: %s\n", resolved)
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
	for i, name := range names {
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
		fmt.Fprintf(cfg.Stdout, "%s%d: %s%s%s\n", marker, i+1, name, modelStr, toolsStr)
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
	reg := cfg.Run.AgentReg()
	if reg == nil {
		fmt.Fprintln(cfg.Stderr, "no agents configured")
		return
	}
	names := reg.List()
	resolved := resolveByNameOrIndex(target, names)
	if resolved == "" {
		fmt.Fprintf(cfg.Stderr, "genie: no such agent: %s\n", target)
		return
	}
	if _, err := cfg.Run.SwitchAgent(resolved); err != nil {
		fmt.Fprintf(cfg.Stderr, "genie: %v\n", err)
		return
	}
	resolvedAgent := cfg.Run.CurrentAgent()
	toolsStr := ""
	if resolvedAgent.Tools != nil {
		toolsStr = fmt.Sprintf(", tools: %s", strings.Join(resolvedAgent.Tools, ", "))
	}
	modelStr := ""
	if resolvedAgent.Model != "" {
		modelStr = fmt.Sprintf(" (model: %s)", resolvedAgent.Model)
	}
	fmt.Fprintf(cfg.Stdout, "switched to %s%s%s\n", resolvedAgent.Name, modelStr, toolsStr)
}

// handlePluginCommand routes /<plugin>[:<command>] [args...] to the plugin command source.
// It returns true when the command was handled (hit or miss).
func handlePluginCommand(line string, cmds plugin.CommandSource, cfg *Config) bool {
	// Parse colon-separated plugin command: /plugin[:command] [args...]
	rest := strings.TrimPrefix(line, "/")
	// Split on first space to separate command path from args
	cmdPath, args, _ := strings.Cut(rest, " ")
	args = strings.TrimSpace(args)

	// Split command path on colon
	pathParts := strings.Split(cmdPath, ":")

	// Handle bare plugin command (just /plugin or /plugin:)
	pluginName := strings.ToLower(pathParts[0])
	var command string
	if len(pathParts) >= 2 {
		command = pathParts[1]
	}

	if command == "" {
		return handlePluginBare(pluginName, cmds, cfg)
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

// printPluginCommandsHelp prints a plugin-commands section for /help, grouped
// per plugin (name + one-line description per command). A plugin whose hooks
// the circuit breaker has tripped is labelled with the events it is out of:
// breaker state is process-lifetime, so a session that starts with a plugin
// already tripped is degraded from its first turn with no new notice, and
// /help is the only place the user can go looking.
func printPluginCommandsHelp(cmds plugin.CommandSource, out io.Writer) {
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Plugin commands:")
	for _, name := range cmds.Plugins() {
		list, err := cmds.ListCommands(name)
		if err != nil {
			continue
		}
		fmt.Fprintf(out, "  %s%s\n", name, trippedLabel(cmds.TrippedHooks(name)))
		for _, c := range list {
			fmt.Fprintf(out, "  /%s:%s  %s\n", name, c.Name, c.Description)
		}
	}
}

// trippedLabel renders the breaker annotation for a plugin's block, or "" when
// the plugin is participating on every event it declared.
func trippedLabel(events []string) string {
	if len(events) == 0 {
		return ""
	}
	return "  [hooks disabled: " + strings.Join(events, ", ") + "]"
}

// resolveByNameOrIndex takes a target string and a list of names, and returns the
// matching name. Target can be a 1-based index (e.g., "1") or a name. Returns
// empty string if not found.
func resolveByNameOrIndex(target string, names []string) string {
	if target == "" {
		return ""
	}
	// Try as 1-based index first
	if idx, err := strconv.Atoi(target); err == nil {
		if idx >= 1 && idx <= len(names) {
			return names[idx-1]
		}
		return ""
	}
	// Try as name
	for _, name := range names {
		if name == target {
			return name
		}
	}
	return ""
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

// handleTask shows the active task and its lifecycle record.
func handleTask(cfg *Config) {
	state := cfg.Run.TaskState()
	active := state.Get()
	if active == nil {
		fmt.Fprintln(cfg.Stdout, "no active task")
		return
	}

	fmt.Fprintf(cfg.Stdout, "Active Task: %s\n", active.Task.ID)
	fmt.Fprintf(cfg.Stdout, "Title:       %s\n", active.Task.Title)
	fmt.Fprintf(cfg.Stdout, "Type:        %s\n", active.Task.Type)
	fmt.Fprintf(cfg.Stdout, "Labels:      %v\n", active.Task.Labels)
	fmt.Fprintf(cfg.Stdout, "Claimed at:  %s\n", active.ClaimedAt.Format(time.RFC3339))

	// Progress checkpoints (from task.progress, og-wm7.1.4)
	fmt.Fprintf(cfg.Stdout, "Progress checkpoints: (not yet implemented — see og-wm7.1.4)\n")

	// Lifecycle history from session transcript markers
	fmt.Fprintf(cfg.Stdout, "Lifecycle history:\n")
	sess := cfg.Run.Session()
	if sess != nil {
		lines, err := sess.Lines()
		if err != nil {
			fmt.Fprintf(cfg.Stdout, "  (error reading session: %v)\n", err)
		} else {
			found := false
			for _, ln := range lines {
				if meta, ok := ln.Metadata["task_state"]; ok {
					found = true
					fmt.Fprintf(cfg.Stdout, "  %s\n", meta)
				}
			}
			if !found {
				fmt.Fprintf(cfg.Stdout, "  (no task state markers in transcript)\n")
			}
		}
	}
}

// handleCopilotCommand handles /copilot:auth:login|status [--host <domain>].
func handleCopilotCommand(ctx context.Context, subPath, args string, cfg *Config) {
	// Parse subcommand path: auth:login or auth:status
	subParts := strings.Split(subPath, ":")
	if len(subParts) < 2 || subParts[0] != "auth" {
		fmt.Fprintln(cfg.Stdout, "Usage: /copilot:auth:login|status [--host <domain>]")
		return
	}

	subcmd := subParts[1]
	host := "github.com"
	// Parse --host from args
	argParts := strings.Fields(args)
	for i := 0; i < len(argParts); i++ {
		if argParts[i] == "--host" && i+1 < len(argParts) {
			host = argParts[i+1]
			i++
		}
	}

	switch subcmd {
	case "login":
		handleCopilotLogin(ctx, host, cfg)
	case "status":
		handleCopilotStatus(host, cfg)
	default:
		fmt.Fprintf(cfg.Stdout, "unknown copilot auth command: %s (try login|status)\n", subcmd)
	}
}

// handleCopilotLogin runs the device-flow login for the given host.
func handleCopilotLogin(ctx context.Context, host string, cfg *Config) {
	fmt.Fprintf(cfg.Stdout, "Starting device-flow login for %s...\n", host)

	dcr, err := copilot.StartDeviceFlow(ctx, host)
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "Error: failed to start device flow: %v\n", err)
		return
	}

	fmt.Fprintf(cfg.Stdout, "\nOpen %s and enter code: %s\n\n", dcr.VerificationURI, dcr.UserCode)

	// Try to open browser
	if err := copilot.OpenBrowser(dcr.VerificationURI); err != nil {
		fmt.Fprintf(cfg.Stdout, "(Could not open browser automatically: %v)\n", err)
	}

	fmt.Fprint(cfg.Stdout, "Waiting for authorization...")

	tr, err := copilot.PollForToken(ctx, host, dcr.DeviceCode, dcr.Interval)
	if err != nil {
		fmt.Fprintf(cfg.Stdout, "\nError: %v\n", err)
		return
	}

	fmt.Fprint(cfg.Stdout, "\n")

	// Fetch user info
	ui, err := copilot.FetchUserInfo(ctx, host, tr.AccessToken)
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "Warning: got token but failed to fetch user info: %v\n", err)
		ui = &copilot.UserInfo{Login: "unknown"}
	}

	// Write to credential store
	xdgDataHome := copilot.DefaultXDGDataHome()
	if err := copilot.WriteOAuthToken(xdgDataHome, host, tr.AccessToken, ui.Login); err != nil {
		fmt.Fprintf(cfg.Stderr, "Error: failed to write credentials: %v\n", err)
		return
	}

	fmt.Fprintf(cfg.Stdout, "Logged in as %s on %s\n", ui.Login, host)
	fmt.Fprintf(cfg.Stdout, "Credentials stored at %s/copilot/credentials.json\n", xdgDataHome)
}

// handleCopilotStatus shows the credential status for the given host.
func handleCopilotStatus(host string, cfg *Config) {
	xdgDataHome := copilot.DefaultXDGDataHome()
	path := copilot.StorePath(xdgDataHome)

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(cfg.Stdout, "No credentials found at %s\n", path)
		return
	}

	var store copilot.CredentialsFile
	if err := json.Unmarshal(data, &store); err != nil {
		fmt.Fprintf(cfg.Stderr, "Error: failed to parse credentials: %v\n", err)
		return
	}

	entry, ok := store.Hosts[host]
	if !ok {
		fmt.Fprintf(cfg.Stdout, "No credentials for host %s\n", host)
		return
	}

	fmt.Fprintf(cfg.Stdout, "Host: %s\n", host)
	fmt.Fprintf(cfg.Stdout, "User: %s\n", entry.User)
	fmt.Fprintf(cfg.Stdout, "Updated: %s\n", entry.UpdatedAt)
	if entry.OAuthToken != "" {
		// Show only prefix for security
		token := entry.OAuthToken
		if len(token) > 8 {
			token = token[:8] + "..."
		}
		fmt.Fprintf(cfg.Stdout, "OAuth Token: %s\n", token)
	}
}
