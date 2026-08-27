// Package repl implements the interactive REPL (Read-Eval-Print Loop) for og.
// It provides a canonical-mode line reader with live streaming, Ctrl+C handling
// across three zones, slash commands, and interactive confirm prompts.
package repl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/okayest-dev/og/internal/agent"
	"github.com/okayest-dev/og/internal/config"
	"github.com/okayest-dev/og/internal/instruct"
	"github.com/okayest-dev/og/internal/ledger"
	"github.com/okayest-dev/og/internal/llm"
	"github.com/okayest-dev/og/internal/session"
	"github.com/okayest-dev/og/internal/tools"
)

const prompt = "og> "

// Config holds the dependencies for running the REPL.
type Config struct {
	Client       llm.Client
	Model        string
	Instruction  string // default instruction (no-agent fallback)
	SessionDir   string
	Registry     *tools.Registry // global registry
	Cwd          string          // working directory for AGENTS.md lookup
	Cfg          *config.Config  // harness config (for instruction assembly, agent resolution)
	AgentReg     *config.AgentReg
	DefaultAgent *config.ResolvedAgent
	BashTimeout  time.Duration
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
}

// replState holds mutable agent state for the duration of a REPL session.
type replState struct {
	currentAgent  *config.ResolvedAgent
	previousAgent *config.ResolvedAgent
	instruction   string
}

// Run starts the interactive REPL loop. It reads user input, runs agent
// turns, and handles slash commands. The REPL exits on /quit or EOF.
func Run(ctx context.Context, cfg *Config) error {
	sess, err := session.New(cfg.SessionDir)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	fmt.Fprintf(cfg.Stderr, "session: %s\n", sess.ID)

	state := &replState{
		currentAgent: cfg.DefaultAgent,
	}
	state.instruction = resolveInstruction(cfg, state.currentAgent)

	// Set up signal handling for Ctrl+C.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	scanner := bufio.NewScanner(cfg.Stdin)
	for {
		// Check for pending interrupt.
		select {
		case <-sigCh:
			// Ctrl+C at idle: exit.
			fmt.Fprintln(cfg.Stderr)
			return nil
		default:
		}

		fmt.Fprint(cfg.Stdout, prompt)
		if !scanner.Scan() {
			// EOF or read error.
			fmt.Fprintln(cfg.Stderr)
			return nil
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// 1. Parse @name one-shot (before slash commands).
		if agentName, rest, ok := parseInlineAgent(line); ok {
			handleInlineAgent(ctx, agentName, rest, cfg, state, sess, sigCh)
			continue
		}

		// 2. Handle slash commands.
		if strings.HasPrefix(line, "/") {
			if handleSlashCommand(ctx, line, cfg, state, &sess) {
				return nil
			}
			continue
		}

		// 3. Normal turn.
		runTurn(ctx, cfg, state, line, sess, sigCh)
	}
}

// resolveInstruction assembles the instruction for the current agent.
func resolveInstruction(cfg *Config, agent *config.ResolvedAgent) string {
	if agent == nil {
		return cfg.Instruction
	}
	s, err := instruct.LoadWithAgent(cfg.Cfg, agent, cfg.Cwd)
	if err != nil {
		slog.Error("failed to resolve instruction", "error", err)
		return cfg.Instruction
	}
	return s
}

// resolveRegistry returns the tool registry scoped to the current agent.
func resolveRegistry(cfg *Config, agent *config.ResolvedAgent) *tools.Registry {
	if agent == nil {
		return cfg.Registry
	}
	if agent.Tools == nil {
		return cfg.Registry
	}
	return cfg.Registry.Subset(agent.Tools)
}

// currentModel returns the model for the current agent, falling back to config.
func currentModel(cfg *Config, agent *config.ResolvedAgent) string {
	if agent != nil && agent.Model != "" {
		return agent.Model
	}
	return cfg.Model
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

// handleInlineAgent runs a one-shot agent turn, then reverts state.
func handleInlineAgent(ctx context.Context, agentName, prompt string, cfg *Config, state *replState, sess *session.Session, sigCh <-chan os.Signal) {
	if prompt == "" {
		fmt.Fprintf(cfg.Stderr, "og: @%s requires a prompt\n", agentName)
		return
	}

	// Look up the agent.
	resolved, err := cfg.AgentReg.GetResolved(agentName, cfg.Cfg)
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "og: %v\n", err)
		return
	}

	// Validate tools.
	if err := cfg.Registry.ValidateTools(resolved.Tools); err != nil {
		fmt.Fprintf(cfg.Stderr, "og: agent %q: %v\n", agentName, err)
		return
	}

	// Save and switch.
	state.previousAgent = state.currentAgent
	state.currentAgent = resolved

	// Run the turn.
	runTurn(ctx, cfg, state, prompt, sess, sigCh)

	// Revert.
	state.currentAgent = state.previousAgent
	state.previousAgent = nil
	state.instruction = resolveInstruction(cfg, state.currentAgent)
}

// runTurn executes a single agent turn with the current agent state.
func runTurn(ctx context.Context, cfg *Config, state *replState, prompt string, sess *session.Session, sigCh <-chan os.Signal) {
	// Resolve instruction for this turn.
	instruction := resolveInstruction(cfg, state.currentAgent)
	registry := resolveRegistry(cfg, state.currentAgent)
	model := currentModel(cfg, state.currentAgent)

	var opts []agent.Option
	if state.currentAgent != nil {
		opts = append(opts, agent.WithAgentName(state.currentAgent.Name))
	}

	turnCtx, cancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.RunTurn(turnCtx, cfg.Client, model, instruction, prompt,
			cfg.Stdout, cfg.Stderr, sess, registry, nil, cfg.Cwd,
			filterHistory(sess.History()), opts...)
	}()

	select {
	case <-sigCh:
		cancel()
		fmt.Fprintln(cfg.Stderr, "\n[turn cancelled]")
	case err := <-errCh:
		cancel()
		if err != nil {
			fmt.Fprintf(cfg.Stderr, "Error: %v\n", err)
		}
	}
}

// handleSlashCommand processes a slash command and returns true if the REPL
// should exit.
func handleSlashCommand(ctx context.Context, line string, cfg *Config, state *replState, sess **session.Session) bool {
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
		fmt.Fprintln(cfg.Stdout, "  /model            list available models")
		fmt.Fprintln(cfg.Stdout, "  /model <id>       switch to a different model")
		fmt.Fprintln(cfg.Stdout, "  /agent            list available agents")
		fmt.Fprintln(cfg.Stdout, "  /agent <name>     switch to a named agent")
		fmt.Fprintln(cfg.Stdout, "")
		fmt.Fprintln(cfg.Stdout, "  @<name> <prompt>  one-shot agent switch")
		fmt.Fprintln(cfg.Stdout, "")
		fmt.Fprintln(cfg.Stdout, "Ctrl+C: quit at idle, cancel mid-turn")

	case "/new":
		var err error
		*sess, err = session.New(cfg.SessionDir)
		if err != nil {
			fmt.Fprintf(cfg.Stderr, "Error: %v\n", err)
		} else {
			fmt.Fprintf(cfg.Stderr, "session: %s\n", (*sess).ID)
		}

	case "/changes":
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}
		handleChanges(args, cfg, (*sess).ID, cfg.Stdout)

	case "/model":
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			// List models.
			models, err := cfg.Client.ListModels(ctx)
			if err != nil {
				fmt.Fprintf(cfg.Stderr, "Error: fetching model catalog: %v\n", err)
				return false
			}
			fmt.Fprintln(cfg.Stdout, "Available models:")
			for _, m := range models {
			_marker := "  "
				if m.ID == cfg.Model {
					_marker = "* "
				}
				fmt.Fprintf(cfg.Stdout, "%s%s\n", _marker, m.ID)
			}
			fmt.Fprintf(cfg.Stdout, "\nCurrent: %s\n", cfg.Model)
		} else {
			// Switch model.
			target := strings.TrimSpace(parts[1])
			models, err := cfg.Client.ListModels(ctx)
			if err != nil {
				fmt.Fprintf(cfg.Stderr, "Error: fetching model catalog: %v\n", err)
				return false
			}
			found := false
			for _, m := range models {
				if m.ID == target {
					found = true
					break
				}
			}
			if !found {
				fmt.Fprintf(cfg.Stdout, "og: no such model: %s\n", target)
				return false
			}
			cfg.Model = target
			fmt.Fprintf(cfg.Stdout, "model: %s\n", cfg.Model)
		}

	case "/agent":
		if cfg.AgentReg == nil {
			fmt.Fprintln(cfg.Stdout, "no agents configured")
			return false
		}
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			// List agents.
			names := cfg.AgentReg.List()
			if len(names) == 0 {
				fmt.Fprintln(cfg.Stdout, "no agents configured")
				return false
			}
			fmt.Fprintln(cfg.Stdout, "Available agents:")
			for _, name := range names {
				_marker := "  "
				if state.currentAgent != nil && state.currentAgent.Name == name {
					_marker = "* "
				}
				def, _ := cfg.AgentReg.Get(name)
				resolved, _ := cfg.AgentReg.GetResolved(name, cfg.Cfg)
				modelStr := ""
				if resolved != nil {
					modelStr = fmt.Sprintf("  model: %s", resolved.Model)
				}
				toolsStr := ""
				if def != nil && def.Tools != nil && resolved != nil {
					toolsStr = fmt.Sprintf("  tools: %s", strings.Join(resolved.Tools, ", "))
				}
				fmt.Fprintf(cfg.Stdout, "%s%s%s%s\n", _marker, name, modelStr, toolsStr)
			}
			if state.currentAgent != nil {
				fmt.Fprintf(cfg.Stdout, "\nCurrent: %s\n", state.currentAgent.Name)
			} else {
				fmt.Fprintln(cfg.Stdout, "\nCurrent: (default)")
			}
			return false
		}
		// Switch agent.
		target := strings.TrimSpace(parts[1])
		resolved, err := cfg.AgentReg.GetResolved(target, cfg.Cfg)
		if err != nil {
			fmt.Fprintf(cfg.Stderr, "og: %v\n", err)
			return false
		}
		if err := cfg.Registry.ValidateTools(resolved.Tools); err != nil {
			fmt.Fprintf(cfg.Stderr, "og: agent %q: %v\n", target, err)
			return false
		}
		state.currentAgent = resolved
		state.instruction = resolveInstruction(cfg, state.currentAgent)
		toolsStr := ""
		if resolved.Tools != nil {
			toolsStr = fmt.Sprintf(", tools: %s", strings.Join(resolved.Tools, ", "))
		}
		fmt.Fprintf(cfg.Stdout, "switched to %s (model: %s%s)\n", resolved.Name, resolved.Model, toolsStr)

	default:
		fmt.Fprintf(cfg.Stdout, "unknown command: %s (try /help)\n", cmd)
	}

	return false
}

// filterHistory strips system messages from the history slice, since RunTurn
// always injects the current instruction as the sole system message.
func filterHistory(history []llm.Message) []llm.Message {
	var out []llm.Message
	for _, msg := range history {
		if msg.Role != llm.RoleSystem {
			out = append(out, msg)
		}
	}
	return out
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
		fmt.Fprintf(out, "og: invalid change id: %s\n", args)
		return
	}
	batch, err := ledger.LoadBatchByID(cfg.SessionDir, sessionID, id)
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "Error: %v\n", err)
		return
	}
	if batch == nil {
		fmt.Fprintf(out, "og: no such change id: %d\n", id)
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
