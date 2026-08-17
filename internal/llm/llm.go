// Package llm is the seam between the agent loop and the provider: one small
// Client interface the loop depends on, a normalized event stream, and the
// error surface. v1 ships exactly one wire implementation (OpenAI-compatible
// chat/completions); later native wires are further implementations of the
// same interface with no loop rework.
package llm

import (
	"context"
	"iter"
)

// Client is the seam. A pull-iterator Stream normalizes provider output into
// events; open failures (auth, 404, network down) surface as the returned
// error, mid-stream failures as a terminal error event.
type Client interface {
	// Stream sends a chat request and returns an iterator of normalized
	// events. The caller stops iteration early (e.g. interrupt) by breaking
	// out of the range loop; the iterator then releases the connection.
	Stream(ctx context.Context, req Request) (iter.Seq[Event], error)

	// ListModels returns the provider's model catalog, normalized to IDs.
	ListModels(ctx context.Context) ([]Model, error)
}

// Request is a chat/completions-style request in canonical form.
type Request struct {
	Model    string
	Messages []Message
	Tools    []ToolDef
}

// ToolDef describes one tool the model may call. Parameters is the JSON
// Schema for the tool's arguments.
type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema object
}

// Message is one canonical conversation message.
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall // assistant message carrying tool calls
	ToolCallID string     // tool result message linking back to the call
}

// ToolCall is one function call the model requests.
type ToolCall struct {
	ID       string
	Name     string
	Arguments string // raw JSON arguments
}

// Roles in the canonical conversation.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// EventKind discriminates the payload of an Event.
type EventKind int

const (
	// EventText carries a text delta to be printed as it arrives.
	EventText EventKind = iota
	// EventFinish carries the canonical finish reason when the provider ends
	// the response.
	EventFinish
	// EventUsage is best-effort token accounting; providers may omit it.
	EventUsage
	// EventError is a terminal mid-stream failure.
	EventError
	// EventToolCall carries a completed tool call from the model.
	EventToolCall
)

// Event is one normalized step of a streamed response.
type Event struct {
	Kind      EventKind
	Text      string       // EventText
	Usage     Usage        // EventUsage
	End       FinishReason // EventFinish
	Err       error        // EventError
	ToolCalls []ToolCall   // EventToolCall
}

// FinishReason is the canonical end-of-response reason.
type FinishReason string

const (
	FinishStop      FinishReason = "stop"
	FinishToolCalls FinishReason = "tool_calls"
	FinishLength    FinishReason = "length"
	FinishOther     FinishReason = "other"
)

// Usage is best-effort token accounting.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ErrorKind classifies a provider failure.
type ErrorKind string

const (
	KindAuth           ErrorKind = "auth"
	KindRateLimit      ErrorKind = "rate_limit"
	KindInvalidRequest ErrorKind = "invalid_request"
	KindNetwork        ErrorKind = "network"
	KindTimeout        ErrorKind = "timeout"
	KindOther          ErrorKind = "other"
)

// ProviderError is the normalized error surface for the provider. Its
// message is safe to show a user; the harness never prints a stack trace.
type ProviderError struct {
	Kind       ErrorKind
	Message    string
	StatusCode int
}

func (e *ProviderError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}

// Model is one entry in a provider's catalog.
type Model struct {
	ID string
}
