package tasktools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/gates"
	"github.com/okayest-dev/genie/internal/plugin"
	"github.com/okayest-dev/genie/internal/session"
	"github.com/okayest-dev/genie/internal/tracker"
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
	expected := `{"active_task":null,"deferred_findings":null,"progress_log":null}`
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
	// Verify progress_log is present (empty array when active task set)
	progressLog, ok := result["progress_log"].([]any)
	if !ok {
		t.Fatal("progress_log not an array")
	}
	if len(progressLog) != 0 {
		t.Errorf("progress_log length = %d, want 0", len(progressLog))
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

func (f *failingGateRunner) Run(ctx context.Context, task *gates.Task, deferredFindings []gates.DeferredFindingRecord) (gates.GateResult, error) {
	return gates.GateResult{
		Failed: []gates.FailedGate{
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

	if tool.Name() != "task_resolve" {
		t.Errorf("Name() = %q, want task_resolve", tool.Name())
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

func TestFakeTrackerCreate(t *testing.T) {
	tasks := []*plugin.TrackerTask{}
	ft := NewFakeTracker(tasks)

	task, err := ft.Create(context.Background(), tracker.CreateArgs{
		Title:       "New Task",
		Type:        "task",
		Labels:      []string{"test", "new"},
		Priority:    1,
		Description: "A new test task",
		Parent:      "og-1",
		DependsOn:   []string{"og-2", "og-3"},
		FindingOf:   "og-4",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if task.ID == "" {
		t.Error("created task should have an ID")
	}
	if task.Title != "New Task" {
		t.Errorf("title = %q, want 'New Task'", task.Title)
	}
	if task.Type != "task" {
		t.Errorf("type = %q, want 'task'", task.Type)
	}
	if task.Priority != 1 {
		t.Errorf("priority = %d, want 1", task.Priority)
	}
	if task.Labels == nil || len(task.Labels) != 2 {
		t.Errorf("labels = %v, want [test new]", task.Labels)
	}
	if task.Description != "A new test task" {
		t.Errorf("description = %q, want 'A new test task'", task.Description)
	}
	if task.Status != "open" {
		t.Errorf("status = %q, want 'open'", task.Status)
	}
}

func TestFakeTrackerCreateDefaults(t *testing.T) {
	tasks := []*plugin.TrackerTask{}
	ft := NewFakeTracker(tasks)

	task, err := ft.Create(context.Background(), tracker.CreateArgs{
		Title:    "Minimal Task",
		Priority: 2, // Explicitly set default priority
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if task.Type != "task" {
		t.Errorf("default type = %q, want 'task'", task.Type)
	}
	if task.Priority != 2 {
		t.Errorf("default priority = %d, want 2", task.Priority)
	}
	if task.Status != "open" {
		t.Errorf("default status = %q, want 'open'", task.Status)
	}
}

func TestFakeTrackerCreateMissingTitle(t *testing.T) {
	tasks := []*plugin.TrackerTask{}
	ft := NewFakeTracker(tasks)

	_, err := ft.Create(context.Background(), tracker.CreateArgs{
		Type: "task",
	})
	if err == nil {
		t.Error("expected error when title is missing")
	}
	if !strings.Contains(err.Error(), "title is required") {
		t.Errorf("error = %q, want 'title is required'", err.Error())
	}
}

func TestTaskCreateToolExecute(t *testing.T) {
	tracker := NewFakeTracker(nil)
	tool := NewTaskCreateTool(tracker, true, 30*time.Second)

	result, err := tool.Execute([]byte(`{"title": "New Task", "type": "feature", "labels": ["test"], "priority": 1, "description": "A new feature", "parent": "og-1", "depends_on": ["og-2"], "finding_of": "og-3"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result == "" {
		t.Error("result should not be empty")
	}
	if !strings.Contains(result, "New Task") {
		t.Errorf("result = %q, want task title in output", result)
	}
	if !strings.Contains(result, "feature") {
		t.Errorf("result = %q, want task type in output", result)
	}
}

func TestTaskCreateToolMissingTitle(t *testing.T) {
	tracker := NewFakeTracker(nil)
	tool := NewTaskCreateTool(tracker, true, 30*time.Second)

	_, err := tool.Execute([]byte(`{"type": "task"}`))
	if err == nil {
		t.Error("expected error when title is missing")
	}
	if !strings.Contains(err.Error(), "title is required") {
		t.Errorf("error = %q, want 'title is required'", err.Error())
	}
}

func TestTaskCreateToolDisabled(t *testing.T) {
	tracker := NewFakeTracker(nil)
	tool := NewTaskCreateTool(tracker, false, 30*time.Second)

	_, err := tool.Execute([]byte(`{"title": "Test"}`))
	if err == nil {
		t.Error("expected error when tool is disabled")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error = %q, want 'disabled'", err.Error())
	}
}

func TestTaskCreateToolDefaults(t *testing.T) {
	tracker := NewFakeTracker(nil)
	tool := NewTaskCreateTool(tracker, true, 30*time.Second)

	result, err := tool.Execute([]byte(`{"title": "Minimal Task"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "task") {
		t.Errorf("result = %q, want default type 'task'", result)
	}
	if !strings.Contains(result, "Priority: 2") {
		t.Errorf("result = %q, want default priority 2", result)
	}
}

func TestTaskCreateToolName(t *testing.T) {
	tracker := NewFakeTracker(nil)
	tool := NewTaskCreateTool(tracker, true, 30*time.Second)

	if tool.Name() != "task_create" {
		t.Errorf("Name() = %q, want task_create", tool.Name())
	}
}

func TestTaskCreateToolDescription(t *testing.T) {
	tracker := NewFakeTracker(nil)
	tool := NewTaskCreateTool(tracker, true, 30*time.Second)

	desc := tool.Description()
	if desc == "" {
		t.Error("Description() should not be empty")
	}
	if !strings.Contains(desc, "Create") {
		t.Errorf("Description() = %q, should mention Create", desc)
	}
}

func TestTaskCreateToolParameters(t *testing.T) {
	tracker := NewFakeTracker(nil)
	tool := NewTaskCreateTool(tracker, true, 30*time.Second)

	params := tool.Parameters()
	if params == nil {
		t.Error("Parameters() should not be nil")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing")
	}
	required, ok := params["required"].([]any)
	if !ok {
		t.Fatal("required missing")
	}
	foundTitle := false
	for _, r := range required {
		if r == "title" {
			foundTitle = true
			break
		}
	}
	if !foundTitle {
		t.Error("title should be required")
	}
	// Check optional fields exist
	optionalFields := []string{"type", "labels", "priority", "description", "parent", "depends_on", "finding_of"}
	for _, field := range optionalFields {
		if _, ok := props[field]; !ok {
			t.Errorf("parameters should have '%s'", field)
		}
	}
	// Check priority constraints
	priorityProp, ok := props["priority"].(map[string]any)
	if !ok {
		t.Fatal("priority property missing")
	}
	minVal := priorityProp["minimum"]
	maxVal := priorityProp["maximum"]
	// Handle both int and float64
	var minOk, maxOk bool
	var minF, maxF float64
	switch v := minVal.(type) {
	case float64:
		minF, minOk = v, true
	case int:
		minF, minOk = float64(v), true
	}
	switch v := maxVal.(type) {
	case float64:
		maxF, maxOk = v, true
	case int:
		maxF, maxOk = float64(v), true
	}
	if !minOk || !maxOk || minF != 0 || maxF != 4 {
		t.Errorf("priority should have minimum 0 and maximum 4, got min=%v (%T) max=%v (%T)", minVal, minVal, maxVal, maxVal)
	}
}

func TestTaskProgressToolNoActiveTask(t *testing.T) {
	state := NewTaskState()
	tracker := NewFakeTracker(nil)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	_, err := tool.Execute([]byte(`{"summary": "test"}`))
	if err == nil {
		t.Error("expected error when no active task")
	}
	if !strings.Contains(err.Error(), "no active task") {
		t.Errorf("error = %q, want 'no active task'", err.Error())
	}
}

func TestTaskProgressToolEmptyArgs(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	// Set active task
	state.Set(tasks[0])

	_, err := tool.Execute([]byte(`{}`))
	if err == nil {
		t.Error("expected error when no fields provided")
	}
	if !strings.Contains(err.Error(), "at least one of summary, notes, or tracker_note must be provided") {
		t.Errorf("error = %q, want 'at least one of summary, notes, or tracker_note must be provided'", err.Error())
	}
}

func TestTaskProgressToolWithSummary(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	// Set active task
	state.Set(tasks[0])

	result, err := tool.Execute([]byte(`{"summary": "Implemented feature X"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result == "" {
		t.Error("result should not be empty")
	}
	if !strings.Contains(result, "og-1") {
		t.Errorf("result = %q, want task ID in output", result)
	}
	if !strings.Contains(result, "Implemented feature X") {
		t.Errorf("result = %q, want summary in output", result)
	}

	// Verify progress log
	log := state.GetProgressLog()
	if len(log) != 1 {
		t.Errorf("progress log length = %d, want 1", len(log))
	}
	if log[0].Summary != "Implemented feature X" {
		t.Errorf("progress summary = %q, want 'Implemented feature X'", log[0].Summary)
	}
}

func TestTaskProgressToolWithNotes(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	state.Set(tasks[0])

	result, err := tool.Execute([]byte(`{"notes": "Detailed evidence about the implementation"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "Detailed evidence") {
		t.Errorf("result = %q, want notes in output", result)
	}

	log := state.GetProgressLog()
	if len(log) != 1 {
		t.Errorf("progress log length = %d, want 1", len(log))
	}
	if log[0].Notes != "Detailed evidence about the implementation" {
		t.Errorf("progress notes = %q, want 'Detailed evidence about the implementation'", log[0].Notes)
	}
}

func TestTaskProgressToolWithTrackerNote(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	state.Set(tasks[0])

	result, err := tool.Execute([]byte(`{"tracker_note": "Checkpoint: done with phase 1"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "Tracker note appended") {
		t.Errorf("result = %q, want tracker note appended message", result)
	}

	log := state.GetProgressLog()
	if len(log) != 1 {
		t.Errorf("progress log length = %d, want 1", len(log))
	}
	if log[0].TrackerNote != "Checkpoint: done with phase 1" {
		t.Errorf("progress tracker_note = %q, want 'Checkpoint: done with phase 1'", log[0].TrackerNote)
	}
}

func TestTaskProgressToolWithAllFields(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	state.Set(tasks[0])

	result, err := tool.Execute([]byte(`{"summary": "Phase 1 done", "notes": "Evidence details", "tracker_note": "Checkpoint comment"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "Phase 1 done") {
		t.Errorf("result = %q, want summary", result)
	}
	if !strings.Contains(result, "Evidence details") {
		t.Errorf("result = %q, want notes", result)
	}
	if !strings.Contains(result, "Tracker note appended") {
		t.Errorf("result = %q, want tracker note appended", result)
	}

	log := state.GetProgressLog()
	if len(log) != 1 {
		t.Errorf("progress log length = %d, want 1", len(log))
	}
	if log[0].Summary != "Phase 1 done" {
		t.Errorf("summary = %q", log[0].Summary)
	}
	if log[0].Notes != "Evidence details" {
		t.Errorf("notes = %q", log[0].Notes)
	}
	if log[0].TrackerNote != "Checkpoint comment" {
		t.Errorf("tracker_note = %q", log[0].TrackerNote)
	}
}

func TestTaskProgressToolMultipleCheckpoints(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	state.Set(tasks[0])

	// First checkpoint
	_, err := tool.Execute([]byte(`{"summary": "Checkpoint 1"}`))
	if err != nil {
		t.Fatalf("first checkpoint failed: %v", err)
	}

	// Second checkpoint
	_, err = tool.Execute([]byte(`{"summary": "Checkpoint 2"}`))
	if err != nil {
		t.Fatalf("second checkpoint failed: %v", err)
	}

	log := state.GetProgressLog()
	if len(log) != 2 {
		t.Errorf("progress log length = %d, want 2", len(log))
	}
	if log[0].Summary != "Checkpoint 1" {
		t.Errorf("first summary = %q", log[0].Summary)
	}
	if log[1].Summary != "Checkpoint 2" {
		t.Errorf("second summary = %q", log[1].Summary)
	}
}

func TestTaskProgressToolLifecycleStateUntouched(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	state.Set(tasks[0])

	// Record progress
	_, err := tool.Execute([]byte(`{"summary": "Checkpoint"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Active task should still be the same
	if !state.HasActive() {
		t.Error("task should still be active after progress")
	}
	if state.ActiveTaskID() != "og-1" {
		t.Errorf("active task ID = %q, want og-1", state.ActiveTaskID())
	}
}

func TestTaskProgressToolDisabled(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskProgressTool(tracker, state, false, 30*time.Second)

	state.Set(tasks[0])

	_, err := tool.Execute([]byte(`{"summary": "test"}`))
	if err == nil {
		t.Error("expected error when tool is disabled")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error = %q, want 'disabled'", err.Error())
	}
}

func TestTaskProgressToolTrackerNotePosted(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	state.Set(tasks[0])

	_, err := tool.Execute([]byte(`{"tracker_note": "Test comment"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify comment was posted by checking the fake tracker (it's a no-op but we can verify no error)
	// The fake tracker Comment method just returns nil if task exists
}

func TestTaskProgressToolName(t *testing.T) {
	state := NewTaskState()
	tracker := NewFakeTracker(nil)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	if tool.Name() != "task_progress" {
		t.Errorf("Name() = %q, want task_progress", tool.Name())
	}
}

func TestTaskProgressToolDescription(t *testing.T) {
	state := NewTaskState()
	tracker := NewFakeTracker(nil)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	desc := tool.Description()
	if desc == "" {
		t.Error("Description() should not be empty")
	}
	if !strings.Contains(desc, "progress") {
		t.Errorf("Description() = %q, should mention progress", desc)
	}
}

func TestTaskProgressToolParameters(t *testing.T) {
	state := NewTaskState()
	tracker := NewFakeTracker(nil)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	params := tool.Parameters()
	if params == nil {
		t.Error("Parameters() should not be nil")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing")
	}
	// Check all optional fields exist
	optionalFields := []string{"summary", "notes", "tracker_note"}
	for _, field := range optionalFields {
		if _, ok := props[field]; !ok {
			t.Errorf("parameters should have '%s'", field)
		}
	}
	// No required fields
	req, ok := params["required"].([]any)
	if !ok {
		t.Fatal("required missing")
	}
	if len(req) != 0 {
		t.Errorf("required should be empty, got %v", req)
	}
}

func TestTaskStateProgressLogClearedOnClear(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	state.Set(tasks[0])
	_, err := tool.Execute([]byte(`{"summary": "Checkpoint"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(state.GetProgressLog()) != 1 {
		t.Error("progress log should have 1 entry before clear")
	}

	state.Clear()

	if state.HasActive() {
		t.Error("task should not be active after clear")
	}
	if len(state.GetProgressLog()) != 0 {
		t.Error("progress log should be cleared after clear")
	}
}

func TestTaskStateMarshalJSONIncludesProgressLog(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open", Labels: []string{"test"}}}
	tracker := NewFakeTracker(tasks)
	tool := NewTaskProgressTool(tracker, state, true, 30*time.Second)

	state.Set(tasks[0])
	_, err := tool.Execute([]byte(`{"summary": "Checkpoint 1"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	progressLog, ok := result["progress_log"].([]any)
	if !ok {
		t.Fatal("progress_log not an array")
	}
	if len(progressLog) != 1 {
		t.Errorf("progress_log length = %d, want 1", len(progressLog))
	}
	entry, ok := progressLog[0].(map[string]any)
	if !ok {
		t.Fatal("progress_log entry not a map")
	}
	if entry["summary"] != "Checkpoint 1" {
		t.Errorf("summary = %v, want 'Checkpoint 1'", entry["summary"])
	}
}

type fakeRunHandle struct {
	sessions []*session.Session
	callCount int
}

func (f *fakeRunHandle) NewSession() (*session.Session, error) {
	f.callCount++
	// Create a real session for testing
	sess, err := session.New(f.sessions[0].TranscriptPath[:len(f.sessions[0].TranscriptPath)-len(f.sessions[0].ID)-5])
	if err != nil {
		return nil, err
	}
	f.sessions = append(f.sessions, sess)
	return sess, nil
}

func TestSessionResetToolNoActiveTask(t *testing.T) {
	state := NewTaskState()
	// Create a temp dir for sessions
	dir := t.TempDir()
	initialSess, err := session.New(dir)
	if err != nil {
		t.Fatalf("failed to create initial session: %v", err)
	}
	runHandle := &fakeRunHandle{sessions: []*session.Session{initialSess}}
	
	tool := NewSessionResetTool(state, true, 30*time.Second, runHandle)

	result, err := tool.Execute([]byte(`{}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result == "" {
		t.Error("result should not be empty")
	}
	if !strings.Contains(result, "Session reset") {
		t.Errorf("result = %q, want session reset message", result)
	}
	if runHandle.callCount != 1 {
		t.Errorf("NewSession called %d times, want 1", runHandle.callCount)
	}
}

func TestSessionResetToolWithActiveTask(t *testing.T) {
	state := NewTaskState()
	tasks := []*plugin.TrackerTask{{ID: "og-1", Title: "Task 1", Type: "task", Status: "open"}}
	state.Set(tasks[0])

	dir := t.TempDir()
	initialSess, err := session.New(dir)
	if err != nil {
		t.Fatalf("failed to create initial session: %v", err)
	}
	runHandle := &fakeRunHandle{sessions: []*session.Session{initialSess}}
	
	tool := NewSessionResetTool(state, true, 30*time.Second, runHandle)

	_, err = tool.Execute([]byte(`{}`))
	if err == nil {
		t.Error("expected error when task is active")
	}
	if !strings.Contains(err.Error(), "cannot reset session with active task") {
		t.Errorf("error = %q, want 'cannot reset session with active task'", err.Error())
	}
	if !strings.Contains(err.Error(), "og-1") {
		t.Errorf("error = %q, want task ID in error", err.Error())
	}
	if runHandle.callCount != 0 {
		t.Errorf("NewSession should not be called when task is active, called %d times", runHandle.callCount)
	}
}

func TestSessionResetToolDisabled(t *testing.T) {
	state := NewTaskState()

	dir := t.TempDir()
	initialSess, err := session.New(dir)
	if err != nil {
		t.Fatalf("failed to create initial session: %v", err)
	}
	runHandle := &fakeRunHandle{sessions: []*session.Session{initialSess}}
	
	tool := NewSessionResetTool(state, false, 30*time.Second, runHandle)

	_, err = tool.Execute([]byte(`{}`))
	if err == nil {
		t.Error("expected error when tool is disabled")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error = %q, want 'disabled'", err.Error())
	}
}

func TestSessionResetToolName(t *testing.T) {
	state := NewTaskState()
	dir := t.TempDir()
	initialSess, _ := session.New(dir)
	runHandle := &fakeRunHandle{sessions: []*session.Session{initialSess}}
	
	tool := NewSessionResetTool(state, true, 30*time.Second, runHandle)

	if tool.Name() != "session_reset" {
		t.Errorf("Name() = %q, want session_reset", tool.Name())
	}
}

func TestSessionResetToolDescription(t *testing.T) {
	state := NewTaskState()
	dir := t.TempDir()
	initialSess, _ := session.New(dir)
	runHandle := &fakeRunHandle{sessions: []*session.Session{initialSess}}
	
	tool := NewSessionResetTool(state, true, 30*time.Second, runHandle)

	desc := tool.Description()
	if desc == "" {
		t.Error("Description() should not be empty")
	}
	if !strings.Contains(desc, "Reset") {
		t.Errorf("Description() = %q, should mention Reset", desc)
	}
	if !strings.Contains(desc, "active task") && !strings.Contains(desc, "is active") {
		t.Errorf("Description() = %q, should mention active task refusal", desc)
	}
}

func TestSessionResetToolParameters(t *testing.T) {
	state := NewTaskState()
	dir := t.TempDir()
	initialSess, _ := session.New(dir)
	runHandle := &fakeRunHandle{sessions: []*session.Session{initialSess}}
	
	tool := NewSessionResetTool(state, true, 30*time.Second, runHandle)

	params := tool.Parameters()
	if params == nil {
		t.Error("Parameters() should not be nil")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing")
	}
	// No properties for session_reset
	if len(props) != 0 {
		t.Errorf("properties should be empty, got %v", props)
	}
	req, ok := params["required"].([]any)
	if !ok {
		t.Fatal("required missing")
	}
	if len(req) != 0 {
		t.Errorf("required should be empty, got %v", req)
	}
}