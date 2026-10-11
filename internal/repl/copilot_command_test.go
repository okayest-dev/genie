package repl

import (
	"bytes"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/llm/copilot"
)

// TestCopilotAuthStatusColonHierarchy drives the /copilot:auth:status command
// exactly as /help advertises it. The deep colon hierarchy must dispatch
// /copilot → auth → status; without forwarding the joined remainder of the
// path, only the usage line prints.
func TestCopilotAuthStatusColonHierarchy(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	if err := copilot.WriteOAuthToken(xdg, "github.com", "ghtoken_ABCDEFGHIJ", "octocat"); err != nil {
		t.Fatalf("WriteOAuthToken: %v", err)
	}

	var out, stderr bytes.Buffer
	cfg := &Config{Stdout: &out, Stderr: &stderr}
	handleSlashCommand(t.Context(), "/copilot:auth:status", cfg)

	got := out.String()
	if strings.Contains(got, "Usage:") {
		t.Errorf("deep colon hierarchy not dispatched, got usage line: %q", got)
	}
	for _, want := range []string{"Host: github.com", "User: octocat", "OAuth Token: ghtoken_..."} {
		if !strings.Contains(got, want) {
			t.Errorf("status output missing %q: %q", want, got)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should stay empty: %q", stderr.String())
	}
}

// TestCopilotAuthStatusHonoursHostFlag pins that the space-separated args tail
// reaches the final subcommand.
func TestCopilotAuthStatusHonoursHostFlag(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	if err := copilot.WriteOAuthToken(xdg, "github.com", "ghtoken_ABCDEFGHIJ", "octocat"); err != nil {
		t.Fatalf("WriteOAuthToken: %v", err)
	}

	var out, stderr bytes.Buffer
	cfg := &Config{Stdout: &out, Stderr: &stderr}
	handleSlashCommand(t.Context(), "/copilot:auth:status --host github.com", cfg)
	if strings.Contains(out.String(), "Usage:") {
		t.Errorf("--host should not break dispatch: %q", out.String())
	}
	if !strings.Contains(out.String(), "Host: github.com") {
		t.Errorf("--host not honoured: %q", out.String())
	}
}

// TestCopilotAuthUnknownSubcommand surfaces the default branch of the
// copilot dispatch.
func TestCopilotAuthUnknownSubcommand(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	var out, stderr bytes.Buffer
	cfg := &Config{Stdout: &out, Stderr: &stderr}
	handleSlashCommand(t.Context(), "/copilot:auth:definitely-not-a-command", cfg)
	if !strings.Contains(out.String(), "unknown copilot auth command") {
		t.Errorf("expected unknown-subcommand error, got %q", out.String())
	}
}

// TestCopilotBarePrintsHelp pins the bare /copilot surface.
func TestCopilotBarePrintsHelp(t *testing.T) {
	var out, stderr bytes.Buffer
	cfg := &Config{Stdout: &out, Stderr: &stderr}
	handleSlashCommand(t.Context(), "/copilot", cfg)
	if !strings.Contains(out.String(), "Usage: /copilot:auth:login|status") {
		t.Errorf("bare /copilot should print help, got %q", out.String())
	}
}