package repl

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/okayest-dev/genie/internal/plugin"
)

var errBoom = errors.New("boom")

type fakeCommandSource struct {
	plugins  map[string][]plugin.CommandInfo
	order    []string
	help     func(plugin, command string) (string, error)
	runs     []struct{ plugin, command, args string }
	runErr   func(plugin, command, args string) error
	inactive map[string]bool
}

func (f *fakeCommandSource) Plugins() []string {
	if f.order != nil {
		return f.order
	}
	out := make([]string, 0, len(f.plugins))
	for k := range f.plugins {
		out = append(out, k)
	}
	return out
}

func (f *fakeCommandSource) ListCommands(pluginName string) ([]plugin.CommandInfo, error) {
	if f.inactive[pluginName] {
		return nil, plugin.ErrPluginInactive
	}
	if _, ok := f.plugins[pluginName]; !ok {
		return nil, plugin.ErrUnknownPlugin
	}
	return f.plugins[pluginName], nil
}

func (f *fakeCommandSource) RunCommand(pluginName, command, args string) (*plugin.CommandResult, error) {
	f.runs = append(f.runs, struct{ plugin, command, args string }{pluginName, command, args})
	if f.inactive[pluginName] {
		return nil, plugin.ErrPluginInactive
	}
	if f.runErr != nil {
		if err := f.runErr(pluginName, command, args); err != nil {
			return nil, err
		}
	}
	if _, ok := f.plugins[pluginName]; !ok {
		return nil, plugin.ErrUnknownPlugin
	}
	for _, c := range f.plugins[pluginName] {
		if c.Name == command {
			return &plugin.CommandResult{Text: "ran " + command + " with " + args}, nil
		}
	}
	return nil, plugin.ErrUnknownCommand
}

func (f *fakeCommandSource) Help(pluginName, command string) (string, error) {
	if f.inactive[pluginName] {
		return "", plugin.ErrPluginInactive
	}
	if _, ok := f.plugins[pluginName]; !ok {
		return "", plugin.ErrUnknownPlugin
	}
	if f.help != nil {
		return f.help(pluginName, command)
	}
	return "", nil
}

func runPluginSlash(t *testing.T, src plugin.CommandSource, line string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cfg := &Config{Stdout: &stdout, Stderr: &stderr}
	if !handlePluginCommand(line, src, cfg) {
		t.Fatalf("command %q was not handled as a plugin command", line)
	}
	return stdout.String()
}

func TestSlashPluginDispatchText(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]plugin.CommandInfo{
		"copilot": {{Name: "greet", Description: "say hi"}},
	}}
	out := runPluginSlash(t, src, "/copilot greet World")
	if !strings.Contains(out, "ran greet with World") {
		t.Errorf("stdout = %q, want dispatched text", out)
	}
	if len(src.runs) != 1 || src.runs[0].command != "greet" || src.runs[0].args != "World" {
		t.Errorf("runs = %+v, want command=greet args=World", src.runs)
	}
}

func TestSlashPluginDispatchRawArgs(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]plugin.CommandInfo{
		"copilot": {{Name: "eval", Description: "evaluate"}},
	}}
	_ = runPluginSlash(t, src, "/copilot eval  x  +  y  ")
	if len(src.runs) != 1 || src.runs[0].args != "x  +  y" {
		t.Errorf("runs = %+v, want raw args preserved", src.runs)
	}
}

func TestSlashPluginDispatchDataRendersCompactJSON(t *testing.T) {
	bs := &bytes.Buffer{}
	cfg := &Config{Stdout: bs, Stderr: &bytes.Buffer{}}
	if !handlePluginCommand("/copilot status", &dataSource{}, cfg) {
		t.Fatal("command was not handled as a plugin command")
	}
	out := bs.String()
	if !strings.Contains(out, `{"ok":true}`) {
		t.Errorf("stdout = %q, want compact JSON of data", out)
	}
}

type dataSource struct{}

func (d *dataSource) Plugins() []string { return []string{"copilot"} }
func (d *dataSource) ListCommands(pluginName string) ([]plugin.CommandInfo, error) {
	return []plugin.CommandInfo{{Name: "status", Description: "s"}}, nil
}
func (d *dataSource) RunCommand(pluginName, command, args string) (*plugin.CommandResult, error) {
	return &plugin.CommandResult{Data: map[string]any{"ok": true}}, nil
}
func (d *dataSource) Help(pluginName, command string) (string, error) { return "", nil }

func TestSlashPluginUnknownPlugin(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]plugin.CommandInfo{}}
	out := runPluginSlash(t, src, "/foo bar")
	if !strings.Contains(out, "unknown command: /foo (try /help)") {
		t.Errorf("stdout = %q, want unknown plugin message", out)
	}
}

func TestSlashPluginUnknownCommand(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]plugin.CommandInfo{
		"copilot": {{Name: "greet", Description: "say hi"}},
	}}
	out := runPluginSlash(t, src, "/copilot nope")
	if !strings.Contains(out, "copilot: no such command: nope") {
		t.Errorf("stdout = %q, want unknown command message", out)
	}
}

func TestSlashPluginInactive(t *testing.T) {
	src := &fakeCommandSource{
		plugins:  map[string][]plugin.CommandInfo{"copilot": {{Name: "greet", Description: "say hi"}}},
		inactive: map[string]bool{"copilot": true},
	}
	out := runPluginSlash(t, src, "/copilot greet")
	if !strings.Contains(out, "plugin copilot is not active") {
		t.Errorf("stdout = %q, want inactive message", out)
	}
}

func TestSlashPluginGenericError(t *testing.T) {
	src := &fakeCommandSource{
		plugins: map[string][]plugin.CommandInfo{"copilot": {{Name: "greet", Description: "say hi"}}},
		runErr:  func(plugin, command, args string) error { return errBoom },
	}
	out := runPluginSlash(t, src, "/copilot greet")
	if !strings.Contains(out, "copilot: boom") {
		t.Errorf("stdout = %q, want plugin error message", out)
	}
}

func TestSlashPluginBareListsCommands(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]plugin.CommandInfo{
		"copilot": {{Name: "greet", Description: "say hi"}, {Name: "bye", Description: "say bye"}},
	}}
	out := runPluginSlash(t, src, "/copilot")
	if !strings.Contains(out, "copilot commands:") {
		t.Errorf("stdout = %q, want command listing header", out)
	}
	if !strings.Contains(out, "greet") || !strings.Contains(out, "say hi") {
		t.Errorf("stdout = %q, want command names and descriptions", out)
	}
}

func TestSlashPluginBareShowsHelp(t *testing.T) {
	src := &fakeCommandSource{
		plugins: map[string][]plugin.CommandInfo{"copilot": {{Name: "greet", Description: "say hi"}}},
		help:    func(plugin, command string) (string, error) { return "curated help", nil },
	}
	out := runPluginSlash(t, src, "/copilot")
	if !strings.Contains(out, "curated help") {
		t.Errorf("stdout = %q, want curated help", out)
	}
}

func TestSlashPluginBareFallsBackToListing(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]plugin.CommandInfo{
		"copilot": {{Name: "greet", Description: "say hi"}},
	}}
	out := runPluginSlash(t, src, "/copilot")
	if !strings.Contains(out, "copilot commands:") {
		t.Errorf("stdout = %q, want fallback to listing when no curated help", out)
	}
}

func TestSlashPluginBareNoCommands(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]plugin.CommandInfo{
		"copilot": {},
	}}
	out := runPluginSlash(t, src, "/copilot")
	if !strings.Contains(out, "copilot has no commands") {
		t.Errorf("stdout = %q, want empty-commands message", out)
	}
}

func TestSlashPluginBareInactive(t *testing.T) {
	src := &fakeCommandSource{
		plugins:  map[string][]plugin.CommandInfo{"copilot": {{Name: "greet", Description: "say hi"}}},
		inactive: map[string]bool{"copilot": true},
	}
	out := runPluginSlash(t, src, "/copilot")
	if !strings.Contains(out, "plugin copilot is not active") {
		t.Errorf("stdout = %q, want inactive message on bare plugin", out)
	}
}

func TestSlashHelpIncludesPluginSection(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]plugin.CommandInfo{
		"copilot": {{Name: "greet", Description: "say hi"}},
	}}
	var stdout bytes.Buffer
	printPluginCommandsHelp(src, &stdout)
	out := stdout.String()
	if !strings.Contains(out, "Plugin commands:") {
		t.Errorf("stdout = %q, want plugin-commands section", out)
	}
	if !strings.Contains(out, "/copilot greet  say hi") {
		t.Errorf("stdout = %q, want enumerated plugin command with description", out)
	}
}
