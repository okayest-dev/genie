// Package bashtool implements the bash tool guard for tracker writes.
package bashtool

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/okayest-dev/genie/internal/tasktools"
	"github.com/okayest-dev/genie/internal/tools"
)

// TrackerGuardPolicy defines the policy for guarding tracker writes.
type TrackerGuardPolicy string

const (
	TrackerGuardStrict     TrackerGuardPolicy = "strict"
	TrackerGuardPermissive TrackerGuardPolicy = "permissive"
	TrackerGuardOff        TrackerGuardPolicy = "off"
)

// GuardedBashTool wraps a bash tool with tracker write protection.
type GuardedBashTool struct {
	inner    *Tool
	state    *tasktools.TaskState
	policy   TrackerGuardPolicy
}

// NewGuardedBashTool creates a new guarded bash tool.
func NewGuardedBashTool(inner *Tool, state *tasktools.TaskState, policy string) *GuardedBashTool {
	p := TrackerGuardPolicy(policy)
	if p == "" {
		p = TrackerGuardStrict
	}
	return &GuardedBashTool{
		inner:  inner,
		state:  state,
		policy: p,
	}
}

// Name returns the tool name (same as inner).
func (g *GuardedBashTool) Name() string {
	return g.inner.Name()
}

// Description returns the tool description (same as inner).
func (g *GuardedBashTool) Description() string {
	return g.inner.Description()
}

// Parameters returns the tool parameters (same as inner).
func (g *GuardedBashTool) Parameters() map[string]any {
	return g.inner.Parameters()
}

// RequiredPermissions delegates to the inner tool.
func (g *GuardedBashTool) RequiredPermissions(raw json.RawMessage) ([]tools.Requirement, error) {
	return g.inner.RequiredPermissions(raw)
}

// Execute runs the command with tracker write guard.
func (g *GuardedBashTool) Execute(raw json.RawMessage) (string, error) {
	// If guard is off or no active task, pass through
	if g.policy == TrackerGuardOff || !g.state.HasActive() {
		return g.inner.Execute(raw)
	}

	var args bashArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}

	if args.Command == "" {
		return "", fmt.Errorf("missing required argument: command")
	}

	// Check if the command is a tracker write
	if g.isTrackerWrite(args.Command) {
		active := g.state.Get()
		taskID := ""
		if active != nil {
			taskID = active.Task.ID
		}

		var governedTool string
		var reason string

		if g.isLifecycleMutation(args.Command, taskID) {
			governedTool = g.governedToolForCommand(args.Command)
			reason = "lifecycle mutation of active task"
		} else if g.policy == TrackerGuardStrict {
			governedTool = g.governedToolForCommand(args.Command)
			reason = "tracker write blocked by strict policy"
		} else {
			// Permissive mode: allow non-lifecycle writes
			return g.inner.Execute(raw)
		}

		return "", fmt.Errorf("Guard: use %s, not %q; %s", governedTool, args.Command, reason)
	}

	// Allow tracker reads and all other commands
	return g.inner.Execute(raw)
}

// isTrackerWrite checks if the command is a bd tracker write command.
func (g *GuardedBashTool) isTrackerWrite(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}

	// Match bd commands that modify tracker state
	writePatterns := []*regexp.Regexp{
		regexp.MustCompile(`^\s*bd\s+(close|claim|create|comment|update|edit|delete|remove)\b`),
		regexp.MustCompile(`^\s*bd\s+\w+\s+(--close|--claim|--create|--comment|--update|--edit|--delete|--remove)\b`),
	}

	for _, re := range writePatterns {
		if re.MatchString(cmd) {
			return true
		}
	}

	return false
}

// isLifecycleMutation checks if the command is a lifecycle mutation of the active task.
func (g *GuardedBashTool) isLifecycleMutation(cmd, activeTaskID string) bool {
	cmd = strings.TrimSpace(cmd)
	if activeTaskID == "" {
		return false
	}

	// Check for close/claim/update on the active task
	lifecyclePatterns := []*regexp.Regexp{
		regexp.MustCompile(`^\s*bd\s+close\s+` + regexp.QuoteMeta(activeTaskID) + `\b`),
		regexp.MustCompile(`^\s*bd\s+claim\s+` + regexp.QuoteMeta(activeTaskID) + `\b`),
		regexp.MustCompile(`^\s*bd\s+update\s+` + regexp.QuoteMeta(activeTaskID) + `\b`),
		regexp.MustCompile(`^\s*bd\s+comment\s+` + regexp.QuoteMeta(activeTaskID) + `\b`),
	}

	for _, re := range lifecyclePatterns {
		if re.MatchString(cmd) {
			return true
		}
	}

	return false
}

// governedToolForCommand returns the governed tool name for a given command.
func (g *GuardedBashTool) governedToolForCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	cmd = strings.ToLower(cmd)

	if strings.Contains(cmd, "bd close") {
		return "task.resolve"
	}
	if strings.Contains(cmd, "bd claim") {
		return "task.claim"
	}
	if strings.Contains(cmd, "bd create") {
		return "task.create"
	}
	if strings.Contains(cmd, "bd comment") {
		return "task.progress"
	}
	if strings.Contains(cmd, "bd update") {
		return "task.progress"
	}
	if strings.Contains(cmd, "bd edit") || strings.Contains(cmd, "bd delete") || strings.Contains(cmd, "bd remove") {
		return "task.progress"
	}
	return "task tool"
}