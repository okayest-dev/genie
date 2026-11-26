// Package genietools implements tools for Genie self-improvement.
package genietools

import (
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/okayest-dev/genie/internal/tools"
)

// GenieSelfUpdateTool checks for and applies Genie updates.
type GenieSelfUpdateTool struct{}

func NewGenieSelfUpdateTool() *GenieSelfUpdateTool {
	return &GenieSelfUpdateTool{}
}

func (t *GenieSelfUpdateTool) Name() string        { return "genie_self_update" }
func (t *GenieSelfUpdateTool) Description() string { return "Check for and apply Genie updates" }

func (t *GenieSelfUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"check_only": map[string]any{
				"type":        "boolean",
				"description": "Only check for updates, don't apply",
			},
			"pre": map[string]any{
				"type":        "boolean",
				"description": "Include pre-releases",
			},
			"auto": map[string]any{
				"type":        "boolean",
				"description": "Auto-approve (for CI/CD)",
			},
		},
	}
}

func (t *GenieSelfUpdateTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		CheckOnly bool `json:"check_only,omitempty"`
		Pre       bool `json:"pre,omitempty"`
		Auto      bool `json:"auto,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Build command
	cmdArgs := []string{"self-update"}
	if params.CheckOnly {
		cmdArgs = append(cmdArgs, "--check-only")
	}
	if params.Pre {
		cmdArgs = append(cmdArgs, "--pre")
	}
	if params.Auto {
		cmdArgs = append(cmdArgs, "--auto")
	}

	// Find genie binary
	binPath, err := exec.LookPath("genie")
	if err != nil {
		// Try to find in common locations
		binPath = "genie"
	}

	cmd := exec.Command(binPath, cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("self-update failed: %w\nOutput: %s", err, string(output))
	}

	return string(output), nil
}

// Ensure tools implement the tools.Tool interface
var _ tools.Tool = (*GenieSelfUpdateTool)(nil)