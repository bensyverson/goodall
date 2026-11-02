package livemodel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// TestPinsListsEveryModelConstant holds the list to the constants, since a pin
// added without a row would never be checked against a vendor's catalog.
func TestPinsListsEveryModelConstant(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "livemodel.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{}
	for _, p := range Pins {
		listed[p.Const] = p.Model
	}
	seen := 0
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			value, err := strconv.Unquote(vs.Values[0].(*ast.BasicLit).Value)
			if err != nil {
				t.Fatal(err)
			}
			seen++
			if got, ok := listed[vs.Names[0].Name]; !ok {
				t.Errorf("%s is not in Pins", vs.Names[0].Name)
			} else if got != value {
				t.Errorf("Pins has %s as %q, want %q", vs.Names[0].Name, got, value)
			}
		}
	}
	if seen == 0 || seen != len(Pins) {
		t.Errorf("%d constants, %d pins", seen, len(Pins))
	}
}
