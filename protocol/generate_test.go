package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGenerateProducesValidGo(t *testing.T) {
	data, err := os.ReadFile("schema.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var schema Schema
	if err := yaml.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	output, err := generate(schema)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	src := string(output)

	// Must have package declaration
	if !strings.Contains(src, "package wireplugin") {
		t.Error("missing package wireplugin declaration")
	}

	// Must have required imports
	for _, imp := range []string{"bufio", "encoding/json", "fmt", "os"} {
		if !strings.Contains(src, imp) {
			t.Errorf("missing import %q", imp)
		}
	}

	// Must define protocol version
	if !strings.Contains(src, "ProtocolVersion = 1") {
		t.Error("missing ProtocolVersion constant")
	}
}

func TestGenerateMethodConstants(t *testing.T) {
	output := generateFromSchema(t)
	src := string(output)

	expected := []string{
		`MethodCapabilitiesList = "capabilities/list"`,
		`MethodWireInit = "wire/init"`,
		`MethodWireStream = "wire/stream"`,
		`MethodWireListModels = "wire/list_models"`,
		`MethodContextBeforeRequest = "context/before_request"`,
		`MethodContextAfterResponse = "context/after_response"`,
		`MethodContextCompact = "context/compact"`,
		`MethodContextCondense = "context/condense"`,
		`MethodCommandsList = "commands/list"`,
		`MethodCommandsRun = "commands/run"`,
		`MethodCommandsHelp = "commands/help"`,
		`MethodPing = "ping"`,
		`MethodShutdown = "shutdown"`,
	}

	for _, want := range expected {
		if !strings.Contains(src, want) {
			t.Errorf("missing method constant: %s", want)
		}
	}
}

func TestGenerateErrorCodes(t *testing.T) {
	output := generateFromSchema(t)
	src := string(output)

	expected := []string{
		"ParseError = -32700",
		"MethodNotFound = -32601",
		"InternalError = -32603",
	}

	for _, want := range expected {
		if !strings.Contains(src, want) {
			t.Errorf("missing error code: %s", want)
		}
	}
}

func TestGenerateTypes(t *testing.T) {
	output := generateFromSchema(t)
	src := string(output)

	typeNames := []string{
		"type Request struct",
		"type Response struct",
		"type Error struct",
		"type Capabilities struct",
		"type WireInitResult struct",
		"type ModelDef struct",
		"type WireListModelsResult struct",
		"type CommandDef struct",
		"type CommandsListResult struct",
		"type CommandsRunParams struct",
		"type CommandsRunResult struct",
		"type CommandsHelpParams struct",
		"type CommandsHelpResult struct",
	}

	for _, want := range typeNames {
		if !strings.Contains(src, want) {
			t.Errorf("missing type: %s", want)
		}
	}

	if !strings.Contains(src, "ContextWindow int `json:\"context_window,omitempty\"`") {
		t.Error("missing ModelDef.ContextWindow field")
	}
}

func TestGenerateHandler(t *testing.T) {
	output := generateFromSchema(t)
	src := string(output)

	handlerParts := []string{
		"type Handler struct",
		"func NewHandler(caps Capabilities) *Handler",
		"func (h *Handler) SetModels(",
		"func (h *Handler) OnInit(",
		"func (h *Handler) OnCommandsList(",
		"func (h *Handler) OnCommandsRun(",
		"func (h *Handler) OnCommandsHelp(",
		"func (h *Handler) OnStream(",
		"func (h *Handler) OnBeforeRequest(",
		"func (h *Handler) OnAfterResponse(",
		"func (h *Handler) OnCompact(",
		"func (h *Handler) OnCondense(",
		"func (h *Handler) Run() error",
		"func (h *Handler) handleRequest(",
		"func (h *Handler) writeResult(",
		"func (h *Handler) writeRawResult(",
		"func (h *Handler) writeError(",
	}

	for _, want := range handlerParts {
		if !strings.Contains(src, want) {
			t.Errorf("missing handler part: %s", want)
		}
	}
}

func TestGenerateDispatch(t *testing.T) {
	output := generateFromSchema(t)
	src := string(output)

	cases := []string{
		"case MethodCapabilitiesList:",
		"case MethodWireInit:",
		"case MethodCommandsList:",
		"case MethodCommandsRun:",
		"case MethodCommandsHelp:",
		"case MethodWireListModels:",
		"case MethodWireStream:",
		"case MethodPing:",
		"case MethodShutdown:",
		"default:",
	}

	for _, want := range cases {
		if !strings.Contains(src, want) {
			t.Errorf("missing dispatch case: %s", want)
		}
	}
}

func TestGenerateParseParams(t *testing.T) {
	output := generateFromSchema(t)
	src := string(output)

	if !strings.Contains(src, "func ParseParams[T any](") {
		t.Error("missing ParseParams generic function")
	}
}

func TestGenerateStructTags(t *testing.T) {
	output := generateFromSchema(t)
	src := string(output)

	// Check that struct tags use backticks properly
	if !strings.Contains(src, "`json:\"jsonrpc\"`") {
		t.Error("missing jsonrpc struct tag")
	}
	if !strings.Contains(src, "`json:\"params,omitempty\"`") {
		t.Error("missing params omitempty struct tag")
	}
	if !strings.Contains(src, "`json:\"name,omitempty\"`") {
		t.Error("missing name omitempty struct tag")
	}
}

func generateFromSchema(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile("schema.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var schema Schema
	if err := yaml.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	output, err := generate(schema)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	return output
}
