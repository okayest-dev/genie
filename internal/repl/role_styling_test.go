package repl

import (
	"bytes"
	"errors"
	"testing"

	"github.com/okayest-dev/genie/internal/style"
	"github.com/okayest-dev/genie/internal/theme"
)

func turnCancelledCfg(t *testing.T, th *theme.Theme, stderrProf *style.RenderProfile) (*Config, *bytes.Buffer) {
	t.Helper()
	var stderr bytes.Buffer
	return &Config{Run: nil, Stderr: &stderr, Theme: th, StderrProfile: stderrProf}, &stderr
}

func displayProfile() *style.RenderProfile {
	return &style.RenderProfile{IsTTY: true, ColourProfile: style.ColourProfileTrueColor, GlyphTier: style.GlyphTierNerd, Width: 80}
}

func TestPrintTurnErrorStyledOnDisplayProfile(t *testing.T) {
	th := &theme.Theme{Data: theme.ThemeData{Roles: map[string]theme.RoleStyle{
		"error": {Foreground: "red"},
	}}}
	cfg, stderr := turnCancelledCfg(t, th, displayProfile())
	cfg.printTurnError(errors.New("boom"))
	if got, want := stderr.String(), "\x1b[31mError: boom\n\x1b[0m"; got != want {
		t.Errorf("styled error = %q, want %q", got, want)
	}
}

func TestPrintTurnErrorStyledFromClassicPreset(t *testing.T) {
	th, err := theme.Load("classic", "/tmp")
	if err != nil {
		t.Fatalf("theme.Load: %v", err)
	}
	cfg, stderr := turnCancelledCfg(t, th, displayProfile())
	cfg.printTurnError(errors.New("disk full"))
	if got, want := stderr.String(), "\x1b[31mError: disk full\n\x1b[0m"; got != want {
		t.Errorf("classic error = %q, want %q", got, want)
	}
}

func TestPrintTurnErrorPlainWithoutTheme(t *testing.T) {
	cfg, stderr := turnCancelledCfg(t, nil, displayProfile())
	cfg.printTurnError(errors.New("boom"))
	if got, want := stderr.String(), "Error: boom\n"; got != want {
		t.Errorf("unthemed error = %q, want %q", got, want)
	}
}

func TestPrintTurnErrorPlainWithoutStderrProfile(t *testing.T) {
	th := &theme.Theme{Data: theme.ThemeData{Roles: map[string]theme.RoleStyle{
		"error": {Foreground: "red"},
	}}}
	cfg, stderr := turnCancelledCfg(t, th, nil)
	cfg.printTurnError(errors.New("boom"))
	if got, want := stderr.String(), "Error: boom\n"; got != want {
		t.Errorf("profile-less error = %q, want %q", got, want)
	}
}

func TestPrintTurnCancelledStyledOnDisplayProfile(t *testing.T) {
	th := &theme.Theme{Data: theme.ThemeData{Roles: map[string]theme.RoleStyle{
		"turn_cancelled": {Attributes: []string{"dim"}},
	}}}
	cfg, stderr := turnCancelledCfg(t, th, displayProfile())
	cfg.printTurnCancelled()
	if got, want := stderr.String(), "\x1b[2m\n[turn cancelled]\n\x1b[0m"; got != want {
		t.Errorf("styled cancellation = %q, want %q", got, want)
	}
}

func TestPrintTurnCancelledPlainWithoutTheme(t *testing.T) {
	cfg, stderr := turnCancelledCfg(t, nil, displayProfile())
	cfg.printTurnCancelled()
	if got, want := stderr.String(), "\n[turn cancelled]\n"; got != want {
		t.Errorf("unthemed cancellation = %q, want %q", got, want)
	}
}

func TestRoleWriterHelperReturnsWriterUnchanged(t *testing.T) {
	var b bytes.Buffer
	cfg := &Config{}
	if got := cfg.roleWriter(&b, nil, "error"); got != &b {
		t.Error("nil theme should pass the writer through unchanged")
	}
	if b.Len() != 0 {
		t.Errorf("writer should be untouched, wrote %q", b.String())
	}
}