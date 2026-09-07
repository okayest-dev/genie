package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/okayest-dev/genie/internal/repl"
	"github.com/okayest-dev/genie/internal/tools"
)

func commandsPluginScriptWithHelp() string {
	return `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"wires":false,"providers":false,"commands":true,"version":1},"id":'"$id"'}'
            ;;
        "commands/list")
            echo '{"jsonrpc":"2.0","result":{"commands":[{"name":"greet","description":"Greet someone"},{"name":"helpme","description":"show help"}]},"id":'"$id"'}'
            ;;
        "commands/run")
            cmd_name=$(echo "$line" | jq -r '.params.name')
            cmd_args=$(echo "$line" | jq -r '.params.arguments')
            echo '{"jsonrpc":"2.0","result":{"text":"Hello from '"$cmd_name"': '"$cmd_args"'"},"id":'"$id"'}'
            ;;
        "commands/help")
            echo '{"jsonrpc":"2.0","result":{"text":"curated help"},"id":'"$id"'}'
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

// commandsPluginNoHelpScript advertises the commands capability but returns
// -32601 for commands/help, exercising the lazy fundamental fallback.
func commandsPluginNoHelpScript() string {
	return `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"wires":false,"providers":false,"commands":true,"version":1},"id":'"$id"'}'
            ;;
        "commands/list")
            echo '{"jsonrpc":"2.0","result":{"commands":[{"name":"greet","description":"Greet someone"}]},"id":'"$id"'}'
            ;;
        "commands/run")
            cmd_name=$(echo "$line" | jq -r '.params.name')
            cmd_args=$(echo "$line" | jq -r '.params.arguments')
            echo '{"jsonrpc":"2.0","result":{"text":"Hello: '"$cmd_args"'"},"id":'"$id"'}'
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

func loadCommandsPluginFromScript(t *testing.T, script string) (*Manager, *Plugin) {
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

func TestManagerCommandsList(t *testing.T) {
	mgr, _ := loadCommandsPluginFromScript(t, commandsPluginScriptWithHelp())
	defer mgr.Shutdown()

	mc := &ManagerCommands{Manager: mgr}
	cmds, err := mc.ListCommands("cmd-plugin")
	if err != nil {
		t.Fatalf("ListCommands: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}
	if cmds[0].Name != "greet" || cmds[0].Description != "Greet someone" {
		t.Errorf("unexpected command info: %+v", cmds[0])
	}
}

func TestManagerCommandsPlugins(t *testing.T) {
	mgr, _ := loadCommandsPluginFromScript(t, commandsPluginScriptWithHelp())
	defer mgr.Shutdown()

	mc := &ManagerCommands{Manager: mgr}
	names := mc.Plugins()
	if len(names) != 1 || names[0] != "cmd-plugin" {
		t.Fatalf("expected [cmd-plugin], got %v", names)
	}
}

func TestManagerCommandsListUnknownPlugin(t *testing.T) {
	mgr, _ := loadCommandsPluginFromScript(t, commandsPluginScriptWithHelp())
	defer mgr.Shutdown()

	mc := &ManagerCommands{Manager: mgr}
	_, err := mc.ListCommands("nope")
	if !errors.Is(err, repl.ErrUnknownPlugin) {
		t.Fatalf("expected ErrUnknownPlugin, got %v", err)
	}
}

func TestManagerCommandsRun(t *testing.T) {
	mgr, _ := loadCommandsPluginFromScript(t, commandsPluginScriptWithHelp())
	defer mgr.Shutdown()

	mc := &ManagerCommands{Manager: mgr}
	res, err := mc.RunCommand("cmd-plugin", "greet", "World")
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if res.Text != "Hello from greet: World" {
		t.Errorf("unexpected result text: %q", res.Text)
	}
}

func TestManagerCommandsRunUnknownCommand(t *testing.T) {
	mgr, _ := loadCommandsPluginFromScript(t, commandsPluginScriptWithHelp())
	defer mgr.Shutdown()

	mc := &ManagerCommands{Manager: mgr}
	_, err := mc.RunCommand("cmd-plugin", "nope", "")
	if !errors.Is(err, repl.ErrUnknownCommand) {
		t.Fatalf("expected ErrUnknownCommand, got %v", err)
	}
}

func TestManagerCommandsRunUnknownPlugin(t *testing.T) {
	mgr, _ := loadCommandsPluginFromScript(t, commandsPluginScriptWithHelp())
	defer mgr.Shutdown()

	mc := &ManagerCommands{Manager: mgr}
	_, err := mc.RunCommand("nope", "greet", "")
	if !errors.Is(err, repl.ErrUnknownPlugin) {
		t.Fatalf("expected ErrUnknownPlugin, got %v", err)
	}
}

func TestManagerCommandsInactive(t *testing.T) {
	// Build a manager with a script that dies so the plugin becomes inactive.
	mgr, p := loadCommandsPluginFromScript(t, commandsPluginScriptWithHelp())
	defer mgr.Shutdown()
	// Mark the plugin inactive directly to simulate a dead plugin.
	p.Active = false

	mc := &ManagerCommands{Manager: mgr}
	_, err := mc.RunCommand("cmd-plugin", "greet", "")
	if !errors.Is(err, repl.ErrPluginInactive) {
		t.Fatalf("expected ErrPluginInactive, got %v", err)
	}
	_, err = mc.ListCommands("cmd-plugin")
	if !errors.Is(err, repl.ErrPluginInactive) {
		t.Fatalf("expected ErrPluginInactive from ListCommands, got %v", err)
	}
}

func TestManagerCommandsHelpCurated(t *testing.T) {
	mgr, _ := loadCommandsPluginFromScript(t, commandsPluginScriptWithHelp())
	defer mgr.Shutdown()

	mc := &ManagerCommands{Manager: mgr}
	text, err := mc.Help("cmd-plugin", "")
	if err != nil {
		t.Fatalf("Help: %v", err)
	}
	if text != "curated help" {
		t.Errorf("expected 'curated help', got %q", text)
	}
}

func TestManagerCommandsHelpFallsBack(t *testing.T) {
	mgr, _ := loadCommandsPluginFromScript(t, commandsPluginNoHelpScript())
	defer mgr.Shutdown()

	mc := &ManagerCommands{Manager: mgr}
	_, err := mc.Help("cmd-plugin", "")
	if !errors.Is(err, repl.ErrUnknownCommand) {
		t.Fatalf("expected ErrUnknownCommand fallback when no curated help, got %v", err)
	}
}

func TestManagerCommandsHelpUnknownPlugin(t *testing.T) {
	mgr, _ := loadCommandsPluginFromScript(t, commandsPluginScriptWithHelp())
	defer mgr.Shutdown()

	mc := &ManagerCommands{Manager: mgr}
	_, err := mc.Help("nope", "")
	if !errors.Is(err, repl.ErrUnknownPlugin) {
		t.Fatalf("expected ErrUnknownPlugin, got %v", err)
	}
}

func TestManagerCommandsHelpInactive(t *testing.T) {
	mgr, p := loadCommandsPluginFromScript(t, commandsPluginScriptWithHelp())
	defer mgr.Shutdown()
	p.Active = false

	mc := &ManagerCommands{Manager: mgr}
	_, err := mc.Help("cmd-plugin", "")
	if !errors.Is(err, repl.ErrPluginInactive) {
		t.Fatalf("expected ErrPluginInactive, got %v", err)
	}
}

func TestManagerCallCommandHelpReturnsText(t *testing.T) {
	mgr, p := loadCommandsPluginFromScript(t, commandsPluginScriptWithHelp())
	defer mgr.Shutdown()

	res, err := p.CallCommandHelp("")
	if err != nil {
		t.Fatalf("CallCommandHelp: %v", err)
	}
	if res.Text != "curated help" {
		t.Errorf("expected 'curated help', got %q", res.Text)
	}
}

func TestManagerCallCommandHelpInactive(t *testing.T) {
	mgr, p := loadCommandsPluginFromScript(t, commandsPluginScriptWithHelp())
	defer mgr.Shutdown()
	p.Active = false

	_, err := p.CallCommandHelp("")
	if err == nil || !strings.Contains(err.Error(), "is not active") {
		t.Fatalf("expected inactive error, got %v", err)
	}
}

func loadSilentHelpPlugin(t *testing.T, helpResult string) (*Manager, *Plugin) {
	t.Helper()
	script := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | jq -r .method)
    id=$(echo "$line" | jq -r .id)
    case "$method" in
        "capabilities/list")
            echo '{"jsonrpc":"2.0","result":{"tools":false,"wires":false,"providers":false,"commands":true,"version":1},"id":'"$id"'}'
            ;;
        "commands/list")
            echo '{"jsonrpc":"2.0","result":{"commands":[{"name":"greet","description":"hi"}]},"id":'"$id"'}'
            ;;
        "commands/help")
            echo '{"jsonrpc":"2.0","result":` + helpResult + `,"id":'"$id"'}'
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

func TestManagerCallCommandHelpParseError(t *testing.T) {
	mgr, p := loadSilentHelpPlugin(t, `43`)
	defer mgr.Shutdown()

	_, err := p.CallCommandHelp("")
	if err == nil || !strings.Contains(err.Error(), "parse command help result") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestIsCode(t *testing.T) {
	fmErr := &Error{Code: MethodNotFound, Message: "Method not found"}
	if !IsCode(fmErr, MethodNotFound) {
		t.Error("expected IsCode to match MethodNotFound")
	}
	if IsCode(fmErr, InternalError) {
		t.Error("expected IsCode not to match InternalError")
	}
	if IsCode(errors.New("plain error"), MethodNotFound) {
		t.Error("expected IsCode to be false for a non-plugin error")
	}
	if IsCode(nil, MethodNotFound) {
		t.Error("expected IsCode to be false for nil")
	}
}

// docsCheck provides a compile-time confirmation that the adapter satisfies the
// repl seam without tieing the test to runtime behavior.
var _ repl.CommandSource = (*ManagerCommands)(nil)

func TestScriptHasHelp(t *testing.T) {
	if !strings.Contains(commandsPluginScriptWithHelp(), "commands/help") {
		t.Error("help script should handle commands/help")
	}
}
