// Package readtool implements the read tool: file content, directory listing,
// offset/limit, shared 2000-line/50KB truncation cap, and binary detection.
package readtool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	defaultLimit = 2000
	maxLines     = 2000
	maxBytes     = 50 << 10 // 50 KB
)

// Tool reads files and lists directories.
type Tool struct {
	cwd string
}

// New creates a read tool rooted at cwd.
func New(cwd string) *Tool {
	return &Tool{cwd: cwd}
}

func (t *Tool) Name() string        { return "read" }
func (t *Tool) Description() string { return "Read a file or list a directory." }

func (t *Tool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "File or directory path (relative to cwd, or absolute)",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "1-indexed line to start from",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of lines to return",
			},
		},
		"required": []any{"path"},
	}
}

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func (t *Tool) Execute(raw json.RawMessage) (string, error) {
	var args readArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("missing required argument: path")
	}

	// Resolve path relative to cwd.
	absPath := args.Path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(t.cwd, absPath)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", args.Path)
		}
		return "", fmt.Errorf("stat %s: %v", args.Path, err)
	}

	if info.IsDir() {
		return t.readDir(absPath, args)
	}
	return t.readFile(absPath, args)
}

func (t *Tool) readDir(absPath string, args readArgs) (string, error) {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return "", fmt.Errorf("read dir %s: %v", args.Path, err)
	}

	// Sort alphabetically, case-insensitive.
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	offset := args.Offset
	if offset < 1 {
		offset = 1
	}
	limit := args.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLines {
		limit = maxLines
	}

	total := len(entries)
	start := offset - 1 // convert to 0-indexed
	if start >= total {
		return t.dirHint(absPath, total, offset, limit), nil
	}

	end := start + limit
	if end > total {
		end = total
	}

	var lines []string
	for _, e := range entries[start:end] {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		lines = append(lines, name)
	}

	result := strings.Join(lines, "\n")
	if end < total || start > 0 {
		result += "\n" + t.dirHint(absPath, total, start+1, end-start)
	}
	return result, nil
}

func (t *Tool) dirHint(absPath string, total, offset, count int) string {
	end := offset + count - 1
	if end > total {
		end = total
	}
	return fmt.Sprintf("[Showing entries %d-%d of %d. Use offset=%d to continue.]", offset, end, total, end+1)
}

func (t *Tool) readFile(absPath string, args readArgs) (string, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %v", args.Path, err)
	}

	// Binary detection: NUL byte or invalid UTF-8.
	if isBinary(data) {
		return "", fmt.Errorf("binary file detected — use `xxd`, `file`, or `head -c` to inspect: %s", args.Path)
	}

	content := string(data)
	lines := strings.Split(content, "\n")
	// Split on "\n" gives an extra empty element if file ends with \n.
	// We want to count actual lines.
	totalLines := len(lines)
	if totalLines > 0 && lines[totalLines-1] == "" {
		totalLines--
	}

	offset := args.Offset
	if offset < 1 {
		offset = 1
	}
	limit := args.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLines {
		limit = maxLines
	}

	start := offset - 1 // 0-indexed
	if start >= totalLines {
		return t.lineHint(totalLines, offset, 0), nil
	}

	end := start + limit
	truncated := false
	if end > totalLines {
		end = totalLines
	} else {
		truncated = true
	}

	selected := lines[start:end]
	result := strings.Join(selected, "\n")

	// Apply the shared 50KB cap.
	if len(result) > maxBytes {
		result = result[:maxBytes]
		truncated = true
	}

	if truncated || start > 0 {
		shown := end - start
		result += "\n" + t.lineHint(totalLines, offset, shown)
	}
	return result, nil
}

func (t *Tool) lineHint(total, offset, count int) string {
	end := offset + count - 1
	if end > total {
		end = total
	}
	if count == 0 {
		return fmt.Sprintf("[Showing lines %d of %d. No more lines.]", offset, total)
	}
	return fmt.Sprintf("[Showing lines %d-%d of %d. Use offset=%d to continue.]", offset, end, total, end+1)
}

// isBinary checks for NUL bytes and invalid UTF-8.
func isBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	// Check for NUL byte.
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	// Check for invalid UTF-8.
	if !utf8.Valid(data) {
		return true
	}
	return false
}
