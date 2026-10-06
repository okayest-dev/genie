package tasktools

import (
	"fmt"
	"time"
)

// TaskToolsConfig holds configuration for the task tools.
type TaskToolsConfig struct {
	// Enable enables the task tools (task.claim, etc.). Defaults to false.
	Enable bool
	// CommandTimeout is the timeout for tracker commands.
	CommandTimeout time.Duration
}

// DefaultTaskToolsConfig returns the default configuration.
func DefaultTaskToolsConfig() TaskToolsConfig {
	return TaskToolsConfig{
		Enable:          false,
		CommandTimeout:  30 * time.Second,
	}
}

// Validate validates the configuration.
func (c *TaskToolsConfig) Validate() error {
	if c.CommandTimeout <= 0 {
		return fmt.Errorf("task_tools.command_timeout must be positive")
	}
	return nil
}