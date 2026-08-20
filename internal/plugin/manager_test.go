package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/okayest-dev/og/internal/tools"
)

func TestParseManifest(t *testing.T) {
	tmpDir := t.TempDir()

	// Test valid manifest
	manifestContent := `
name = "test-plugin"
version = "1.0.0"
capabilities = ["tools", "wires"]
`
	manifestPath := filepath.Join(tmpDir, "test-plugin.toml")
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseManifest(tmpDir, "test-plugin")
	if err != nil {
		t.Fatalf("ParseManifest failed: %v", err)
	}
	if m.Name != "test-plugin" {
		t.Errorf("expected name 'test-plugin', got %q", m.Name)
	}
	if m.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", m.Version)
	}
	if !m.HasCapability("tools") || !m.HasCapability("wires") {
		t.Error("expected capabilities tools and wires")
	}

	// Test missing manifest
	_, err = ParseManifest(tmpDir, "nonexistent")
	if err != nil {
		t.Errorf("expected nil for missing manifest, got %v", err)
	}

	// Test invalid manifest (missing name)
	invalidContent := `
version = "1.0.0"
capabilities = ["tools"]
`
	invalidPath := filepath.Join(tmpDir, "invalid.toml")
	if err := os.WriteFile(invalidPath, []byte(invalidContent), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = ParseManifest(tmpDir, "invalid")
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestManifestValidate(t *testing.T) {
	m := &Manifest{
		Name:         "test",
		Version:      "1.0.0",
		Capabilities: []string{"tools", "wires", "providers"},
	}
	if err := m.Validate(); err != nil {
		t.Errorf("valid manifest should not error: %v", err)
	}

	m.Capabilities = []string{"invalid"}
	if err := m.Validate(); err == nil {
		t.Error("expected error for invalid capability")
	}
}

func TestCodec(t *testing.T) {
	// Test encoding/decoding roundtrip
	req := &Request{
		JSONRPC: "2.0",
		Method:  "test/method",
		Params:  json.RawMessage(`{"key":"value"}`),
		ID:      float64(123), // JSON numbers are float64
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Method != req.Method {
		t.Error("method roundtrip failed")
	}
	// ID becomes float64 after JSON roundtrip
	if decoded.ID != float64(123) {
		t.Errorf("ID roundtrip failed: got %v", decoded.ID)
	}
}

func TestManagerLoadPlugins(t *testing.T) {
	tmpDir := t.TempDir()

	// Create fake plugin script
	pluginScript := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":true,"wires":false,"providers":false,"version":1},"id":'"$id"'}'
            ;;
        "tools/list")
            echo '{"jsonrpc":"2.0","result":{"tools":[{"name":"test-tool","description":"A test tool","parameters":{"type":"object","properties":{}}}]},"id":'"$id"'}'
            ;;
        "tools/call")
            echo '{"jsonrpc":"2.0","result":{"content":[{"type":"text","text":"tool result"}]},"id":'"$id"'}'
            ;;
        "ping")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            ;;
        "shutdown")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            exit 0
            ;;
        *)
            echo '{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":'"$id"'}'
            ;;
    esac
done
`
	pluginPath := filepath.Join(tmpDir, "test-plugin")
	if err := os.WriteFile(pluginPath, []byte(pluginScript), 0755); err != nil {
		t.Fatal(err)
	}

	// Create manifest
	manifestContent := `
name = "test-plugin"
version = "1.0.0"
capabilities = ["tools"]
`
	manifestPath := filepath.Join(tmpDir, "test-plugin.toml")
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewRegistry()
	mgr := NewManager(tmpDir, nil, nil, reg)

	// Load plugins with a short timeout
	done := make(chan error, 1)
	go func() {
		done <- mgr.LoadPlugins()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}

	// Verify tool was registered
	tool, ok := reg.Get("test-tool")
	if !ok {
		t.Fatal("plugin tool not registered")
	}
	result, err := tool.Execute(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("tool execution failed: %v", err)
	}
	if result != "tool result" {
		t.Errorf("expected 'tool result', got %q", result)
	}

	mgr.Shutdown()
}

func TestProtocolValidation(t *testing.T) {
	// Test valid request
	req := &Request{
		JSONRPC: "2.0",
		Method:  "test",
		ID:      1,
	}
	if err := ValidateRequest(req); err != nil {
		t.Errorf("valid request should not error: %v", err)
	}

	// Test invalid JSONRPC version
	req.JSONRPC = "1.0"
	if err := ValidateRequest(req); err != ErrInvalidJSONRPC {
		t.Errorf("expected ErrInvalidJSONRPC, got %v", err)
	}

	// Test missing ID
	req.JSONRPC = "2.0"
	req.ID = nil
	if err := ValidateRequest(req); err != ErrMissingID {
		t.Errorf("expected ErrMissingID, got %v", err)
	}
}

func TestCapabilitiesValidation(t *testing.T) {
	caps := Capabilities{
		Tools:     true,
		Wires:     false,
		Providers: false,
		Version:   ProtocolVersion,
	}
	if err := caps.Validate(); err != nil {
		t.Errorf("valid capabilities should not error: %v", err)
	}

	caps.Version = 999
	if err := caps.Validate(); err != ErrProtocolVersion {
		t.Errorf("expected ErrProtocolVersion, got %v", err)
	}

	caps.Version = ProtocolVersion
	caps.Tools = false
	caps.Wires = false
	caps.Providers = false
	if err := caps.Validate(); err != ErrCapabilitiesMismatch {
		t.Errorf("expected ErrCapabilitiesMismatch, got %v", err)
	}
}

func TestErrorResponse(t *testing.T) {
	resp := NewErrorResponse(1, MethodNotFound, "Method not found", nil)
	if resp.Error == nil || resp.Error.Code != MethodNotFound {
		t.Error("error response not created correctly")
	}

	resp, err := NewSuccessResponse(1, map[string]string{"key": "value"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Result == nil {
		t.Error("success response has no result")
	}
}

func TestManagerLoadWirePlugin(t *testing.T) {
	tmpDir := t.TempDir()

	pluginScript := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"wires":true,"providers":false,"version":1},"id":'"$id"'}'
            ;;
        "wire/init")
            echo '{"jsonrpc":"2.0","result":{"ok":true},"id":'"$id"'}'
            ;;
        "wire/list_models")
            echo '{"jsonrpc":"2.0","result":{"models":[{"id":"copilot-gpt-4","name":"Copilot GPT-4"},{"id":"copilot-claude-3","name":"Copilot Claude 3"}]},"id":'"$id"'}'
            ;;
        "wire/stream")
            echo '{"jsonrpc":"2.0","result":{"events":[{"kind":"text","text":"hello"}]},"id":'"$id"'}'
            ;;
        "ping")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            ;;
        "shutdown")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            exit 0
            ;;
        *)
            echo '{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":'"$id"'}'
            ;;
    esac
done
`
	pluginPath := filepath.Join(tmpDir, "copilot")
	if err := os.WriteFile(pluginPath, []byte(pluginScript), 0755); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewRegistry()
	mgr := NewManager(tmpDir, nil, nil, reg)

	done := make(chan error, 1)
	go func() {
		done <- mgr.LoadPlugins()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}

	plugins := mgr.GetPlugins()
	p, ok := plugins["copilot"]
	if !ok {
		t.Fatal("copilot plugin not found")
	}

	if len(p.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(p.Models))
	}
	if p.Models[0].ID != "copilot-gpt-4" {
		t.Errorf("expected model ID 'copilot-gpt-4', got %q", p.Models[0].ID)
	}
	if p.Models[1].Name != "Copilot Claude 3" {
		t.Errorf("expected model name 'Copilot Claude 3', got %q", p.Models[1].Name)
	}

	mgr.Shutdown()
}

func TestManagerLoadDirectoryPlugin(t *testing.T) {
	tmpDir := t.TempDir()

	pluginScript := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":true,"wires":false,"providers":false,"version":1},"id":'"$id"'}'
            ;;
        "tools/list")
            echo '{"jsonrpc":"2.0","result":{"tools":[{"name":"dir-tool","description":"A directory plugin tool","parameters":{"type":"object","properties":{}}}]},"id":'"$id"'}'
            ;;
        "tools/call")
            echo '{"jsonrpc":"2.0","result":{"content":[{"type":"text","text":"dir tool result"}]},"id":'"$id"'}'
            ;;
        "ping")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            ;;
        "shutdown")
            echo '{"jsonrpc":"2.0","result":{},"id":'"$id"'}'
            exit 0
            ;;
        *)
            echo '{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":'"$id"'}'
            ;;
    esac
done
`
	// Create directory layout: plugins/dir-plugin/dir-plugin (binary)
	pluginDir := filepath.Join(tmpDir, "dir-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(pluginDir, "dir-plugin")
	if err := os.WriteFile(binPath, []byte(pluginScript), 0755); err != nil {
		t.Fatal(err)
	}

	// Create manifest inside directory
	manifestContent := `
name = "dir-plugin"
version = "1.0.0"
capabilities = ["tools"]
`
	manifestPath := filepath.Join(pluginDir, "manifest.toml")
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewRegistry()
	mgr := NewManager(tmpDir, nil, nil, reg)

	done := make(chan error, 1)
	go func() {
		done <- mgr.LoadPlugins()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}

	plugins := mgr.GetPlugins()
	p, ok := plugins["dir-plugin"]
	if !ok {
		t.Fatal("dir-plugin not found")
	}

	if len(p.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(p.Tools))
	}
	if p.Tools[0].Name != "dir-tool" {
		t.Errorf("expected tool name 'dir-tool', got %q", p.Tools[0].Name)
	}

	mgr.Shutdown()
}

func TestParseManifestDirectoryLayout(t *testing.T) {
	tmpDir := t.TempDir()

	// Create directory layout
	pluginDir := filepath.Join(tmpDir, "my-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}

	manifestContent := `
name = "my-plugin"
version = "2.0.0"
capabilities = ["wires"]
`
	manifestPath := filepath.Join(pluginDir, "manifest.toml")
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseManifest(tmpDir, "my-plugin")
	if err != nil {
		t.Fatalf("ParseManifest failed: %v", err)
	}
	if m.Name != "my-plugin" {
		t.Errorf("expected name 'my-plugin', got %q", m.Name)
	}
	if m.Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %q", m.Version)
	}
	if !m.HasCapability("wires") {
		t.Error("expected capability 'wires'")
	}
}

func TestParseManifestDirectoryTakesPrecedence(t *testing.T) {
	tmpDir := t.TempDir()

	// Create flat layout
	flatManifest := `
name = "flat-plugin"
version = "1.0.0"
capabilities = ["tools"]
`
	if err := os.WriteFile(filepath.Join(tmpDir, "flat-plugin.toml"), []byte(flatManifest), 0644); err != nil {
		t.Fatal(err)
	}

	// Create directory layout (should take precedence)
	pluginDir := filepath.Join(tmpDir, "flat-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	dirManifest := `
name = "flat-plugin"
version = "2.0.0"
capabilities = ["wires"]
`
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.toml"), []byte(dirManifest), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseManifest(tmpDir, "flat-plugin")
	if err != nil {
		t.Fatalf("ParseManifest failed: %v", err)
	}
	if m.Version != "2.0.0" {
		t.Errorf("expected directory layout to take precedence, got version %q", m.Version)
	}
	if !m.HasCapability("wires") {
		t.Error("expected directory layout capability 'wires'")
	}
}