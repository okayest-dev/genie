package tasktools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Disposition values accepted for a review finding. Every recorded finding must
// carry one so the review gate can hold the task to it.
const (
	DispositionResolveInline   = "resolve-inline"
	DispositionDeferWithTicket = "defer-with-ticket"
	DispositionWontfix         = "wontfix"
)

// TaskReviewFindingArgs describes a single finding reported by task_review.
type TaskReviewFindingArgs struct {
	FindingID      string `json:"finding_id"`
	Invariant      string `json:"invariant"`
	Category       string `json:"category"`
	Severity       string `json:"severity"`
	BlastRadius    int    `json:"blast_radius"`
	Recurrence     int    `json:"recurrence"`
	SourceFile     string `json:"source_file"`
	SourceEvidence string `json:"source_evidence"`
	Description    string `json:"description"`
	// Disposition is the triage decision: resolve-inline, defer-with-ticket, or wontfix.
	Disposition string `json:"disposition"`
	// Reason records why a finding was wontfixed (required for wontfix).
	Reason string `json:"reason"`
}

// TaskReviewArgs are the arguments for the task_review tool.
type TaskReviewArgs struct {
	// Subject describes what was reviewed (e.g. "diff main..HEAD"). Free text.
	Subject string `json:"subject"`
	// Method names how the review was performed (e.g. "code-review skill"). Free text.
	Method string `json:"method"`
	// HumanConfirmed marks that a human made the review decisions; required to
	// wontfix a security or hygiene finding.
	HumanConfirmed bool `json:"human_confirmed"`
	// Findings are the review's findings. Empty means a clean review.
	Findings []TaskReviewFindingArgs `json:"findings"`
}

// TaskReviewTool implements the task_review tool.
type TaskReviewTool struct {
	state   *TaskState
	enabled bool
	timeout time.Duration
}

// NewTaskReviewTool creates a new task_review tool.
func NewTaskReviewTool(state *TaskState, enabled bool, timeout time.Duration) *TaskReviewTool {
	return &TaskReviewTool{
		state:   state,
		enabled: enabled,
		timeout: timeout,
	}
}

// Name returns the tool name.
func (t *TaskReviewTool) Name() string {
	return "task_review"
}

// Description returns the tool description.
func (t *TaskReviewTool) Description() string {
	return "Attest that a review was performed for the active task and report its findings. " +
		"The harness does not define how the review is run; it records the findings and their triage decisions. " +
		"Each finding needs a disposition: resolve-inline (fixed here), defer-with-ticket (a follow-up ticket is created with provenance), " +
		"or wontfix (a reason is required; security/hygiene also needs human_confirmed). A review with no findings is recorded as-is. " +
		"The review gate refuses task_resolve until a review is recorded and every finding is dispositioned. Requires an active task."
}

// Parameters returns the JSON Schema for the tool arguments.
func (t *TaskReviewTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subject": map[string]any{
				"type":        "string",
				"description": "What was reviewed (e.g. \"diff main..HEAD\").",
			},
			"method": map[string]any{
				"type":        "string",
				"description": "How the review was performed (e.g. \"code-review skill\").",
			},
			"human_confirmed": map[string]any{
				"type":        "boolean",
				"description": "Set when a human made the review decisions; required to wontfix a security/hygiene finding.",
			},
			"findings": map[string]any{
				"type":        "array",
				"description": "Findings produced by the review. Omit or pass [] for a clean review.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"finding_id": map[string]any{
							"type":        "string",
							"description": "Unique identifier for this finding.",
						},
						"invariant": map[string]any{
							"type":        "string",
							"description": "The invariant that was violated.",
						},
						"category": map[string]any{
							"type":        "string",
							"description": "Category: security, hygiene, functional, style, architecture, other.",
							"enum":        []string{"security", "hygiene", "functional", "style", "architecture", "other"},
						},
						"severity": map[string]any{
							"type":        "string",
							"description": "Severity: P0 (critical), P1 (high), P2 (medium), P3 (low).",
							"enum":        []string{"P0", "P1", "P2", "P3"},
						},
						"blast_radius": map[string]any{
							"type":        "integer",
							"description": "Number of files affected.",
							"minimum":     0,
						},
						"recurrence": map[string]any{
							"type":        "integer",
							"description": "Number of times this issue has recurred.",
							"minimum":     0,
						},
						"source_file": map[string]any{
							"type":        "string",
							"description": "File the finding was observed in.",
						},
						"source_evidence": map[string]any{
							"type":        "string",
							"description": "Evidence supporting the finding.",
						},
						"description": map[string]any{
							"type":        "string",
							"description": "Human-readable description of the finding.",
						},
						"disposition": map[string]any{
							"type":        "string",
							"description": "Triage decision: resolve-inline, defer-with-ticket, or wontfix.",
							"enum":        []string{DispositionResolveInline, DispositionDeferWithTicket, DispositionWontfix},
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "Why the finding was wontfixed (required for wontfix).",
						},
					},
					"required": []any{"finding_id", "invariant", "category", "severity", "disposition"},
				},
			},
		},
		"required": []any{"subject"},
	}
}

var reviewValidSeverities = map[string]bool{"P0": true, "P1": true, "P2": true, "P3": true}
var reviewValidCategories = map[string]bool{"security": true, "hygiene": true, "functional": true, "style": true, "architecture": true, "other": true}

// Execute records a review attestation and its findings for the active task.
func (t *TaskReviewTool) Execute(raw json.RawMessage) (string, error) {
	if !t.enabled {
		return "", fmt.Errorf("task_review is disabled (task_tools not enabled in config)")
	}

	active := t.state.Get()
	if active == nil {
		return "", fmt.Errorf("no active task to record a review for")
	}

	var args TaskReviewArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if strings.TrimSpace(args.Subject) == "" {
		return "", fmt.Errorf("subject is required")
	}

	record := ReviewRecord{
		Subject:        args.Subject,
		Method:         args.Method,
		HumanConfirmed: args.HumanConfirmed,
		Findings:       make([]ReviewFindingRecord, 0, len(args.Findings)),
	}

	seen := map[string]bool{}
	for i, f := range args.Findings {
		if f.FindingID == "" {
			return "", fmt.Errorf("findings[%d]: finding_id is required", i)
		}
		if seen[f.FindingID] {
			return "", fmt.Errorf("findings[%d]: duplicate finding_id %q", i, f.FindingID)
		}
		seen[f.FindingID] = true
		if f.Invariant == "" {
			return "", fmt.Errorf("findings[%d]: invariant is required", i)
		}
		if !reviewValidSeverities[f.Severity] {
			return "", fmt.Errorf("findings[%d]: invalid severity %q (must be P0, P1, P2, or P3)", i, f.Severity)
		}
		if !reviewValidCategories[f.Category] {
			return "", fmt.Errorf("findings[%d]: invalid category %q (must be security, hygiene, functional, style, architecture, or other)", i, f.Category)
		}
		if err := validateDisposition(f.Disposition, f.Reason, f.Category, args.HumanConfirmed); err != nil {
			return "", fmt.Errorf("findings[%d]: %w", i, err)
		}

		record.Findings = append(record.Findings, ReviewFindingRecord{
			FindingID:      f.FindingID,
			Invariant:      f.Invariant,
			Category:       f.Category,
			Severity:       f.Severity,
			BlastRadius:    f.BlastRadius,
			Recurrence:     f.Recurrence,
			SourceFile:     f.SourceFile,
			SourceEvidence: f.SourceEvidence,
			Description:    f.Description,
			Disposition:    f.Disposition,
			Reason:         f.Reason,
		})
	}

	if err := t.state.AddReview(record); err != nil {
		return "", fmt.Errorf("failed to record review: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Review recorded for task %s:\n", active.Task.ID)
	fmt.Fprintf(&b, "  Subject: %s\n", record.Subject)
	if record.Method != "" {
		fmt.Fprintf(&b, "  Method: %s\n", record.Method)
	}
	if len(record.Findings) == 0 {
		b.WriteString("  Findings: none (clean review)\n")
	} else {
		fmt.Fprintf(&b, "  Findings: %d\n", len(record.Findings))
		for _, f := range record.Findings {
			fmt.Fprintf(&b, "    - %s [%s/%s] disposition=%s\n", f.FindingID, f.Category, f.Severity, f.Disposition)
		}
	}
	fmt.Fprintf(&b, "  Recorded at: %s", time.Now().Format(time.RFC3339))
	return b.String(), nil
}

// validateDisposition checks a disposition is known, that wontfix carries a
// reason, and that a security/hygiene wontfix was confirmed by a human.
func validateDisposition(disposition, reason, category string, humanConfirmed bool) error {
	switch disposition {
	case DispositionResolveInline, DispositionDeferWithTicket:
		return nil
	case DispositionWontfix:
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("wontfix requires a reason")
		}
		if (category == "security" || category == "hygiene") && !humanConfirmed {
			return fmt.Errorf("wontfix of a %s finding requires human confirmation (set human_confirmed=true)", category)
		}
		return nil
	case "":
		return fmt.Errorf("disposition is required (resolve-inline, defer-with-ticket, or wontfix)")
	default:
		return fmt.Errorf("invalid disposition %q (resolve-inline, defer-with-ticket, or wontfix)", disposition)
	}
}
