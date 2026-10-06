package tasktools

import (
	"context"
	"encoding/json"
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