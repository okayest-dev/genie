package tasktools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/plugin"
)

func TestNewTaskState(t *testing.T) {
	s := NewTaskState()
	if s.HasActive() {
		t.Error("new task state should not have active task")
	}
	if s.Get() != nil {
		t.Error("new task state Get() should return nil")
	}
	if s.ActiveTaskID() != "" {
		t.Error("new task state ActiveTaskID() should be empty")
	}
}

func TestTaskStateSetAndGet(t *testing.T) {
	s := NewTaskState()
	task := &Task{
		ID:          "og-test",
		Title:       "Test Task",
		Type:        "task",
		Labels:      []string{"test"},
		Description: "A test task",
		Status:      "open",
		Priority:    2,
		CreatedAt:   time.Now().Format(time.RFC3339),
		UpdatedAt:   time.Now().Format(time.RFC3339),
	}

	s.Set(task)
	if !s.HasActive() {
		t.Error("should have active task after Set")
	}
	if s.Get() == nil {
		t.Error("Get() should return task after Set")
	}
	if s.ActiveTaskID() != "og-test" {
		t.Errorf("ActiveTaskID() = %q, want %q", s.ActiveTaskID(), "og-test")
	}
	if s.Get().Task.Title != "Test Task" {
		t.Errorf("task title = %q, want %q", s.Get().Task.Title, "Test Task")
	}
}

func TestTaskStateClear(t *testing.T) {
	s := NewTaskState()
	task := &plugin.TrackerTask{ID: "og-test", Title: "Test", Status: "open"}
	s.Set(task)
	s.Clear()
	if s.HasActive() {
		t.Error("should not have active task after Clear")
	}
	if s.Get() != nil {
		t.Error("Get() should return nil after Clear")
	}
	if s.ActiveTaskID() != "" {
		t.Error("ActiveTaskID() should be empty after Clear")
	}
}

func TestTaskStateMarshalJSON(t *testing.T) {
	s := NewTaskState()

	// Test nil case
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}
	expected := `{"active_task":null}`
	if string(data) != expected {
		t.Errorf("MarshalJSON() = %s, want %s", data, expected)
	}

	// Test with active task
	task := &plugin.TrackerTask{
		ID:          "og-test",
		Title:       "Test Task",
		Type:        "task",
		Labels:      []string{"label1", "label2"},
		Status:      "open",
		Priority:    2,
		CreatedAt:   time.Now().Format(time.RFC3339),
		UpdatedAt:   time.Now().Format(time.RFC3339),
	}
	s.Set(task)
	data, err = json.Marshal(s)
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}
	// Just verify it's valid JSON and contains the task ID
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	activeTask, ok := result["active_task"].(map[string]any)
	if !ok {
		t.Fatal("active_task not a map")
	}
	if activeTask["id"] != "og-test" {
		t.Errorf("id = %v, want og-test", activeTask["id"])
	}
	if activeTask["title"] != "Test Task" {
		t.Errorf("title = %v, want Test Task", activeTask["title"])
	}
}

func TestFakeTrackerFrontier(t *testing.T) {
	tasks := []*plugin.TrackerTask{
		{ID: "og-1", Title: "Task 1", Status: "open"},
		{ID: "og-2", Title: "Task 2", Status: "open", Assignee: "someone"},
		{ID: "og-3", Title: "Task 3", Status: "closed"},
	}
	ft := NewFakeTracker(tasks)

	task, err := ft.Frontier(context.Background())
	if err != nil {
		t.Fatalf("Frontier failed: %v", err)
	}
	if task == nil {
		t.Fatal("expected task, got nil")
	}
	if task.ID != "og-1" {
		t.Errorf("frontier task ID = %q, want og-1", task.ID)
	}
}

func TestFakeTrackerFrontierEmpty(t *testing.T) {
	tasks := []*plugin.TrackerTask{
		{ID: "og-1", Title: "Task 1", Status: "closed"},
		{ID: "og-2", Title: "Task 2", Status: "open", Assignee: "someone"},
	}
	ft := NewFakeTracker(tasks)

	_, err := ft.Frontier(context.Background())
	if err != ErrNoReadyTasks {
		t.Errorf("expected ErrNoReadyTasks, got %v", err)
	}
}

func TestFakeTrackerClaim(t *testing.T) {
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Status: "open"}}
	ft := NewFakeTracker(tasks)

	task, err := ft.Claim(context.Background(), "og-1")
	if err != nil {
		t.Fatalf("Claim failed: %v", err)
	}
	if task.ID != "og-1" {
		t.Errorf("claimed task ID = %q, want og-1", task.ID)
	}
	if task.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress", task.Status)
	}
}

func TestFakeTrackerClaimTwice(t *testing.T) {
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Status: "open"}}
	ft := NewFakeTracker(tasks)

	_, err := ft.Claim(context.Background(), "og-1")
	if err != nil {
		t.Fatalf("first claim failed: %v", err)
	}

	// Try to claim the same task again (should fail because it's already claimed)
	_, err = ft.Claim(context.Background(), "og-1")
	if err != ErrTaskAlreadyActive {
		t.Errorf("second claim error = %v, want ErrTaskAlreadyActive", err)
	}
}

func TestFakeTrackerRead(t *testing.T) {
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Status: "open"}}
	ft := NewFakeTracker(tasks)

	task, err := ft.Read(context.Background(), "og-1")
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if task.ID != "og-1" {
		t.Errorf("ID = %q, want og-1", task.ID)
	}
}

func TestFakeTrackerClose(t *testing.T) {
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Status: "in_progress"}}
	ft := NewFakeTracker(tasks)
	ft.SetClaimed("og-1")

	err := ft.Close(context.Background(), "og-1", "done")
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	task := tasks[0]
	if task.Status != "closed" {
		t.Errorf("status = %q, want closed", task.Status)
	}
	if ft.claimedID != "" {
		t.Error("claimedID should be cleared after close")
	}
}

func TestTaskClaimToolExecute(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Test Task", Type: "task", Labels: []string{"test"}, Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskClaimTool(tracker, state, true, 30*time.Second)

	// Claim frontier task
	result, err := tool.Execute([]byte(`{}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if state.ActiveTaskID() != "og-1" {
		t.Errorf("active task ID = %q, want og-1", state.ActiveTaskID())
	}
	if result == "" {
		t.Error("result should not be empty")
	}
}

func TestTaskClaimToolSpecificTask(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{
		{ID: "og-1", Title: "Task 1", Status: "open"},
		{ID: "og-2", Title: "Task 2", Status: "open"},
	}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskClaimTool(tracker, state, true, 30*time.Second)

	result, err := tool.Execute([]byte(`{"task_id": "og-2"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if state.ActiveTaskID() != "og-2" {
		t.Errorf("active task ID = %q, want og-2", state.ActiveTaskID())
	}
	if result == "" {
		t.Error("result should not be empty")
	}
}

func TestTaskClaimToolAlreadyActive(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{
		{ID: "og-1", Title: "Task 1", Status: "open"},
		{ID: "og-2", Title: "Task 2", Status: "open"},
	}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskClaimTool(tracker, state, true, 30*time.Second)

	// Claim first task
	_, err := tool.Execute([]byte(`{}`))
	if err != nil {
		t.Fatalf("first claim failed: %v", err)
	}

	// Try to claim another - should fail
	_, err = tool.Execute([]byte(`{"task_id": "og-2"}`))
	if err == nil {
		t.Error("expected error when task already active")
	}
	if state.ActiveTaskID() != "og-1" {
		t.Error("active task should still be og-1")
	}
}

func TestTaskClaimToolDisabled(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskClaimTool(tracker, state, false, 30*time.Second)

	_, err := tool.Execute([]byte(`{}`))
	if err == nil {
		t.Error("expected error when tool is disabled")
	}
}

func TestTaskResolveToolNoActiveTask(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskResolveTool(tracker, state, NoOpGateRunner{}, true, 30*time.Second)

	_, err := tool.Execute([]byte(`{"summary": "done"}`))
	if err == nil {
		t.Error("expected error when no active task")
	}
	if !strings.Contains(err.Error(), "no active task") {
		t.Errorf("error = %q, want 'no active task'", err.Error())
	}
}

func TestTaskResolveToolMissingSummary(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskResolveTool(tracker, state, NoOpGateRunner{}, true, 30*time.Second)

	// Set active task
	state.Set(tasks[0])

	_, err := tool.Execute([]byte(`{}`))
	if err == nil {
		t.Error("expected error when summary is missing")
	}
	if !strings.Contains(err.Error(), "summary is required") {
		t.Errorf("error = %q, want 'summary is required'", err.Error())
	}
}

func TestTaskResolveToolGateFailure(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	
	// Create a gate runner that fails
	failingRunner := &failingGateRunner{}
	tool := NewTaskResolveTool(tracker, state, failingRunner, true, 30*time.Second)

	// Set active task
	state.Set(tasks[0])

	_, err := tool.Execute([]byte(`{"summary": "done"}`))
	if err == nil {
		t.Error("expected error when gate fails")
	}
	if !strings.Contains(err.Error(), "resolve refused") {
		t.Errorf("error = %q, want 'resolve refused'", err.Error())
	}
	if !strings.Contains(err.Error(), "gate1: missing evidence") {
		t.Errorf("error = %q, want gate failure details", err.Error())
	}
}

func TestTaskResolveToolHumanInTheLoopRequired(t *testing.T) {
	state := NewTaskState()
	// Epic type requires human confirmation
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Epic Task", Type: "epic", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskResolveTool(tracker, state, NoOpGateRunner{}, true, 30*time.Second)

	// Set active task
	state.Set(tasks[0])

	// Try without human_confirmed
	_, err := tool.Execute([]byte(`{"summary": "done"}`))
	if err == nil {
		t.Error("expected error when human confirmation required but not provided")
	}
	if !strings.Contains(err.Error(), "requires human confirmation") {
		t.Errorf("error = %q, want 'requires human confirmation'", err.Error())
	}

	// Try with human_confirmed
	_, err = tool.Execute([]byte(`{"summary": "done", "human_confirmed": true}`))
	if err != nil {
		t.Errorf("expected success with human_confirmed=true, got: %v", err)
	}
}

func TestTaskResolveToolHappyPath(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskResolveTool(tracker, state, NoOpGateRunner{}, true, 30*time.Second)

	// Set active task
	state.Set(tasks[0])

	result, err := tool.Execute([]byte(`{"summary": "Completed the task"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result == "" {
		t.Error("result should not be empty")
	}
	if !strings.Contains(result, "Task og-1 resolved") {
		t.Errorf("result = %q, want task resolved message", result)
	}
	if !strings.Contains(result, "og-1") {
		t.Errorf("result = %q, want task ID in resolution record", result)
	}
	// Task state should be cleared
	if state.HasActive() {
		t.Error("task state should be cleared after resolve")
	}
}

func TestTaskResolveToolDisabled(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskResolveTool(tracker, state, NoOpGateRunner{}, false, 30*time.Second)

	_, err := tool.Execute([]byte(`{"summary": "done"}`))
	if err == nil {
		t.Error("expected error when tool is disabled")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error = %q, want 'disabled'", err.Error())
	}
}

func TestTaskResolveToolPostsCommentAndCloses(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskResolveTool(tracker, state, NoOpGateRunner{}, true, 30*time.Second)

	// Set active task
	state.Set(tasks[0])

	result, err := tool.Execute([]byte(`{"summary": "Done"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify task was closed
	task, err := tracker.Read(context.Background(), "og-1")
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if task.Status != "closed" {
		t.Errorf("task status = %q, want closed", task.Status)
	}

	// Verify result contains resolution record
	if !strings.Contains(result, "gates_passed") {
		t.Errorf("result = %q, want resolution record", result)
	}
}

// failingGateRunner is a test gate runner that always fails.
type failingGateRunner struct{}

func (f *failingGateRunner) Run(ctx context.Context, task *Task) (GateResult, error) {
	return GateResult{
		Failed: []FailedGate{
			{Name: "gate1", EvidenceGap: "missing evidence"},
		},
	}, nil
}

func TestTaskStateSetOnChange(t *testing.T) {
	state := NewTaskState()
	var callbackData []byte
	state.SetOnChange(func(data []byte) error {
		callbackData = data
		return nil
	})

	task := &plugin.TrackerTask{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}
	state.Set(task)

	if callbackData == nil {
		t.Error("onChange callback should have been called on Set")
	}
	var result map[string]any
	if err := json.Unmarshal(callbackData, &result); err != nil {
		t.Fatalf("callback data not valid JSON: %v", err)
	}
	activeTask, ok := result["active_task"].(map[string]any)
	if !ok {
		t.Fatal("active_task not a map")
	}
	if activeTask["id"] != "og-1" {
		t.Errorf("id = %v, want og-1", activeTask["id"])
	}

	// Test Clear
	callbackData = nil
	state.Clear()
	if callbackData == nil {
		t.Error("onChange callback should have been called on Clear")
	}
	if err := json.Unmarshal(callbackData, &result); err != nil {
		t.Fatalf("callback data not valid JSON: %v", err)
	}
	// After Clear, active_task should be null
	if result["active_task"] != nil {
		t.Errorf("active_task = %v, want null after Clear", result["active_task"])
	}
}

func TestTaskResolveToolName(t *testing.T) {
	state := NewTaskState()
	tracker := NewFakeTracker(nil)
	tool := NewTaskResolveTool(tracker, state, NoOpGateRunner{}, true, 30*time.Second)

	if tool.Name() != "task.resolve" {
		t.Errorf("Name() = %q, want task.resolve", tool.Name())
	}
}

func TestTaskResolveToolDescription(t *testing.T) {
	state := NewTaskState()
	tracker := NewFakeTracker(nil)
	tool := NewTaskResolveTool(tracker, state, NoOpGateRunner{}, true, 30*time.Second)

	desc := tool.Description()
	if desc == "" {
		t.Error("Description() should not be empty")
	}
	if !strings.Contains(desc, "Resolve") {
		t.Errorf("Description() = %q, should mention Resolve", desc)
	}
}

func TestTaskResolveToolParameters(t *testing.T) {
	state := NewTaskState()
	tracker := NewFakeTracker(nil)
	tool := NewTaskResolveTool(tracker, state, NoOpGateRunner{}, true, 30*time.Second)

	params := tool.Parameters()
	if params == nil {
		t.Error("Parameters() should not be nil")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing")
	}
	if _, ok := props["summary"]; !ok {
		t.Error("parameters should have 'summary'")
	}
	if _, ok := props["human_confirmed"]; !ok {
		t.Error("parameters should have 'human_confirmed'")
	}
	req, ok := params["required"].([]any)
	if !ok {
		t.Fatal("required missing")
	}
	found := false
	for _, r := range req {
		if r == "summary" {
			found = true
			break
		}
	}
	if !found {
		t.Error("summary should be required")
	}
}

func TestJoinWithComma(t *testing.T) {
	tests := []struct {
		input    []string
		expected string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a, b"},
		{[]string{"a", "b", "c"}, "a, b, c"},
	}
	for _, tc := range tests {
		result := joinWithComma(tc.input)
		if result != tc.expected {
			t.Errorf("joinWithComma(%v) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}