package gates

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FindingSeverity represents the severity of a finding.
type FindingSeverity string

const (
	SeverityP0 FindingSeverity = "P0" // Critical - security, data loss, correctness
	SeverityP1 FindingSeverity = "P1" // High - significant functional issue
	SeverityP2 FindingSeverity = "P2" // Medium - functional but suboptimal
	SeverityP3 FindingSeverity = "P3" // Low - cosmetic, style, out-of-scope
)

// FindingDisposition represents the triage decision for a finding.
type FindingDisposition string

const (
	DispositionResolveInline  FindingDisposition = "resolve-inline"
	DispositionDeferWithTicket FindingDisposition = "defer-with-ticket"
	DispositionWontfix        FindingDisposition = "wontfix"
)

// FindingCategory categorizes the type of finding.
type FindingCategory string

const (
	CategorySecurity  FindingCategory = "security"
	CategoryHygiene   FindingCategory = "hygiene"
	CategoryFunctional FindingCategory = "functional"
	CategoryStyle     FindingCategory = "style"
	CategoryArchitecture FindingCategory = "architecture"
	CategoryOther     FindingCategory = "other"
)

// Finding represents a review gate finding.
type Finding struct {
	ID                string            `json:"id"`
	InvariantViolated string            `json:"invariant_violated"`
	Category          FindingCategory   `json:"category"`
	Severity          FindingSeverity   `json:"severity"`
	BlastRadiusFiles  int               `json:"blast_radius_files"`
	RecurrenceCount   int               `json:"recurrence_count"`
	SourceTaskID      string            `json:"source_task_id"`
	SourceFile        string            `json:"source_file"`
	SourceEvidence    string            `json:"source_evidence"`
	Disposition       FindingDisposition `json:"disposition,omitempty"`
	DispositionReason string            `json:"disposition_reason,omitempty"`
	RelatedTicketID   string            `json:"related_ticket_id,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	ResolvedAt        *time.Time        `json:"resolved_at,omitempty"`
}

// FindingSet is a collection of findings from a review.
type FindingSet struct {
	Findings []Finding `json:"findings"`
}

// TriageRequest is a request to triage a set of findings.
type TriageRequest struct {
	TaskID     string       `json:"task_id"`
	Findings   []Finding    `json:"findings"`
	HumanInput bool         `json:"human_input"` // true if human is making the decision
}

// TriageResult is the result of triaging findings.
type TriageResult struct {
	Decisions []FindingDecision `json:"decisions"`
	CreatedTickets []string     `json:"created_tickets,omitempty"`
}

// FindingDecision represents a triage decision for a single finding.
type FindingDecision struct {
	FindingID  string               `json:"finding_id"`
	Disposition FindingDisposition  `json:"disposition"`
	Reason     string               `json:"reason"`
	TicketID   string               `json:"ticket_id,omitempty"`
}

// IsSecurityOrHygiene returns true if the finding is security or hygiene related.
func (f *Finding) IsSecurityOrHygiene() bool {
	return f.Category == CategorySecurity || f.Category == CategoryHygiene
}

// ValidateDisposition checks if a disposition is valid for the finding.
func (f *Finding) ValidateDisposition(disposition FindingDisposition, humanInput bool) error {
	if disposition == DispositionWontfix && f.IsSecurityOrHygiene() && !humanInput {
		return fmt.Errorf("security/hygiene findings require human confirmation for wontfix")
	}
	return nil
}

// MarshalJSON implements custom JSON serialization for Finding.
func (f Finding) MarshalJSON() ([]byte, error) {
	type Alias Finding
	return json.Marshal(&struct {
		Alias
		CreatedAt string `json:"created_at"`
		ResolvedAt string `json:"resolved_at,omitempty"`
	}{
		Alias:     Alias(f),
		CreatedAt: f.CreatedAt.Format(time.RFC3339),
		ResolvedAt: func() string {
			if f.ResolvedAt != nil {
				return f.ResolvedAt.Format(time.RFC3339)
			}
			return ""
		}(),
	})
}

// UnmarshalJSON implements custom JSON deserialization for Finding.
func (f *Finding) UnmarshalJSON(data []byte) error {
	type Alias Finding
	aux := &struct {
		Alias
		CreatedAt string `json:"created_at"`
		ResolvedAt string `json:"resolved_at,omitempty"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*f = Finding(aux.Alias)
	if t, err := time.Parse(time.RFC3339, aux.CreatedAt); err == nil {
		f.CreatedAt = t
	}
	if aux.ResolvedAt != "" {
		if t, err := time.Parse(time.RFC3339, aux.ResolvedAt); err == nil {
			f.ResolvedAt = &t
		}
	}
	return nil
}

// TriageEngine implements the triage rules for findings.
type TriageEngine struct {
	humanInputRequired bool
}

// NewTriageEngine creates a new triage engine.
func NewTriageEngine(humanInputRequired bool) *TriageEngine {
	return &TriageEngine{
		humanInputRequired: humanInputRequired,
	}
}

// Triage evaluates findings and returns triage decisions.
func (e *TriageEngine) Triage(req TriageRequest) (TriageResult, error) {
	result := TriageResult{
		Decisions: make([]FindingDecision, 0, len(req.Findings)),
	}

	for _, finding := range req.Findings {
		decision, err := e.triageFinding(finding, req.HumanInput)
		if err != nil {
			return TriageResult{}, fmt.Errorf("triage finding %s: %w", finding.ID, err)
		}
		result.Decisions = append(result.Decisions, decision)
	}

	return result, nil
}

// triageFinding applies the triage rules to a single finding.
func (e *TriageEngine) triageFinding(finding Finding, humanInput bool) (FindingDecision, error) {
	decision := FindingDecision{
		FindingID:  finding.ID,
		Disposition: finding.Disposition,
		Reason:     finding.DispositionReason,
	}

	// If no disposition provided, determine it
	if decision.Disposition == "" {
		decision.Disposition = e.defaultDisposition(finding)
		decision.Reason = e.defaultReason(finding, decision.Disposition)
	}

	// Validate the disposition
	if err := finding.ValidateDisposition(decision.Disposition, humanInput); err != nil {
		return FindingDecision{}, err
	}

	return decision, nil
}

// defaultDisposition determines the default disposition based on finding properties.
func (e *TriageEngine) defaultDisposition(finding Finding) FindingDisposition {
	// Security/hygiene findings: prefer defer with ticket unless clearly out of scope
	if finding.Category == CategorySecurity || finding.Category == CategoryHygiene {
		if finding.Severity <= SeverityP1 && finding.BlastRadiusFiles > 0 {
			return DispositionDeferWithTicket
		}
		if finding.Severity == SeverityP3 && finding.BlastRadiusFiles == 0 {
			// Low severity security/hygiene might be acceptable to wontfix with human
			return DispositionWontfix
		}
		return DispositionDeferWithTicket
	}

	// Functional findings: P0/P1 defer, P2 resolve inline if small, P3 wontfix
	switch finding.Severity {
	case SeverityP0, SeverityP1:
		return DispositionDeferWithTicket
	case SeverityP2:
		if finding.BlastRadiusFiles <= 3 && finding.RecurrenceCount == 0 {
			return DispositionResolveInline
		}
		return DispositionDeferWithTicket
	case SeverityP3:
		return DispositionWontfix
	default:
		return DispositionDeferWithTicket
	}
}

// defaultReason provides a default reason for the disposition.
func (e *TriageEngine) defaultReason(finding Finding, disposition FindingDisposition) string {
	switch disposition {
	case DispositionResolveInline:
		return fmt.Sprintf("Small, in-scope fix (severity=%s, blast_radius=%d files, recurrence=%d)", finding.Severity, finding.BlastRadiusFiles, finding.RecurrenceCount)
	case DispositionDeferWithTicket:
		return fmt.Sprintf("Systemic or out-of-scope for this task (severity=%s, blast_radius=%d files, recurrence=%d, category=%s)", finding.Severity, finding.BlastRadiusFiles, finding.RecurrenceCount, finding.Category)
	case DispositionWontfix:
		return fmt.Sprintf("Consciously ruled out: %s (severity=%s, category=%s)", finding.DispositionReason, finding.Severity, finding.Category)
	default:
		return "No reason provided"
	}
}

// FindingFilter helps filter findings by various criteria.
type FindingFilter struct {
	Category     []FindingCategory
	Severity     []FindingSeverity
	Disposition  []FindingDisposition
	MinBlastRadius int
	MaxBlastRadius int
	MinRecurrence  int
	MaxRecurrence  int
	SourceTaskID string
	SourceFile   string
}

// Filter applies the filter to a list of findings.
func (f *FindingFilter) Filter(findings []Finding) []Finding {
	var result []Finding
	for _, finding := range findings {
		if f.matches(finding) {
			result = append(result, finding)
		}
	}
	return result
}

func (f *FindingFilter) matches(finding Finding) bool {
	if len(f.Category) > 0 && !contains(f.Category, finding.Category) {
		return false
	}
	if len(f.Severity) > 0 && !contains(f.Severity, finding.Severity) {
		return false
	}
	if len(f.Disposition) > 0 && !contains(f.Disposition, finding.Disposition) {
		return false
	}
	if f.MinBlastRadius > 0 && finding.BlastRadiusFiles < f.MinBlastRadius {
		return false
	}
	if f.MaxBlastRadius > 0 && finding.BlastRadiusFiles > f.MaxBlastRadius {
		return false
	}
	if f.MinRecurrence > 0 && finding.RecurrenceCount < f.MinRecurrence {
		return false
	}
	if f.MaxRecurrence > 0 && finding.RecurrenceCount > f.MaxRecurrence {
		return false
	}
	if f.SourceTaskID != "" && finding.SourceTaskID != f.SourceTaskID {
		return false
	}
	if f.SourceFile != "" && finding.SourceFile != f.SourceFile {
		return false
	}
	return true
}

func contains[T comparable](slice []T, item T) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ReviewGateFindings extracts findings from review tool output.
func ReviewGateFindings(output string) ([]Finding, error) {
	// In a real implementation, this would parse structured review output
	// For now, we return a placeholder that the review tool can populate
	var findings []Finding
	
	// Parse any structured finding markers in the output
	// Example format: FINDING: <json>
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "FINDING:") {
			findingJSON := strings.TrimPrefix(line, "FINDING:")
			findingJSON = strings.TrimSpace(findingJSON)
			var finding Finding
			if err := json.Unmarshal([]byte(findingJSON), &finding); err == nil {
				findings = append(findings, finding)
			}
		}
	}
	
	return findings, nil
}