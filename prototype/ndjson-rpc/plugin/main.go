package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id"`
}

type Response struct {
	JSONRPC string `json:"jsonrpc"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
	ID      any    `json:"id"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
	writer := bufio.NewWriter(os.Stdout)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			writeResponse(writer, &Response{
				JSONRPC: "2.0",
				Error:   &Error{Code: -32700, Message: "Parse error"},
				ID:      nil,
			})
			continue
		}

		resp := handle(req)
		writeResponse(writer, resp)
	}
}

func handle(req Request) *Response {
	base := &Response{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "capabilities/list":
		base.Result = map[string]bool{
			"tools":     true,
			"wires":     false,
			"providers": false,
		}

	case "tools/list":
		base.Result = map[string]any{
			"tools": []map[string]any{
				{
					"name":        "greet",
					"description": "A greeting tool",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{
								"type":        "string",
								"description": "The name to greet",
							},
						},
						"required": []string{"name"},
					},
				},
			},
		}

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			base.Error = &Error{Code: -32602, Message: "Invalid params"}
			return base
		}
		if params.Name != "greet" {
			base.Error = &Error{Code: -32602, Message: "Unknown tool: " + params.Name}
			return base
		}
		name, _ := params.Arguments["name"].(string)
		if name == "" {
			name = "World"
		}
		base.Result = map[string]any{
			"content": []map[string]string{
				{"type": "text", "text": fmt.Sprintf("Hello, %s!", name)},
			},
		}

	case "wire/init":
		base.Result = map[string]bool{"ok": true}

	default:
		base.Error = &Error{Code: -32601, Message: "Method not found"}
	}

	return base
}

func writeResponse(w *bufio.Writer, resp *Response) {
	data, _ := json.Marshal(resp)
	w.Write(data)
	w.WriteByte('\n')
	w.Flush()
}
