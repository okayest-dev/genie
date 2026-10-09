package tasktools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/okayest-dev/genie/internal/tracker"
)

// Task represents a tracker issue/task.
type Task = tracker.TrackerTask

// Tracker is the interface for interacting with the issue tracker.
type Tracker = tracker.TrackerSource

// ErrNoReadyTasks is returned when no ready tasks are available.
var ErrNoReadyTasks = errors.New("no ready tasks available")

// ErrTaskAlreadyActive is returned when trying to claim while a task is active.
var ErrTaskAlreadyActive = errors.New("a task is already active")

// ErrTaskNotClaimable is returned when a task cannot be claimed.
var ErrTaskNotClaimable = errors.New("task is not claimable")

// FakeTracker is a test implementation of Tracker and tracker.TrackerSource.
type FakeTracker struct {
	tasks         map[string]*tracker.TrackerTask
	claimedID     string
	frontierOrder []string
	createdTickets []string
}

// Ensure FakeTracker implements tracker.TrackerSource
var _ tracker.TrackerSource = (*FakeTracker)(nil)

// NewFakeTracker creates a new fake tracker for testing.
func NewFakeTracker(tasks []*tracker.TrackerTask) *FakeTracker {
	m := make(map[string]*tracker.TrackerTask)
	order := make([]string, 0, len(tasks))
	for _, t := range tasks {
		m[t.ID] = t
		order = append(order, t.ID)
	}
	return &FakeTracker{
		tasks:          m,
		frontierOrder:  order,
		createdTickets: make([]string, 0),
	}
}

// Ensure FakeTracker implements tracker.TrackerSource
var _ tracker.TrackerSource = (*FakeTracker)(nil)

// Frontier returns the first unclaimed, unblocked task.
func (f *FakeTracker) Frontier(ctx context.Context) (*tracker.TrackerTask, error) {
	for _, id := range f.frontierOrder {
		t := f.tasks[id]
		if t.Status == "open" && t.Assignee == "" {
			return t, nil
		}
	}
	return nil, ErrNoReadyTasks
}

// Claim claims a task.
func (f *FakeTracker) Claim(ctx context.Context, id string) (*tracker.TrackerTask, error) {
	t, ok := f.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	if f.claimedID != "" {
		return nil, ErrTaskAlreadyActive
	}
	if t.Status != "open" {
		return nil, ErrTaskNotClaimable
	}
	f.claimedID = id
	t.Assignee = "test"
	t.Status = "in_progress"
	return t, nil
}

// Read returns a task by ID.
func (f *FakeTracker) Read(ctx context.Context, id string) (*tracker.TrackerTask, error) {
	t, ok := f.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	return t, nil
}

// Close closes a task.
func (f *FakeTracker) Close(ctx context.Context, id string, reason string) error {
	t, ok := f.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	t.Status = "closed"
	if f.claimedID == id {
		f.claimedID = ""
	}
	return nil
}

// Comment adds a comment to a task (no-op for fake tracker).
func (f *FakeTracker) Comment(ctx context.Context, id string, comment string) error {
	_, ok := f.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}
	return nil
}

// Create creates a new task in the fake tracker.
func (f *FakeTracker) Create(ctx context.Context, args tracker.CreateArgs) (*tracker.TrackerTask, error) {
	if args.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	// Generate a task ID with the project prefix
	// For testing, we'll use a simple counter-based approach
	newID := fmt.Sprintf("og-new%d", len(f.tasks)+1)

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

	f.tasks[newID] = task
	f.frontierOrder = append(f.frontierOrder, newID)
	f.createdTickets = append(f.createdTickets, newID)

	return task, nil
}

// SetFrontierOrder sets the order for frontier queries.
func (f *FakeTracker) SetFrontierOrder(order []string) {
	f.frontierOrder = order
}

// SetClaimed sets the currently claimed task ID.
func (f *FakeTracker) SetClaimed(id string) {
	f.claimedID = id
}

// GetCreatedTickets returns the IDs of tickets created during this session.
func (f *FakeTracker) GetCreatedTickets() []string {
	return f.createdTickets
}

// SearchOpen searches for open tasks matching the query.
// It performs a word-based search on title and description.
func (f *FakeTracker) SearchOpen(ctx context.Context, query string, limit int) ([]*tracker.TrackerTask, error) {
	queryWords := strings.Fields(strings.ToLower(query))
	var matches []*tracker.TrackerTask

	for _, task := range f.tasks {
		if task.Status != "open" {
			continue
		}

		titleLower := strings.ToLower(task.Title)
		descLower := strings.ToLower(task.Description)

		// Check if all query words appear in title or description
		allMatch := true
		for _, word := range queryWords {
			if !strings.Contains(titleLower, word) && !strings.Contains(descLower, word) {
				allMatch = false
				break
			}
		}

		if allMatch {
			matches = append(matches, task)
			if limit > 0 && len(matches) >= limit {
				break
			}
		}
	}

	return matches, nil
}