package gates

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileEvidenceLedger is a file-backed evidence ledger.
type FileEvidenceLedger struct {
	mu       sync.Mutex
	basePath string
}

// NewFileEvidenceLedger creates a new file-backed evidence ledger.
func NewFileEvidenceLedger(basePath string) (*FileEvidenceLedger, error) {
	if basePath == "" {
		basePath = ".genie/gates"
	}
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("create gates directory: %w", err)
	}
	return &FileEvidenceLedger{basePath: basePath}, nil
}

func (l *FileEvidenceLedger) ledgerPath(taskID string) string {
	return filepath.Join(l.basePath, taskID+".jsonl")
}

func (l *FileEvidenceLedger) Append(ctx context.Context, ev Evidence) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal evidence: %w", err)
	}

	f, err := os.OpenFile(l.ledgerPath(ev.TaskID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open ledger: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write evidence: %w", err)
	}
	return nil
}

func (l *FileEvidenceLedger) Get(ctx context.Context, taskID string) ([]Evidence, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	path := l.ledgerPath(taskID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Evidence{}, nil
		}
		return nil, fmt.Errorf("read ledger: %w", err)
	}

	if len(data) == 0 {
		return []Evidence{}, nil
	}

	var evidence []Evidence
	lines := splitLines(data)
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var ev Evidence
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // skip malformed lines
		}
		evidence = append(evidence, ev)
	}
	return evidence, nil
}

func (l *FileEvidenceLedger) GetSince(ctx context.Context, taskID string, since time.Time) ([]Evidence, error) {
	all, err := l.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var result []Evidence
	for _, ev := range all {
		if ev.Timestamp.After(since) {
			result = append(result, ev)
		}
	}
	return result, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// InMemoryEvidenceLedger is an in-memory evidence ledger for testing.
type InMemoryEvidenceLedger struct {
	mu       sync.Mutex
	evidence map[string][]Evidence
}

func NewInMemoryEvidenceLedger() *InMemoryEvidenceLedger {
	return &InMemoryEvidenceLedger{
		evidence: make(map[string][]Evidence),
	}
}

func (l *InMemoryEvidenceLedger) Append(ctx context.Context, ev Evidence) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.evidence[ev.TaskID] = append(l.evidence[ev.TaskID], ev)
	return nil
}

func (l *InMemoryEvidenceLedger) Get(ctx context.Context, taskID string) ([]Evidence, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	evs := l.evidence[taskID]
	if evs == nil {
		return []Evidence{}, nil
	}
	result := make([]Evidence, len(evs))
	copy(result, evs)
	return result, nil
}

func (l *InMemoryEvidenceLedger) GetSince(ctx context.Context, taskID string, since time.Time) ([]Evidence, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	evs := l.evidence[taskID]
	if evs == nil {
		return []Evidence{}, nil
	}
	var result []Evidence
	for _, ev := range evs {
		if ev.Timestamp.After(since) {
			result = append(result, ev)
		}
	}
	return result, nil
}