package repl

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/permissions"
)

// negotiatorHarness builds an interactiveNegotiator reading from a buffered
// line channel, returning the negotiator and a send function.
func negotiatorHarness(t *testing.T) (*interactiveNegotiator, chan string, *bytes.Buffer) {
	t.Helper()
	lines := make(chan string, 8)
	var out bytes.Buffer
	router := newInterruptRouter()
	n := &interactiveNegotiator{lines: lines, out: &out, router: router}
	return n, lines, &out
}

func TestParseChoice(t *testing.T) {
	cases := []struct {
		in   string
		want permissions.Response
		ok   bool
	}{
		{"o", permissions.ResponseOnce, true},
		{"O", permissions.ResponseOnce, true},
		{"once", permissions.ResponseOnce, true},
		{"s", permissions.ResponseSession, true},
		{" session ", permissions.ResponseSession, true},
		{"p", permissions.ResponsePermanent, true},
		{"permanent", permissions.ResponsePermanent, true},
		{"r", permissions.ResponseReject, true},
		{"reject", permissions.ResponseReject, true},
		{"", "", false},
		{"y", "", false},
		{"always", "", false},
	}
	for _, tc := range cases {
		got, ok := parseChoice(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseChoice(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestInteractiveNegotiatorGrantsSession(t *testing.T) {
	n, lines, out := negotiatorHarness(t)
	lines <- "s"

	got, err := n.Negotiate(context.Background(), permissions.AxisWrite, "/tmp/x.go")
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if got != permissions.ResponseSession {
		t.Fatalf("resp = %q, want %q", got, permissions.ResponseSession)
	}
	wantPrompt := permissions.RenderPrompt(permissions.AxisWrite, "/tmp/x.go")
	if !strings.Contains(out.String(), wantPrompt) {
		t.Errorf("out = %q, want it to contain prompt %q", out.String(), wantPrompt)
	}
}

func TestInteractiveNegotiatorBlanketPromptOmitsScope(t *testing.T) {
	n, lines, out := negotiatorHarness(t)
	lines <- "o"

	if _, err := n.Negotiate(context.Background(), permissions.AxisNet, ""); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if strings.Contains(out.String(), "net /") {
		t.Errorf("out = %q, want blanket prompt without a scope", out.String())
	}
	if !strings.Contains(out.String(), "allow net?") {
		t.Errorf("out = %q, want blanket prompt 'allow net?'", out.String())
	}
}

func TestInteractiveNegotiatorUnknownChoiceReprompts(t *testing.T) {
	n, lines, out := negotiatorHarness(t)
	lines <- "maybe"
	lines <- "p"

	got, err := n.Negotiate(context.Background(), permissions.AxisEnv, "PATH")
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if got != permissions.ResponsePermanent {
		t.Fatalf("resp = %q, want %q", got, permissions.ResponsePermanent)
	}
	if !strings.Contains(out.String(), permissions.RenderUnknownHint()) {
		t.Errorf("out = %q, want the unknown-choice hint", out.String())
	}
	if n := strings.Count(out.String(), permissions.RenderPrompt(permissions.AxisEnv, "PATH")); n != 2 {
		t.Errorf("prompt rendered %d times, want 2 (initial + re-prompt)", n)
	}
}

func TestInteractiveNegotiatorCtrlCRejects(t *testing.T) {
	n, _, out := negotiatorHarness(t)
	// Stage the interrupt the way a SIGINT arriving mid-prompt would: mode is
	// already prompt, and the value is waiting when Negotiate selects.
	n.router.enterPrompt()
	n.router.deliver()

	got, err := n.Negotiate(context.Background(), permissions.AxisRun, "ls")
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if got != permissions.ResponseReject {
		t.Fatalf("resp = %q, want reject on ^C", got)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Errorf("out = %q, want a newline after the aborted prompt", out.String())
	}
}

func TestInteractiveNegotiatorEOFRejects(t *testing.T) {
	n, lines, _ := negotiatorHarness(t)
	close(lines)

	got, err := n.Negotiate(context.Background(), permissions.AxisWrite, "x")
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if got != permissions.ResponseReject {
		t.Fatalf("resp = %q, want reject on EOF", got)
	}
}

func TestInteractiveNegotiatorCtxCancelRejects(t *testing.T) {
	n, _, _ := negotiatorHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := n.Negotiate(ctx, permissions.AxisWrite, "x")
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if got != permissions.ResponseReject {
		t.Fatalf("resp = %q, want reject on cancel", got)
	}
}

func TestInteractiveNegotiatorRestoresTurnMode(t *testing.T) {
	n, lines, _ := negotiatorHarness(t)
	lines <- "r"

	if _, err := n.Negotiate(context.Background(), permissions.AxisWrite, "x"); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	n.router.mu.Lock()
	mode := n.router.mode
	n.router.mu.Unlock()
	if mode != modeTurn {
		t.Fatalf("router mode = %v, want modeTurn after prompt", mode)
	}
}

func TestInterruptRouterRoutesByMode(t *testing.T) {
	cases := []struct {
		mode routerMode
		recv func(r *interruptRouter) <-chan struct{}
	}{
		{modeIdle, func(r *interruptRouter) <-chan struct{} { return r.idleCh }},
		{modeTurn, func(r *interruptRouter) <-chan struct{} { return r.turnCh }},
		{modePrompt, func(r *interruptRouter) <-chan struct{} { return r.promptCh }},
	}
	for _, tc := range cases {
		r := newInterruptRouter()
		r.set(tc.mode)
		r.deliver()
		select {
		case <-tc.recv(r):
		default:
			t.Errorf("mode %v: interrupt not delivered to expected channel", tc.mode)
		}
	}
}

func TestInterruptRouterDeliverNeverBlocks(t *testing.T) {
	r := newInterruptRouter()
	r.set(modeIdle)
	r.deliver()
	r.deliver()
}

func TestInterruptRouterDrainTurn(t *testing.T) {
	r := newInterruptRouter()
	r.set(modeTurn)
	r.deliver()
	// The turn finishes before consuming the interrupt; the next turn must not
	// inherit it.
	r.set(modeIdle)
	r.drainTurn()
	r.set(modeTurn)
	select {
	case <-r.turnCh:
		t.Fatal("stale turn interrupt survived drain")
	default:
	}
}
