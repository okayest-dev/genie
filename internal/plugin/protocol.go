package plugin

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/okayest-dev/genie/internal/llm"
)

const (
	ProtocolVersion = 1

	MethodCapabilitiesList       = "capabilities/list"
	MethodToolsList              = "tools/list"
	MethodToolsCall              = "tools/call"
	MethodCommandsList           = "commands/list"
	MethodCommandsRun            = "commands/run"
	MethodCommandsHelp           = "commands/help"
	MethodWireInit               = "wire/init"
	MethodWireStream             = "wire/stream"
	MethodWireListModels         = "wire/list_models"
	MethodContextBeforeRequest   = "context/before_request"
	MethodContextAfterResponse   = "context/after_response"
	MethodContextCompact         = "context/compact"
	MethodContextCondense        = "context/condense"
	MethodLifecycleRequestBuilt  = "lifecycle/request_built"
	MethodLifecycleToolBefore    = "lifecycle/tool_before"
	MethodLifecycleToolAfter     = "lifecycle/tool_after"
	MethodLifecycleResponseReady = "lifecycle/response_ready"
	MethodLifecycleTurnError     = "lifecycle/turn_error"
	MethodPing                   = "ping"
	MethodShutdown               = "shutdown"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	ID      any             `json:"id"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

func NewErrorResponse(id any, code int, message string, data any) *Response {
	return &Response{
		JSONRPC: "2.0",
		Error:   &Error{Code: code, Message: message, Data: data},
		ID:      id,
	}
}

func NewSuccessResponse(id any, result any) (*Response, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &Response{
		JSONRPC: "2.0",
		Result:  data,
		ID:      id,
	}, nil
}

type Capabilities struct {
	Tools     bool `json:"tools"`
	Wires     bool `json:"wires"`
	Providers bool `json:"providers"`
	// Commands registers user-typed slash commands the plugin exposes in the
	// REPL as /<plugin> <command>, discovered via commands/list and driven via
	// commands/run.
	Commands bool `json:"commands"`
	// Context hooks the plugin can perform, declared granularly so a plugin
	// participates in context processing without faking unrelated seams.
	BeforeRequest bool `json:"context_before"`
	AfterResponse bool `json:"context_after"`
	CompactHook   bool `json:"context_compact"`
	CondenseHook  bool `json:"context_condense"`
	Version       int  `json:"version"`
	// Lifecycle hooks the plugin can perform, declared granularly per event so a
	// plugin participates in the agent loop only where it has something to say.
	LifecycleRequestBuilt  bool `json:"lifecycle_request_built"`
	LifecycleToolBefore    bool `json:"lifecycle_tool_before"`
	LifecycleToolAfter     bool `json:"lifecycle_tool_after"`
	LifecycleResponseReady bool `json:"lifecycle_response_ready"`
	LifecycleTurnError     bool `json:"lifecycle_turn_error"`
}

// HasAny reports whether the plugin declares at least one capability. A plugin
// that declares none is a protocol/validation error.
func (c *Capabilities) HasAny() bool {
	return c.Tools || c.Wires || c.Providers || c.Commands || c.BeforeRequest || c.AfterResponse || c.CompactHook || c.CondenseHook ||
		c.LifecycleRequestBuilt || c.LifecycleToolBefore || c.LifecycleToolAfter || c.LifecycleResponseReady || c.LifecycleTurnError
}

// PresenceMask is a bit-field of the capabilities present, so a peer can detect
// which seams a plugin participates in without re-negotiating the protocol.
type PresenceMask int

const (
	PresenceTools PresenceMask = 1 << iota
	PresenceWires
	PresenceProviders
	PresenceCommands
	PresenceBeforeRequest
	PresenceAfterResponse
	PresenceCompact
	PresenceCondense
	PresenceLifecycleRequestBuilt
	PresenceLifecycleToolBefore
	PresenceLifecycleToolAfter
	PresenceLifecycleResponseReady
	PresenceLifecycleTurnError
)

// Mask returns the capability presence bitmask for this declaration.
func (c *Capabilities) Mask() PresenceMask {
	var m PresenceMask
	if c.Tools {
		m |= PresenceTools
	}
	if c.Wires {
		m |= PresenceWires
	}
	if c.Providers {
		m |= PresenceProviders
	}
	if c.Commands {
		m |= PresenceCommands
	}
	if c.BeforeRequest {
		m |= PresenceBeforeRequest
	}
	if c.AfterResponse {
		m |= PresenceAfterResponse
	}
	if c.CompactHook {
		m |= PresenceCompact
	}
	if c.CondenseHook {
		m |= PresenceCondense
	}
	if c.LifecycleRequestBuilt {
		m |= PresenceLifecycleRequestBuilt
	}
	if c.LifecycleToolBefore {
		m |= PresenceLifecycleToolBefore
	}
	if c.LifecycleToolAfter {
		m |= PresenceLifecycleToolAfter
	}
	if c.LifecycleResponseReady {
		m |= PresenceLifecycleResponseReady
	}
	if c.LifecycleTurnError {
		m |= PresenceLifecycleTurnError
	}
	return m
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolsListResult struct {
	Tools []ToolDef `json:"tools"`
}

type ToolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ToolsCallResult struct {
	Content []ContentItem `json:"content"`
}

type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CommandDef is one slash command a wire plugin registers over the commands
// capability, addressed in the REPL as /<plugin> <name>. Usage is free-text,
// the contract for the command's arguments, not a structured schema.
type CommandDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Usage       string `json:"usage,omitempty"`
}

// CommandsListResult is the response to commands/list: the plugin's advertised
// commands, cached at load mirroring tools/list.
type CommandsListResult struct {
	Commands []CommandDef `json:"commands"`
}

// CommandsRunParams carries a command invocation; Arguments is the raw string
// the user typed after the command token, and the plugin owns its own
// sub-command parsing.
type CommandsRunParams struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// CommandsRunResult is the outcome of a command run: the REPL prints Text, or
// compact JSON of Data when Text is empty.
type CommandsRunResult struct {
	Text string `json:"text,omitempty"`
	Data any    `json:"data,omitempty"`
}

// CommandsHelpParams requests curated help for a plugin (Name omitted) or a
// single command; probed lazily, an absent method falls back to the flat
// commands/list listing.
type CommandsHelpParams struct {
	Name string `json:"name,omitempty"`
}

// CommandsHelpResult is the curated help text for the requested plugin/command.
type CommandsHelpResult struct {
	Text string `json:"text"`
}

type WireInitParams struct {
	Config map[string]any `json:"config"`
}

type WireInitResult struct {
	OK bool `json:"ok"`
}

type WireStreamParams struct {
	Request json.RawMessage `json:"request"`
}

type WireListModelsResult struct {
	Models []ModelDef `json:"models"`
}

// ContextMessage is the canonical message shape passed to and returned from a
// context hook. It mirrors llm.Message with explicit JSON tags so plugins have
// a stable wire schema to rewrite.
type ContextMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	ToolCalls  []ContextToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

// ContextToolCall is one function call carried by an assistant message.
type ContextToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ContextRequest is the request handed to a context hook. A before/compact/
// condense hook returns a (possibly rewritten) request; an after hook observes
// it and returns only a usage delta.
type ContextRequest struct {
	Model    string           `json:"model"`
	Messages []ContextMessage `json:"messages"`
	Tools    []ToolDef        `json:"tools,omitempty"`
}

// ContextBeforeRequestResult is the result of a before_request hook: the
// possibly-rewritten request to continue the chain/forward.
type ContextBeforeRequestResult struct {
	Request ContextRequest `json:"request"`
}

// ContextAfterResponseParams carries the observed completion data to an
// after_response hook.
type ContextAfterResponseParams struct {
	Request ContextRequest `json:"request"`
	Usage   Usage          `json:"usage"`
}

// Usage mirrors llm.Usage on the wire for after_response hooks.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ContextAfterResponseResult is the narrow usage/session delta an
// after_response hook reports; it never rewrites history.
type ContextAfterResponseResult struct {
	Usage Usage `json:"usage"`
}

// ContextCompactResult / ContextCondenseResult carry the rewritten history a
// single-active compact/condense implementation produces.
type ContextCompactResult struct {
	Request ContextRequest `json:"request"`
}

type ContextCondenseResult struct {
	Request ContextRequest `json:"request"`
}

// LifecycleRequestBuiltResult is the result of a request_built hook: the
// possibly-rewritten turn request plus the opt-in fatal escalation flag. A
// fatal result aborts the turn.
type LifecycleRequestBuiltResult struct {
	Request ContextRequest `json:"request"`
	Fatal   bool           `json:"fatal,omitempty"`
}

// LifecycleToolBeforeParams carries a tool call to the guardrail slot before it
// executes. The hook may rewrite arguments and/or suppress the call.
type LifecycleToolBeforeParams struct {
	Name      string `json:"name"`
	ID        string `json:"id"`
	Arguments string `json:"arguments"`
}

// LifecycleToolBeforeResult is the guardrail outcome: possibly-rewritten
// arguments, an optional suppression, and the opt-in fatal flag. An empty
// Arguments means "no change"; a hook that wants to wipe the arguments to the
// empty string sets SetEmpty, so the wipe is distinct from the no-change case.
// When SetEmpty is set the arguments are wiped to "" regardless of Arguments.
type LifecycleToolBeforeResult struct {
	Arguments string `json:"arguments"`
	Suppress  bool   `json:"suppress,omitempty"`
	SetEmpty  bool   `json:"set_empty,omitempty"`
	Fatal     bool   `json:"fatal,omitempty"`
}

// LifecycleToolAfterParams carries a completed tool call; err is a field, not a
// JSON-RPC error, so a failing call is still observable to every hook.
type LifecycleToolAfterParams struct {
	Name      string `json:"name"`
	ID        string `json:"id"`
	Arguments string `json:"arguments"`
	Result    string `json:"result"`
	Error     string `json:"error,omitempty"`
}

// LifecycleToolAfterResult lets a hook rewrite the result text and/or escalate
// to a turn-scoped abort.
type LifecycleToolAfterResult struct {
	Result string `json:"result"`
	Fatal  bool   `json:"fatal,omitempty"`
}

// LifecycleResponseReadyParams carries one response text delta; the final call
// carries Final=true plus the finish reason and usage observed at end-of-stream.
type LifecycleResponseReadyParams struct {
	Chunk        string `json:"chunk"`
	Final        bool   `json:"final,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
	Usage        Usage  `json:"usage,omitempty"`
}

// LifecycleResponseReadyResult lets a hook rewrite the delta and/or escalate.
type LifecycleResponseReadyResult struct {
	Chunk string `json:"chunk"`
	Fatal bool   `json:"fatal,omitempty"`
}

// LifecycleTurnErrorParams carries a hard turn error to the observe-only
// turn_error slot.
type LifecycleTurnErrorParams struct {
	Error string `json:"error"`
	Phase string `json:"phase"`
	// Partial is the assistant text accumulated before the failure.
	Partial string `json:"partial,omitempty"`
}

// LifecycleTurnErrorResult is observe-only; the fatal flag merely escalates the
// already-failing turn for downstream hooks/harness.
type LifecycleTurnErrorResult struct {
	Fatal bool `json:"fatal,omitempty"`
}

type ModelDef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// ContextWindow is the model's authoritative context window in tokens as
	// reported by the model-provider plugin. Zero means unknown.
	ContextWindow int `json:"context_window,omitempty"`
}

var (
	ErrInvalidJSONRPC       = errors.New("invalid JSON-RPC version")
	ErrMissingID            = errors.New("missing request ID")
	ErrMethodNotFound       = errors.New("method not found")
	ErrInvalidParams        = errors.New("invalid params")
	ErrInternalError        = errors.New("internal error")
	ErrProtocolVersion      = errors.New("unsupported protocol version")
	ErrCapabilitiesMismatch = errors.New("capabilities mismatch")
)

func ValidateRequest(req *Request) error {
	if req.JSONRPC != "2.0" {
		return ErrInvalidJSONRPC
	}
	if req.ID == nil {
		return ErrMissingID
	}
	return nil
}

func (c *Capabilities) Validate() error {
	if c.Version != ProtocolVersion {
		return fmt.Errorf("%w %d (genie supports protocol version %d; install a matching plugin release from the plugin repo's releases page)", ErrProtocolVersion, c.Version, ProtocolVersion)
	}
	if !c.HasAny() {
		return ErrCapabilitiesMismatch
	}
	return nil
}

func (e *Error) Error() string {
	if e.Data != nil {
		return e.Message + ": " + toString(e.Data)
	}
	return e.Message
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case error:
		return t.Error()
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// toContextRequest converts an llm.Request into the wire shape handed to a
// context hook.
func toContextRequest(req llm.Request) ContextRequest {
	out := ContextRequest{Model: req.Model}
	for _, td := range req.Tools {
		out.Tools = append(out.Tools, ToolDef{Name: td.Name, Description: td.Description, Parameters: td.Parameters})
	}
	out.Messages = make([]ContextMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		cm := ContextMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, ContextToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		out.Messages = append(out.Messages, cm)
	}
	return out
}

// fromContextRequest converts a hook-returned wire request back into an
// llm.Request.
func fromContextRequest(cr ContextRequest) llm.Request {
	out := llm.Request{Model: cr.Model}
	for _, td := range cr.Tools {
		out.Tools = append(out.Tools, llm.ToolDef{Name: td.Name, Description: td.Description, Parameters: td.Parameters})
	}
	out.Messages = make([]llm.Message, 0, len(cr.Messages))
	for _, cm := range cr.Messages {
		m := llm.Message{Role: cm.Role, Content: cm.Content, ToolCallID: cm.ToolCallID}
		for _, tc := range cm.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		out.Messages = append(out.Messages, m)
	}
	return out
}
