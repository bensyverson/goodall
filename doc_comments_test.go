package goodall

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestEveryExportedIdentifierHasADocComment walks every Go source file in the
// module and fails on an exported identifier with no doc comment: top-level
// functions, types, constants and variables, methods, struct fields and
// interface methods. A constant or variable inside a grouped declaration may
// share the group's comment, as go/doc allows.
//
// It is a test rather than a linter configuration so that it runs wherever the
// suite runs, including the pre-commit hook, and needs no tool the repository
// does not already have.
func TestEveryExportedIdentifierHasADocComment(t *testing.T) {
	var missing []string
	for _, path := range moduleSourceFiles(t) {
		missing = append(missing, undocumented(t, path)...)
	}
	slices.Sort(missing)
	for _, m := range missing {
		t.Error(m)
	}
	if len(missing) > 0 {
		t.Logf("%d exported identifiers have no doc comment", len(missing))
	}
}

// moduleSourceFiles lists every non-test Go file under the module root,
// skipping dot-directories (the agent worktrees live under .claude) and
// testdata.
func moduleSourceFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != "." && (strings.HasPrefix(name, ".") || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("found no Go files; the test must run from the module root")
	}
	return files
}

// undocumented reports every exported identifier in one file that has no doc
// comment, as "path:line: what".
func undocumented(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	var out []string
	report := func(pos token.Pos, what string) {
		out = append(out, fset.Position(pos).String()+": "+what+" has no doc comment")
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() && d.Doc == nil && receiverIsExported(d) {
				report(d.Pos(), "func "+funcName(d))
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() && s.Doc == nil && d.Doc == nil {
						report(s.Pos(), "type "+s.Name.Name)
					}
					if s.Name.IsExported() {
						out = append(out, undocumentedMembers(fset, s)...)
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.IsExported() && s.Doc == nil && d.Doc == nil {
							report(name.Pos(), name.Name)
						}
					}
				}
			}
		}
	}
	return out
}

// undocumentedMembers reports the exported fields of a struct type and the
// exported methods of an interface type that have no doc comment.
func undocumentedMembers(fset *token.FileSet, spec *ast.TypeSpec) []string {
	var fields *ast.FieldList
	switch typ := spec.Type.(type) {
	case *ast.StructType:
		fields = typ.Fields
	case *ast.InterfaceType:
		fields = typ.Methods
	default:
		return nil
	}
	var out []string
	for _, field := range fields.List {
		if field.Doc != nil {
			continue
		}
		for _, name := range field.Names {
			if name.IsExported() {
				out = append(out, fset.Position(name.Pos()).String()+": "+spec.Name.Name+"."+name.Name+" has no doc comment")
			}
		}
	}
	return out
}

// receiverIsExported reports whether a method's receiver type is exported, so
// an exported method on an unexported type — invisible to a consumer — is not
// held to the rule.
func receiverIsExported(d *ast.FuncDecl) bool {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return true
	}
	return ast.IsExported(receiverTypeName(d.Recv.List[0].Type))
}

// receiverTypeName reads the type name out of a receiver, through a pointer
// and through type parameters.
func receiverTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(e.X)
	case *ast.IndexExpr:
		return receiverTypeName(e.X)
	case *ast.IndexListExpr:
		return receiverTypeName(e.X)
	case *ast.Ident:
		return e.Name
	}
	return ""
}

// funcName is "Type.Method" for a method and the bare name otherwise.
func funcName(d *ast.FuncDecl) string {
	if d.Recv != nil && len(d.Recv.List) > 0 {
		return receiverTypeName(d.Recv.List[0].Type) + "." + d.Name.Name
	}
	return d.Name.Name
}
