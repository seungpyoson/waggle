package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOwnershipHasNoAlternateDatabaseOrProviderConstructors(t *testing.T) {
	files := token.NewFileSet()
	opens := 0
	for _, root := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(files, path, nil, 0)
			if err != nil {
				return err
			}
			imports := make(map[string]string)
			for _, imp := range file.Imports {
				pkg, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				name := filepath.Base(pkg)
				if imp.Name != nil {
					name = imp.Name.Name
				}
				imports[name] = pkg
			}
			owned := strings.HasPrefix(path, "internal/brokerstate/")
			ast.Inspect(file, func(node ast.Node) bool {
				switch n := node.(type) {
				case *ast.CallExpr:
					selector, ok := n.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					alias, ok := selector.X.(*ast.Ident)
					if !ok {
						return true
					}
					if imports[alias.Name] == "database/sql" && selector.Sel.Name == "Open" {
						opens++
						if !owned {
							t.Errorf("raw canonical open outside owner: %s", files.Position(n.Pos()))
						}
					}
					if imports[alias.Name] == "os" && (selector.Sel.Name == "Remove" || selector.Sel.Name == "RemoveAll") && !owned && !strings.HasPrefix(path, "internal/install/") {
						t.Errorf("cleanup outside ownership or integration installer: %s", files.Position(n.Pos()))
					}
				case *ast.StarExpr:
					selector, ok := n.X.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					alias, ok := selector.X.(*ast.Ident)
					if ok && imports[alias.Name] == "database/sql" && selector.Sel.Name == "DB" && !owned {
						t.Errorf("raw database capability escaped ownership: %s", files.Position(n.Pos()))
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if opens != 1 {
		t.Fatalf("canonical database open sites: %d, want 1", opens)
	}
}
