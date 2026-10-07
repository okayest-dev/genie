package run

import (
	"testing"

	"github.com/okayest-dev/genie/internal/config"
)

func TestTaskClaimToolRegistered(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	opts := Options{
		Config:   cfg,
		Cwd:      ".",
		Stdin:    nil,
		Stdout:   nil,
		Stderr:   nil,
		Provider: cfg.Provider,
	}

	h, err := New(opts)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	registry := h.Registry()
	tool, ok := registry.Get("task.claim")
	if !ok {
		t.Error("task.claim tool not registered")
		return
	}

	t.Logf("task.claim tool registered: %s", tool.Name())

	// Test the tool
	result, err := tool.Execute([]byte(`{}`))
	if err != nil {
		t.Logf("Execute error (expected): %v", err)
	} else {
		t.Logf("Execute result: %s", result)
	}
}

func TestTaskCreateToolRegistered(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	opts := Options{
		Config:   cfg,
		Cwd:      ".",
		Stdin:    nil,
		Stdout:   nil,
		Stderr:   nil,
		Provider: cfg.Provider,
	}

	h, err := New(opts)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	registry := h.Registry()
	tool, ok := registry.Get("task.create")
	if !ok {
		t.Error("task.create tool not registered")
		return
	}

	t.Logf("task.create tool registered: %s", tool.Name())

	// Test the tool
	result, err := tool.Execute([]byte(`{"title": "Test Task"}`))
	if err != nil {
		t.Logf("Execute error (expected): %v", err)
	} else {
		t.Logf("Execute result: %s", result)
	}
}
