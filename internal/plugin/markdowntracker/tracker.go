package markdowntracker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/okayest-dev/genie/internal/tracker"
)

// MarkdownTracker implements tracker.TrackerSource using markdown files.
type MarkdownTracker struct {
	tasksDir string
}

var _ tracker.TrackerSource = (*MarkdownTracker)(nil)

// NewMarkdownTracker creates a new markdown tracker.
func NewMarkdownTracker(tasksDir string) *MarkdownTracker {
	if tasksDir == "" {
		home, _ := os.UserHomeDir()
		tasksDir = filepath.Join(home, ".config", "genie", "tasks")
	}
	return &MarkdownTracker{tasksDir: tasksDir}
}

func (m *MarkdownTracker) ensureDir() error {
	return os.MkdirAll(m.tasksDir, 0755)
}

// Frontier returns the first open, unassigned task.
func (m *MarkdownTracker) Frontier(ctx context.Context) (*tracker.TrackerTask, error) {
	if err := m.ensureDir(); err != nil {
		return nil, fmt.Errorf("ensure tasks dir: %w", err)
	}

	files, err := filepath.Glob(filepath.Join(m.tasksDir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("glob tasks: %w", err)
	}

	type taskWithMtime struct {
		task  *tracker.TrackerTask
		mtime time.Time
	}

	var tasks []taskWithMtime
	for _, file := range files {
		task, err := m.readTask(file)
		if err != nil {
			continue // skip unparseable files
		}
		if task.Status == "open" && task.Assignee == "" {
			info, _ := os.Stat(file)
			mtime := time.Time{}
			if info != nil {
				mtime = info.ModTime()
			}
			tasks = append(tasks, taskWithMtime{task: task, mtime: mtime})
		}
	}

	if len(tasks) == 0 {
		return nil, tracker.ErrNoReadyTasks
	}

	// Sort by mtime (oldest first) for FIFO
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].mtime.Before(tasks[j].mtime)
	})

	return tasks[0].task, nil
}

func (m *MarkdownTracker) Claim(ctx context.Context, id string) (*tracker.TrackerTask, error) {
	task, err := m.Read(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.Status != "open" {
		return nil, tracker.ErrTaskNotClaimable
	}

	task.Status = "in_progress"
	task.Assignee = "genie"
	task.UpdatedAt = time.Now().Format(time.RFC3339)

	if err := m.writeTask(task); err != nil {
		return nil, err
	}
	return task, nil
}

func (m *MarkdownTracker) Read(ctx context.Context, id string) (*tracker.TrackerTask, error) {
	file := filepath.Join(m.tasksDir, id+".md")
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	return m.readTask(file)
}

func (m *MarkdownTracker) Close(ctx context.Context, id, reason string) error {
	task, err := m.Read(ctx, id)
	if err != nil {
		return err
	}

	task.Status = "closed"
	task.UpdatedAt = time.Now().Format(time.RFC3339)
	return m.writeTask(task)
}

// Comment adds a comment to a task by appending to the markdown file.
func (m *MarkdownTracker) Comment(ctx context.Context, id, comment string) error {
	task, err := m.Read(ctx, id)
	if err != nil {
		return err
	}

	// Append comment to description with timestamp
	timestamp := time.Now().Format(time.RFC3339)
	task.Description = strings.TrimSpace(task.Description) + fmt.Sprintf("\n\n---\n\n**Comment** (%s):\n%s", timestamp, comment)
	return m.writeTask(task)
}

// Create creates a new task in the markdown tracker.
func (m *MarkdownTracker) Create(ctx context.Context, args tracker.CreateArgs) (*tracker.TrackerTask, error) {
	if args.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	if err := m.ensureDir(); err != nil {
		return nil, fmt.Errorf("ensure tasks dir: %w", err)
	}

	// Generate a task ID with the project prefix
	// Use a simple timestamp-based ID
	newID := fmt.Sprintf("og-%d", time.Now().Unix())

	task := &tracker.TrackerTask{
		ID:          newID,
		Title:       args.Title,
		Type:        args.Type,
		Status:      "open",
		Priority:    args.Priority,
		Labels:      args.Labels,
		Description: args.Description,
		CreatedAt:   time.Now().Format(time.RFC3339),
		UpdatedAt:   time.Now().Format(time.RFC3339),
	}

	if task.Type == "" {
		task.Type = "task"
	}
	// Priority 0 is valid (P0/critical), so only default if negative
	if task.Priority < 0 {
		task.Priority = 2
	}

	if err := m.writeTask(task); err != nil {
		return nil, err
	}

	return task, nil
}

// SearchOpen searches for open tasks matching the query.
// It performs a simple text search on title and description.
func (m *MarkdownTracker) SearchOpen(ctx context.Context, query string, limit int) ([]*tracker.TrackerTask, error) {
	if err := m.ensureDir(); err != nil {
		return nil, fmt.Errorf("ensure tasks dir: %w", err)
	}

	files, err := filepath.Glob(filepath.Join(m.tasksDir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("glob tasks: %w", err)
	}

	query = strings.ToLower(query)
	var matches []*tracker.TrackerTask

	for _, file := range files {
		task, err := m.readTask(file)
		if err != nil {
			continue // skip unparseable files
		}
		if task.Status != "open" {
			continue
		}

		// Simple text search on title and description
		titleLower := strings.ToLower(task.Title)
		descLower := strings.ToLower(task.Description)
		if strings.Contains(titleLower, query) || strings.Contains(descLower, query) {
			matches = append(matches, task)
			if limit > 0 && len(matches) >= limit {
				break
			}
		}
	}

	return matches, nil
}

func (m *MarkdownTracker) readTask(file string) (*tracker.TrackerTask, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	// Parse frontmatter
	re := regexp.MustCompile(`(?s)^---\n(.*?)\n---`)
	matches := re.FindSubmatch(content)
	if len(matches) < 2 {
		return nil, fmt.Errorf("no frontmatter in %s", file)
	}

	var task tracker.TrackerTask
	if err := json.Unmarshal(matches[1], &task); err != nil {
		// Try parsing as YAML-like frontmatter
		return parseYAMLFrontmatter(string(matches[1]))
	}

	// Also read description from body
	body := string(content)
	if idx := strings.Index(body, "---"); idx >= 0 {
		body = body[idx+3:]
		if idx := strings.Index(body, "---"); idx >= 0 {
			body = body[idx+3:]
		}
	}
	task.Description = strings.TrimSpace(body)

	return &task, nil
}

func parseYAMLFrontmatter(frontmatter string) (*tracker.TrackerTask, error) {
	task := &tracker.TrackerTask{}
	lines := strings.Split(frontmatter, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, "\"'")

		switch key {
		case "id":
			task.ID = value
		case "title":
			task.Title = value
		case "type":
			task.Type = value
		case "status":
			task.Status = value
		case "priority":
			var p int
			fmt.Sscanf(value, "%d", &p)
			task.Priority = p
		case "assignee":
			task.Assignee = value
		case "labels":
			// Parse array-like: [label1, label2]
			value = strings.Trim(value, "[]")
			if value != "" {
				task.Labels = strings.Split(value, ",")
				for i := range task.Labels {
					task.Labels[i] = strings.TrimSpace(task.Labels[i])
				}
			}
		case "created_at":
			task.CreatedAt = value
		case "updated_at":
			task.UpdatedAt = value
		}
	}
	return task, nil
}

func (m *MarkdownTracker) writeTask(task *tracker.TrackerTask) error {
	if err := m.ensureDir(); err != nil {
		return err
	}

	// Create frontmatter
	frontmatter := map[string]any{
		"id":          task.ID,
		"title":       task.Title,
		"type":        task.Type,
		"status":      task.Status,
		"priority":    task.Priority,
		"assignee":    task.Assignee,
		"labels":      task.Labels,
		"created_at":  task.CreatedAt,
		"updated_at":  task.UpdatedAt,
	}

	data, err := json.MarshalIndent(frontmatter, "", "  ")
	if err != nil {
		return err
	}

	content := fmt.Sprintf("---\n%s\n---\n\n%s\n", string(data), task.Description)
	file := filepath.Join(m.tasksDir, task.ID+".md")
	return os.WriteFile(file, []byte(content), 0644)
}