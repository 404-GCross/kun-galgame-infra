package editing_test

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestEngineImportsNoFamilyPackages pins the engine's zero-family-dependency
// rule (charter ruling 1): field knowledge only ever arrives by injection,
// so this package must never import an entity family. The repo-wide archtest
// already forbids galgame for every platform package; this test additionally
// forbids catalog (and any future family) for the engine specifically —
// non-test files only, since the tests here fake a family on purpose.
func TestEngineImportsNoFamilyPackages(t *testing.T) {
	forbidden := []string{
		"api/internal/platform/galgame",
		"api/internal/platform/catalog",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", name, err)
			}
			for _, fb := range forbidden {
				if path == fb || strings.HasPrefix(path, fb+"/") {
					t.Errorf("%s:%d: the editing engine must not import family package %s",
						name, fset.Position(imp.Path.Pos()).Line, path)
				}
			}
		}
	}
}
