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
	"strings"

	"github.com/okayest-dev/og/internal/agent"
	"github.com/okayest-dev/og/internal/llm/openai"
	"github.com/okayest-dev/og/internal/session"
	"github.com/okayest-dev/og/internal/tools"
)

const prompt = "og> "

// Config holds the dependencies for running the REPL.
type Config struct {
	Client      *openai.Client
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
func Run(ctx context.Context, cfg Config) error {
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
			if handleSlashCommand(line, cfg, &sess) {
				return nil
			}
			continue
		}

		// Run the agent turn with context for cancellation.
		turnCtx, cancel := context.WithCancel(ctx)

		// Run the turn in a goroutine so we can listen for Ctrl+C.
		errCh := make(chan error, 1)
		go func() {
			errCh <- agent.RunTurn(turnCtx, cfg.Client, cfg.Model, cfg.Instruction, line, cfg.Stdout, cfg.Stderr, sess, cfg.Registry)
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
func handleSlashCommand(line string, cfg Config, sess **session.Session) bool {
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/quit", "/exit":
		return true

	case "/help":
		fmt.Fprintln(cfg.Stdout, "Commands:")
		fmt.Fprintln(cfg.Stdout, "  /help    show this help")
		fmt.Fprintln(cfg.Stdout, "  /quit    exit the REPL")
		fmt.Fprintln(cfg.Stdout, "  /new     start a new session")
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

	default:
		fmt.Fprintf(cfg.Stdout, "unknown command: %s (try /help)\n", cmd)
	}

	return false
}
