// Package session manages JSONL session transcripts. A session has a unique
// id and a transcript file where the conversation is stored as JSONL. The
// transcript reconstructs the canonical conversation in order so a session
// is resumable.
package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/okayest-dev/og/internal/llm"
)

// Session represents a persisted conversation session.
type Session struct {
	// ID is the unique session identifier.
	ID string
	// TranscriptPath is the path to the JSONL transcript file.
	TranscriptPath string
	// dir is the session directory.
	dir string
}

// TranscriptLine is one line in the JSONL transcript.
type TranscriptLine struct {
	// Role is the message role (system, user, assistant).
	Role string `json:"role"`
	// Content is the message content.
	Content string `json:"content"`
}

// New creates a new session in the given directory. It creates the directory
// if it doesn't exist and generates a unique session id. The transcript file
// is created on the first append.
func New(dir string) (*Session, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session: create dir: %w", err)
	}

	id := generateID()
	path := filepath.Join(dir, id+".jsonl")

	s := &Session{
		ID:             id,
		TranscriptPath: path,
		dir:            dir,
	}

	slog.Info("session created", "id", id, "path", path)
	return s, nil
}

// Append adds a message to the session transcript.
func (s *Session) Append(msg llm.Message) error {
	line := TranscriptLine{
		Role:    msg.Role,
		Content: msg.Content,
	}

	data, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("session: marshal message: %w", err)
	}

	data = append(data, '\n')

	f, err := os.OpenFile(s.TranscriptPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("session: open transcript: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("session: write to transcript: %w", err)
	}

	slog.Debug("message appended to transcript", "session", s.ID, "role", msg.Role)
	return nil
}

// Load reconstructs the canonical conversation from the transcript file.
// Messages are returned in order.
func (s *Session) Load() ([]llm.Message, error) {
	data, err := os.ReadFile(s.TranscriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: read transcript: %w", err)
	}

	var messages []llm.Message
	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var line TranscriptLine
		if err := decoder.Decode(&line); err != nil {
			return nil, fmt.Errorf("session: decode transcript line: %w", err)
		}
		messages = append(messages, llm.Message{
			Role:    line.Role,
			Content: line.Content,
		})
	}

	return messages, nil
}

// generateID creates a unique session id based on timestamp and random suffix.
func generateID() string {
	return time.Now().Format("20060102-150405") + "-" + randomSuffix()
}

// randomSuffix returns a short random string for session id uniqueness.
func randomSuffix() string {
	b := make([]byte, 4)
	// Use crypto/rand for uniqueness
	if _, err := randomRead(b); err != nil {
		// Fallback to timestamp-based approach
		return fmt.Sprintf("%x", time.Now().UnixNano()%0xFFFFFFFF)
	}
	return fmt.Sprintf("%x", b)
}

// randomRead fills b with cryptographically secure random bytes.
// This is a thin wrapper to make the function testable.
var randomRead = cryptRead

func cryptRead(b []byte) (int, error) {
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Read(b)
}
