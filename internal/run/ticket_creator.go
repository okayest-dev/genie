package run

import (
	"context"
	"fmt"

	"github.com/okayest-dev/genie/internal/gates"
	"github.com/okayest-dev/genie/internal/tasktools"
	"github.com/okayest-dev/genie/internal/tracker"
)

// TrackerTicketCreator implements gates.TicketCreator using the task tracker.
type TrackerTicketCreator struct {
	tracker      tracker.TrackerSource
	followup     *tasktools.FollowupEngine
	enabled      bool
}

// NewTrackerTicketCreator creates a new ticket creator using the task tracker.
func NewTrackerTicketCreator(tracker tracker.TrackerSource) *TrackerTicketCreator {
	return &TrackerTicketCreator{
		tracker: tracker,
		enabled: true,
	}
}

// NewTrackerTicketCreatorWithFollowup creates a new ticket creator with follow-up engine.
func NewTrackerTicketCreatorWithFollowup(tracker tracker.TrackerSource, followup *tasktools.FollowupEngine) *TrackerTicketCreator {
	return &TrackerTicketCreator{
		tracker:  tracker,
		followup: followup,
		enabled:  true,
	}
}

// Disable disables the ticket creator.
func (c *TrackerTicketCreator) Disable() {
	c.enabled = false
}

// CreateTicket creates a follow-up ticket for a deferred finding.
func (c *TrackerTicketCreator) CreateTicket(ctx context.Context, args gates.CreateTicketArgs) (string, error) {
	if !c.enabled || c.tracker == nil {
		return "", fmt.Errorf("no tracker available")
	}

	// If followup engine is available, use it for dedup guard and priority shaping
	if c.followup != nil {
		result, err := c.followup.ProcessFinding(ctx, tasktools.FindingInput{
			InvariantViolated: args.Invariant,
			Category:          args.Category,
			Severity:          args.Severity,
			BlastRadiusFiles:  args.BlastRadius,
			RecurrenceCount:   args.Recurrence,
			SourceTaskID:      args.FindingOf,
			SourceFile:        "",
			SourceEvidence:    "",
			Description:       args.Description,
		})
		if err != nil {
			return "", fmt.Errorf("followup processing failed: %w", err)
		}

		// If deduped, return the existing ticket ID
		if result.Action == "deduped" {
			return result.TicketID, nil
		}
		// If wontfix, return empty (no ticket created)
		if result.Action == "wontfix" {
			return "", nil
		}
		// Created new ticket
		return result.TicketID, nil
	}

	// Fallback to direct ticket creation without followup engine
	description := args.Description
	if description == "" {
		description = fmt.Sprintf(
			"Follow-up ticket for deferred finding from gate %s.\n\n"+
				"**Finding ID**: %s\n"+
				"**Invariant Violated**: %s\n"+
				"**Severity**: %s\n"+
				"**Category**: %s\n"+
				"**Blast Radius**: %d files\n"+
				"**Recurrence**: %d\n"+
				"**Source Task**: %s\n",
			args.SourceGate,
			args.FindingID,
			args.Invariant,
			args.Severity,
			args.Category,
			args.BlastRadius,
			args.Recurrence,
			args.FindingOf,
		)
	}

	task, err := c.tracker.Create(ctx, tracker.CreateArgs{
		Title:       args.Title,
		Type:        "task",
		Labels:      []string{"follow-up", "deferred", args.SourceGate},
		Priority:    2, // medium priority
		Description: description,
		FindingOf:   args.FindingOf,
	})
	if err != nil {
		return "", fmt.Errorf("create follow-up ticket: %w", err)
	}

	return task.ID, nil
}