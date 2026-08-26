package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestGeneratedMatchesSharedProtocol(t *testing.T) {
	output := generateFromSchema(t)

	// Parse generated code
	genFile, err := parser.ParseFile(token.NewFileSet(), "wireplugin.go", output, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse generated code: %v", err)
	}

	// Parse original shared code - find it relative to this test file
	origFile, err := parser.ParseFile(token.NewFileSet(), "../plugins/shared/protocol.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse original code: %v", err)
	}

	// Compare exported types
	genTypes := extractTypes(genFile)
	origTypes := extractTypes(origFile)

	// Original has fewer types (no ToolDef, ContentItem, etc. - those are host-side)
	// But all wire plugin types must match
	requiredTypes := []string{
		"Request", "Response", "Error", "Capabilities",
		"WireInitResult", "ModelDef", "WireListModelsResult",
	}

	for _, name := range requiredTypes {
		if _, ok := genTypes[name]; !ok {
			t.Errorf("generated code missing type: %s", name)
		}
		if _, ok := origTypes[name]; !ok {
			t.Errorf("original code missing type: %s", name)
		}
	}

	// Compare exported functions
	genFuncs := extractFuncs(genFile)
	origFuncs := extractFuncs(origFile)

	requiredFuncs := []string{
		"NewHandler", "ParseParams",
	}

	for _, name := range requiredFuncs {
		if _, ok := genFuncs[name]; !ok {
			t.Errorf("generated code missing function: %s", name)
		}
		if _, ok := origFuncs[name]; !ok {
			t.Errorf("original code missing function: %s", name)
		}
	}

	// Compare method sets on Handler
	genMethods := extractMethods(genFile, "Handler")
	origMethods := extractMethods(origFile, "Handler")

	requiredMethods := []string{
		"SetModels", "OnInit", "OnStream", "Run",
	}

	for _, name := range requiredMethods {
		if !genMethods[name] {
			t.Errorf("generated code missing Handler method: %s", name)
		}
		if !origMethods[name] {
			t.Errorf("original code missing Handler method: %s", name)
		}
	}

	// Compare method constants
	genConsts := extractConsts(genFile)
	origConsts := extractConsts(origFile)

	requiredConsts := []string{
		"MethodCapabilitiesList", "MethodWireInit", "MethodWireStream",
		"MethodWireListModels", "MethodPing", "MethodShutdown",
	}

	for _, name := range requiredConsts {
		if _, ok := genConsts[name]; !ok {
			t.Errorf("generated code missing constant: %s", name)
		}
		if _, ok := origConsts[name]; !ok {
			t.Errorf("original code missing constant: %s", name)
		}
	}

	// Verify constant values match
	for _, name := range requiredConsts {
		genVal, genOk := genConsts[name]
		origVal, origOk := origConsts[name]
		if !genOk {
			t.Errorf("generated code missing constant value: %s", name)
		}
		if !origOk {
			t.Errorf("original code missing constant value: %s", name)
		}
		if genOk && origOk && genVal != origVal {
			t.Errorf("constant %s mismatch: generated=%q original=%q", name, genVal, origVal)
		}
	}
}

func extractTypes(file *ast.File) map[string]bool {
	types := make(map[string]bool)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if ok {
				types[ts.Name.Name] = true
			}
		}
	}
	return types
}

func extractFuncs(file *ast.File) map[string]bool {
	funcs := make(map[string]bool)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil {
			funcs[fn.Name.Name] = true
		}
	}
	return funcs
}

func extractMethods(file *ast.File, typeName string) map[string]bool {
	methods := make(map[string]bool)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			continue
		}
		recv := fn.Recv.List[0].Type
		if ident, ok := recv.(*ast.Ident); ok && ident.Name == typeName {
			methods[fn.Name.Name] = true
		}
		if star, ok := recv.(*ast.StarExpr); ok {
			if ident, ok := star.X.(*ast.Ident); ok && ident.Name == typeName {
				methods[fn.Name.Name] = true
			}
		}
	}
	return methods
}

func extractConsts(file *ast.File) map[string]string {
	consts := make(map[string]string)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i < len(vs.Values) {
					val := extractValue(vs.Values[i])
					consts[name.Name] = val
				}
			}
		}
	}
	return consts
}

func extractValue(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.BasicLit:
		return strings.Trim(v.Value, "\"")
	case *ast.UnaryExpr:
		return v.Op.String() + extractValue(v.X)
	default:
		return ""
	}
}
