package tasktools

import (
	"context"
	"fmt"
	"strings"

	"github.com/okayest-dev/genie/internal/tracker"
)

// FollowupMetrics tracks metrics for follow-up ticket creation.
type FollowupMetrics struct {
	TicketsCreated int
	TicketsDeduped int
	TicketsWontfix int
}

// FollowupEngine handles follow-up ticket creation with dedup guard and priority shaping.
type FollowupEngine struct {
	tracker       tracker.TrackerSource
	config        FollowupConfig
	metrics       *FollowupMetrics
}

// NewFollowupEngine creates a new follow-up engine.
func NewFollowupEngine(tracker tracker.TrackerSource, config FollowupConfig, metrics *FollowupMetrics) *FollowupEngine {
	if metrics == nil {
		metrics = &FollowupMetrics{}
	}
	return &FollowupEngine{
		tracker: tracker,
		config:  config,
		metrics: metrics,
	}
}

// FindingInput represents a finding that needs a follow-up ticket.
type FindingInput struct {
	InvariantViolated string
	Category          string
	Severity          string
	BlastRadiusFiles  int
	RecurrenceCount   int
	SourceTaskID      string
	SourceFile        string
	SourceEvidence    string
	Description       string
}

// FollowupResult is the result of processing a finding for follow-up.
type FollowupResult struct {
	Action        string // "created", "deduped", "wontfix"
	TicketID      string
	TicketTitle   string
	Priority      int
	Reason        string
	DedupMatch    *tracker.DedupMatch
	IsSecurity    bool
}

// ProcessFinding processes a finding and creates a follow-up ticket if needed.
// Returns the result of the operation.
func (e *FollowupEngine) ProcessFinding(ctx context.Context, finding FindingInput) (FollowupResult, error) {
	// Check for auto-wontfix rules
	if e.shouldAutoWontfix(finding) {
		e.metrics.TicketsWontfix++
		return FollowupResult{
			Action:     "wontfix",
			Reason:     fmt.Sprintf("Auto-wontfix: matches rule for category=%s severity=%s", finding.Category, finding.Severity),
			IsSecurity: finding.Category == "security" || finding.Category == "hygiene",
		}, nil
	}

	// Search for similar open tickets (dedup guard)
	matches, err := e.searchSimilarOpen(ctx, finding)
	if err != nil {
		return FollowupResult{}, fmt.Errorf("search similar open tickets: %w", err)
	}

	// Check for near-duplicate
	for _, match := range matches {
		similarity := e.calculateSimilarity(finding, match)
		if similarity >= e.config.DedupSimilarityThreshold {
			// Near-duplicate found - absorb as comment
			comment := fmt.Sprintf("**Absorbed finding** (similarity: %.0f%%):\n%s\n\nInvariant: %s\nBlast radius: %d files\nRecurrence: %d",
				similarity*100, finding.Description, finding.InvariantViolated, finding.BlastRadiusFiles, finding.RecurrenceCount)
			if err := e.tracker.Comment(ctx, match.ID, comment); err != nil {
				return FollowupResult{}, fmt.Errorf("add comment to existing ticket: %w", err)
			}
			e.metrics.TicketsDeduped++
			return FollowupResult{
				Action:     "deduped",
				TicketID:   match.ID,
				TicketTitle: match.Title,
				Reason:     fmt.Sprintf("Absorbed into existing ticket %s (similarity: %.0f%%)", match.ID, similarity*100),
				DedupMatch: &tracker.DedupMatch{Task: match, Similarity: similarity},
				IsSecurity: finding.Category == "security" || finding.Category == "hygiene",
			}, nil
		}
	}

	// Calculate priority based on blast radius + recurrence
	priority := e.calculatePriority(finding)

	// Create the ticket
	title := e.generateTitle(finding)
	description := e.generateDescription(finding)

	task, err := e.tracker.Create(ctx, tracker.CreateArgs{
		Title:       title,
		Type:        "task",
		Labels:      []string{"followup", finding.Category},
		Priority:    priority,
		Description: description,
		FindingOf:   finding.SourceTaskID,
	})
	if err != nil {
		return FollowupResult{}, fmt.Errorf("create follow-up ticket: %w", err)
	}

	e.metrics.TicketsCreated++
	return FollowupResult{
		Action:     "created",
		TicketID:   task.ID,
		TicketTitle: task.Title,
		Priority:   priority,
		Reason:     fmt.Sprintf("Created follow-up ticket with priority %d (blast_radius=%d, recurrence=%d)", priority, finding.BlastRadiusFiles, finding.RecurrenceCount),
		IsSecurity: finding.Category == "security" || finding.Category == "hygiene",
	}, nil
}

// shouldAutoWontfix checks if the finding matches any auto-wontfix rules.
func (e *FollowupEngine) shouldAutoWontfix(finding FindingInput) bool {
	for _, rule := range e.config.AutoWontfixRules {
		// Rule format: "category:severity" or "category" or "severity"
		parts := strings.Split(rule, ":")
		if len(parts) == 2 {
			if (parts[0] == "*" || parts[0] == finding.Category) && (parts[1] == "*" || parts[1] == finding.Severity) {
				return true
			}
		} else if len(parts) == 1 {
			if parts[0] == "*" || parts[0] == finding.Category || parts[0] == finding.Severity {
				return true
			}
		}
	}
	return false
}

// searchSimilarOpen searches for similar open tickets based on the finding.
func (e *FollowupEngine) searchSimilarOpen(ctx context.Context, finding FindingInput) ([]*tracker.TrackerTask, error) {
	// Build search query from invariant and category
	query := finding.InvariantViolated
	if finding.Category != "" {
		query += " " + finding.Category
	}
	return e.tracker.SearchOpen(ctx, query, 20)
}

// calculatePriority calculates the priority based on blast radius and recurrence.
// Priority mapping (per og-hd8):
// - Security/data-integrity flavour → P0 (priority 0) regardless
// - (recurrence >= 2 OR blast_radius_files > 10) AND NOT security → P1 (priority 1)
// - Singular, local → P2/P3 backlog (priority 2 or 3)
func (e *FollowupEngine) calculatePriority(finding FindingInput) int {
	isSecurity := finding.Category == "security" || finding.Category == "hygiene"

	// Security/data-integrity → P0 regardless
	if isSecurity {
		return e.config.PrioritySecurity
	}

	// (recurrence >= threshold OR blast_radius > threshold) AND NOT security → P1
	if finding.RecurrenceCount >= e.config.PriorityRecurrenceThreshold || finding.BlastRadiusFiles > e.config.PriorityBlastRadiusThreshold {
		return 1 // P1
	}

	// Singular, local → P2/P3 backlog
	if finding.Severity == "P3" {
		return 3 // P3
	}
	return 2 // P2
}

// calculateSimilarity calculates a simple similarity score between a finding and an existing task.
func (e *FollowupEngine) calculateSimilarity(finding FindingInput, task *tracker.TrackerTask) float64 {
	// Simple similarity based on keyword overlap in title and description
	findingText := strings.ToLower(finding.InvariantViolated + " " + finding.Description + " " + finding.Category)
	taskText := strings.ToLower(task.Title + " " + task.Description)

	findingWords := strings.Fields(findingText)
	taskWords := strings.Fields(taskText)

	if len(findingWords) == 0 || len(taskWords) == 0 {
		return 0
	}

	// Count common words
	wordSet := make(map[string]bool)
	for _, w := range taskWords {
		wordSet[w] = true
	}

	common := 0
	for _, w := range findingWords {
		if wordSet[w] {
			common++
		}
	}

	// Jaccard-like similarity
	union := len(findingWords) + len(taskWords) - common
	if union == 0 {
		return 0
	}
	return float64(common) / float64(union)
}

// generateTitle generates a ticket title from the finding.
func (e *FollowupEngine) generateTitle(finding FindingInput) string {
	// Format: "[category] invariant_violated"
	return fmt.Sprintf("[%s] %s", strings.Title(finding.Category), finding.InvariantViolated)
}

// generateDescription generates a ticket description from the finding.
func (e *FollowupEngine) generateDescription(finding FindingInput) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("**Invariant Violated:** %s", finding.InvariantViolated))
	parts = append(parts, fmt.Sprintf("**Category:** %s", finding.Category))
	parts = append(parts, fmt.Sprintf("**Severity:** %s", finding.Severity))
	parts = append(parts, fmt.Sprintf("**Blast Radius:** %d files", finding.BlastRadiusFiles))
	parts = append(parts, fmt.Sprintf("**Recurrence Count:** %d", finding.RecurrenceCount))
	if finding.SourceFile != "" {
		parts = append(parts, fmt.Sprintf("**Source File:** %s", finding.SourceFile))
	}
	if finding.SourceEvidence != "" {
		parts = append(parts, fmt.Sprintf("**Evidence:** %s", finding.SourceEvidence))
	}
	parts = append(parts, fmt.Sprintf("**Description:** %s", finding.Description))
	parts = append(parts, fmt.Sprintf("**Source Task:** %s", finding.SourceTaskID))
	return strings.Join(parts, "\n\n")
}

// GetMetrics returns the current metrics.
func (e *FollowupEngine) GetMetrics() FollowupMetrics {
	return *e.metrics
}