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

// DedupMatch represents a potential duplicate ticket found during search.
type DedupMatch struct {
	Task       *TrackerTask
	Similarity float64
}

// TrackerSource is the interface for plugins that provide tracker functionality.
type TrackerSource interface {
	Frontier(ctx context.Context) (*TrackerTask, error)
	Claim(ctx context.Context, id string) (*TrackerTask, error)
	Read(ctx context.Context, id string) (*TrackerTask, error)
	Close(ctx context.Context, id, reason string) error
	Comment(ctx context.Context, id, comment string) error
	Create(ctx context.Context, args CreateArgs) (*TrackerTask, error)
	// SearchOpen searches for open tasks matching the query, used for dedup detection.
	SearchOpen(ctx context.Context, query string, limit int) ([]*TrackerTask, error)
}

// CreateArgs holds the arguments for creating a new task.
type CreateArgs struct {
	Title       string
	Type        string
	Labels      []string
	Priority    int
	Description string
	Parent      string
	DependsOn   []string
	FindingOf   string
}

var (
	ErrNoReadyTasks     = errors.New("no ready tasks available")
	ErrTaskAlreadyActive = errors.New("a task is already active")
	ErrTaskNotClaimable  = errors.New("task is not claimable")
)