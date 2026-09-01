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

	"github.com/okayest-dev/genie/internal/llm"
)

// RoleCompaction marks a JSONL metadata line that records a durable-intent
// compaction: the line is not a message (it belongs to no context layer) and
// reconstructs the summary of the evicted turns on resume.
const RoleCompaction = "compaction"

// Session represents a persisted conversation session.
type Session struct {
	// ID is the unique session identifier.
	ID string
	// TranscriptPath is the path to the JSONL transcript file.
	TranscriptPath string
	// dir is the session directory.
	dir string
	// msgs holds the in-memory message mirror, growing as Append is called.
	msgs []llm.Message
}

// TranscriptLine is one line in the JSONL transcript.
type TranscriptLine struct {
	// Role is the message role (system, user, assistant, tool).
	Role string `json:"role"`
	// Content is the message content.
	Content string `json:"content"`
	// ToolCalls holds the tool calls made by an assistant message.
	ToolCalls []transcriptToolCall `json:"tool_calls,omitempty"`
	// ToolCallID links a tool result message back to its call.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// CompactionFrom bounds the start (inclusive) of the transcript lines a
	// compaction marker (RoleCompaction) summarised. Set on marker lines only;
	// message lines leave it zero.
	CompactionFrom int `json:"compacted_from,omitempty"`
	// CompactionTo bounds the end (inclusive) of the compacted line range. A
	// marker with To <= 0 carries no usable range and stays inert for assembly.
	CompactionTo int `json:"compacted_to,omitempty"`
	// Metadata carries optional key-value pairs (e.g. agent name on user turns).
	Metadata map[string]string `json:"metadata,omitempty"`
}

// transcriptToolCall is the JSONL representation of a tool call.
type transcriptToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message converts a message transcript line into the canonical llm.Message.
// It is only meaningful for message lines (role in system/user/assistant/tool);
// calling it on a marker line yields a message with a non-standard role.
func (t TranscriptLine) Message() llm.Message {
	msg := llm.Message{Role: t.Role, Content: t.Content}

	if len(t.ToolCalls) > 0 {
		msg.ToolCalls = make([]llm.ToolCall, len(t.ToolCalls))
		for i, tc := range t.ToolCalls {
			msg.ToolCalls[i] = llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}
		}
	}

	if t.ToolCallID != "" {
		msg.ToolCallID = t.ToolCallID
	}

	return msg
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
	return s.AppendWithMeta(msg, nil)
}

// AppendWithMeta adds a message with optional metadata to the transcript.
// The metadata is persisted in the JSONL but not carried in llm.Message.
func (s *Session) AppendWithMeta(msg llm.Message, meta map[string]string) error {
	line := TranscriptLine{
		Role:    msg.Role,
		Content: msg.Content,
	}

	if len(msg.ToolCalls) > 0 {
		line.ToolCalls = make([]transcriptToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			line.ToolCalls[i] = transcriptToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			}
		}
	}

	if msg.ToolCallID != "" {
		line.ToolCallID = msg.ToolCallID
	}

	if len(meta) > 0 {
		line.Metadata = meta
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

	s.msgs = append(s.msgs, msg)

	slog.Debug("message appended to transcript", "session", s.ID, "role", msg.Role)
	return nil
}

// AppendCompaction persists a durable-intent compaction marker to the
// transcript: a JSONL metadata line (not a message) recording that the
// transcript lines [from, to] were compacted into the given summary. The
// transcript stays append-only — the compacted lines themselves are never
// rewritten — so a resumed session recognises an already-compacted intent
// layer by reading this marker during the context-map build.
func (s *Session) AppendCompaction(summary string, from, to int) error {
	line := TranscriptLine{
		Role:            RoleCompaction,
		Content:         summary,
		CompactionFrom:  from,
		CompactionTo:    to,
	}
	data, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("session: marshal compaction marker: %w", err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(s.TranscriptPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("session: open transcript: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("session: write compaction marker: %w", err)
	}

	slog.Debug("compaction marker appended to transcript", "session", s.ID, "from", from, "to", to)
	return nil
}

// Load reconstructs the canonical conversation from the transcript file.
// Messages are returned in order, including tool call metadata.
func (s *Session) Load() ([]llm.Message, error) {
	lines, err := s.readLines()
	if err != nil {
		return nil, err
	}
	var messages []llm.Message
	for _, ln := range lines {
		messages = append(messages, ln.Message())
	}
	return messages, nil
}

// Lines returns the raw decoded transcript lines in order. Each line's slice
// index IS its stable session-line index, so callers can key by position
// across resume. Unlike Load, Lines exposes the full metadata surface and any
// marker lines, not just canonical messages.
func (s *Session) Lines() ([]TranscriptLine, error) {
	return s.readLines()
}

// readLines decodes every line of the transcript file in order. A missing or
// empty file yields a nil slice.
func (s *Session) readLines() ([]TranscriptLine, error) {
	data, err := os.ReadFile(s.TranscriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("session: read transcript: %w", err)
	}

	var lines []TranscriptLine
	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var line TranscriptLine
		if err := decoder.Decode(&line); err != nil {
			return nil, fmt.Errorf("session: decode transcript line: %w", err)
		}
		lines = append(lines, line)
	}

	return lines, nil
}

// History returns the in-memory message mirror. This is the ordered list of
// all messages appended to this session since creation (or since Load
// populated it). Unlike Load, it does not re-read the transcript file.
func (s *Session) History() []llm.Message {
	return s.msgs
}

// LoadInto loads the transcript from disk and populates the in-memory message
// mirror. Use this when resuming an existing session so that History returns
// the full conversation state.
func (s *Session) LoadInto() error {
	msgs, err := s.Load()
	if err != nil {
		return err
	}
	s.msgs = msgs
	return nil
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
