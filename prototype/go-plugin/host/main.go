package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	hclog "github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"

	"github.com/danjones/og/prototype/go-plugin/shared"
)

func main() {
	pluginBin := filepath.Join("plugins", "plugin")

	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: goplugin.HandshakeConfig{
			ProtocolVersion:  1,
			MagicCookieKey:   "OG_PLUGIN",
			MagicCookieValue: "og-agent-harness",
		},
		Plugins: map[string]goplugin.Plugin{
			"og": &shared.OGPluginNetRPC{},
		},
		Cmd:              exec.Command(pluginBin),
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolNetRPC},
		Logger: hclog.New(&hclog.LoggerOptions{
			Output: os.Stderr,
			Name:   "host",
		}),
	})
	defer client.Kill()

	rpcClient, err := client.Client()
	if err != nil {
		log.Fatalf("failed to create RPC client: %v", err)
	}

	raw, err := rpcClient.Dispense("og")
	if err != nil {
		log.Fatalf("failed to dispense plugin: %v", err)
	}
	plugin := raw.(shared.OGPlugin)

	// --- Exercise the interface ---

	fmt.Println("=== Capabilities ===")
	caps, err := plugin.Capabilities()
	if err != nil {
		log.Printf("Capabilities error: %v", err)
	} else {
		fmt.Printf("  tools=%v wires=%v providers=%v\n", caps.Tools, caps.Wires, caps.Providers)
	}

	fmt.Println("\n=== ListTools ===")
	tools, err := plugin.ListTools()
	if err != nil {
		log.Printf("ListTools error: %v", err)
	} else {
		for _, t := range tools {
			fmt.Printf("  %s — %s\n", t.Name, t.Description)
		}
	}

	fmt.Println("\n=== CallTool(\"greet\", {name: \"World\"}) ===")
	result, err := plugin.CallTool("greet", map[string]any{"name": "World"})
	if err != nil {
		log.Printf("CallTool error: %v", err)
	} else {
		fmt.Printf("  %s\n", result)
	}

	fmt.Println("\n=== CallTool(\"unknown\", {}) ===")
	_, err = plugin.CallTool("unknown", map[string]any{})
	if err != nil {
		fmt.Printf("  (expected error) %v\n", err)
	}

	fmt.Println("\nAll done. Plugin will shut down with host.")
}
