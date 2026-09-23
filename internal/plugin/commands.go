package plugin

import "errors"

// CommandInfo describes a single command a plugin exposes.
type CommandInfo struct {
	Name        string
	Description string
}

// CommandResult is the outcome of running a plugin command: the caller prints
// Text, or compact JSON of Data when Text is empty.
type CommandResult struct {
	Text string
	Data any
}

// CommandSource is the narrow seam used to discover and run plugin commands,
// carried by the interactive entry points (the REPL) and wired from the plugin
// manager so they stay decoupled from the manager.
type CommandSource interface {
	// Plugins returns the names of available plugins in deterministic order.
	// It lets /help enumerate the flat plugin-commands section.
	Plugins() []string
	// ListCommands returns the commands a plugin exposes. Returns
	// ErrUnknownPlugin when the name is not a loaded plugin.
	ListCommands(plugin string) ([]CommandInfo, error)
	// RunCommand invokes a plugin command and returns the result. The raw
	// argument string is passed through unchanged.
	RunCommand(plugin, command, args string) (*CommandResult, error)
	// Help returns curated help text for a plugin or a single command.
	// The plugin may not supply curated help; callers should fall back to
	// ListCommands when Help returns an error.
	Help(plugin, command string) (string, error)
}

var (
	// ErrUnknownPlugin is returned by CommandSource when the plugin name
	// does not match any loaded plugin.
	ErrUnknownPlugin = errors.New("unknown plugin")
	// ErrUnknownCommand is returned by CommandSource when a command name
	// is not registered by the plugin.
	ErrUnknownCommand = errors.New("unknown command")
	// ErrPluginInactive is returned by CommandSource when the plugin is
	// not currently active.
	ErrPluginInactive = errors.New("plugin not active")
)
