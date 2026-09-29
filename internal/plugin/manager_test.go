package plugin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/llm"
	"github.com/okayest-dev/genie/internal/tools"
)

func TestParseManifest(t *testing.T) {
	tmpDir := t.TempDir()

	// Test valid manifest
	manifestContent := `
name = "test-plugin"
version = "1.0.0"
capabilities = ["tools"]
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
	if !m.HasCapability("tools") {
		t.Error("expected capability tools")
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
		Capabilities: []string{"tools", "providers"},
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
            echo '{"jsonrpc":"2.0","result":{"tools":true,"providers":false,"version":1},"id":'"$id"'}'
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
		Providers: false,
		Version:   ProtocolVersion,
	}
	if err := caps.Validate(); err != nil {
		t.Errorf("valid capabilities should not error: %v", err)
	}

	caps.Version = 999
	err := caps.Validate()
	if !errors.Is(err, ErrProtocolVersion) {
		t.Errorf("expected ErrProtocolVersion, got %v", err)
	}
	if !strings.Contains(err.Error(), "999") || !strings.Contains(err.Error(), strconv.Itoa(ProtocolVersion)) {
		t.Errorf("message should name the reported and supported versions, got %q", err)
	}

	caps.Version = ProtocolVersion
	caps.Tools = false
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

func TestManagerLoadToolPlugin(t *testing.T) {
	tmpDir := t.TempDir()

	pluginScript := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":true,"providers":false,"version":1},"id":'"$id"'}'
            ;;
        "tools/list")
            echo '{"jsonrpc":"2.0","result":{"tools":[]},"id":'"$id"'}'
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

	if !p.Capabilities.Tools {
		t.Error("expected the plugin to declare the tools capability")
	}
	if len(p.Tools) != 0 {
		t.Errorf("expected no tools, got %d", len(p.Tools))
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
            echo '{"jsonrpc":"2.0","result":{"tools":true,"providers":false,"version":1},"id":'"$id"'}'
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
capabilities = ["providers"]
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
	if !m.HasCapability("providers") {
		t.Error("expected capability 'providers'")
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
capabilities = ["providers"]
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
	if !m.HasCapability("providers") {
		t.Error("expected directory layout capability 'providers'")
	}
}

func commandsPluginScript() string {
	return `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"providers":false,"commands":true,"version":1},"id":'"$id"'}'
            ;;
        "commands/list")
            echo '{"jsonrpc":"2.0","result":{"commands":[{"name":"greet","description":"Greet someone","usage":"<name>"}]},"id":'"$id"'}'
            ;;
        "commands/run")
            cmd_name=$(echo "$line" | jq -r '.params.name')
            cmd_args=$(echo "$line" | jq -r '.params.arguments')
            echo '{"jsonrpc":"2.0","result":{"text":"Hello from '"$cmd_name"': '"$cmd_args"'"},"id":'"$id"'}'
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
}

func loadCommandsPlugin(t *testing.T, script string) (*Manager, *Plugin) {
	t.Helper()
	tmpDir := t.TempDir()
	pluginPath := filepath.Join(tmpDir, "cmd-plugin")
	if err := os.WriteFile(pluginPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	mgr := NewManager(tmpDir, nil, nil, reg)
	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}
	plugins := mgr.GetPlugins()
	p, ok := plugins["cmd-plugin"]
	if !ok {
		t.Fatal("cmd-plugin not found")
	}
	return mgr, p
}

func TestManagerLoadCommandsPlugin(t *testing.T) {
	mgr, p := loadCommandsPlugin(t, commandsPluginScript())
	defer mgr.Shutdown()

	if len(p.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(p.Commands))
	}
	if p.Commands[0].Name != "greet" {
		t.Errorf("expected command name 'greet', got %q", p.Commands[0].Name)
	}
	if p.Commands[0].Description != "Greet someone" {
		t.Errorf("expected description 'Greet someone', got %q", p.Commands[0].Description)
	}
	if p.Commands[0].Usage != "<name>" {
		t.Errorf("expected usage '<name>', got %q", p.Commands[0].Usage)
	}
}

func TestManagerCallCommand(t *testing.T) {
	mgr, p := loadCommandsPlugin(t, commandsPluginScript())
	defer mgr.Shutdown()

	result, err := p.CallCommand("greet", "World")
	if err != nil {
		t.Fatalf("CallCommand failed: %v", err)
	}
	if result.Text != "Hello from greet: World" {
		t.Errorf("expected 'Hello from greet: World', got %q", result.Text)
	}
}

func TestManagerCallCommandTimeout(t *testing.T) {
	tmpDir := t.TempDir()

	slowScript := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"providers":false,"commands":true,"version":1},"id":'"$id"'}'
            ;;
        "commands/list")
            echo '{"jsonrpc":"2.0","result":{"commands":[{"name":"slow","description":"A slow command"}]},"id":'"$id"'}'
            ;;
        "commands/run")
            sleep 10
            echo '{"jsonrpc":"2.0","result":{"text":"too late"},"id":'"$id"'}'
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
	pluginPath := filepath.Join(tmpDir, "cmd-plugin")
	if err := os.WriteFile(pluginPath, []byte(slowScript), 0755); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	mgr := NewManager(tmpDir, nil, nil, reg)
	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}
	plugins := mgr.GetPlugins()
	p, ok := plugins["cmd-plugin"]
	if !ok {
		t.Fatal("cmd-plugin not found")
	}

	_, err := p.CallCommand("slow", "")
	if err == nil {
		t.Fatal("expected error from timed-out command")
	}
	if p.Active {
		t.Error("plugin should be inactive after timeout")
	}

	mgr.Shutdown()
}

func TestManagerCallCommandRPCError(t *testing.T) {
	tmpDir := t.TempDir()

	errorScript := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"providers":false,"commands":true,"version":1},"id":'"$id"'}'
            ;;
        "commands/list")
            echo '{"jsonrpc":"2.0","result":{"commands":[{"name":"fail","description":"A command that fails"}]},"id":'"$id"'}'
            ;;
        "commands/run")
            echo '{"jsonrpc":"2.0","error":{"code":-32602,"message":"bad input"},"id":'"$id"'}'
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
	pluginPath := filepath.Join(tmpDir, "cmd-plugin")
	if err := os.WriteFile(pluginPath, []byte(errorScript), 0755); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	mgr := NewManager(tmpDir, nil, nil, reg)
	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}
	plugins := mgr.GetPlugins()
	p, ok := plugins["cmd-plugin"]
	if !ok {
		t.Fatal("cmd-plugin not found")
	}

	_, err := p.CallCommand("fail", "")
	if err == nil {
		t.Fatal("expected error from RPC error")
	}
	if !p.Active {
		t.Error("plugin should still be active after RPC error")
	}

	mgr.Shutdown()
}

func TestManagerCallCommandDeadPlugin(t *testing.T) {
	tmpDir := t.TempDir()

	dieScript := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"providers":false,"commands":true,"version":1},"id":'"$id"'}'
            ;;
        "commands/list")
            echo '{"jsonrpc":"2.0","result":{"commands":[{"name":"bye","description":"Exit"}]},"id":'"$id"'}'
            ;;
        "commands/run")
            exit 1
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
	pluginPath := filepath.Join(tmpDir, "cmd-plugin")
	if err := os.WriteFile(pluginPath, []byte(dieScript), 0755); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	mgr := NewManager(tmpDir, nil, nil, reg)
	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}
	plugins := mgr.GetPlugins()
	p, ok := plugins["cmd-plugin"]
	if !ok {
		t.Fatal("cmd-plugin not found")
	}

	_, err := p.CallCommand("bye", "")
	if err == nil {
		t.Fatal("expected error from dead plugin")
	}
	if !strings.Contains(err.Error(), "not active") {
		t.Errorf("expected 'not active' error, got %q", err.Error())
	}

	mgr.Shutdown()
}

func TestManagerReservedNameRejected(t *testing.T) {
	reservedNames := []string{"help", "quit", "exit", "new", "changes", "provider", "model", "agent"}
	for _, name := range reservedNames {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			script := commandsPluginScript()
			pluginPath := filepath.Join(tmpDir, name)
			if err := os.WriteFile(pluginPath, []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			mgr := NewManager(tmpDir, nil, nil, reg)
			done := make(chan error, 1)
			go func() { done <- mgr.LoadPlugins() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("LoadPlugins failed: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("LoadPlugins timed out")
			}
			plugins := mgr.GetPlugins()
			if _, ok := plugins[name]; ok {
				t.Errorf("plugin with reserved name %q should have been rejected", name)
			}
			mgr.Shutdown()
		})
	}
}

func TestManagerCommandsEmptyList(t *testing.T) {
	tmpDir := t.TempDir()

	emptyScript := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"providers":false,"commands":true,"version":1},"id":'"$id"'}'
            ;;
        "commands/list")
            echo '{"jsonrpc":"2.0","result":{"commands":[]},"id":'"$id"'}'
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
	pluginPath := filepath.Join(tmpDir, "cmd-plugin")
	if err := os.WriteFile(pluginPath, []byte(emptyScript), 0755); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	mgr := NewManager(tmpDir, nil, nil, reg)
	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}
	plugins := mgr.GetPlugins()
	p, ok := plugins["cmd-plugin"]
	if !ok {
		t.Fatal("cmd-plugin not found")
	}
	if len(p.Commands) != 0 {
		t.Errorf("expected 0 commands, got %d", len(p.Commands))
	}
	mgr.Shutdown()
}

func TestNormalizeCommands(t *testing.T) {
	defs := []CommandDef{
		{Name: "keep-start", Description: "first valid"},
		{Name: "two words", Description: "whitespace dropped"},
		{Name: "tab\there", Description: "tab dropped"},
		{Name: "/leading", Description: "leading slash dropped"},
		{Name: "", Description: "empty name dropped"},
		{Name: "keep-start", Description: "duplicate, last-wins"},
		{Name: "dup", Description: "first dup"},
		{Name: "dup", Description: "last dup"},
		{Name: "tail", Description: "last valid"},
	}
	got := normalizeCommands(defs)
	want := []string{"keep-start", "dup", "tail"}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %+v", len(want), len(got), got)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("expected command %d to be %q, got %q", i, name, got[i].Name)
		}
	}
	if got[0].Description != "duplicate, last-wins" {
		t.Errorf("expected duplicate to resolve last-wins, got %q", got[0].Description)
	}
	if got[1].Description != "last dup" {
		t.Errorf("expected duplicate to resolve last-wins, got %q", got[1].Description)
	}

	if got := normalizeCommands(nil); len(got) != 0 {
		t.Errorf("expected empty list tolerated, got %d", len(got))
	}
}

func TestManagerCommandsNameValidation(t *testing.T) {
	tmpDir := t.TempDir()

	script := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"providers":false,"commands":true,"version":1},"id":'"$id"'}'
            ;;
        "commands/list")
            echo '{"jsonrpc":"2.0","result":{"commands":[{"name":"good","description":"kept"},{"name":"two words","description":"dropped"},{"name":"good","description":"last-wins"},{"name":"/nope","description":"leading slash"}]},"id":'"$id"'}'
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
	pluginPath := filepath.Join(tmpDir, "cmd-plugin")
	if err := os.WriteFile(pluginPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	mgr := NewManager(tmpDir, nil, nil, reg)
	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}
	plugins := mgr.GetPlugins()
	p, ok := plugins["cmd-plugin"]
	if !ok {
		t.Fatal("cmd-plugin not found")
	}
	if len(p.Commands) != 1 {
		t.Fatalf("expected 1 command after filtering, got %d: %+v", len(p.Commands), p.Commands)
	}
	if p.Commands[0].Name != "good" || p.Commands[0].Description != "last-wins" {
		t.Errorf("expected single 'good' (last-wins) command, got %+v", p.Commands)
	}
	mgr.Shutdown()
}

func TestManagerCommandsListRPCError(t *testing.T) {
	tmpDir := t.TempDir()

	script := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"providers":false,"commands":true,"version":1},"id":'"$id"'}'
            ;;
        "commands/list")
            echo '{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":'"$id"'}'
            ;;
        "shutdown")
            exit 0
            ;;
        *)
            echo '{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":'"$id"'}'
            ;;
    esac
done
`
	pluginPath := filepath.Join(tmpDir, "cmd-plugin")
	if err := os.WriteFile(pluginPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	mgr := NewManager(tmpDir, nil, nil, reg)
	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}
	plugins := mgr.GetPlugins()
	p, ok := plugins["cmd-plugin"]
	if !ok {
		t.Fatal("plugin should remain registered (inactive) after a commands/list RPC error")
	}
	if p.isActive() {
		t.Error("plugin should be inactive after a commands/list RPC error")
	}
	mgr.Shutdown()
}

func TestManagerCommandsListParseError(t *testing.T) {
	tmpDir := t.TempDir()

	script := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"providers":false,"commands":true,"version":1},"id":'"$id"'}'
            ;;
        "commands/list")
            echo '{"jsonrpc":"2.0","result":"not-an-object","id":'"$id"'}'
            ;;
        "shutdown")
            exit 0
            ;;
        *)
            echo '{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":'"$id"'}'
            ;;
    esac
done
`
	pluginPath := filepath.Join(tmpDir, "cmd-plugin")
	if err := os.WriteFile(pluginPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	mgr := NewManager(tmpDir, nil, nil, reg)
	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}
	plugins := mgr.GetPlugins()
	p, ok := plugins["cmd-plugin"]
	if !ok {
		t.Fatal("plugin should remain registered (inactive) after a malformed commands/list")
	}
	if p.isActive() {
		t.Error("plugin should be inactive after a malformed commands/list")
	}
	mgr.Shutdown()
}

// hookPluginScript is a plugin that answers the context hooks, exposes one
// tool, and honours a trigger file: the moment the file appears, the hook
// named by $GENIE_TRIGGER_HOOK sleeps for $GENIE_HANG_SECS and then exits
// without replying. A hook call made after the trigger is in flight across the
// process death, which is the interleaving the monitor's Active write must
// survive.
func hookPluginScript() string {
	return `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":true,"providers":false,"commands":true,"version":1},"id":'"$id"'}'
            ;;
        "tools/list")
            echo '{"jsonrpc":"2.0","result":{"tools":[]},"id":'"$id"'}'
            ;;
        "commands/list")
            echo '{"jsonrpc":"2.0","result":{"commands":[]},"id":'"$id"'}'
            ;;
        "context/before_request"|"lifecycle/response_ready")
            if [ -n "$GENIE_TRIGGER" ] && [ -f "$GENIE_TRIGGER" ] && [ "$method" = "$GENIE_TRIGGER_HOOK" ]; then
                sleep "${GENIE_HANG_SECS:-2}"
                exit 1
            fi
            echo '{"jsonrpc":"2.0","result":{"request":{}},"id":'"$id"'}'
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
}

// loadHookPlugin writes hookPluginScript as a plugin and loads it, returning the
// manager and the loaded plugin. No manifest is written: the flat layout probes
// capabilities, so the script is self-describing and a manifest would be dead
// setup (the directory layout is not what a flat script is parsed for).
func loadHookPlugin(t *testing.T, env ...string) (*Manager, *Plugin) {
	t.Helper()
	tmpDir := t.TempDir()
	pluginPath := filepath.Join(tmpDir, "hook-plugin")
	if err := os.WriteFile(pluginPath, []byte(hookPluginScript()), 0755); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(tmpDir, nil, nil, tools.NewRegistry())
	if len(env) > 0 {
		for _, kv := range env {
			k, v, _ := strings.Cut(kv, "=")
			t.Setenv(k, v)
		}
	}

	done := make(chan error, 1)
	go func() { done <- mgr.LoadPlugins() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadPlugins failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadPlugins timed out")
	}

	p, ok := mgr.GetPlugins()["hook-plugin"]
	if !ok {
		t.Fatal("hook-plugin not found")
	}
	return mgr, p
}

// waitInactive polls the plugin until it reports inactive or the budget runs
// out. The monitor latches Active on its own goroutine after the process exits,
// and p.Done closes before that happens, so the only sound way to wait for the
// latch is to poll the same accessor the race is about.
func waitInactive(t *testing.T, p *Plugin, budget time.Duration) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if !p.isActive() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("plugin still active after the exit-detection budget")
}

// TestMonitorLatchesInactiveOnProcessExitDuringHookCall is the regression test
// for the monitor's process-exit case writing Active without the plugin mutex.
// A hook call is parked inside the plugin's read loop when the process dies, so
// the monitor's write lands concurrently with the call's own read of Active.
// Run under -race; the assertion alone cannot distinguish the locked and
// unlocked write, only the detector can.
func TestMonitorLatchesInactiveOnProcessExitDuringHookCall(t *testing.T) {
	dir := t.TempDir()
	trigger := filepath.Join(dir, "trigger")
	mgr, p := loadHookPlugin(t, "GENIE_TRIGGER="+trigger, "GENIE_TRIGGER_HOOK=context/before_request", "GENIE_HANG_SECS=2")
	defer mgr.Shutdown()

	hookErr := make(chan error, 1)
	go func() {
		_, err := p.CallContextBefore(t.Context(), llm.Request{})
		hookErr <- err
	}()

	// Let the hook request reach the plugin's read loop, then kill the process
	// underneath it so the call is in flight across the exit.
	if err := os.WriteFile(trigger, nil, 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-hookErr:
		if !errors.Is(err, ErrPluginInactive) {
			t.Errorf("expected an inactive-plugin error from the in-flight hook, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the in-flight hook call never returned")
	}

	waitInactive(t, p, 5*time.Second)
}

// TestMonitorLatchesInactiveWhenPluginExitsOnItsOwn covers the same write from
// the other side: the plugin exits by itself, unannounced, while another
// goroutine polls isActive. Nothing kills the process here, so the latch can
// only come from the monitor noticing the exit. Run under -race.
func TestMonitorLatchesInactiveWhenPluginExitsOnItsOwn(t *testing.T) {
	dir := t.TempDir()
	trigger := filepath.Join(dir, "trigger")
	mgr, p := loadHookPlugin(t, "GENIE_TRIGGER="+trigger, "GENIE_TRIGGER_HOOK=context/before_request", "GENIE_HANG_SECS=0")
	defer mgr.Shutdown()

	// Drive the plugin into exiting on its own: it reads the trigger, skips the
	// sleep, and exits mid-hook.
	hookErr := make(chan error, 1)
	go func() {
		_, err := p.CallContextBefore(t.Context(), llm.Request{})
		hookErr <- err
	}()
	if err := os.WriteFile(trigger, nil, 0644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-hookErr:
	case <-time.After(10 * time.Second):
		t.Fatal("the hook call never returned")
	}

	// Concurrently poll the accessor the monitor races against until the latch
	// lands, so the detector sees a read overlapping the write.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = p.isActive()
			}
		}
	}()

	waitInactive(t, p, 5*time.Second)
	close(stop)
	wg.Wait()
}
