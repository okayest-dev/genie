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

	// Block raw git worktree commands while a task is active: the same way the
	// governed task tools are the only sanctioned tracker write path, they are
	// the only sanctioned worktree-allocation path (og-wm7.3). Read-only
	// inspection (`git worktree list`) passes, like tracker reads do.
	if isWorktreeCommand(args.Command) {
		return "", fmt.Errorf("Guard: raw git worktree is blocked while a task is active; the task worktree is managed by task.claim and task.resolve, not by running git directly")
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

// worktreeCommand matches a raw `git worktree` invocation, optionally with a
// leading `-C <dir>` and optionally after a shell separator, so
// `cd x && git worktree list` is caught alongside a bare `git worktree add`.
var worktreeCommand = regexp.MustCompile(`(^|[;&|]\s*)git(\s+(--?\S+)(\s+\S+)?)*\s+worktree\b`)

// worktreeSubcommandRE captures the subcommand token following `git worktree`.
var worktreeSubcommandRE = regexp.MustCompile(`\bworktree\s+([a-z]+)`)

// readOnlyWorktreeSubcommands are the git worktree subcommands that only
// inspect state. Like tracker reads, they pass the guard while a task is
// active; every subcommand that allocates or mutates worktrees is blocked.
var readOnlyWorktreeSubcommands = map[string]bool{"list": true}

// worktreeSubcommand returns the git worktree subcommand in cmd ("" when the
// token after `worktree` is not a plain word, e.g. a bare `git worktree`).
func worktreeSubcommand(cmd string) string {
	m := worktreeSubcommandRE.FindStringSubmatch(cmd)
	if m == nil {
		return ""
	}
	return m[1]
}

// isWorktreeCommand reports whether the command mutates raw `git worktree`
// state. Read-only inspection (`git worktree list`) is allowed while a task is
// active, mirroring how tracker reads pass the guard.
func isWorktreeCommand(cmd string) bool {
	if !worktreeCommand.MatchString(strings.TrimSpace(cmd)) {
		return false
	}
	return !readOnlyWorktreeSubcommands[worktreeSubcommand(cmd)]
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