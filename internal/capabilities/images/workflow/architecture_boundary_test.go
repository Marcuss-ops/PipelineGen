package workflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestApplicationImagesHasNoChromeInfrastructure(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filename)
	entries, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatalf("glob application image files: %v", err)
	}
	fset := token.NewFileSet()
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath == "os/exec" ||
				strings.HasPrefix(importPath, "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/images/chrome") ||
				strings.Contains(importPath, "/visual_validate") {
				t.Errorf("%s imports concrete Chrome infrastructure: %s", filepath.Base(path), importPath)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if ok && (ident.Name == "ChromeImageProvider" || ident.Name == "ChromeImageProviderPool") {
				t.Errorf("%s references concrete Chrome implementation symbol %q", filepath.Base(path), ident.Name)
			}
			return true
		})
	}
}
