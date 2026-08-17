// Package writetool implements the write tool: whole-file writes with
// auto-mkdir for new files, a confirm gate for overwrites, and a 1MB cap.
package writetool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/okayest-dev/og/internal/tools"
)

const maxContentBytes = 1 << 20 // 1 MB

// Tool writes files.
type Tool struct {
	cwd      string
	confirmer tools.Confirmer
}

// New creates a write tool rooted at cwd. Writes to existing files go
// through confirmer; new files are always allowed.
func New(cwd string, confirmer tools.Confirmer) *Tool {
	return &Tool{cwd: cwd, confirmer: confirmer}
}

func (t *Tool) Name() string        { return "write" }
func (t *Tool) Description() string { return "Write a file. New files are created automatically." }

func (t *Tool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "File path (relative to cwd, or absolute)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The full content to write",
			},
		},
		"required": []any{"path", "content"},
	}
}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *Tool) Execute(raw json.RawMessage) (string, error) {
	var args writeArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("missing required argument: path")
	}
	if args.Content == "" {
		return "", fmt.Errorf("missing required argument: content")
	}

	// Enforce 1 MB cap.
	if len(args.Content) > maxContentBytes {
		return "", fmt.Errorf("content exceeds 1 MB limit (%d bytes)", len(args.Content))
	}

	// Resolve path relative to cwd.
	absPath := args.Path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(t.cwd, absPath)
	}

	// Check if file already exists.
	_, err := os.Stat(absPath)
	exists := err == nil

	if exists {
		// Overwrite: go through the confirm gate.
		if !t.confirmer.Confirm(fmt.Sprintf("overwrite %s?", args.Path)) {
			return "", fmt.Errorf("write denied by user")
		}
	} else {
		// New file: auto-create parent directories.
		dir := filepath.Dir(absPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create directories: %v", err)
		}
	}

	if err := os.WriteFile(absPath, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %v", args.Path, err)
	}

	if exists {
		return fmt.Sprintf("overwrote %s", args.Path), nil
	}
	return fmt.Sprintf("created %s", args.Path), nil
}
