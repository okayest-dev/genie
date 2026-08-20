// Package shared provides protocol helpers for wire plugins.
package shared

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

const (
	MethodCapabilitiesList = "capabilities/list"
	MethodWireInit         = "wire/init"
	MethodWireStream       = "wire/stream"
	MethodWireListModels   = "wire/list_models"
	MethodPing             = "ping"
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
}

type Capabilities struct {
	Tools     bool `json:"tools"`
	Wires     bool `json:"wires"`
	Providers bool `json:"providers"`
	Version   int  `json:"version"`
}

type WireInitResult struct {
	OK bool `json:"ok"`
}

type ModelDef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type WireListModelsResult struct {
	Models []ModelDef `json:"models"`
}

type Handler struct {
	scanner    *bufio.Scanner
	writer     *json.Encoder
	caps       Capabilities
	models     []ModelDef
	onInit     func() error
	onStream  func(request json.RawMessage) (json.RawMessage, error)
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

func (h *Handler) Run() error {
	for h.scanner.Scan() {
		line := h.scanner.Text()
		if line == "" {
			continue
		}

		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			h.writeError(req.ID, -32700, "parse error")
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
				h.writeError(req.ID, -32603, err.Error())
				return
			}
		}
		h.writeResult(req.ID, WireInitResult{OK: true})
	case MethodWireListModels:
		h.writeResult(req.ID, WireListModelsResult{Models: h.models})
	case MethodWireStream:
		if h.onStream == nil {
			h.writeError(req.ID, -32601, "wire/stream not implemented")
			return
		}
		result, err := h.onStream(req.Params)
		if err != nil {
			h.writeError(req.ID, -32603, err.Error())
			return
		}
		h.writeRawResult(req.ID, result)
	case MethodPing:
		h.writeResult(req.ID, map[string]bool{"pong": true})
	case MethodShutdown:
		h.writeResult(req.ID, map[string]bool{"ok": true})
		os.Exit(0)
	default:
		h.writeError(req.ID, -32601, "method not found")
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
