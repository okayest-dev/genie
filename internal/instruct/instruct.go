// Package instruct assembles the agent instruction from three append-only
// sources in order: a built-in default prompt (always present), an optional
// config instruction file, and an optional AGENTS.md in the working directory.
package instruct

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/okayest-dev/og/internal/config"
)

// DefaultPrompt is the built-in system instruction always prepended.
const DefaultPrompt = "You are og, a helpful terminal agent."

// Load assembles the instruction from three sources in order:
//  1. The built-in default prompt (always present).
//  2. The config instruction file, if set (errors if the file is missing).
//  3. An AGENTS.md in cwd, if present (no parent-directory walk).
func Load(cfg *config.Config, cwd string) (string, error) {
	instruction := DefaultPrompt

	if cfg.InstructionFile != "" {
		b, err := os.ReadFile(cfg.InstructionFile)
		if err != nil {
			return "", fmt.Errorf("instruction file %s: %w", cfg.InstructionFile, err)
		}
		instruction += "\n" + string(b)
	}

	agentsPath := filepath.Join(cwd, "AGENTS.md")
	b, err := os.ReadFile(agentsPath)
	if err == nil {
		instruction += "\n" + string(b)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("reading AGENTS.md: %w", err)
	}

	return instruction, nil
}
