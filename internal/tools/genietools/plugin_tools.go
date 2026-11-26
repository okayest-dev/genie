// Package genietools implements tools for Genie self-improvement.
package genietools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/okayest-dev/genie/internal/tools"
)

// PluginType represents the type of plugin.
type PluginType string

const (
	PluginTypeTool       PluginType = "tool"
	PluginTypeLifecycle  PluginType = "lifecycle"
	PluginTypeCommand    PluginType = "command"
)

// PluginLanguage represents the implementation language.
type PluginLanguage string

const (
	PluginLanguageGo      PluginLanguage = "go"
	PluginLanguageTypeScript PluginLanguage = "typescript"
	PluginLanguagePython  PluginLanguage = "python"
	PluginLanguageShell   PluginLanguage = "shell"
)

// PluginScaffold holds the template data for generating a plugin.
type PluginScaffold struct {
	Name        string
	Type        PluginType
	Language    PluginLanguage
	Description string
	Capabilities []string
}

// GeniePluginCreateTool creates a new plugin scaffold.
type GeniePluginCreateTool struct {
	pluginDir string
}

func NewGeniePluginCreateTool(pluginDir string) *GeniePluginCreateTool {
	return &GeniePluginCreateTool{pluginDir: pluginDir}
}

func (t *GeniePluginCreateTool) Name() string        { return "genie_plugin_create" }
func (t *GeniePluginCreateTool) Description() string { return "Create a new plugin scaffold (tool, lifecycle, or command type)" }

func (t *GeniePluginCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Plugin name (kebab-case)",
			},
			"type": map[string]any{
				"type":        "string",
				"enum":        []string{"tool", "lifecycle", "command"},
				"description": "Plugin type",
			},
			"language": map[string]any{
				"type":        "string",
				"enum":        []string{"go", "typescript", "python", "shell"},
				"description": "Implementation language (default: go)",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Plugin description",
			},
		},
		"required": []any{"name", "type"},
	}
}

func (t *GeniePluginCreateTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Language    string `json:"language,omitempty"`
		Description string `json:"description,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.Language == "" {
		params.Language = "go"
	}

	pluginType := PluginType(params.Type)
	pluginLang := PluginLanguage(params.Language)

	scaffold := PluginScaffold{
		Name:        params.Name,
		Type:        pluginType,
		Language:    pluginLang,
		Description: params.Description,
		Capabilities: t.capabilitiesForType(pluginType),
	}

	pluginPath := filepath.Join(t.pluginDir, params.Name)
	if err := os.MkdirAll(pluginPath, 0o755); err != nil {
		return "", fmt.Errorf("create plugin dir: %w", err)
	}

	// Generate manifest.toml
	manifestPath := filepath.Join(pluginPath, "manifest.toml")
	manifestTmpl := `name = "{{.Name}}"
version = "1.0.0"
capabilities = [{{range $i, $c := .Capabilities}}{{if $i}}, {{end}}"{{$c}}"{{end}}]
`
	if err := t.writeTemplate(manifestPath, manifestTmpl, scaffold); err != nil {
		return "", err
	}

	// Generate main source file
	var sourcePath string
	switch pluginLang {
	case PluginLanguageGo:
		sourcePath = filepath.Join(pluginPath, params.Name+".go")
		sourceContent := t.goSourceTemplate(pluginType, scaffold)
		if err := os.WriteFile(sourcePath, []byte(sourceContent), 0o644); err != nil {
			return "", fmt.Errorf("write source: %w", err)
		}
	case PluginLanguageTypeScript:
		sourcePath = filepath.Join(pluginPath, params.Name+".ts")
		sourceTmpl := t.tsSourceTemplate(pluginType)
		if err := t.writeTemplate(sourcePath, sourceTmpl, scaffold); err != nil {
			return "", err
		}
	case PluginLanguagePython:
		sourcePath = filepath.Join(pluginPath, params.Name+".py")
		sourceTmpl := t.pySourceTemplate(pluginType)
		if err := t.writeTemplate(sourcePath, sourceTmpl, scaffold); err != nil {
			return "", err
		}
	case PluginLanguageShell:
		sourcePath = filepath.Join(pluginPath, params.Name+".sh")
		sourceTmpl := t.shSourceTemplate(pluginType)
		if err := t.writeTemplate(sourcePath, sourceTmpl, scaffold); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unsupported language: %s", pluginLang)
	}

	// Generate config.toml template
	configPath := filepath.Join(pluginPath, "config.toml")
	configTmpl := `# {{.Name}} plugin configuration
# Add your plugin-specific settings here
`
	if err := t.writeTemplate(configPath, configTmpl, scaffold); err != nil {
		return "", err
	}

	// Make executable if needed
	if pluginLang == PluginLanguageShell || pluginLang == PluginLanguagePython {
		if err := os.Chmod(sourcePath, 0o755); err != nil {
			return "", fmt.Errorf("chmod: %w", err)
		}
	}

	return fmt.Sprintf("Plugin scaffold created at %s\nManifest: %s\nSource: %s\nConfig: %s",
		pluginPath, manifestPath, sourcePath, configPath), nil
}

func (t *GeniePluginCreateTool) capabilitiesForType(pt PluginType) []string {
	switch pt {
	case PluginTypeTool:
		return []string{"tools"}
	case PluginTypeLifecycle:
		return []string{"lifecycle"}
	case PluginTypeCommand:
		return []string{"commands"}
	default:
		return []string{}
	}
}

func (t *GeniePluginCreateTool) writeTemplate(path, tmplStr string, data PluginScaffold) error {
	tmpl, err := template.New("plugin").Parse(tmplStr)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()
	return tmpl.Execute(f, data)
}

func (t *GeniePluginCreateTool) goSourceTemplate(pt PluginType, scaffold PluginScaffold) string {
	base := `package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/okayest-dev/genie/internal/plugin"
)

func main() {
	codec := plugin.NewCodec(os.Stdout, os.Stdin)
	defer codec.Close()

	// Handshake: respond to capabilities/list
	req, err := codec.ReadRequest()
	if err != nil {
		log.Fatal(err)
	}
	if req.Method == plugin.MethodCapabilitiesList {
		caps := plugin.Capabilities{
`
	switch pt {
	case PluginTypeTool:
		base += `			Tools: true,
`
	case PluginTypeLifecycle:
		base += `			Lifecycle: true,
`
	case PluginTypeCommand:
		base += `			Commands: true,
`
	}
	base += `		}
		resp := &plugin.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
		}
		resp.Result, _ = json.Marshal(caps)
		if err := codec.WriteResponse(resp); err != nil {
			log.Fatal(err)
		}
	}

	// Main loop
	for {
		req, err := codec.ReadRequest()
		if err != nil {
			return
		}
		handleRequest(codec, req)
	}
}

func handleRequest(codec *plugin.Codec, req *plugin.Request) {
	switch req.Method {
`
	switch pt {
	case PluginTypeTool:
		base += `	case plugin.MethodToolsList:
			tools := []plugin.ToolDef{
				{
					Name:        "__NAME__",
					Description: "__DESCRIPTION__",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"input": map[string]any{"type": "string"},
						},
						"required": []any{"input"},
					},
				},
			}
			result := plugin.ToolsListResult{Tools: tools}
			resp := &plugin.Response{JSONRPC: "2.0", ID: req.ID}
			resp.Result, _ = json.Marshal(result)
			codec.WriteResponse(resp)

		case plugin.MethodToolsCall:
			var params plugin.ToolsCallParams
			json.Unmarshal(req.Params, &params)
			if params.Name == "__NAME__" {
				var args map[string]any
				json.Unmarshal(params.Arguments, &args)
				input := args["input"].(string)
				result := &plugin.ToolsCallResult{
					Content: []plugin.ToolContent{{Text: fmt.Sprintf("Echo: %s", input)}},
				}
				resp := &plugin.Response{JSONRPC: "2.0", ID: req.ID}
				resp.Result, _ = json.Marshal(result)
				codec.WriteResponse(resp)
			}
`
	case PluginTypeLifecycle:
		base += `	case plugin.MethodLifecycleRequestBuilt:
			// Rewrite the request
			var reqData plugin.ContextRequest
			json.Unmarshal(req.Params, &reqData)
			resp := &plugin.Response{JSONRPC: "2.0", ID: req.ID}
			resp.Result, _ = json.Marshal(plugin.ContextBeforeRequestResult{Request: reqData.Request})
			codec.WriteResponse(resp)

		case plugin.MethodLifecycleToolBefore:
			// Guardrail hook
			resp := &plugin.Response{JSONRPC: "2.0", ID: req.ID}
			resp.Result, _ = json.Marshal(plugin.LifecycleToolBeforeResult{})
			codec.WriteResponse(resp)

		case plugin.MethodLifecycleToolAfter:
			resp := &plugin.Response{JSONRPC: "2.0", ID: req.ID}
			resp.Result, _ = json.Marshal(plugin.LifecycleToolAfterResult{})
			codec.WriteResponse(resp)

		case plugin.MethodLifecycleResponseReady:
			resp := &plugin.Response{JSONRPC: "2.0", ID: req.ID}
			resp.Result, _ = json.Marshal(plugin.LifecycleResponseReadyResult{})
			codec.WriteResponse(resp)

		case plugin.MethodLifecycleTurnError:
			resp := &plugin.Response{JSONRPC: "2.0", ID: req.ID}
			resp.Result, _ = json.Marshal(plugin.LifecycleTurnErrorResult{})
			codec.WriteResponse(resp)
`
	case PluginTypeCommand:
		base += `	case plugin.MethodCommandsList:
			cmds := []plugin.CommandDef{
				{
					Name:        "__NAME__",
					Description: "__DESCRIPTION__",
					Usage:       "__NAME__ [args...]",
				},
			}
			result := plugin.CommandsListResult{Commands: cmds}
			resp := &plugin.Response{JSONRPC: "2.0", ID: req.ID}
			resp.Result, _ = json.Marshal(result)
			codec.WriteResponse(resp)

		case plugin.MethodCommandsRun:
			var params plugin.CommandsRunParams
			json.Unmarshal(req.Params, &params)
			if params.Name == "__NAME__" {
				result := &plugin.CommandsRunResult{
					Text: fmt.Sprintf("Command __NAME__ executed with args: %s", params.Arguments),
				}
				resp := &plugin.Response{JSONRPC: "2.0", ID: req.ID}
				resp.Result, _ = json.Marshal(result)
				codec.WriteResponse(resp)
			}

		case plugin.MethodCommandsHelp:
			var params plugin.CommandsHelpParams
			json.Unmarshal(req.Params, &params)
			if params.Name == "__NAME__" {
				result := &plugin.CommandsHelpResult{
					Text: "__DESCRIPTION__\n\nUsage: __NAME__ [args...]",
				}
				resp := &plugin.Response{JSONRPC: "2.0", ID: req.ID}
				resp.Result, _ = json.Marshal(result)
				codec.WriteResponse(resp)
			}
`
	}
	base += `	}
}

`
	// Replace placeholders
	base = strings.ReplaceAll(base, "__NAME__", scaffold.Name)
	base = strings.ReplaceAll(base, "__DESCRIPTION__", scaffold.Description)
	return base
}

func (t *GeniePluginCreateTool) tsSourceTemplate(pt PluginType) string {
	return `#!/usr/bin/env deno run --allow-read --allow-write --allow-net --allow-env --allow-run
// {{.Name}} - {{.Description}}
// TypeScript plugin for Genie

import { Codec, Request, Response, Capabilities, ToolDef, ToolsListResult, ToolsCallParams, ToolsCallResult, ToolContent } from "jsr:@genie/plugin@^1.0";

const codec = new Codec(Deno.stdout, Deno.stdin);

async function main() {
	// Handshake
	const req = await codec.readRequest();
	if (req.method === "capabilities/list") {
		const caps: Capabilities = {
			tools: true,
		};
		await codec.writeResponse({ jsonrpc: "2.0", id: req.id, result: caps });
	}

	// Main loop
	for await (const req of codec) {
		await handleRequest(req);
	}
}

async function handleRequest(req: Request) {
	switch (req.method) {
		case "tools/list":
			const tools: ToolDef[] = [{
				name: "{{.Name}}",
				description: "{{.Description}}",
				parameters: {
					type: "object",
					properties: { input: { type: "string" } },
					required: ["input"],
				},
			}];
			await codec.writeResponse({ jsonrpc: "2.0", id: req.id, result: { tools } });
			break;

		case "tools/call":
			const params = req.params as ToolsCallParams;
			if (params.name === "{{.Name}}") {
				const args = params.arguments as { input: string };
				const result: ToolsCallResult = {
					content: [{ type: "text", text: "Echo: " + args.input }],
				};
				await codec.writeResponse({ jsonrpc: "2.0", id: req.id, result });
			}
			break;
	}
}

if (import.meta.main) {
	main().catch(console.error);
}
`
}

func (t *GeniePluginCreateTool) pySourceTemplate(pt PluginType) string {
	return `#!/usr/bin/env python3
"""{{.Name}} - {{.Description}}"""
import json
import sys
from typing import Any, Dict

class Codec:
    def __init__(self):
        self.stdin = sys.stdin.buffer
        self.stdout = sys.stdout.buffer

    def read_request(self) -> Dict[str, Any]:
        line = self.stdin.readline()
        if not line:
            return None
        return json.loads(line)

    def write_response(self, resp: Dict[str, Any]) -> None:
        self.stdout.write((json.dumps(resp) + "\n").encode())
        self.stdout.flush()

def main():
    codec = Codec()
    
    # Handshake
    req = codec.read_request()
    if req and req.get("method") == "capabilities/list":
        caps = {"tools": True}
        codec.write_response({"jsonrpc": "2.0", "id": req["id"], "result": caps})

    # Main loop
    while True:
        req = codec.read_request()
        if not req:
            break
        handle_request(codec, req)

def handle_request(codec: Codec, req: Dict[str, Any]):
    method = req.get("method")
    if method == "tools/list":
        tools = [{
            "name": "{{.Name}}",
            "description": "{{.Description}}",
            "parameters": {
                "type": "object",
                "properties": {"input": {"type": "string"}},
                "required": ["input"],
            },
        }]
        codec.write_response({"jsonrpc": "2.0", "id": req["id"], "result": {"tools": tools}})
    elif method == "tools/call":
        params = req.get("params", {})
        if params.get("name") == "{{.Name}}":
            args = params.get("arguments", {})
            result = {"content": [{"type": "text", "text": f"Echo: {args.get('input', '')}"}]}
            codec.write_response({"jsonrpc": "2.0", "id": req["id"], "result": result})

if __name__ == "__main__":
    main()
`
}

func (t *GeniePluginCreateTool) shSourceTemplate(pt PluginType) string {
	return `#!/bin/bash
# {{.Name}} - {{.Description}}
# Shell plugin for Genie

read_request() {
    IFS= read -r line || return 1
    echo "$line"
}

write_response() {
    echo "$1"
}

# Handshake
req=$(read_request) || exit 0
method=$(echo "$req" | jq -r '.method')
id=$(echo "$req" | jq -r '.id')

if [[ "$method" == "capabilities/list" ]]; then
    caps='{"tools": true}'
    write_response "{\"jsonrpc\": \"2.0\", \"id\": $id, \"result\": $caps}"
fi

# Main loop
while req=$(read_request); do
    method=$(echo "$req" | jq -r '.method')
    id=$(echo "$req" | jq -r '.id')

    case "$method" in
        "tools/list")
            tools='[{"name": "{{.Name}}", "description": "{{.Description}}", "parameters": {"type": "object", "properties": {"input": {"type": "string"}}, "required": ["input"]}}]'
            write_response "{\"jsonrpc\": \"2.0\", \"id\": $id, \"result\": {\"tools\": $tools}}"
            ;;
        "tools/call")
            name=$(echo "$req" | jq -r '.params.name')
            if [[ "$name" == "{{.Name}}" ]]; then
                input=$(echo "$req" | jq -r '.params.arguments.input')
                result="{\"content\": [{\"type\": \"text\", \"text\": \"Echo: $input\"}]}"
                write_response "{\"jsonrpc\": \"2.0\", \"id\": $id, \"result\": $result}"
            fi
            ;;
    esac
done
`
}

// GeniePluginInstallTool installs a plugin from source or binary.
type GeniePluginInstallTool struct {
	pluginDir string
}

func NewGeniePluginInstallTool(pluginDir string) *GeniePluginInstallTool {
	return &GeniePluginInstallTool{pluginDir: pluginDir}
}

func (t *GeniePluginInstallTool) Name() string        { return "genie_plugin_install" }
func (t *GeniePluginInstallTool) Description() string { return "Install a plugin from a local directory or binary" }

func (t *GeniePluginInstallTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source": map[string]any{
				"type":        "string",
				"description": "Path to plugin source directory or binary",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Plugin name (defaults to directory name)",
			},
		},
		"required": []any{"source"},
	}
}

func (t *GeniePluginInstallTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Source string `json:"source"`
		Name   string `json:"name,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	srcPath := params.Source
	if !filepath.IsAbs(srcPath) {
		// Resolve relative to cwd
		cwd, _ := os.Getwd()
		srcPath = filepath.Join(cwd, srcPath)
	}

	info, err := os.Stat(srcPath)
	if err != nil {
		return "", fmt.Errorf("source not found: %w", err)
	}

	name := params.Name
	if name == "" {
		name = filepath.Base(srcPath)
	}

	destDir := filepath.Join(t.pluginDir, name)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create plugin dir: %w", err)
	}

	if info.IsDir() {
		// Copy directory
		return t.copyDir(srcPath, destDir, name)
	} else {
		// Single binary - create directory layout
		binPath := filepath.Join(destDir, name)
		if err := copyFile(srcPath, binPath); err != nil {
			return "", err
		}
		if err := os.Chmod(binPath, 0o755); err != nil {
			return "", fmt.Errorf("chmod: %w", err)
		}
		// Look for manifest next to binary
		manifestSrc := srcPath + ".toml"
		manifestDest := filepath.Join(destDir, "manifest.toml")
		if _, err := os.Stat(manifestSrc); err == nil {
			if err := copyFile(manifestSrc, manifestDest); err != nil {
				return "", err
			}
		}
		return fmt.Sprintf("Plugin installed at %s", destDir), nil
	}
}

func (t *GeniePluginInstallTool) copyDir(src, dest, name string) (string, error) {
	// Copy all files
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		destPath := filepath.Join(dest, rel)
		if info.IsDir() {
			return os.MkdirAll(destPath, 0o755)
		}
		return copyFile(path, destPath)
	})
	if err != nil {
		return "", err
	}
	// Ensure binary is executable
	binPath := filepath.Join(dest, name)
	if info, err := os.Stat(binPath); err == nil && !info.IsDir() {
		os.Chmod(binPath, 0o755)
	}
	return fmt.Sprintf("Plugin installed at %s", dest), nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = in.WriteTo(out)
	return err
}

// GeniePluginRemoveTool removes an installed plugin.
type GeniePluginRemoveTool struct {
	pluginDir string
}

func NewGeniePluginRemoveTool(pluginDir string) *GeniePluginRemoveTool {
	return &GeniePluginRemoveTool{pluginDir: pluginDir}
}

func (t *GeniePluginRemoveTool) Name() string        { return "genie_plugin_remove" }
func (t *GeniePluginRemoveTool) Description() string { return "Remove an installed plugin" }

func (t *GeniePluginRemoveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Plugin name to remove",
			},
		},
		"required": []any{"name"},
	}
}

func (t *GeniePluginRemoveTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	pluginPath := filepath.Join(t.pluginDir, params.Name)
	if err := os.RemoveAll(pluginPath); err != nil {
		return "", fmt.Errorf("remove plugin: %w", err)
	}

	return fmt.Sprintf("Plugin %s removed", params.Name), nil
}

// GeniePluginListTool lists installed plugins.
type GeniePluginListTool struct {
	pluginDir string
}

func NewGeniePluginListTool(pluginDir string) *GeniePluginListTool {
	return &GeniePluginListTool{pluginDir: pluginDir}
}

func (t *GeniePluginListTool) Name() string        { return "genie_plugin_list" }
func (t *GeniePluginListTool) Description() string { return "List installed plugins with status" }

func (t *GeniePluginListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{},
	}
}

func (t *GeniePluginListTool) Execute(args json.RawMessage) (string, error) {
	entries, err := os.ReadDir(t.pluginDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "No plugins installed (plugin directory does not exist)", nil
		}
		return "", fmt.Errorf("read plugin dir: %w", err)
	}

	var plugins []string
	for _, e := range entries {
		if e.IsDir() {
			name := e.Name()
			manifestPath := filepath.Join(t.pluginDir, name, "manifest.toml")
			binPath := filepath.Join(t.pluginDir, name, name)
			status := "directory"
			if _, err := os.Stat(binPath); err == nil {
				status = "ready"
			}
			if _, err := os.Stat(manifestPath); err == nil {
				status += " (with manifest)"
			}
			plugins = append(plugins, fmt.Sprintf("  %s - %s", name, status))
		}
	}

	if len(plugins) == 0 {
		return "No plugins installed", nil
	}
	return "Installed plugins:\n" + strings.Join(plugins, "\n"), nil
}

// Ensure tools implement the tools.Tool interface
var _ tools.Tool = (*GeniePluginCreateTool)(nil)
var _ tools.Tool = (*GeniePluginInstallTool)(nil)
var _ tools.Tool = (*GeniePluginRemoveTool)(nil)
var _ tools.Tool = (*GeniePluginListTool)(nil)