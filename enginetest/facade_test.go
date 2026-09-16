package enginetest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// exportedNames returns the package-level exported identifiers declared in dir:
// types, funcs, consts and vars, but not methods, which ride along on a type
// alias for free and so never need re-exporting.
func exportedNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	pkgs, err := parser.ParseDir(token.NewFileSet(), dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	names := map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, d := range file.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil && d.Name.IsExported() {
						names[d.Name.Name] = true
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							if s.Name.IsExported() {
								names[s.Name.Name] = true
							}
						case *ast.ValueSpec:
							for _, n := range s.Names {
								if n.IsExported() {
									names[n.Name] = true
								}
							}
						}
					}
				}
			}
		}
	}
	return names
}

// The root package is a facade: it holds the published import path and aliases
// everything the engine package declares. Nothing enforces that at compile time
// — an engine that grows a new exported name simply becomes unreachable from
// the import every embedder actually wrote, silently, and the gap is only found
// by the person it fails. So it is enforced here instead.
func TestEverythingTheEngineExportsIsReachableFromThePublishedImportPath(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	facade := exportedNames(t, root)
	var missing []string
	for name := range exportedNames(t, filepath.Join(root, "engine")) {
		if !facade[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("engine exports %s, which an embedder importing github.com/Coder8124/logos cannot reach; alias them in logos.go", strings.Join(missing, ", "))
	}
}
