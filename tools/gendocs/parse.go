package gendocs

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// constDef is one exported string constant.
type constDef struct {
	Name  string
	Type  string // named type of the constant; empty when untyped
	Value string
	Doc   string
}

// fieldDef is one exported struct field.
type fieldDef struct {
	Name      string
	JSON      string
	Type      string
	Doc       string
	Omitempty bool
}

// structDef is one exported struct type.
type structDef struct {
	Name   string
	Doc    string
	Fields []fieldDef
}

// funcDef is one exported function's documentation.
type funcDef struct {
	Name string
	Doc  string
}

// packageInfo is the documentation-relevant surface of a Go package.
type packageInfo struct {
	Name    string
	Doc     string
	Consts  []constDef
	Structs []structDef
	Funcs   []funcDef
}

// parsePackage parses the non-test Go files in dir and extracts the package's
// documented surface. Files are processed in sorted order (ReadDir sorts) so
// extraction is deterministic.
func parsePackage(dir string) (packageInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return packageInfo{}, fmt.Errorf("read %s: %w", dir, err)
	}

	fset := token.NewFileSet()
	var info packageInfo
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, ".") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			return packageInfo{}, fmt.Errorf("parse %s: %w", name, err)
		}
		if info.Name == "" {
			info.Name = file.Name.Name
		}
		if info.Doc == "" && file.Doc != nil {
			info.Doc = file.Doc.Text()
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				collectDecl(&info, d)
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.IsExported() && d.Doc != nil {
					info.Funcs = append(info.Funcs, funcDef{Name: d.Name.Name, Doc: d.Doc.Text()})
				}
			}
		}
	}
	if info.Name == "" {
		return packageInfo{}, fmt.Errorf("no non-test Go files found in %s", dir)
	}
	return info, nil
}

// collectDecl extracts exported string constants and struct types from one
// declaration group.
func collectDecl(info *packageInfo, d *ast.GenDecl) {
	switch d.Tok {
	case token.CONST:
		for _, spec := range d.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) == 0 || len(vs.Values) == 0 || !vs.Names[0].IsExported() {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			typeName := ""
			if ident, ok := vs.Type.(*ast.Ident); ok {
				typeName = ident.Name
			}
			doc := ""
			if vs.Doc != nil {
				doc = vs.Doc.Text()
			} else if d.Doc != nil {
				doc = d.Doc.Text()
			}
			doc = cleanDoc(doc)
			doc = strings.TrimPrefix(doc, vs.Names[0].Name+" ")
			info.Consts = append(info.Consts, constDef{
				Name:  vs.Names[0].Name,
				Type:  typeName,
				Value: value,
				Doc:   doc,
			})
		}
	case token.TYPE:
		for _, spec := range d.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || !ts.Name.IsExported() {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			sd := structDef{Name: ts.Name.Name}
			if ts.Doc != nil {
				sd.Doc = cleanDoc(ts.Doc.Text())
			} else if d.Doc != nil {
				sd.Doc = cleanDoc(d.Doc.Text())
			}
			for _, field := range st.Fields.List {
				fieldType := types.ExprString(field.Type)
				name, omitempty := splitJSONTag(field.Tag)
				doc := ""
				if field.Doc != nil {
					doc = cleanDoc(field.Doc.Text())
				} else if field.Comment != nil {
					doc = cleanDoc(field.Comment.Text())
				}
				for _, fieldName := range field.Names {
					if !fieldName.IsExported() {
						continue
					}
					sd.Fields = append(sd.Fields, fieldDef{
						Name:      fieldName.Name,
						JSON:      name,
						Type:      fieldType,
						Doc:       doc,
						Omitempty: omitempty,
					})
				}
			}
			info.Structs = append(info.Structs, sd)
		}
	}
}

// splitJSONTag extracts the json field name and omitempty flag from a struct
// tag literal. Tags without a json key yield an empty name.
func splitJSONTag(tag *ast.BasicLit) (name string, omitempty bool) {
	if tag == nil {
		return "", false
	}
	for pair := range strings.FieldsSeq(strings.Trim(tag.Value, "`")) {
		key, value, found := strings.Cut(pair, ":")
		if !found || key != "json" {
			continue
		}
		value = strings.Trim(value, `"`)
		parts := strings.Split(value, ",")
		if parts[0] == "-" || parts[0] == "" {
			return parts[0], false
		}
		for _, opt := range parts[1:] {
			if opt == "omitempty" {
				omitempty = true
			}
		}
		return parts[0], omitempty
	}
	return "", false
}

// cleanDoc collapses a doc comment onto one line so it renders safely inside
// markdown tables.
func cleanDoc(doc string) string {
	return strings.Join(strings.Fields(doc), " ")
}
