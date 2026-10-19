package repl

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/chzyer/readline"
	"github.com/okayest-dev/genie/internal/permissions"
	"github.com/okayest-dev/genie/internal/style"
)

// fakeLineReader is a lineReader whose replies are scripted: it returns queued
// lines in order, then err (io.EOF when unset). It records the prompts set.
type fakeLineReader struct {
	lines     []string
	err       error
	prompts   []string // all prompts set via SetPrompt
	lastPrompt string  // last non-empty prompt set
}

func (r *fakeLineReader) Readline() (string, error) {
	if len(r.lines) == 0 {
		if r.err != nil {
			return "", r.err
		}
		return "", io.EOF
	}
	line := r.lines[0]
	r.lines = r.lines[1:]
	return line, nil
}

func (r *fakeLineReader) SetPrompt(prompt string) {
	r.prompts = append(r.prompts, prompt)
	if prompt != "" {
		r.lastPrompt = prompt
	}
}

// negotiatorHarness builds an interactiveNegotiator over a fakeLineReader.
func negotiatorHarness(t *testing.T, lines ...string) (*interactiveNegotiator, *fakeLineReader, *bytes.Buffer) {
	t.Helper()
	rl := &fakeLineReader{lines: lines}
	var out bytes.Buffer
	router := newInterruptRouter()
	// Use a TTY profile so the negotiator uses the readline prompt path.
	renderer := style.NewRenderer(&style.RenderProfile{ColourProfile: style.ColourProfileAscii, IsTTY: true})
	n := &interactiveNegotiator{rl: rl, renderer: renderer, promptStyle: style.Style{}, router: router}
	return n, rl, &out
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
	n, rl, _ := negotiatorHarness(t, "s")

	got, err := n.Negotiate(context.Background(), permissions.AxisWrite, "/tmp/x.go")
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if got != permissions.ResponseSession {
		t.Fatalf("resp = %q, want %q", got, permissions.ResponseSession)
	}
	wantPrompt := permissions.RenderPrompt(permissions.AxisWrite, "/tmp/x.go")
	if rl.lastPrompt == "" {
		t.Errorf("readline prompt was not set during negotiation")
	}
	if !strings.Contains(rl.lastPrompt, wantPrompt) {
		t.Errorf("readline prompt = %q, want it to contain prompt %q", rl.lastPrompt, wantPrompt)
	}
	// After negotiation, prompt should be blanked (lastPrompt stays as the last non-empty)
	// The current prompt is blanked, but lastPrompt retains the negotiation prompt
}

func TestInteractiveNegotiatorBlanketPromptOmitsScope(t *testing.T) {
	n, rl, _ := negotiatorHarness(t, "o")

	if _, err := n.Negotiate(context.Background(), permissions.AxisNet, ""); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if rl.lastPrompt == "" {
		t.Errorf("readline prompt was not set during negotiation")
	}
	if strings.Contains(rl.lastPrompt, "net /") {
		t.Errorf("readline prompt = %q, want blanket prompt without a scope", rl.lastPrompt)
	}
	if !strings.Contains(rl.lastPrompt, "allow net?") {
		t.Errorf("readline prompt = %q, want blanket prompt 'allow net?'", rl.lastPrompt)
	}
}

func TestInteractiveNegotiatorUnknownChoiceReprompts(t *testing.T) {
	n, rl, _ := negotiatorHarness(t, "maybe", "p")

	got, err := n.Negotiate(context.Background(), permissions.AxisEnv, "PATH")
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if got != permissions.ResponsePermanent {
		t.Fatalf("resp = %q, want %q", got, permissions.ResponsePermanent)
	}
	// The prompt should have been set twice (initial + re-prompt)
	// We can't easily check the unknown hint output since it goes to os.Stdout,
	// but we can verify the prompt was set (lastPrompt retains the last non-empty)
	if rl.lastPrompt == "" {
		t.Errorf("readline prompt was not set during negotiation")
	}
	// Should have been called SetPrompt at least twice (initial + re-prompt + blank)
	if len(rl.prompts) < 3 {
		t.Errorf("SetPrompt called %d times, want at least 3 (prompt + re-prompt + blank)", len(rl.prompts))
	}
}

func TestInteractiveNegotiatorCtrlCRejects(t *testing.T) {
	n, rl, _ := negotiatorHarness(t)
	// readline reports ^C as a read error, not a line.
	rl.err = readline.ErrInterrupt

	got, err := n.Negotiate(context.Background(), permissions.AxisRun, "ls")
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if got != permissions.ResponseReject {
		t.Fatalf("resp = %q, want reject on ^C", got)
	}
	// Prompt should be blanked after rejection (current prompt is "", lastPrompt retains the negotiation prompt)
	if len(rl.prompts) == 0 {
		t.Errorf("SetPrompt was not called during negotiation")
	}
}

func TestInteractiveNegotiatorEOFRejects(t *testing.T) {
	n, rl, _ := negotiatorHarness(t)
	rl.err = io.EOF

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
	n, _, _ := negotiatorHarness(t, "r")

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
