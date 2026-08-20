package plugin

import (
	"encoding/json"
	"errors"
)

const (
	ProtocolVersion = 1

	MethodCapabilitiesList = "capabilities/list"
	MethodToolsList        = "tools/list"
	MethodToolsCall        = "tools/call"
	MethodWireInit       = "wire/init"
	MethodWireStream     = "wire/stream"
	MethodWireListModels = "wire/list_models"
	MethodPing           = "ping"
	MethodShutdown         = "shutdown"
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
	Version   int  `json:"version"`
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

type ModelDef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

var (
	ErrInvalidJSONRPC    = errors.New("invalid JSON-RPC version")
	ErrMissingID         = errors.New("missing request ID")
	ErrMethodNotFound    = errors.New("method not found")
	ErrInvalidParams     = errors.New("invalid params")
	ErrInternalError     = errors.New("internal error")
	ErrProtocolVersion   = errors.New("unsupported protocol version")
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
		return ErrProtocolVersion
	}
	if !c.Tools && !c.Wires && !c.Providers {
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