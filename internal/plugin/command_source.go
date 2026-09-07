package plugin

import (
	"fmt"
	"strings"

	"github.com/okayest-dev/genie/internal/repl"
)

// ManagerCommands adapts the plugin Manager into the repl.CommandSource seam.
// The REPL stays decoupled from the manager; main wires this adapter into
// repl.Config.Commands.
type ManagerCommands struct {
	Manager *Manager
}

var _ repl.CommandSource = (*ManagerCommands)(nil)

// Plugins returns the loaded plugin names in discovery order, enabling /help
// to enumerate the flat plugin-commands section. Inactive plugins are omitted.
func (mc *ManagerCommands) Plugins() []string {
	mc.Manager.pluginsMu.RLock()
	defer mc.Manager.pluginsMu.RUnlock()
	var out []string
	for _, name := range mc.Manager.pluginOrder {
		if p, ok := mc.Manager.plugins[name]; ok && p.isActive() {
			out = append(out, name)
		}
	}
	return out
}

// ListCommands returns the commands a plugin exposes, mapping a missing plugin
// to repl.ErrUnknownPlugin.
func (mc *ManagerCommands) ListCommands(name string) ([]repl.CommandInfo, error) {
	p := mc.Manager.pluginByName(name)
	if p == nil {
		return nil, repl.ErrUnknownPlugin
	}
	if !p.isActive() {
		return nil, repl.ErrPluginInactive
	}
	cmds := make([]repl.CommandInfo, 0, len(p.Commands))
	for _, c := range p.Commands {
		cmds = append(cmds, repl.CommandInfo{Name: c.Name, Description: c.Description})
	}
	return cmds, nil
}

// RunCommand invokes a plugin command, mapping protocol-level misses to the
// repl error sentinels and printing plugin errors directly.
func (mc *ManagerCommands) RunCommand(plugin, command, args string) (*repl.CommandResult, error) {
	p := mc.Manager.pluginByName(plugin)
	if p == nil {
		return nil, repl.ErrUnknownPlugin
	}
	if !p.isActive() {
		return nil, repl.ErrPluginInactive
	}
	if !p.hasCommand(command) {
		return nil, repl.ErrUnknownCommand
	}
	result, err := p.CallCommand(command, args)
	if err != nil {
		return nil, err
	}
	return &repl.CommandResult{Text: result.Text, Data: result.Data}, nil
}

// Help returns curated help for a plugin or single command, falling back to a
// -32601 (method-not-found) lazily when the plugin does not supply curated
// help.
func (mc *ManagerCommands) Help(plugin, command string) (string, error) {
	p := mc.Manager.pluginByName(plugin)
	if p == nil {
		return "", repl.ErrUnknownPlugin
	}
	if !p.isActive() {
		return "", repl.ErrPluginInactive
	}
	result, err := p.CallCommandHelp(command)
	if err != nil {
		if IsCode(err, MethodNotFound) {
			return "", fmt.Errorf("%w: %s", repl.ErrUnknownCommand, command)
		}
		return "", err
	}
	return result.Text, nil
}

func (m *Manager) pluginByName(name string) *Plugin {
	m.pluginsMu.RLock()
	defer m.pluginsMu.RUnlock()
	p, ok := m.plugins[name]
	if !ok {
		// Fall back to case-insensitive lookup on the display name.
		for k, v := range m.plugins {
			if strings.EqualFold(k, name) {
				return v
			}
		}
	}
	return p
}

func (p *Plugin) hasCommand(name string) bool {
	for _, c := range p.Commands {
		if c.Name == name {
			return true
		}
	}
	return false
}

// isActive reports whether the plugin process is currently active, guarding
// the Active field behind the plugin mutex so concurrent callers read a
// consistent value.
func (p *Plugin) isActive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Active
}
