// Package shared provides protocol helpers for wire plugins.
package shared

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

const (
	MethodCapabilitiesList     = "capabilities/list"
	MethodToolsList            = "tools/list"
	MethodToolsCall            = "tools/call"
	MethodWireInit             = "wire/init"
	MethodWireStream           = "wire/stream"
	MethodWireListModels       = "wire/list_models"
	MethodContextBeforeRequest = "context/before_request"
	MethodContextAfterResponse = "context/after_response"
	MethodContextCompact       = "context/compact"
	MethodContextCondense      = "context/condense"
	MethodPing                 = "ping"
	MethodShutdown             = "shutdown"
)

// ProtocolVersion is the wire plugin protocol version.
const ProtocolVersion = 1

// Error codes.
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
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
}

type Capabilities struct {
	Tools     bool `json:"tools"`
	Wires     bool `json:"wires"`
	Providers bool `json:"providers"`
	BeforeRequest bool `json:"context_before"`
	AfterResponse bool `json:"context_after"`
	CompactHook   bool `json:"context_compact"`
	CondenseHook  bool `json:"context_condense"`
	Version       int  `json:"version"`
}

// HasAny reports whether the plugin declares at least one capability.
func (c *Capabilities) HasAny() bool {
	return c.Tools || c.Wires || c.Providers || c.BeforeRequest || c.AfterResponse || c.CompactHook || c.CondenseHook
}

type WireInitResult struct {
	OK bool `json:"ok"`
}

type ModelDef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// ContextWindow is the model's authoritative context window in tokens as
	// reported by the plugin's provider. Zero means unknown.
	ContextWindow int `json:"context_window,omitempty"`
}

type WireListModelsResult struct {
	Models []ModelDef `json:"models"`
}

// Context types for the context/* hook family.
type ContextMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	ToolCalls  []ContextToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

type ContextToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ContextRequest struct {
	Model    string           `json:"model"`
	Messages []ContextMessage `json:"messages"`
	Tools    []ToolDef        `json:"tools,omitempty"`
}

type ContextBeforeRequestResult struct {
	Request ContextRequest `json:"request"`
}

type ContextAfterResponseParams struct {
	Request ContextRequest `json:"request"`
	Usage   Usage          `json:"usage"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ContextAfterResponseResult struct {
	Usage Usage `json:"usage"`
}

type ContextCompactResult struct {
	Request ContextRequest `json:"request"`
}

type ContextCondenseResult struct {
	Request ContextRequest `json:"request"`
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Handler struct {
	scanner         *bufio.Scanner
	writer          *json.Encoder
	caps            Capabilities
	models          []ModelDef
	onInit          func() error
	onStream        func(request json.RawMessage) (json.RawMessage, error)
	onBeforeRequest func(request ContextRequest) (ContextRequest, error)
	onAfterResponse func(request ContextRequest, usage Usage) (Usage, error)
	onCompact       func(request ContextRequest) (ContextRequest, error)
	onCondense      func(request ContextRequest) (ContextRequest, error)
}

func NewHandler(caps Capabilities) *Handler {
	return &Handler{
		scanner: bufio.NewScanner(os.Stdin),
		writer:  json.NewEncoder(os.Stdout),
		caps:    caps,
	}
}

func (h *Handler) SetModels(models []ModelDef) {
	h.models = models
}

func (h *Handler) OnInit(fn func() error) {
	h.onInit = fn
}

func (h *Handler) OnStream(fn func(request json.RawMessage) (json.RawMessage, error)) {
	h.onStream = fn
}

func (h *Handler) OnBeforeRequest(fn func(request ContextRequest) (ContextRequest, error)) {
	h.onBeforeRequest = fn
}

func (h *Handler) OnAfterResponse(fn func(request ContextRequest, usage Usage) (Usage, error)) {
	h.onAfterResponse = fn
}

func (h *Handler) OnCompact(fn func(request ContextRequest) (ContextRequest, error)) {
	h.onCompact = fn
}

func (h *Handler) OnCondense(fn func(request ContextRequest) (ContextRequest, error)) {
	h.onCondense = fn
}

func (h *Handler) Run() error {
	for h.scanner.Scan() {
		line := h.scanner.Text()
		if line == "" {
			continue
		}

		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			h.writeError(req.ID, ParseError, "parse error")
			continue
		}

		h.handleRequest(&req)
	}
	return h.scanner.Err()
}

func (h *Handler) handleRequest(req *Request) {
	switch req.Method {
	case MethodCapabilitiesList:
		h.writeResult(req.ID, h.caps)
	case MethodWireInit:
		if h.onInit != nil {
			if err := h.onInit(); err != nil {
				h.writeError(req.ID, InternalError, err.Error())
				return
			}
		}
		h.writeResult(req.ID, WireInitResult{OK: true})
	case MethodWireListModels:
		h.writeResult(req.ID, WireListModelsResult{Models: h.models})
	case MethodWireStream:
		if h.onStream == nil {
			h.writeError(req.ID, MethodNotFound, "wire/stream not implemented")
			return
		}
		result, err := h.onStream(req.Params)
		if err != nil {
			h.writeError(req.ID, InternalError, err.Error())
			return
		}
		h.writeRawResult(req.ID, result)
	case MethodContextBeforeRequest:
		if h.onBeforeRequest == nil {
			h.writeError(req.ID, MethodNotFound, "context/before_request not implemented")
			return
		}
		params, err := ParseParams[ContextRequest](req.Params)
		if err != nil {
			h.writeError(req.ID, InvalidParams, err.Error())
			return
		}
		out, err := h.onBeforeRequest(params)
		if err != nil {
			h.writeError(req.ID, InternalError, err.Error())
			return
		}
		h.writeResult(req.ID, ContextBeforeRequestResult{Request: out})
	case MethodContextAfterResponse:
		if h.onAfterResponse == nil {
			h.writeError(req.ID, MethodNotFound, "context/after_response not implemented")
			return
		}
		params, err := ParseParams[ContextAfterResponseParams](req.Params)
		if err != nil {
			h.writeError(req.ID, InvalidParams, err.Error())
			return
		}
		out, err := h.onAfterResponse(params.Request, params.Usage)
		if err != nil {
			h.writeError(req.ID, InternalError, err.Error())
			return
		}
		h.writeResult(req.ID, ContextAfterResponseResult{Usage: out})
	case MethodContextCompact:
		if h.onCompact == nil {
			h.writeError(req.ID, MethodNotFound, "context/compact not implemented")
			return
		}
		params, err := ParseParams[ContextRequest](req.Params)
		if err != nil {
			h.writeError(req.ID, InvalidParams, err.Error())
			return
		}
		out, err := h.onCompact(params)
		if err != nil {
			h.writeError(req.ID, InternalError, err.Error())
			return
		}
		h.writeResult(req.ID, ContextCompactResult{Request: out})
	case MethodContextCondense:
		if h.onCondense == nil {
			h.writeError(req.ID, MethodNotFound, "context/condense not implemented")
			return
		}
		params, err := ParseParams[ContextRequest](req.Params)
		if err != nil {
			h.writeError(req.ID, InvalidParams, err.Error())
			return
		}
		out, err := h.onCondense(params)
		if err != nil {
			h.writeError(req.ID, InternalError, err.Error())
			return
		}
		h.writeResult(req.ID, ContextCondenseResult{Request: out})
	case MethodPing:
		h.writeResult(req.ID, map[string]bool{"pong": true})
	case MethodShutdown:
		h.writeResult(req.ID, map[string]bool{"ok": true})
		os.Exit(0)
	default:
		h.writeError(req.ID, MethodNotFound, "method not found")
	}
}

func (h *Handler) writeResult(id any, result any) {
	data, _ := json.Marshal(result)
	resp := Response{
		JSONRPC: "2.0",
		Result:  data,
		ID:      id,
	}
	h.writer.Encode(resp)
}

func (h *Handler) writeRawResult(id any, raw json.RawMessage) {
	resp := Response{
		JSONRPC: "2.0",
		Result:  raw,
		ID:      id,
	}
	h.writer.Encode(resp)
}

func (h *Handler) writeError(id any, code int, message string) {
	resp := Response{
		JSONRPC: "2.0",
		Error:   &Error{Code: code, Message: message},
		ID:      id,
	}
	h.writer.Encode(resp)
}

func ParseParams[T any](raw json.RawMessage) (T, error) {
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, fmt.Errorf("parse params: %w", err)
	}
	return result, nil
}
