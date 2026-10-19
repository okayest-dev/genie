package repl

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/style"
)

// routerMode is the current SIGINT consumer.
type routerMode int

const (
	modeIdle routerMode = iota
	modeTurn
	modePrompt
)

// interruptRouter routes SIGINT (Ctrl+C) to the active consumer: a live
// escalation prompt, else the running turn, else the idle REPL. While a prompt
// is active it owns delivery, so ^C rejects the current axis instead of
// cancelling the turn.
type interruptRouter struct {
	mu       sync.Mutex
	mode     routerMode
	turnCh   chan struct{}
	promptCh chan struct{}
	idleCh   chan struct{}
}

func newInterruptRouter() *interruptRouter {
	return &interruptRouter{
		turnCh:   make(chan struct{}, 1),
		promptCh: make(chan struct{}, 1),
		idleCh:   make(chan struct{}, 1),
	}
}

func (r *interruptRouter) set(mode routerMode) {
	r.mu.Lock()
	r.mode = mode
	r.mu.Unlock()
}

// deliver routes one SIGINT to the consumer for the current mode. It never
// blocks: a consumer that has already gone is a no-op. The lock is held across
// the send so a mode change cannot leave an interrupt queued for a consumer
// that has already retired.
func (r *interruptRouter) deliver() {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ch chan struct{}
	switch r.mode {
	case modePrompt:
		ch = r.promptCh
	case modeTurn:
		ch = r.turnCh
	default:
		ch = r.idleCh
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (r *interruptRouter) enterPrompt() { r.set(modePrompt) }

// leavePrompt returns delivery to the turn and drops any interrupt that raced
// the prompt resolving on a line, so the next prompt starts clean.
func (r *interruptRouter) leavePrompt() {
	r.drainPrompt()
	r.set(modeTurn)
}

// drainPrompt drops a stale prompt interrupt left by a race between a SIGINT
// and the previous prompt resolving.
func (r *interruptRouter) drainPrompt() {
	for {
		select {
		case <-r.promptCh:
		default:
			return
		}
	}
}

// drainTurn drops a stale turn interrupt left by a SIGINT that landed as the
// previous turn finished, so it cannot cancel the next turn.
func (r *interruptRouter) drainTurn() {
	for {
		select {
		case <-r.turnCh:
		default:
			return
		}
	}
}

// lineReader is the input surface the negotiator needs: read one line, and
// take over the prompt (the negotiator renders its own, so readline's idle
// prompt is blanked while a permission question is live).
type lineReader interface {
	Readline() (string, error)
	SetPrompt(prompt string)
}

// interactiveNegotiator implements permissions.Negotiator over a lineReader:
// it renders the terse prompt, reads one line per attempt, loops on unknown
// input with the choice hint, and treats ^C as a rejection of the current axis
// (never a turn cancel).
type interactiveNegotiator struct {
	rl           lineReader
	renderer     *style.Renderer
	promptStyle  style.Style
	router       *interruptRouter
}

// Negotiate prompts for one axis. The prompt and its re-prompts are
// stdout-only; nothing here enters the session transcript or model history.
func (n *interactiveNegotiator) Negotiate(ctx context.Context, axis permissions.Axis, scope string) (permissions.Response, error) {
	n.router.enterPrompt()
	defer n.router.leavePrompt()

	promptText := permissions.RenderPrompt(axis, scope)
	styledPrompt := n.renderer.Render(n.promptStyle, promptText)

	// In non-TTY environments (tests, pipes), readline doesn't display the
	// prompt. Write it directly to stdout, then use readline to read the line.
	if !n.renderer.IsTTY() {
		return n.negotiateNonTTY(ctx, styledPrompt)
	}

	for {
		if ctx.Err() != nil {
			// The turn was cancelled; decline the axis.
			n.rl.SetPrompt("")
			return permissions.ResponseReject, nil
		}
		n.rl.SetPrompt(styledPrompt)
		line, err := n.rl.Readline()
		n.rl.SetPrompt("")
		if err != nil {
			// EOF, ^C (readline.ErrInterrupt), or read error denies (fail safe).
			return permissions.ResponseReject, nil
		}
		resp, known := parseChoice(line)
		if !known {
			fmt.Fprint(os.Stdout, n.renderer.Render(n.promptStyle, "\n"))
			fmt.Fprint(os.Stdout, n.renderer.Render(n.promptStyle, permissions.RenderUnknownHint()+"\n"))
			continue
		}
		return resp, nil
	}
}

// negotiateNonTTY handles permission prompts in non-TTY environments by
// writing the prompt directly to stdout, then using readline to read the line.
func (n *interactiveNegotiator) negotiateNonTTY(ctx context.Context, styledPrompt string) (permissions.Response, error) {
	for {
		if ctx.Err() != nil {
			return permissions.ResponseReject, nil
		}
		fmt.Fprint(os.Stdout, styledPrompt)
		line, err := n.rl.Readline()
		if err != nil {
			// EOF, ^C, or read error denies (fail safe).
			return permissions.ResponseReject, nil
		}
		resp, known := parseChoice(line)
		if !known {
			fmt.Fprint(os.Stdout, n.renderer.Render(n.promptStyle, "\n"))
			fmt.Fprint(os.Stdout, n.renderer.Render(n.promptStyle, permissions.RenderUnknownHint()+"\n"))
			continue
		}
		return resp, nil
	}
}

// parseChoice maps a prompt line to a response. Terse keys and full words are
// accepted, case-insensitively.
func parseChoice(line string) (permissions.Response, bool) {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "o", "once":
		return permissions.ResponseOnce, true
	case "s", "session":
		return permissions.ResponseSession, true
	case "p", "permanent":
		return permissions.ResponsePermanent, true
	case "r", "reject":
		return permissions.ResponseReject, true
	default:
		return "", false
	}
}
