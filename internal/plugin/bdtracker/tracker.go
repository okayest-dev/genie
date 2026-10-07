package bdtracker

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/okayest-dev/genie/internal/tracker"
)

// BDTracker implements tracker.TrackerSource using the bd CLI.
type BDTracker struct {
	binary  string
	timeout time.Duration
}

// Ensure BDTracker implements tracker.TrackerSource
var _ tracker.TrackerSource = (*BDTracker)(nil)

// NewBDTracker creates a new bd-backed tracker.
func NewBDTracker(binary string, timeout time.Duration) *BDTracker {
	if binary == "" {
		binary = "bd"
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &BDTracker{binary: binary, timeout: timeout}
}

// Frontier returns the next ready task using `bd ready`.
func (b *BDTracker) Frontier(ctx context.Context) (*tracker.TrackerTask, error) {
	output, err := b.runBD(ctx, "ready")
	if err != nil {
		return nil, fmt.Errorf("bd ready failed: %w", err)
	}
	task, err := b.parseFrontier(output)
	if err != nil {
		return nil, fmt.Errorf("parse frontier: %w", err)
	}
	if task == nil {
		return nil, tracker.ErrNoReadyTasks
	}
	return task, nil
}

// Claim claims a task using `bd update <id> --claim`.
func (b *BDTracker) Claim(ctx context.Context, id string) (*tracker.TrackerTask, error) {
	_, err := b.runBD(ctx, "update", id, "--claim")
	if err != nil {
		return nil, fmt.Errorf("bd claim failed: %w", err)
	}
	return b.Read(ctx, id)
}

// Read returns a task by ID using `bd show <id>`.
func (b *BDTracker) Read(ctx context.Context, id string) (*tracker.TrackerTask, error) {
	_, err := b.runBD(ctx, "show", id)
	if err != nil {
		return nil, fmt.Errorf("bd show failed: %w", err)
	}
	return b.parseShow("")
}

// Close closes a task using `bd close <id>`.
func (b *BDTracker) Close(ctx context.Context, id string, reason string) error {
	args := []string{"close", id}
	if reason != "" {
		args = append(args, "--reason", reason)
	}
	_, err := b.runBD(ctx, args...)
	return err
}

// Comment adds a comment to a task using `bd comment <id> --message <comment>`.
func (b *BDTracker) Comment(ctx context.Context, id string, comment string) error {
	_, err := b.runBD(ctx, "comment", id, "--message", comment)
	return err
}

// Create creates a new task using `bd create`.
func (b *BDTracker) Create(ctx context.Context, args tracker.CreateArgs) (*tracker.TrackerTask, error) {
	bdArgs := []string{"create", "--title", args.Title, "--type", args.Type}

	if args.Priority > 0 {
		bdArgs = append(bdArgs, "--priority", fmt.Sprintf("%d", args.Priority))
	}
	if args.Description != "" {
		bdArgs = append(bdArgs, "--description", args.Description)
	}
	if len(args.Labels) > 0 {
		bdArgs = append(bdArgs, "--labels", strings.Join(args.Labels, ","))
	}
	if args.Parent != "" {
		bdArgs = append(bdArgs, "--parent", args.Parent)
	}
	if len(args.DependsOn) > 0 {
		bdArgs = append(bdArgs, "--depends-on", strings.Join(args.DependsOn, ","))
	}
	if args.FindingOf != "" {
		bdArgs = append(bdArgs, "--finding-of", args.FindingOf)
	}

	output, err := b.runBD(ctx, bdArgs...)
	if err != nil {
		return nil, fmt.Errorf("bd create failed: %w", err)
	}

	// Parse the output to get the created task ID
	// bd create output format: "Created og-xxx"
	re := regexp.MustCompile(`Created\s+(\S+)`)
	matches := re.FindStringSubmatch(output)
	if matches == nil {
		return nil, fmt.Errorf("could not parse created task ID from output: %s", output)
	}

	taskID := matches[1]
	return b.Read(ctx, taskID)
}

// runBD runs a bd command and returns stdout.
func (b *BDTracker) runBD(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, b.binary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bd %s failed: %w: %s", strings.Join(args, " "), err, string(output))
	}
	return string(output), nil
}

// parseFrontier parses the output of `bd ready` to find the first ready task.
func (b *BDTracker) parseFrontier(output string) (*tracker.TrackerTask, error) {
	// bd ready output format:
	// ○ og-wm7.1 ● P1 [epic] ...
	// Status: ○ open  ◐ in_progress  ● blocked  ✓ closed  ❄ deferred
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Status:") || strings.HasPrefix(line, "---") {
			continue
		}
		// Format: ○ og-wm7.1 ● P1 [epic] ACM: ...
		re := regexp.MustCompile(`^[○◐●✓❄]\s+(\S+)\s+●\s+(\S+)\s+(\S+)\s+(.+)$`)
		matches := re.FindStringSubmatch(line)
		if matches != nil {
			id := matches[1]
			status := parseStatus(matches[2])
			priority := parsePriority(matches[3])
			rest := matches[4]
			// Extract type and title from rest
			taskType, title := extractTypeAndTitle(rest)
			return &tracker.TrackerTask{
				ID:        id,
				Title:     title,
				Type:      taskType,
				Status:    status,
				Priority:  priority,
				CreatedAt: time.Now().Format(time.RFC3339),
				UpdatedAt: time.Now().Format(time.RFC3339),
			}, nil
		}
	}
	return nil, tracker.ErrNoReadyTasks
}

// parseShow parses the output of `bd show <id>`.
func (b *BDTracker) parseShow(output string) (*tracker.TrackerTask, error) {
	lines := strings.Split(output, "\n")
	var task *tracker.TrackerTask

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: ○ og-wm7.1 [EPIC] · ACM: ...
		if strings.HasPrefix(line, "○ ") || strings.HasPrefix(line, "◐ ") || strings.HasPrefix(line, "● ") || strings.HasPrefix(line, "● ") || strings.HasPrefix(line, "✓ ") || strings.HasPrefix(line, "❄ ") {
			re := regexp.MustCompile(`^[○◐●✓❄]\s+(\S+)\s+(\[?\S+\]?)\s*[·:]\s*(.+)$`)
			matches := re.FindStringSubmatch(line)
			if matches != nil {
				id := matches[1]
				desc := matches[3]
				taskType, title := extractTypeAndTitle(desc)
				if task == nil {
					task = &tracker.TrackerTask{
						ID:        id,
						Title:     title,
						Type:      taskType,
						Status:    "open",
						Priority:  2,
						CreatedAt: time.Now().Format(time.RFC3339),
						UpdatedAt: time.Now().Format(time.RFC3339),
					}
				}
			}
		}
		// Owner: Dan · Assignee: Dan · Type: epic
		if strings.HasPrefix(line, "Owner:") {
			if task != nil && strings.Contains(line, "Assignee:") {
				parts := strings.Split(line, "·")
				for _, part := range parts {
					part = strings.TrimSpace(part)
					if strings.HasPrefix(part, "Assignee:") {
						task.Assignee = strings.TrimSpace(strings.TrimPrefix(part, "Assignee:"))
					}
				}
			}
		}
		// Created: 2026-09-07 · Started: 2026-09-26 · Updated: 2026-10-01
		if strings.HasPrefix(line, "Created:") {
			parts := strings.Split(line, "·")
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if strings.HasPrefix(part, "Created:") {
					if t, err := time.Parse("2006-01-02", strings.TrimSpace(strings.TrimPrefix(part, "Created:"))); err == nil {
						if task != nil {
							task.CreatedAt = t.Format(time.RFC3339)
						}
					}
				}
				if strings.HasPrefix(part, "Updated:") {
					if t, err := time.Parse("2006-01-02", strings.TrimSpace(strings.TrimPrefix(part, "Updated:"))); err == nil {
						if task != nil {
							task.UpdatedAt = t.Format(time.RFC3339)
						}
					}
				}
			}
		}
		// Labels: feature:agent-coding-model, feature:genie-harness
		if strings.HasPrefix(line, "Labels:") {
			if task != nil {
				labelsStr := strings.TrimSpace(strings.TrimPrefix(line, "Labels:"))
				task.Labels = strings.Split(labelsStr, ", ")
			}
		}
	}

	if task == nil {
		return nil, fmt.Errorf("could not parse bd show output")
	}
	return task, nil
}

func parseStatus(status string) string {
	switch status {
	case "P0", "0":
		return "critical"
	case "P1", "1":
		return "high"
	case "P2", "2":
		return "medium"
	case "P3", "3":
		return "low"
	case "P4", "4":
		return "backlog"
	default:
		return "unknown"
	}
}

func parsePriority(priority string) int {
	switch priority {
	case "P0", "0":
		return 0
	case "P1", "1":
		return 1
	case "P2", "2":
		return 2
	case "P3", "3":
		return 3
	case "P4", "4":
		return 4
	default:
		return 2
	}
}

func extractTypeAndTitle(desc string) (string, string) {
	// Format: [EPIC] · ACM: governed task lifecycle tools & tracker interface
	// or: ACM: governed task lifecycle tools & tracker interface
	parts := strings.Split(desc, "·")
	if len(parts) >= 2 {
		typePart := strings.TrimSpace(parts[0])
		typePart = strings.Trim(typePart, "[]")
		titlePart := strings.TrimSpace(parts[1])
		// Remove prefix like "ACM:" or "feature:"
		if idx := strings.Index(titlePart, ":"); idx >= 0 {
			titlePart = strings.TrimSpace(titlePart[idx+1:])
		}
		return typePart, titlePart
	}
	// No type prefix
	if idx := strings.Index(desc, ":"); idx >= 0 {
		return "task", strings.TrimSpace(desc[idx+1:])
	}
	return "task", desc
}