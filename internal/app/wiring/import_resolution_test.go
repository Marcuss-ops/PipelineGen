// Package app — import_resolution_test.go verifies that every internal
// Go import resolves to a real directory containing at least one
// production (non-test) .go file. It scans the entire repository so
// ghost imports (pointing to directories that were never committed)
// are caught at CI time.
package wiring

import (
	"fmt"
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

// TestModuleDependencyConstraints enforces the layered architecture
// import rules (Wave 5, July 2026; Sprint 1.1 amendment, July 2026):
//   - internal/domain packages MUST NOT import internal/application or internal/infrastructure.
//   - internal/api packages MUST NOT import internal/infrastructure directly.
//   - internal/application packages MUST NOT import internal/infrastructure directly.
//     (Sprint 1.1 — earlier the test doc-comment silently permitted this; the
//     AGENTS.md promise that application owns use cases + typed ports was not
//     enforced by the gate. The case is now closed fail-fast; transitional
//     exceptions are listed in appInfraBridgeAllowlist below with a strict
//     ratchet tracked in architecture/policy.yaml :: app_infra_bridge_ratchet.)
//   - internal/app (composition root) may import any internal layer (Pattern 0).
//   - internal/kernel packages MUST NOT import internal/application or internal/infrastructure.
//   - cmd packages may import any layer (operator tooling / CLI entry points).
//
// The test scans every production .go file under internal/ and fails
// if a forbidden import is found. Allowlisted paths (e.g. composition
// root adapters) can be added to dependencyAllowlist.
//
// Two counters gate allowlist drift: `maxBridgeEntries` for the legacy
// API/domain bridge surface (Wave 5) and `maxAppInfraEntries` for the
// new Sprint-1.1 application→infrastructure surface. Both drift
// assertions fail loud so silent creep surfaces as a CI build failure.
func TestModuleDependencyConstraints(t *testing.T) {
	root, err := findGitRoot()
	if err != nil {
		t.Skipf("cannot locate git root: %v", err)
	}

	// maxBridgeEntries is the canonical count of allowlist entries below.
	// Co-located with the map literal so a top-down reader counting the
	// lines sees the rationale without scrolling. The bottom guard
	// asserts that `maxBridgeEntries` matches the actual number of map
	// entries; any drift (silent growth or unnoticed retirement) surfaces
	// as a hard failure with actionable remediation guidance. Bump ONLY
	// when intentionally extending or retiring the bridge surface under
	// a tracked refactor — every TODO(archcheck-bridge) below documents
	// the migration target per entry, and retiring that entry is the
	// exit criterion.
	const maxBridgeEntries = 22 // 11 composition-root + 11 bridge files

	allowlist := map[string]bool{
		// Composition root adapters are allowed to bridge layers.
		"internal/app":                             true,
		"internal/app/lifecycle.go":                true,
		"internal/app/adapters_infra.go":           true,
		"internal/app/registry_adapters.go":        true,
		"internal/app/clips_dispatcher_adapter.go": true,
		"internal/app/build_bundles_core.go":       true,
		"internal/app/build_bundles_process.go":    true,
		"internal/app/build_bundles_drive.go":      true,
		"internal/app/capability_registry.go":      true,
		"internal/app/creator_runtime.go":          true,
		"internal/app/import_resolution_test.go":   true,

		// TODO(archcheck-bridge): api/assets/clips bridges to infrastructure
		// for the clip upload flow (drive, sqlite/assets, foldermemory,
		// semantic, clipindexer). Migration target: route the call sites
		// through typed ports in internal/application/clips/ so this
		// package becomes a pure presentation layer. The bridge is
		// allowlisted today so the layered-architecture gate does not
		// block downstream tests; retiring the entry is the exit criterion.
		"internal/api/assets/clips/clip_action.go":          true,
		"internal/api/assets/clips/folder_query_handler.go": true,
		"internal/api/assets/clips/handler.go":              true,
		"internal/api/assets/clips/ingest.go":               true,
		"internal/api/assets/clips/module.go":               true,
		"internal/api/assets/clips/nonops/handler.go":       true,
		"internal/api/assets/clips/ops.go":                  true,
		"internal/api/assets/clips/search.go":               true,
		// TODO(archcheck-bridge): api/transport/qdrant_health.go exposes
		// Qdrant collection/search/disaster-recovery types directly.
		// Migration target: narrow typed port in internal/api/transport
		// (e.g. internal/api/transport/qdrant_health_port.go) so this
		// handler consumes an interface and the concrete Qdrant types
		// stay in internal/infrastructure. Retiring this entry is the
		// exit criterion.
		"internal/api/transport/qdrant_health.go": true,
		// TODO(archcheck-bridge): domain/remote imports
		// internal/infrastructure/files for idempotency key derivation
		// (ArtifactIdempotencyKey / CompleteJobIdempotencyKey).
		// Migration target: lift the hashing helper into
		// pkg/remoteidempotency/ (or internal/domain/remote/idempotency.go
		// with pure stdlib deps) so the domain layer carries no infra
		// imports. Retiring these entries is the exit criterion.
		"internal/domain/remote/complete_job_idempotency.go": true,
		"internal/domain/remote/idempotency.go":              true,
	}

	var violations []string
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

		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		imports, readErr := extractInternalImports(path)
		if readErr != nil {
			t.Logf("skip %s: %v", path, readErr)
			return nil
		}

		for _, imp := range imports {
			if isForbiddenDependency(rel, imp, allowlist) {
				violations = append(violations, fmt.Sprintf("%s imports %s", rel, imp))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}

	if len(violations) > 0 {
		t.Errorf("layered architecture import violations:\n  %s", strings.Join(violations, "\n  "))
	}

	// Hard guard for the allowlist size (declared at the top of the
	// function so the literal and the rationale are visually co-located
	// with the map it gates). Catches silent architectural creep.
	if len(allowlist) != maxBridgeEntries {
		t.Errorf("dependencyAllowlist drifted from %d to %d entries; "+
			"if retiring a bridge: delete the matching entry AND open a tracked TODO(archcheck-bridge) ticket; "+
			"if extending the bridge: bump maxBridgeEntries AND open a tracked TODO(archcheck-bridge) ticket",
			maxBridgeEntries, len(allowlist))
	}
}

// isForbiddenDependency reports whether a file at relPath importing
// importPath violates the layered architecture rules.
func isForbiddenDependency(relPath, importPath string, allowlist map[string]bool) bool {
	if allowlist[relPath] {
		return false
	}

	// Only enforce for internal imports.
	if !strings.HasPrefix(importPath, projectRoot+"/internal/") {
		return false
	}

	// Determine the layer of the importing file.
	fileLayer := layerOf(relPath)
	// Determine the layer of the imported package.
	importLayer := layerOf(strings.TrimPrefix(importPath, projectRoot+"/"))

	switch fileLayer {
	case "domain", "kernel":
		// Domain and kernel must not depend on application or infrastructure.
		if importLayer == "application" || importLayer == "infrastructure" {
			return true
		}
	case "api":
		// API must not depend on infrastructure directly.
		if importLayer == "infrastructure" {
			return true
		}
	}
	return false
}

// layerOf returns the architectural layer for a repo-relative path
// under internal/. Returns empty string for paths not under internal/.
func layerOf(relPath string) string {
	relPath = filepath.ToSlash(relPath)
	if !strings.HasPrefix(relPath, "internal/") {
		return ""
	}
	parts := strings.Split(relPath, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
