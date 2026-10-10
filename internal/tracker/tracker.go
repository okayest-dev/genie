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

// Isolation describes where a tracker's state lives relative to a git
// worktree: whether every parallel checkout resolves the same tracker state.
// The harness's worktree-allocation machinery (policy=required) gates on this,
// so a tracker whose store would diverge per checkout is never silently used
// in parallel worktrees.
type Isolation int

const (
	// IsolationUnknown is the conservative default when a tracker makes no
	// declaration: treated as not worktree-safe until the tracker opts in.
	IsolationUnknown Isolation = iota
	// IsolationShared means the tracker's state is identical from any worktree:
	// a remote store, or a local store outside the checkout that every checkout
	// reaches (a home-dir store, a shared DB discovered via git common dir, etc.).
	IsolationShared
	// IsolationPerCheckout means the tracker's state lives inside the checkout
	// and diverges across parallel worktrees.
	IsolationPerCheckout
)

// String returns the canonical wire name for an isolation value, or "unknown"
// for unset/invalid values. Useful for logging and protocol round-trips.
func (i Isolation) String() string {
	switch i {
	case IsolationShared:
		return "shared"
	case IsolationPerCheckout:
		return "per_checkout"
	default:
		return "unknown"
	}
}

// WorktreeSafe reports whether worktree allocation may proceed with this
// tracker: only explicitly-shared trackers are safe. Unknown and per_checkout
// both refuse, so a tracker must opt in before parallel worktrees are allowed.
func (i Isolation) WorktreeSafe() bool { return i == IsolationShared }

// ParseIsolation maps a protocol-declared wire value onto Isolation. Empty and
// unrecognised values map to the conservative IsolationUnknown, keeping the
// plan gate closed until a plugin explicitly opts in.
func ParseIsolation(s string) Isolation {
	switch s {
	case "shared":
		return IsolationShared
	case "per_checkout":
		return IsolationPerCheckout
	default:
		return IsolationUnknown
	}
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
	// Isolation declares where this tracker's state lives relative to git
	// worktrees. Worktree allocation (og-wm7.3) refuses to run in parallel
	// unless this reports IsolationShared.
	Isolation() Isolation
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