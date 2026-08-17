package instruct

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/okayest-dev/og/internal/config"
)

func TestDefaultPromptAlwaysPresent(t *testing.T) {
	cfg := &config.Config{}
	got, err := Load(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !strings.Contains(got, DefaultPrompt) {
		t.Errorf("Load() = %q, want it to contain the default prompt %q", got, DefaultPrompt)
	}
}

func TestInstructionFileAppendedAfterDefault(t *testing.T) {
	dir := t.TempDir()
	instFile := filepath.Join(dir, "instructions.md")
	if err := os.WriteFile(instFile, []byte("custom instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{InstructionFile: instFile}
	got, err := Load(cfg, dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	dIdx := strings.Index(got, DefaultPrompt)
	iIdx := strings.Index(got, "custom instructions")
	if dIdx < 0 || iIdx < 0 {
		t.Fatalf("expected both sources in output; got %q", got)
	}
	if dIdx >= iIdx {
		t.Errorf("instruction file (at %d) should come after default prompt (at %d)", iIdx, dIdx)
	}
}

func TestMissingInstructionFileErrors(t *testing.T) {
	cfg := &config.Config{InstructionFile: "/nonexistent/path/instructions.md"}
	_, err := Load(cfg, t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing instruction file, got nil")
	}
	if !strings.Contains(err.Error(), "instruction file") {
		t.Errorf("error = %q, want it to mention instruction file", err)
	}
}

func TestAGENTSMDAppendedAfterInstructionFile(t *testing.T) {
	dir := t.TempDir()
	instFile := filepath.Join(dir, "instructions.md")
	if err := os.WriteFile(instFile, []byte("config instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentsMD := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agentsMD, []byte("agents rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{InstructionFile: instFile}
	got, err := Load(cfg, dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	dIdx := strings.Index(got, DefaultPrompt)
	iIdx := strings.Index(got, "config instructions")
	aIdx := strings.Index(got, "agents rules")
	if dIdx < 0 || iIdx < 0 || aIdx < 0 {
		t.Fatalf("expected all three sources in output; got %q", got)
	}
	if dIdx >= iIdx || iIdx >= aIdx {
		t.Errorf("sources should be in order default(%d) < config(%d) < agents(%d)", dIdx, iIdx, aIdx)
	}
}

func TestAGENTSMDIsCwdOnly(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	agentsMD := filepath.Join(parent, "AGENTS.md")
	if err := os.WriteFile(agentsMD, []byte("parent rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	got, err := Load(cfg, child)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.Contains(got, "parent rules") {
		t.Errorf("AGENTS.md from parent directory should not be loaded; got %q", got)
	}
}

func TestNoConfigInstructionFileNoAGENTSMD(t *testing.T) {
	cfg := &config.Config{}
	got, err := Load(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != DefaultPrompt {
		t.Errorf("with no sources, Load() = %q, want just the default prompt %q", got, DefaultPrompt)
	}
}

func TestAllThreeSourcesInOrder(t *testing.T) {
	dir := t.TempDir()
	instFile := filepath.Join(dir, "custom.md")
	if err := os.WriteFile(instFile, []byte("---config---"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentsMD := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agentsMD, []byte("---agents---"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{InstructionFile: instFile}
	got, err := Load(cfg, dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := DefaultPrompt + "\n---config---\n---agents---"
	if got != want {
		t.Errorf("Load() = %q, want %q", got, want)
	}
}
