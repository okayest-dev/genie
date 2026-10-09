package tasktools

import (
	"fmt"
	"time"
)

// FollowupConfig holds configuration for follow-up ticket creation (og-hd8).
type FollowupConfig struct {
	// PrioritySecurity is the priority for security/data-integrity findings. Default 0 (P0).
	PrioritySecurity int
	// PriorityRecurrenceThreshold is the recurrence count threshold for P1 priority. Default 2.
	PriorityRecurrenceThreshold int
	// PriorityBlastRadiusThreshold is the blast radius (files) threshold for P1 priority. Default 10.
	PriorityBlastRadiusThreshold int
	// DedupSimilarityThreshold is the similarity threshold for near-duplicate detection (0.0-1.0). Default 0.8.
	DedupSimilarityThreshold float64
	// AutoWontfixRules are rules for auto-wontfixing findings (e.g., "style:P3" to auto-wontfix style P3 findings).
	AutoWontfixRules []string
}

// TaskToolsConfig holds configuration for the task tools.
type TaskToolsConfig struct {
	// Enable enables the task tools (task.claim, etc.). Defaults to false.
	Enable bool
	// CommandTimeout is the timeout for tracker commands.
	CommandTimeout time.Duration
	// TrackerGuardPolicy controls the bash guard for raw tracker writes.
	// Values: "strict" (default, blocks all tracker writes), "permissive" (blocks only active task lifecycle mutations), "off" (disabled).
	TrackerGuardPolicy string
	// Followup configures follow-up ticket creation behavior.
	Followup FollowupConfig
}

// DefaultTaskToolsConfig returns the default configuration.
func DefaultTaskToolsConfig() TaskToolsConfig {
	return TaskToolsConfig{
		Enable:              false,
		CommandTimeout:      30 * time.Second,
		TrackerGuardPolicy:  "strict",
		Followup: FollowupConfig{
			PrioritySecurity:            0,
			PriorityRecurrenceThreshold: 2,
			PriorityBlastRadiusThreshold: 10,
			DedupSimilarityThreshold:    0.8,
			AutoWontfixRules:            []string{},
		},
	}
}

// Validate validates the configuration.
func (c *TaskToolsConfig) Validate() error {
	if c.CommandTimeout <= 0 {
		return fmt.Errorf("task_tools.command_timeout must be positive")
	}
	if c.TrackerGuardPolicy != "" && c.TrackerGuardPolicy != "strict" && c.TrackerGuardPolicy != "permissive" && c.TrackerGuardPolicy != "off" {
		return fmt.Errorf("task_tools.tracker_guard_policy must be one of: strict, permissive, off")
	}
	if c.Followup.DedupSimilarityThreshold < 0 || c.Followup.DedupSimilarityThreshold > 1 {
		return fmt.Errorf("task_tools.followup.dedup_similarity_threshold must be between 0.0 and 1.0")
	}
	if c.Followup.PrioritySecurity < 0 || c.Followup.PrioritySecurity > 4 {
		return fmt.Errorf("task_tools.followup.priority_security must be between 0 and 4")
	}
	if c.Followup.PriorityRecurrenceThreshold < 0 {
		return fmt.Errorf("task_tools.followup.priority_recurrence_threshold must be non-negative")
	}
	if c.Followup.PriorityBlastRadiusThreshold < 0 {
		return fmt.Errorf("task_tools.followup.priority_blast_radius_threshold must be non-negative")
	}
	return nil
}