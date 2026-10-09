package tasktools

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/okayest-dev/genie/internal/session"
	"github.com/okayest-dev/genie/internal/tools"
)

type SessionResetArgs struct{}

type SessionResetResult struct {
	SessionID string `json:"session_id"`
}

type SessionResetTool struct {
	state    *TaskState
	enabled  bool
	timeout  time.Duration
	runHandle interface {
		NewSession() (*session.Session, error)
	}
}

func NewSessionResetTool(state *TaskState, enabled bool, timeout time.Duration, runHandle interface {
	NewSession() (*session.Session, error)
}) *SessionResetTool {
	return &SessionResetTool{
		state:     state,
		enabled:   enabled,
		timeout:   timeout,
		runHandle: runHandle,
	}
}

func (t *SessionResetTool) Name() string {
	return "session_reset"
}

func (t *SessionResetTool) Description() string {
	return "Reset the session (equivalent to /new), creating a fresh context. " +
		"Refused if a task is active — resolve the task first to flush its context via boundary compaction, " +
		"then call session_reset if a full reset is still needed. " +
		"Requires task_tools enabled in config."
}

func (t *SessionResetTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []any{},
	}
}

func (t *SessionResetTool) Execute(raw json.RawMessage) (string, error) {
	if !t.enabled {
		return "", fmt.Errorf("session_reset is disabled (task_tools not enabled in config)")
	}

	if t.state.HasActive() {
		active := t.state.Get()
		return "", fmt.Errorf("cannot reset session with active task %s (%q); resolve it first with task.resolve to flush context via boundary compaction, then call session_reset if a full reset is still needed", active.Task.ID, active.Task.Title)
	}

	sess, err := t.runHandle.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create new session: %w", err)
	}

	result := SessionResetResult{SessionID: sess.ID}
	resultJSON, _ := json.Marshal(result)
	return fmt.Sprintf("Session reset. New session: %s\n%s", sess.ID, string(resultJSON)), nil
}

func SessionResetDef() tools.Tool {
	return nil
}