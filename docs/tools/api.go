//go:build ignore

// Generate the API index from public declarations. Resolve root aliases so the reference also
// shows the fields and methods that applications use through the public package.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type symbolKind string

const (
	// symbolType identifies a public type declaration.
	symbolType symbolKind = "type"
	// symbolFunction identifies a public function or method.
	symbolFunction symbolKind = "function"
	// symbolValue identifies a public constant or variable.
	symbolValue symbolKind = "value"
)

type symbol struct {
	Name    string     `json:"name"`
	Package string     `json:"package"`
	Kind    symbolKind `json:"kind"`
	Code    string     `json:"code"`
	Doc     string     `json:"doc"`
	Source  string     `json:"source"`
	Line    int        `json:"line"`
	Alias   string     `json:"-"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	packages := []string{".", "agents", "agents/react", "executor", "models", "toolchain",
		"toolchain/jsruntime", "output", "schema", "compaction", "format", "search", "policy",
		"trace", "common", "internal/core"}
	var symbols []symbol
	for _, path := range packages {
		items, err := extract(root, path)
		if err != nil {
			return err
		}
		symbols = append(symbols, items...)
	}
	core := map[string]symbol{}
	for _, item := range symbols {
		if item.Package == "internal/core" {
			core[item.Name] = item
		}
	}
	var public []symbol
	for _, item := range symbols {
		if item.Package == "internal/core" {
			continue
		}
		if original, found := core[item.Alias]; found {
			item.Code = original.Code
			item.Source, item.Line = original.Source, original.Line
			if item.Doc == "" {
				item.Doc = original.Doc
			}
			for name, method := range core {
				if strings.HasPrefix(name, item.Alias+".") {
					method.Name = item.Name + strings.TrimPrefix(name, item.Alias)
					method.Package = item.Package
					public = append(public, method)
				}
			}
		}
		public = append(public, item)
	}
	sort.Slice(public, func(i, j int) bool {
		return public[i].Package+"/"+public[i].Name < public[j].Package+"/"+public[j].Name
	})
	// Build-tag variants can declare the same public name. One reference entry owns that name.
	unique := make([]symbol, 0, len(public))
	for _, item := range public {
		if len(unique) > 0 {
			last := unique[len(unique)-1]
			if last.Package == item.Package && last.Name == item.Name {
				continue
			}
		}
		unique = append(unique, item)
	}
	return json.NewEncoder(os.Stdout).Encode(unique)
}

func extract(root, path string) ([]symbol, error) {
	files, err := filepath.Glob(filepath.Join(root, path, "*.go"))
	if err != nil {
		return nil, err
	}
	var result []symbol
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, name, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			items := declarations(decl)
			for _, item := range items {
				item.Package = path
				if path == "." {
					item.Package = "gent"
				}
				item.Source, err = filepath.Rel(root, name)
				if err != nil {
					return nil, err
				}
				item.Line = set.Position(decl.Pos()).Line
				result = append(result, item)
			}
		}
	}
	return result, nil
}

func declarations(decl ast.Decl) []symbol {
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		if !decl.Name.IsExported() {
			return nil
		}
		name := decl.Name.Name
		if decl.Recv != nil {
			receiver := typeName(decl.Recv.List[0].Type)
			if !ast.IsExported(receiver) {
				return nil
			}
			name = receiver + "." + name
		}
		doc := decl.Doc.Text()
		decl.Body, decl.Doc = nil, nil
		return []symbol{{Name: name, Kind: symbolFunction, Code: code(decl), Doc: doc}}
	case *ast.GenDecl:
		var result []symbol
		for _, spec := range decl.Specs {
			switch spec := spec.(type) {
			case *ast.TypeSpec:
				if !spec.Name.IsExported() {
					continue
				}
				item := symbol{Name: spec.Name.Name, Kind: symbolType,
					Doc: textOr(spec.Doc.Text(), decl.Doc.Text())}
				if spec.Assign.IsValid() {
					item.Alias = typeName(spec.Type)
				}
				if structure, ok := spec.Type.(*ast.StructType); ok {
					var fields []*ast.Field
					for _, field := range structure.Fields.List {
						if len(field.Names) == 0 || field.Names[0].IsExported() {
							fields = append(fields, field)
						}
					}
					structure.Fields.List = fields
				}
				item.Code = code(&ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{spec}})
				result = append(result, item)
			case *ast.ValueSpec:
				for index, name := range spec.Names {
					if !name.IsExported() {
						continue
					}
					value := &ast.ValueSpec{Names: []*ast.Ident{name}, Type: spec.Type}
					if index < len(spec.Values) {
						value.Values = []ast.Expr{spec.Values[index]}
					}
					item := symbol{Name: name.Name, Kind: symbolValue,
						Code: code(&ast.GenDecl{Tok: decl.Tok, Specs: []ast.Spec{value}}),
						Doc:  textOr(spec.Doc.Text(), decl.Doc.Text())}
					if len(value.Values) == 1 {
						if selector, ok := value.Values[0].(*ast.SelectorExpr); ok {
							if owner, ok := selector.X.(*ast.Ident); ok && owner.Name == "core" {
								item.Alias = selector.Sel.Name
							}
						}
					}
					result = append(result, item)
				}
			}
		}
		return result
	}
	return nil
}

func typeName(value ast.Expr) string {
	switch value := value.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return typeName(value.X)
	case *ast.SelectorExpr:
		return value.Sel.Name
	case *ast.IndexExpr:
		return typeName(value.X)
	case *ast.IndexListExpr:
		return typeName(value.X)
	}
	return ""
}

func textOr(first, second string) string {
	if first != "" {
		return first
	}
	return second
}

func code(node ast.Node) string {
	var output bytes.Buffer
	if err := format.Node(&output, token.NewFileSet(), node); err != nil {
		panic(err)
	}
	return output.String()
}
