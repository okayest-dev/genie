package tracker

import (
	"context"
	"errors"
)

// TrackerTask represents a task in the tracker.
type TrackerTask struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Status      string   `json:"status"`
	Priority    int      `json:"priority"`
	Assignee    string   `json:"assignee,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Description string   `json:"description,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

// TrackerSource is the interface for plugins that provide tracker functionality.
type TrackerSource interface {
	Frontier(ctx context.Context) (*TrackerTask, error)
	Claim(ctx context.Context, id string) (*TrackerTask, error)
	Read(ctx context.Context, id string) (*TrackerTask, error)
	Close(ctx context.Context, id, reason string) error
	Comment(ctx context.Context, id, comment string) error
}

var (
	ErrNoReadyTasks     = errors.New("no ready tasks available")
	ErrTaskAlreadyActive = errors.New("a task is already active")
	ErrTaskNotClaimable  = errors.New("task is not claimable")
)