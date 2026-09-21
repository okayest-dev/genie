// Package instruct assembles the agent instruction from three append-only
// sources in order: a built-in default prompt (always present), an optional
// config instruction file, and an optional AGENTS.md in the working directory.
// When a resolved base policy is provided, a permission snapshot and
// negotiation mechanism paragraph are appended last (og-uy5.5).
package instruct

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/okayest-dev/genie/internal/config"
	"github.com/okayest-dev/genie/internal/permissions"
)

// DefaultPrompt is the built-in system instruction always prepended.
const DefaultPrompt = "You are genie, a helpful terminal agent."

// Load assembles the instruction from three sources in order:
//  1. The built-in default prompt (always present).
//  2. The config instruction file, if set (errors if the file is missing).
//  3. An AGENTS.md in cwd, if present (no parent-directory walk).
func Load(cfg *config.Config, cwd string) (string, error) {
	return LoadWithAgent(cfg, nil, "", cwd)
}

// LoadWithAgent assembles the instruction with an optional agent override.
// When agent is nil, behaves identically to Load.
// When agent is set:
//   - agent.InstructionFile replaces cfg.InstructionFile (if agent's is non-empty)
//   - agent.InheritAgentsMD controls AGENTS.md inclusion (default true)
//   - skillLayer is injected between the instruction file and AGENTS.md
func LoadWithAgent(cfg *config.Config, agent *config.ResolvedAgent, skillLayer string, cwd string) (string, error) {
	return assemble(cfg, agent, skillLayer, cwd, nil)
}

// LoadWithAgentAndPermissions assembles the instruction like LoadWithAgent and
// appends the base-policy snapshot plus negotiation mechanism paragraph when
// base is non-nil. base must come from the permission store's BaseSnapshot() so
// the model's view is flat and tier-free: permanent/session/once grants never
// enter the instruction (og-73l.4). The snapshot is a session-start list; it
// changes only when the base changes (e.g. an /agent switch).
func LoadWithAgentAndPermissions(cfg *config.Config, agent *config.ResolvedAgent, skillLayer string, cwd string, base map[permissions.Axis][]string) (string, error) {
	return assemble(cfg, agent, skillLayer, cwd, base)
}

func assemble(cfg *config.Config, agent *config.ResolvedAgent, skillLayer string, cwd string, base map[permissions.Axis][]string) (string, error) {
	instruction := DefaultPrompt

	// Determine which instruction file to use.
	instructionFile := cfg.InstructionFile
	if agent != nil && agent.InstructionFile != "" {
		instructionFile = agent.InstructionFile
	}

	if instructionFile != "" {
		b, err := os.ReadFile(instructionFile)
		if err != nil {
			return "", fmt.Errorf("instruction file %s: %w", instructionFile, err)
		}
		instruction += "\n" + string(b)
		slog.Info("instruction file loaded", "path", instructionFile, "bytes", len(b))
		slog.Debug("instruction source", "name", "instruction_file", "path", instructionFile, "bytes", len(b))
	}

	// Skill layer: injected between instruction file and AGENTS.md.
	if skillLayer != "" {
		instruction += "\n" + skillLayer
		slog.Info("skill layer injected", "bytes", len(skillLayer))
		slog.Debug("instruction source", "name", "skill_layer", "bytes", len(skillLayer))
	}

	// AGENTS.md: included unless agent explicitly excludes it.
	inheritAgentsMD := true
	if agent != nil {
		inheritAgentsMD = agent.InheritAgentsMD
	}

	if inheritAgentsMD {
		agentsPath := filepath.Join(cwd, "AGENTS.md")
		b, err := os.ReadFile(agentsPath)
		if err == nil {
			instruction += "\n" + string(b)
			slog.Info("AGENTS.md loaded", "path", agentsPath, "bytes", len(b))
			slog.Debug("instruction source", "name", "AGENTS.md", "path", agentsPath, "bytes", len(b))
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("reading AGENTS.md: %w", err)
		} else {
			slog.Info("AGENTS.md not found", "path", agentsPath)
		}
	}

	if base != nil {
		section := permissions.RenderPermissionsSection(base)
		instruction += "\n" + section
		slog.Info("permission policy appended", "bytes", len(section))
	}

	slog.Info("instruction assembled", "total_bytes", len(instruction))
	slog.Debug("instruction assembled", "total_bytes", len(instruction), "content", instruction)
	return instruction, nil
}
