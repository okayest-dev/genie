package main

import (
	"fmt"
	"os"

	hclog "github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"

	"github.com/danjones/og/prototype/go-plugin/shared"
)

// greetPlugin implements shared.OGPlugin.
type greetPlugin struct{}

func (g *greetPlugin) Capabilities() (shared.Capabilities, error) {
	return shared.Capabilities{Tools: true}, nil
}

func (g *greetPlugin) ListTools() ([]shared.ToolInfo, error) {
	return []shared.ToolInfo{
		{
			Name:        "greet",
			Description: "Greets a person by name",
			Parameters:  `{"type":"object","properties":{"name":{"type":"string","description":"Name of the person to greet"}},"required":["name"]}`,
		},
	}, nil
}

func (g *greetPlugin) CallTool(name string, args map[string]any) (string, error) {
	if name != "greet" {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	nameVal, ok := args["name"].(string)
	if !ok {
		nameVal = "stranger"
	}
	return "Hello, " + nameVal + "!", nil
}

func main() {
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: goplugin.HandshakeConfig{
			ProtocolVersion:  1,
			MagicCookieKey:   "OG_PLUGIN",
			MagicCookieValue: "og-agent-harness",
		},
		Plugins: map[string]goplugin.Plugin{
			"og": &shared.OGPluginNetRPC{Impl: &greetPlugin{}},
		},
		Logger: hclog.New(&hclog.LoggerOptions{
			Output: os.Stderr,
			Name:   "greet-plugin",
		}),
	})
}
