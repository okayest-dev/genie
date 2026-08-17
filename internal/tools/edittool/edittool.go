// Package edittool implements the edit tool: surgical single-pair text
// replacement with exact whitespace-sensitive matching, line-ending
// preservation, and BOM stripping.
package edittool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Tool makes surgical text replacements in files.
type Tool struct {
	cwd string
}

// New creates an edit tool rooted at cwd.
func New(cwd string) *Tool {
	return &Tool{cwd: cwd}
}

func (t *Tool) Name() string        { return "edit" }
func (t *Tool) Description() string { return "Edit a file by replacing text." }

func (t *Tool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "File path (relative to cwd, or absolute)",
			},
			"oldText": map[string]any{
				"type":        "string",
				"description": "The exact text to find and replace",
			},
			"newText": map[string]any{
				"type":        "string",
				"description": "The replacement text",
			},
		},
		"required": []any{"path", "oldText", "newText"},
	}
}

type editArgs struct {
	Path    string `json:"path"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

func (t *Tool) Execute(raw json.RawMessage) (string, error) {
	var args editArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("missing required argument: path")
	}
	if args.OldText == "" {
		return "", fmt.Errorf("missing required argument: oldText")
	}

	// Resolve path relative to cwd.
	absPath := args.Path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(t.cwd, absPath)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", args.Path)
		}
		return "", fmt.Errorf("read %s: %v", args.Path, err)
	}

	// Detect line endings: normalise to \n for matching, then restore.
	isCRLF := strings.Contains(string(data), "\r\n")

	// Strip BOM if present.
	content := string(data)
	if strings.HasPrefix(content, "\uFEFF") {
		content = content[1:]
	}

	// Normalise to \n for matching.
	if isCRLF {
		content = strings.ReplaceAll(content, "\r\n", "\n")
	}

	// Count occurrences of oldText.
	count := strings.Count(content, args.OldText)
	switch {
	case count == 0:
		return "", fmt.Errorf("oldText not found in %s", args.Path)
	case count > 1:
		return "", fmt.Errorf("oldText is ambiguous (%d matches) in %s", count, args.Path)
	}

	// Apply the replacement.
	result := strings.Replace(content, args.OldText, args.NewText, 1)

	// Restore original line endings.
	if isCRLF {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}

	if err := os.WriteFile(absPath, []byte(result), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %v", args.Path, err)
	}

	return fmt.Sprintf("edited %s", args.Path), nil
}
