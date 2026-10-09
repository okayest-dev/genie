package gates

import (
	"context"
	"sync"
)

// InMemoryDeferredFindingStore is an in-memory implementation of DeferredFindingStore.
type InMemoryDeferredFindingStore struct {
	mu       sync.Mutex
	findings map[string][]DeferredFinding // gate name -> findings
}

// NewInMemoryDeferredFindingStore creates a new in-memory deferred finding store.
func NewInMemoryDeferredFindingStore() *InMemoryDeferredFindingStore {
	return &InMemoryDeferredFindingStore{
		findings: make(map[string][]DeferredFinding),
	}
}

// RecordDeferredFinding records a deferred finding.
func (s *InMemoryDeferredFindingStore) RecordDeferredFinding(ctx context.Context, finding DeferredFinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.findings[finding.GateName] = append(s.findings[finding.GateName], finding)
	return nil
}

// GetDeferredFindings returns deferred findings for a specific gate.
func (s *InMemoryDeferredFindingStore) GetDeferredFindings(ctx context.Context, gateName string) ([]DeferredFinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	findings := s.findings[gateName]
	// Return a copy to avoid race conditions
	result := make([]DeferredFinding, len(findings))
	copy(result, findings)
	return result, nil
}

// GetAllDeferredFindings returns all deferred findings.
func (s *InMemoryDeferredFindingStore) GetAllDeferredFindings(ctx context.Context) ([]DeferredFinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []DeferredFinding
	for _, findings := range s.findings {
		all = append(all, findings...)
	}
	return all, nil
}