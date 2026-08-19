package repl

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestSlashHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/help\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Commands:") {
		t.Errorf("stdout = %q, want help text", stdout.String())
	}
}

func TestSlashQuit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSlashUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("/foo\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "unknown command: /foo") {
		t.Errorf("stdout = %q, want unknown command message", stdout.String())
	}
}

func TestEmptyInputSkipped(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader("\n\n/quit\n"),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty lines should not produce any output except the prompt.
	if strings.Contains(stdout.String(), "Error:") {
		t.Errorf("stdout = %q, want no errors from empty input", stdout.String())
	}
}

func TestEOFExitsCleanly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader(""),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
	}
	err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error on EOF: %v", err)
	}
}
