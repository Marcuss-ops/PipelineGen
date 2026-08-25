// Package app — storage_boundary_test.go verifies that the storage
// packages contain real implementations (handlers, services, use cases),
// not just forwarding aliases or re-exports that only exist to make
// the compiler pass without providing real functionality.
package wiring

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoPath(parts ...string) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join(parts...)
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	return filepath.Join(append([]string{root}, parts...)...)
}

// TestStoragePackages_NoPhantomTypes verifies that storage packages
// contain no type-aliases that redirect to another package (anti-pattern:
// `type Handler = oldpackage.Handler`). Type aliases are permitted only
// when they serve a documented back-compat purpose with a removal plan.
func TestStoragePackages_NoPhantomTypes(t *testing.T) {
	paths := []string{
		repoPath("internal", "capabilities", "assets", "storage"),
	}
	for _, pkg := range paths {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			t.Errorf("package %s does not exist or is not readable: %v", pkg, err)
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			if strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			data, readErr := os.ReadFile(pkg + "/" + e.Name())
			if readErr != nil {
				t.Errorf("cannot read %s/%s: %v", pkg, e.Name(), readErr)
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				// Catch: type X = other.X  (type alias)
				if strings.HasPrefix(trimmed, "type ") && strings.Contains(trimmed, " = ") {
					t.Errorf("%s/%s: type alias found (phantom pattern): %s",
						pkg, e.Name(), trimmed)
				}
				// Catch: var NewX = other.NewX  (var forwarding)
				if strings.HasPrefix(trimmed, "var New") && strings.Contains(trimmed, " = ") {
					t.Errorf("%s/%s: var forwarding found (phantom pattern): %s",
						pkg, e.Name(), trimmed)
				}
			}
		}
	}
}

// TestStoragePackages_ExistAndAreNotEmpty verifies that both storage
// packages exist on disk and contain at least one production .go file
// with real declarations (not empty stubs).
func TestStoragePackages_ExistAndAreNotEmpty(t *testing.T) {
	paths := map[string]string{
		"capabilities": repoPath("internal", "capabilities", "assets", "storage"),
	}
	for role, pkg := range paths {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			t.Errorf("%s storage package (%s) does not exist: %v", role, pkg, err)
			continue
		}
		hasProd := false
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
			hasProd = true
			// Verify the file is not empty or stub-only.
			data, _ := os.ReadFile(pkg + "/" + e.Name())
			content := string(data)
			if strings.Contains(content, "// TODO(PR3): implement") {
				t.Errorf("%s/%s: file contains unimplemented TODO comment", pkg, e.Name())
			}
			if strings.TrimSpace(content) == "package storage" {
				t.Errorf("%s/%s: file is an empty stub (package declaration only)", pkg, e.Name())
			}
		}
		if !hasProd {
			t.Errorf("%s storage package (%s): no production .go files found", role, pkg)
		}
	}
}
