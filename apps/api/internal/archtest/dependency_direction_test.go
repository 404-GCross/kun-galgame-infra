package archtest

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const productDomain = "galgame"

func TestPlatformMustNotImportGalgame(t *testing.T) {
	root := moduleRoot(t)
	forbidden := modulePath(t, root) + "/internal/platform/" + productDomain

	platformDir := filepath.Join(root, "internal", "platform")
	if _, err := os.Stat(filepath.Join(platformDir, productDomain)); err != nil {
		t.Fatalf("product domain directory missing (layout changed?): %v", err)
	}

	entries, err := os.ReadDir(platformDir)
	if err != nil {
		t.Fatalf("read %s: %v", platformDir, err)
	}
	var protected []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != productDomain {
			protected = append(protected, filepath.Join(platformDir, e.Name()))
		}
	}
	if len(protected) == 0 {
		t.Fatal("no protected platform domains found under internal/platform")
	}
	protected = append(protected, filepath.Join(root, "internal", "infrastructure"))

	var violations []string
	for _, dir := range protected {
		v, err := findForbiddenImports(dir, root, forbidden)
		if err != nil {
			t.Fatalf("scan %s: %v", dir, err)
		}
		violations = append(violations, v...)
	}
	if len(violations) > 0 {
		t.Errorf("platform/infrastructure packages must not import %s:\n  %s",
			forbidden, strings.Join(violations, "\n  "))
	}
}

func findForbiddenImports(dir, root, forbidden string) ([]string, error) {
	fset := token.NewFileSet()
	var violations []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return fmt.Errorf("unquote import in %s: %w", path, err)
			}
			if p == forbidden || strings.HasPrefix(p, forbidden+"/") {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				line := fset.Position(imp.Path.Pos()).Line
				violations = append(violations, fmt.Sprintf("%s:%d imports %s", rel, line, p))
			}
		}
		return nil
	})
	return violations, err
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod not found at %s (layout changed?): %v", root, err)
	}
	return root
}

func modulePath(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatal("module directive not found in go.mod")
	return ""
}
