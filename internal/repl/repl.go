// Package repl implements the interactive REPL (Read-Eval-Print Loop) for og.
// It provides a canonical-mode line reader with live streaming, Ctrl+C handling
// across three zones, slash commands, and interactive confirm prompts.
package repl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/okayest-dev/og/internal/agent"
	"github.com/okayest-dev/og/internal/ledger"
	"github.com/okayest-dev/og/internal/llm"
	"github.com/okayest-dev/og/internal/session"
	"github.com/okayest-dev/og/internal/tools"
)

const prompt = "og> "

// Config holds the dependencies for running the REPL.
type Config struct {
	Client      llm.Client
	Model       string
	Instruction string
	SessionDir  string
	Registry    *tools.Registry
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

// Run starts the interactive REPL loop. It reads user input, runs agent
// turns, and handles slash commands. The REPL exits on /quit or EOF.
func Run(ctx context.Context, cfg *Config) error {
	sess, err := session.New(cfg.SessionDir)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	fmt.Fprintf(cfg.Stderr, "session: %s\n", sess.ID)

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

		// Handle slash commands.
		if strings.HasPrefix(line, "/") {
			if handleSlashCommand(ctx, line, cfg, &sess) {
				return nil
			}
			continue
		}

		// Run the agent turn with context for cancellation.
		turnCtx, cancel := context.WithCancel(ctx)

		// Run the turn in a goroutine so we can listen for Ctrl+C.
		errCh := make(chan error, 1)
		go func() {
			errCh <- agent.RunTurn(turnCtx, cfg.Client, cfg.Model, cfg.Instruction, line, cfg.Stdout, cfg.Stderr, sess, cfg.Registry, nil, "")
		}()

		// Wait for turn to complete or Ctrl+C.
		select {
		case <-sigCh:
			// Ctrl+C mid-turn: cancel the turn.
			cancel()
			fmt.Fprintln(cfg.Stderr, "\n[turn cancelled]")
		case err := <-errCh:
			cancel()
			if err != nil {
				fmt.Fprintf(cfg.Stderr, "Error: %v\n", err)
			}
		}
	}
}

// handleSlashCommand processes a slash command and returns true if the REPL
// should exit.
func handleSlashCommand(ctx context.Context, line string, cfg *Config, sess **session.Session) bool {
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

	default:
		fmt.Fprintf(cfg.Stdout, "unknown command: %s (try /help)\n", cmd)
	}

	return false
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
