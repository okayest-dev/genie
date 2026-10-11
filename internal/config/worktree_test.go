package config

import (
	"testing"

	"github.com/okayest-dev/genie/internal/worktree"
)

func TestWorktreeConfigDefaults(t *testing.T) {
	cfg, err := Parse(nil, "/tmp", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Worktree.Policy != worktree.PolicyOff {
		t.Errorf("Worktree.Policy = %q, want off by default", cfg.Worktree.Policy)
	}
	if cfg.Worktree.Path != worktree.DefaultPath {
		t.Errorf("Worktree.Path = %q, want %q", cfg.Worktree.Path, worktree.DefaultPath)
	}
	if cfg.Worktree.BranchPrefix != worktree.DefaultBranchPrefix {
		t.Errorf("Worktree.BranchPrefix = %q, want %q", cfg.Worktree.BranchPrefix, worktree.DefaultBranchPrefix)
	}
}

func TestWorktreeConfigParsing(t *testing.T) {
	tomlContent := `
provider = "anthropic"

[worktree]
policy = "required"
path = ".worktrees"
branch_prefix = "wt/"
merge = "auto"
base_branch = "main"
drift_check = "block"
`
	cfg, err := Parse([]byte(tomlContent), "/tmp", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Worktree.Policy != "required" {
		t.Errorf("Policy = %q", cfg.Worktree.Policy)
	}
	if cfg.Worktree.Path != ".worktrees" {
		t.Errorf("Path = %q", cfg.Worktree.Path)
	}
	if cfg.Worktree.BranchPrefix != "wt/" {
		t.Errorf("BranchPrefix = %q", cfg.Worktree.BranchPrefix)
	}
	if cfg.Worktree.Merge != "auto" {
		t.Errorf("Merge = %q", cfg.Worktree.Merge)
	}
	if cfg.Worktree.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q", cfg.Worktree.BaseBranch)
	}
	if cfg.Worktree.DriftCheck != "block" {
		t.Errorf("DriftCheck = %q", cfg.Worktree.DriftCheck)
	}
}

func TestWorktreeConfigPartialOverlay(t *testing.T) {
	cfg, err := Parse([]byte("[worktree]\npolicy = \"optional\"\n"), "/tmp", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Worktree.Policy != "optional" {
		t.Errorf("Policy = %q", cfg.Worktree.Policy)
	}
	// Omitted keys keep their defaults.
	if cfg.Worktree.Path != worktree.DefaultPath || cfg.Worktree.BranchPrefix != worktree.DefaultBranchPrefix {
		t.Errorf("omitted keys lost their defaults: %+v", cfg.Worktree)
	}
}

func TestWorktreeConfigInvalidPolicy(t *testing.T) {
	_, err := Parse([]byte("[worktree]\npolicy = \"always\"\n"), "/tmp", nil)
	if err == nil {
		t.Fatal("expected error for invalid worktree.policy")
	}
	if !contains(err.Error(), "worktree.policy") {
		t.Errorf("error = %q, want worktree.policy validation error", err.Error())
	}
}

func TestWorktreeConfigInvalidMerge(t *testing.T) {
	_, err := Parse([]byte("[worktree]\npolicy = \"required\"\nmerge = \"rebase\"\n"), "/tmp", nil)
	if err == nil {
		t.Fatal("expected error for invalid worktree.merge")
	}
	if !contains(err.Error(), "worktree.merge") {
		t.Errorf("error = %q, want worktree.merge validation error", err.Error())
	}
}
