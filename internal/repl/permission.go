package repl

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/okayest-dev/genie/internal/permissions"
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

// interactiveNegotiator implements permissions.Negotiator over the REPL's
// shared line channel: it renders the terse prompt, reads one line per
// attempt, loops on unknown input with the choice hint, and treats ^C as a
// rejection of the current axis (never a turn cancel).
type interactiveNegotiator struct {
	lines  <-chan string
	out    io.Writer
	router *interruptRouter
}

// Negotiate prompts for one axis. The prompt and its re-prompts are
// stdout-only; nothing here enters the session transcript or model history.
func (n *interactiveNegotiator) Negotiate(ctx context.Context, axis permissions.Axis, scope string) (permissions.Response, error) {
	n.router.enterPrompt()
	defer n.router.leavePrompt()

	promptText := permissions.RenderPrompt(axis, scope)
	for {
		fmt.Fprint(n.out, promptText)
		select {
		case <-ctx.Done():
			fmt.Fprintln(n.out)
			return permissions.ResponseReject, nil
		case <-n.router.promptCh:
			// ^C rejects the current axis; the chain continues.
			fmt.Fprintln(n.out)
			return permissions.ResponseReject, nil
		case line, ok := <-n.lines:
			if !ok {
				// EOF denies (fail safe).
				fmt.Fprintln(n.out)
				return permissions.ResponseReject, nil
			}
			resp, known := parseChoice(line)
			if !known {
				fmt.Fprintln(n.out)
				fmt.Fprintln(n.out, permissions.RenderUnknownHint())
				continue
			}
			fmt.Fprintln(n.out)
			return resp, nil
		}
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
