package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Request struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      int    `json:"id"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	ID      int             `json:"id"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var nextID = 0

func main() {
	pluginDir := "./plugins"
	if len(os.Args) > 1 {
		pluginDir = os.Args[1]
	}

	pluginPath := findPlugin(pluginDir)
	if pluginPath == "" {
		fmt.Fprintf(os.Stderr, "No plugin found in %s\n", pluginDir)
		os.Exit(1)
	}
	fmt.Printf("→ Found plugin: %s\n", pluginPath)

	cmd := exec.Command(pluginPath)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "stdin pipe: %v\n", err)
		os.Exit(1)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "stdout pipe: %v\n", err)
		os.Exit(1)
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start plugin: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("→ Plugin started (PID", cmd.Process.Pid, ")")

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
	writer := bufio.NewWriter(stdin)

	send := func(method string, params any) *Response {
		nextID++
		req := Request{JSONRPC: "2.0", Method: method, Params: params, ID: nextID}
		data, _ := json.Marshal(req)
		fmt.Printf("\n→ [%s] %s\n", method, data)
		writer.Write(data)
		writer.WriteByte('\n')
		writer.Flush()

		if !scanner.Scan() {
			fmt.Fprintf(os.Stderr, "← plugin closed pipe (err: %v)\n", scanner.Err())
			return nil
		}
		line := scanner.Bytes()
		fmt.Printf("← %s\n", line)

		var resp Response
		json.Unmarshal(line, &resp)
		return &resp
	}

	printResult := func(label string, resp *Response) {
		if resp == nil {
			fmt.Printf("  %s: (no response)\n", label)
			return
		}
		if resp.Error != nil {
			fmt.Printf("  %s: ERROR %d: %s\n", label, resp.Error.Code, resp.Error.Message)
			return
		}
		var pretty map[string]any
		json.Unmarshal(resp.Result, &pretty)
		out, _ := json.MarshalIndent(pretty, "  ", "  ")
		fmt.Printf("  %s:\n  %s\n", label, out)
	}

	// 1. capabilities/list
	r := send("capabilities/list", nil)
	printResult("capabilities", r)

	// 2. tools/list
	r = send("tools/list", nil)
	printResult("tools", r)

	// 3. tools/call
	r = send("tools/call", map[string]any{
		"name":      "greet",
		"arguments": map[string]string{"name": "Neovim"},
	})
	printResult("greet result", r)

	// 4. unknown method → error
	r = send("foo/bar", nil)
	printResult("unknown method", r)

	// Shutdown
	fmt.Println("\n→ Shutting down plugin...")
	stdin.Close()
	cmd.Wait()
	fmt.Println("→ Done.")
}

func findPlugin(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		full := filepath.Join(dir, e.Name())
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		if info.Mode()&0111 != 0 && !strings.HasSuffix(e.Name(), ".go") {
			return full
		}
	}
	return ""
}
