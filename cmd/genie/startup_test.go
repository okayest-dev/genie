package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/permissions"
)

// TestRegistryFromConfigResolvesDefaults is the integration seam: the parsed
// config tables (shipped defaults) bridge into the registry and boot the
// named provider on its own default model.
func TestRegistryFromConfigResolvesDefaults(t *testing.T) {
	cfg, err := config.Parse(nil, "/home/u", nil)
	if err != nil {
		t.Fatalf("config.Parse: %v", err)
	}
	r := registryFromConfig(cfg)
	model, err := r.DefaultModel("zen")
	if err != nil {
		t.Fatalf("DefaultModel(zen): %v", err)
	}
	if model != "big-pickle" {
		t.Errorf("model = %q, want the shipped zen default %q", model, "big-pickle")
	}
	client, err := r.Client("zen")
	if err != nil {
		t.Fatalf("Client(zen): %v", err)
	}
	if client == nil {
		t.Fatal("registry returned a nil client")
	}
}

// TestSelectStartupProviderExplicitWins: an explicitly selected provider is
// returned as-is in both modes — no prompt, no fallback, no warning.
func TestSelectStartupProviderExplicitWins(t *testing.T) {
	r := llm.NewRegistry(map[string]llm.ProviderSpec{
		"zen": {Wire: "openai", Model: "big-pickle"},
	})
	for _, interactive := range []bool{true, false} {
		var stderr bytes.Buffer
		picked, err := selectStartupProvider(r, "zen", interactive, strings.NewReader(""), io.Discard, &stderr)
		if err != nil {
			t.Fatalf("interactive=%v: selectStartupProvider: %v", interactive, err)
		}
		if picked != "zen" {
			t.Errorf("interactive=%v: picked = %q, want %q", interactive, picked, "zen")
		}
		if stderr.String() != "" {
			t.Errorf("interactive=%v: stderr = %q, want empty", interactive, stderr.String())
		}
	}
}

// TestSelectStartupProviderInteractivePrompts: with the selection key unset,
// an interactive run prompts for a pick from the declared set and returns it.
func TestSelectStartupProviderInteractivePrompts(t *testing.T) {
	r := llm.NewRegistry(map[string]llm.ProviderSpec{
		"zen": {Wire: "openai", Model: "big-pickle"},
	})
	var stdout bytes.Buffer
	picked, err := selectStartupProvider(r, "", true, strings.NewReader("zen\n"), &stdout, io.Discard)
	if err != nil {
		t.Fatalf("selectStartupProvider: %v", err)
	}
	if picked != "zen" {
		t.Errorf("picked = %q, want %q", picked, "zen")
	}
	if !strings.Contains(stdout.String(), "Available providers:") {
		t.Errorf("stdout = %q, want the provider list", stdout.String())
	}
}

// TestSelectStartupProviderOneShotFallsBackToFirstDeclared pins the one-shot
// -p rule: with no selection, the run starts on the first declared provider —
// the registry's deterministic sorted order — and warns on stderr naming it.
func TestSelectStartupProviderOneShotFallsBackToFirstDeclared(t *testing.T) {
	r := llm.NewRegistry(map[string]llm.ProviderSpec{
		"zen":   {Wire: "openai", Model: "zen-model"},
		"alpha": {Wire: "openai", Model: "alpha-model"},
		"mid":   {Wire: "openai", Model: "mid-model"},
	})
	var stderr bytes.Buffer
	picked, err := selectStartupProvider(r, "", false, nil, io.Discard, &stderr)
	if err != nil {
		t.Fatalf("selectStartupProvider: %v", err)
	}
	if picked != "alpha" {
		t.Errorf("picked = %q, want the first declared provider %q", picked, "alpha")
	}
	if !strings.Contains(stderr.String(), "no provider selected") || !strings.Contains(stderr.String(), "alpha") {
		t.Errorf("stderr = %q, want a warning naming the fallback provider", stderr.String())
	}
}

// TestSelectStartupProviderZeroProvidersError: with no declared providers at
// all, startup fails in both modes with an error naming the requirement. The
// shipped defaults make this unreachable through config alone, so it is pinned
// at the unit seam on an empty registry.
func TestSelectStartupProviderZeroProvidersError(t *testing.T) {
	r := llm.NewRegistry(map[string]llm.ProviderSpec{})
	for _, interactive := range []bool{true, false} {
		_, err := selectStartupProvider(r, "", interactive, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil {
			t.Fatalf("interactive=%v: selectStartupProvider should error with no declared providers", interactive)
		}
		for _, want := range []string{"no active provider", "provider"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("interactive=%v: error = %v, want it to mention %q", interactive, err, want)
			}
		}
	}
}

// TestPromptProviderChoiceListsAndPicks: the prompt lists every declared
// provider, asks for a selection, and a valid name boots off the pick.
func TestPromptProviderChoiceListsAndPicks(t *testing.T) {
	names := []string{"alpha", "mid", "zen"}
	var stdout bytes.Buffer
	picked, err := promptProviderChoice(names, strings.NewReader("mid\n"), &stdout)
	if err != nil {
		t.Fatalf("promptProviderChoice: %v", err)
	}
	if picked != "mid" {
		t.Errorf("picked = %q, want %q", picked, "mid")
	}
	out := stdout.String()
	for _, want := range []string{"Available providers:", "  alpha", "  mid", "  zen", "Select provider:"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}
}

// TestPromptProviderChoiceRetriesUnknown: an unknown name is flagged and the
// list re-prompts until a declared name lands.
func TestPromptProviderChoiceRetriesUnknown(t *testing.T) {
	var stdout bytes.Buffer
	picked, err := promptProviderChoice([]string{"zen"}, strings.NewReader("nope\nzen\n"), &stdout)
	if err != nil {
		t.Fatalf("promptProviderChoice: %v", err)
	}
	if picked != "zen" {
		t.Errorf("picked = %q, want %q after an unknown retry", picked, "zen")
	}
	out := stdout.String()
	if !strings.Contains(out, "no such provider: nope") {
		t.Errorf("stdout = %q, want a 'no such provider' line", out)
	}
	if strings.Count(out, "Available providers:") != 2 {
		t.Errorf("stdout = %q, want the list shown twice (one per prompt)", out)
	}
}

// TestPromptProviderChoiceAbortsAtEOF: when stdin is exhausted, re-prompting
// is pointless — the prompt returns a clean error instead of spinning.
func TestPromptProviderChoiceAbortsAtEOF(t *testing.T) {
	for _, tc := range []struct {
		name       string
		input      string
		wantStderr string
	}{
		{name: "empty input", input: "", wantStderr: "no provider selected"},
		{name: "unknown at eof", input: "nope", wantStderr: "no such provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := promptProviderChoice([]string{"zen"}, strings.NewReader(tc.input), io.Discard)
			if err == nil {
				t.Fatal("promptProviderChoice should error at EOF")
			}
			if !strings.Contains(err.Error(), tc.wantStderr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantStderr)
			}
		})
	}
}

// TestApproveAllRefusedWithoutHeadless: --approve-all is refused before any
// provider or config work when no -p prompt was given — an interactive run has
// a user to ask and no single-turn scope for a blanket grant to expire in
// (og-uy5.6). The refusal is a usage error (code 3) and must not reach config
// loading or publish any approval.
func TestApproveAllRefusedWithoutHeadless(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--approve-all"}, &stdout, &stderr)
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--approve-all requires headless mode") {
		t.Errorf("stderr = %q, want the headless-only refusal", stderr.String())
	}
}

// TestHoistApproveAllAfterPrompt: "-p --approve-all <prompt>" is rewritten so
// the approval flag parses before -p takes its value; the single-dash and
// --prompt spellings hoist too, and unrelated forms pass through unchanged.
func TestHoistApproveAllAfterPrompt(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{in: []string{"-p", "--approve-all", "hello"}, want: []string{"--approve-all", "-p", "hello"}},
		{in: []string{"--prompt", "-approve-all", "hello"}, want: []string{"-approve-all", "--prompt", "hello"}},
		{in: []string{"--approve-all", "-p", "hello"}, want: []string{"--approve-all", "-p", "hello"}},
		{in: []string{"-p", "hello"}, want: []string{"-p", "hello"}},
		{in: []string{"-p"}, want: []string{"-p"}},
		{in: []string{"-p", "--approve-all"}, want: []string{"--approve-all", "-p"}},
	}
	for _, tc := range cases {
		got := hoistApproveAllAfterPrompt(tc.in)
		if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
			t.Errorf("hoistApproveAllAfterPrompt(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestHeadlessNegotiator: the -p gate picks DenyAll by default and ApproveAll
// under --approve-all, never anything persistent.
func TestHeadlessNegotiator(t *testing.T) {
	if _, ok := headlessNegotiator(false).(permissions.DenyAll); !ok {
		t.Error("default negotiator must be DenyAll")
	}
	if _, ok := headlessNegotiator(true).(permissions.ApproveAll); !ok {
		t.Error("--approve-all negotiator must be ApproveAll")
	}
}
