// Command generate reads protocol/schema.yaml and outputs a standalone Go
// wireplugin package that plugins can vendor as internal/wireplugin/.
//
// Usage:
//
//	go run ./protocol -schema protocol/schema.yaml -out /path/to/internal/wireplugin
package main

import (
	"embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

//go:embed wireplugin.go.tmpl
var templateFS embed.FS

// Schema is the top-level YAML structure.

type Schema struct {
	Protocol   Protocol      `yaml:"protocol"`
	Constants  []Constant    `yaml:"constants"`
	ErrorCodes []ErrorCode   `yaml:"error_codes"`
	Types      []TypeDef     `yaml:"types"`
	Handler    HandlerDef    `yaml:"handler"`
	ParseParam ParseParamDef `yaml:"parse_params"`
}

type Protocol struct {
	Version   int    `yaml:"version"`
	Package   string `yaml:"package"`
	Transport string `yaml:"transport"`
	Encoding  string `yaml:"encoding"`
}

type Constant struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type ErrorCode struct {
	Name  string `yaml:"name"`
	Value int    `yaml:"value"`
}

type TypeDef struct {
	Name   string     `yaml:"name"`
	Fields []FieldDef `yaml:"fields"`
}

type FieldDef struct {
	Name      string `yaml:"name"`
	JSON      string `yaml:"json"`
	Type      string `yaml:"type"`
	Omitempty bool   `yaml:"omitempty"`
}

type HandlerDef struct {
	Struct        HandlerStruct  `yaml:"struct"`
	Methods       []MethodDef    `yaml:"methods"`
	StaticMethods []MethodDef    `yaml:"static_methods"`
	Dispatch      []DispatchCase `yaml:"dispatch"`
}

type HandlerStruct struct {
	Fields []HandlerField `yaml:"fields"`
}

type HandlerField struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

type MethodDef struct {
	Name     string     `yaml:"name"`
	Receiver string     `yaml:"receiver"`
	Params   []ParamDef `yaml:"params"`
	Returns  []string   `yaml:"returns"`
}

type ParamDef struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

type DispatchCase struct {
	Method string `yaml:"method"`
	Action string `yaml:"action"`
}

type ParseParamDef struct {
	Name    string     `yaml:"name"`
	Generic bool       `yaml:"generic"`
	Params  []ParamDef `yaml:"params"`
	Returns []string   `yaml:"returns"`
}

func main() {
	schemaPath := flag.String("schema", "protocol/schema.yaml", "path to schema YAML")
	outDir := flag.String("out", "", "output directory (default: stdout)")
	flag.Parse()

	data, err := os.ReadFile(*schemaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read schema: %v\n", err)
		os.Exit(1)
	}

	var schema Schema
	if err := yaml.Unmarshal(data, &schema); err != nil {
		fmt.Fprintf(os.Stderr, "parse schema: %v\n", err)
		os.Exit(1)
	}

	output, err := generate(schema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate: %v\n", err)
		os.Exit(1)
	}

	if *outDir != "" {
		if err := os.MkdirAll(*outDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
			os.Exit(1)
		}
		path := filepath.Join(*outDir, "wireplugin.go")
		if err := os.WriteFile(path, output, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", path)
	} else {
		fmt.Print(string(output))
	}
}

func generate(schema Schema) ([]byte, error) {
	tmplData, err := templateFS.ReadFile("wireplugin.go.tmpl")
	if err != nil {
		return nil, fmt.Errorf("read template: %w", err)
	}

	tmpl, err := template.New("wireplugin").Funcs(template.FuncMap{
		"join":       strings.Join,
		"lower":      strings.ToLower,
		"hasPrefix":  strings.HasPrefix,
		"trimPrefix": strings.TrimPrefix,
		"contains":   strings.Contains,
		"bt":         func() string { return "`" },
	}).Parse(string(tmplData))
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, schema); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	return []byte(buf.String()), nil
}
