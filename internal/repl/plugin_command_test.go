package repl

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

var errBoom = errors.New("boom")

type fakeCommandSource struct {
	plugins  map[string][]CommandInfo
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

func (f *fakeCommandSource) ListCommands(plugin string) ([]CommandInfo, error) {
	if f.inactive[plugin] {
		return nil, ErrPluginInactive
	}
	if _, ok := f.plugins[plugin]; !ok {
		return nil, ErrUnknownPlugin
	}
	return f.plugins[plugin], nil
}

func (f *fakeCommandSource) RunCommand(plugin, command, args string) (*CommandResult, error) {
	f.runs = append(f.runs, struct{ plugin, command, args string }{plugin, command, args})
	if f.inactive[plugin] {
		return nil, ErrPluginInactive
	}
	if f.runErr != nil {
		if err := f.runErr(plugin, command, args); err != nil {
			return nil, err
		}
	}
	if _, ok := f.plugins[plugin]; !ok {
		return nil, ErrUnknownPlugin
	}
	for _, c := range f.plugins[plugin] {
		if c.Name == command {
			return &CommandResult{Text: "ran " + command + " with " + args}, nil
		}
	}
	return nil, ErrUnknownCommand
}

func (f *fakeCommandSource) Help(plugin, command string) (string, error) {
	if f.inactive[plugin] {
		return "", ErrPluginInactive
	}
	if _, ok := f.plugins[plugin]; !ok {
		return "", ErrUnknownPlugin
	}
	if f.help != nil {
		return f.help(plugin, command)
	}
	return "", nil
}

func runReplWithCommands(t *testing.T, input string, src CommandSource) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cfg := &Config{
		Stdin:      strings.NewReader(input),
		Stdout:     &stdout,
		Stderr:     &stderr,
		SessionDir: t.TempDir(),
		Commands:   src,
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return stdout.String()
}

func TestSlashPluginDispatchText(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]CommandInfo{
		"copilot": {{Name: "greet", Description: "say hi"}},
	}}
	out := runReplWithCommands(t, "/copilot greet World\n/quit\n", src)
	if !strings.Contains(out, "ran greet with World") {
		t.Errorf("stdout = %q, want dispatched text", out)
	}
	if len(src.runs) != 1 || src.runs[0].command != "greet" || src.runs[0].args != "World" {
		t.Errorf("runs = %+v, want command=greet args=World", src.runs)
	}
}

func TestSlashPluginDispatchRawArgs(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]CommandInfo{
		"copilot": {{Name: "eval", Description: "evaluate"}},
	}}
	out := runReplWithCommands(t, "/copilot eval  x  +  y  \n/quit\n", src)
	if len(src.runs) != 1 || src.runs[0].args != "x  +  y" {
		t.Errorf("runs = %+v, want raw args preserved", src.runs)
	}
	_ = out
}

func TestSlashPluginDispatchDataRendersCompactJSON(t *testing.T) {
	bs := &bytes.Buffer{}
	cfg := &Config{
		Stdin:      strings.NewReader("/copilot status\n/quit\n"),
		Stdout:     bs,
		Stderr:     &bytes.Buffer{},
		SessionDir: t.TempDir(),
		Commands:   &dataSource{},
	}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := bs.String()
	if !strings.Contains(out, `{"ok":true}`) {
		t.Errorf("stdout = %q, want compact JSON of data", out)
	}
}

type dataSource struct{}

func (d *dataSource) Plugins() []string { return []string{"copilot"} }
func (d *dataSource) ListCommands(plugin string) ([]CommandInfo, error) {
	return []CommandInfo{{Name: "status", Description: "s"}}, nil
}
func (d *dataSource) RunCommand(plugin, command, args string) (*CommandResult, error) {
	return &CommandResult{Data: map[string]any{"ok": true}}, nil
}
func (d *dataSource) Help(plugin, command string) (string, error) { return "", nil }

func TestSlashPluginUnknownPlugin(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]CommandInfo{}}
	out := runReplWithCommands(t, "/foo bar\n/quit\n", src)
	if !strings.Contains(out, "unknown command: /foo (try /help)") {
		t.Errorf("stdout = %q, want unknown plugin message", out)
	}
}

func TestSlashPluginUnknownCommand(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]CommandInfo{
		"copilot": {{Name: "greet", Description: "say hi"}},
	}}
	out := runReplWithCommands(t, "/copilot nope\n/quit\n", src)
	if !strings.Contains(out, "copilot: no such command: nope") {
		t.Errorf("stdout = %q, want unknown command message", out)
	}
}

func TestSlashPluginInactive(t *testing.T) {
	src := &fakeCommandSource{
		plugins:  map[string][]CommandInfo{"copilot": {{Name: "greet", Description: "say hi"}}},
		inactive: map[string]bool{"copilot": true},
	}
	out := runReplWithCommands(t, "/copilot greet\n/quit\n", src)
	if !strings.Contains(out, "plugin copilot is not active") {
		t.Errorf("stdout = %q, want inactive message", out)
	}
}

func TestSlashPluginGenericError(t *testing.T) {
	src := &fakeCommandSource{
		plugins: map[string][]CommandInfo{"copilot": {{Name: "greet", Description: "say hi"}}},
		runErr:  func(plugin, command, args string) error { return errBoom },
	}
	out := runReplWithCommands(t, "/copilot greet\n/quit\n", src)
	if !strings.Contains(out, "copilot: boom") {
		t.Errorf("stdout = %q, want plugin error message", out)
	}
}

func TestSlashPluginBareListsCommands(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]CommandInfo{
		"copilot": {{Name: "greet", Description: "say hi"}, {Name: "bye", Description: "say bye"}},
	}}
	out := runReplWithCommands(t, "/copilot\n/quit\n", src)
	if !strings.Contains(out, "copilot commands:") {
		t.Errorf("stdout = %q, want command listing header", out)
	}
	if !strings.Contains(out, "greet") || !strings.Contains(out, "say hi") {
		t.Errorf("stdout = %q, want command names and descriptions", out)
	}
}

func TestSlashPluginBareShowsHelp(t *testing.T) {
	src := &fakeCommandSource{
		plugins: map[string][]CommandInfo{"copilot": {{Name: "greet", Description: "say hi"}}},
		help:    func(plugin, command string) (string, error) { return "curated help", nil },
	}
	out := runReplWithCommands(t, "/copilot\n/quit\n", src)
	if !strings.Contains(out, "curated help") {
		t.Errorf("stdout = %q, want curated help", out)
	}
}

func TestSlashPluginBareFallsBackToListing(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]CommandInfo{
		"copilot": {{Name: "greet", Description: "say hi"}},
	}}
	out := runReplWithCommands(t, "/copilot\n/quit\n", src)
	if !strings.Contains(out, "copilot commands:") {
		t.Errorf("stdout = %q, want fallback to listing when no curated help", out)
	}
}

func TestSlashPluginBareNoCommands(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]CommandInfo{
		"copilot": {},
	}}
	out := runReplWithCommands(t, "/copilot\n/quit\n", src)
	if !strings.Contains(out, "copilot has no commands") {
		t.Errorf("stdout = %q, want empty-commands message", out)
	}
}

func TestSlashPluginBareInactive(t *testing.T) {
	src := &fakeCommandSource{
		plugins:  map[string][]CommandInfo{"copilot": {{Name: "greet", Description: "say hi"}}},
		inactive: map[string]bool{"copilot": true},
	}
	out := runReplWithCommands(t, "/copilot\n/quit\n", src)
	if !strings.Contains(out, "plugin copilot is not active") {
		t.Errorf("stdout = %q, want inactive message on bare plugin", out)
	}
}

func TestSlashHelpIncludesPluginSection(t *testing.T) {
	src := &fakeCommandSource{plugins: map[string][]CommandInfo{
		"copilot": {{Name: "greet", Description: "say hi"}},
	}}
	out := runReplWithCommands(t, "/help\n/quit\n", src)
	if !strings.Contains(out, "Plugin commands:") {
		t.Errorf("stdout = %q, want plugin-commands section", out)
	}
	if !strings.Contains(out, "/copilot greet  say hi") {
		t.Errorf("stdout = %q, want enumerated plugin command with description", out)
	}
}
