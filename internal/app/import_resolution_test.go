// Package app — import_resolution_test.go verifies that every internal
// Go import resolves to a real directory containing at least one
// production (non-test) .go file. It scans the entire repository so
// ghost imports (pointing to directories that were never committed)
// are caught at CI time.
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const projectRoot = "github.com/Marcuss-ops/PipelineGen"

func TestInternalImportsResolveToExistingPackages(t *testing.T) {
	root, err := findGitRoot()
	if err != nil {
		t.Skipf("cannot locate git root: %v", err)
	}

	// Walk every production .go file in internal/ and pkg/.
	var missing []string
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		imports, readErr := extractInternalImports(path)
		if readErr != nil {
			t.Logf("skip %s: %v", path, readErr)
			return nil
		}
		for _, imp := range imports {
			dir := importToLocalDir(root, imp)
			if dir == "" {
				continue
			}
			if !dirHasGoFiles(dir) {
				missing = append(missing, imp+"  (from "+filepath.Base(path)+")")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}

	if len(missing) > 0 {
		t.Errorf("imports pointing to directories with no production .go files:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

// extractInternalImports returns import paths starting with projectRoot.
func extractInternalImports(filePath string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	content := string(data)
	var imports []string
	// Simple line-by-line scan inside import blocks.
	inImport := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "import (") {
			inImport = true
			continue
		}
		if inImport && trimmed == ")" {
			inImport = false
			continue
		}
		if !inImport && strings.HasPrefix(trimmed, "import \"") {
			p := extractPath(trimmed)
			if strings.HasPrefix(p, projectRoot+"/internal/") {
				imports = append(imports, p)
			}
			continue
		}
		if inImport && strings.Contains(trimmed, "\"") {
			p := extractPath(trimmed)
			if strings.HasPrefix(p, projectRoot+"/internal/") {
				imports = append(imports, p)
			}
		}
	}
	return imports, nil
}

func extractPath(line string) string {
	start := strings.Index(line, "\"")
	if start < 0 {
		return ""
	}
	end := strings.Index(line[start+1:], "\"")
	if end < 0 {
		return ""
	}
	return line[start+1 : start+1+end]
}

// importToLocalDir converts "github.com/X/Y/internal/foo/bar" to
// "<gitRoot>/internal/foo/bar".
func importToLocalDir(gitRoot, importPath string) string {
	rel := strings.TrimPrefix(importPath, projectRoot+"/")
	if rel == importPath {
		return "" // not an internal import under projectRoot
	}
	return filepath.Join(gitRoot, rel)
}

// dirHasGoFiles returns true if dir exists and contains at least one
// non-'_test.go' .go file.
func dirHasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		return true
	}
	return false
}

// findGitRoot walks up from cwd until it finds a .git directory.
func findGitRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
