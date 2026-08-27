// Package main - archcheck import-graph rules.
//
// The current internal topology has four roots:
//
//   - internal/app
//   - internal/kernel
//   - internal/capabilities
//   - internal/platform
//
// The former internal/application, internal/api, internal/infrastructure, and
// internal/domain roots are migration-only and must not re-enter production
// source or import edges. Capability-to-capability imports are also checked
// semantically from parsed Go imports, rather than by matching comments or
// duplicated path lists.
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const moduleImportPrefix = "github.com/Marcuss-ops/PipelineGen/"

var currentInternalRoots = [...]string{
	"internal/app",
	"internal/kernel",
	"internal/capabilities",
	"internal/platform",
}

var retiredInternalRoots = [...]string{
	"internal/application",
	"internal/api",
	"internal/infrastructure",
	"internal/domain",
}

type productionImport struct {
	file       string
	importPath string
}

// checkRetiredRootImports rejects production files and imports that mention
// retired roots. It is deliberately filesystem/parser based: comments,
// documentation, and test fixtures do not create architecture edges.
// The current roots are intentionally explicit so this checker cannot drift
// back to the removed application/api/infrastructure topology.
func checkRetiredRootImports(label string, roots []string) (map[string]int, []string) {
	stats := map[string]int{
		"current_roots": len(currentInternalRoots),
		"retired_roots": len(roots),
		"actual":        0,
		"violations":    0,
	}
	violations, err := scanRetiredRootViolations(".", roots)
	if err != nil {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkRetiredRootImports(%s): scan: %v", label, err)}
	}
	stats["actual"] = len(violations)
	stats["violations"] = len(violations)
	return stats, violations
}

// scanRetiredRootViolations scans production Go files under internal/ and
// reports both source files recreated under a retired root and imports of any
// retired root. Test files are excluded.
func scanRetiredRootViolations(root string, roots []string) ([]string, error) {
	imports, err := scanProductionImports(root)
	if err != nil {
		return nil, err
	}

	retired := make(map[string]bool, len(roots))
	for _, rootName := range roots {
		retired[normalizeRoot(rootName)] = true
	}

	violations := make([]string, 0)
	for _, file := range productionGoFiles(root) {
		normalized := filepath.ToSlash(file)
		for retiredRoot := range retired {
			if pathUnderRoot(normalized, retiredRoot) {
				violations = append(violations, "retired root source reintroduced: "+normalized)
				break
			}
		}
	}
	for _, edge := range imports {
		for retiredRoot := range retired {
			if importUnderRoot(edge.importPath, retiredRoot) {
				violations = append(violations, fmt.Sprintf(
					"retired root import reintroduced: %s -> %s", edge.file, edge.importPath,
				))
				break
			}
		}
	}
	sort.Strings(violations)
	return uniqueStrings(violations), nil
}

func scanProductionImports(root string) ([]productionImport, error) {
	imports := make([]productionImport, 0)
	for _, file := range productionGoFiles(root) {
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, file), nil, parser.ImportsOnly)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, fmt.Errorf("unquote import in %s: %w", file, err)
			}
			imports = append(imports, productionImport{file: filepath.ToSlash(file), importPath: importPath})
		}
	}
	sort.Slice(imports, func(i, j int) bool {
		if imports[i].file != imports[j].file {
			return imports[i].file < imports[j].file
		}
		return imports[i].importPath < imports[j].importPath
	})
	return imports, nil
}

func productionGoFiles(root string) []string {
	files := make([]string, 0)
	internalRoot := filepath.Join(root, "internal")
	if _, err := os.Stat(internalRoot); err != nil {
		return files
	}
	_ = filepath.WalkDir(internalRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err == nil {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)
	return files
}

func normalizeRoot(root string) string {
	return strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(root)), "/")
}

func currentRootForPath(path string) string {
	return matchingRoot(path, currentInternalRoots[:], pathUnderRoot)
}

func retiredRootForPath(path string) string {
	return matchingRoot(path, retiredInternalRoots[:], pathUnderRoot)
}

func retiredRootForImport(importPath string) string {
	return matchingRoot(importPath, retiredInternalRoots[:], importUnderRoot)
}

func matchingRoot(value string, roots []string, matcher func(string, string) bool) string {
	for _, root := range roots {
		if matcher(value, root) {
			return root
		}
	}
	return ""
}

func pathUnderRoot(path, root string) bool {
	path = filepath.ToSlash(path)
	root = normalizeRoot(root)
	return path == root || strings.HasPrefix(path, root+"/")
}

func importUnderRoot(importPath, root string) bool {
	root = normalizeRoot(root)
	return strings.HasPrefix(importPath, moduleImportPrefix+root+"/")
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

// checkCrossCapabilityImport counts and reports distinct imports from one
// current capability to another. Imports into kernel, platform, or app are
// not cross-capability edges; those are governed by the root boundary rules.
func checkCrossCapabilityImport() (map[string]int, []string) {
	return checkCrossCapabilityImportAt(".")
}

func checkCrossCapabilityImportAt(root string) (map[string]int, []string) {
	stats := map[string]int{"actual": 0, "violations": 0}
	imports, err := scanProductionImports(root)
	if err != nil {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkCrossCapabilityImport: scan: %v", err)}
	}

	capabilities := capabilityNamesAt(root)
	pairs := make(map[string][]string)
	for _, edge := range imports {
		sourceCapability := capabilityOfFile(edge.file, capabilities)
		importCapability := capabilityOfImport(edge.importPath, capabilities)
		if sourceCapability == "" || importCapability == "" || sourceCapability == importCapability {
			continue
		}
		pair := sourceCapability + "->" + importCapability
		pairs[pair] = append(pairs[pair], edge.file+" -> "+edge.importPath)
	}

	pairNames := make([]string, 0, len(pairs))
	for pair := range pairs {
		pairNames = append(pairNames, pair)
	}
	sort.Strings(pairNames)
	violations := make([]string, 0, len(pairNames))
	for _, pair := range pairNames {
		edges := uniqueStrings(pairs[pair])
		violations = append(violations, fmt.Sprintf("cross-capability import %s: %s", pair, edges[0]))
	}
	stats["actual"] = len(pairNames)
	stats["violations"] = len(violations)
	return stats, violations
}

func capabilityNamesAt(root string) map[string]bool {
	capabilities := make(map[string]bool)
	entries, err := os.ReadDir(filepath.Join(root, "internal", "capabilities"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: capabilityNames: read %s: %v\n", filepath.Join(root, "internal", "capabilities"), err)
		return capabilities
	}
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			capabilities[entry.Name()] = true
		}
	}
	return capabilities
}

func capabilityOfFile(relPath string, capabilities map[string]bool) string {
	const prefix = "internal/capabilities/"
	if !strings.HasPrefix(filepath.ToSlash(relPath), prefix) {
		return ""
	}
	rest := strings.TrimPrefix(filepath.ToSlash(relPath), prefix)
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return ""
	}
	candidate := rest[:slash]
	if !capabilities[candidate] {
		return ""
	}
	return candidate
}

func capabilityOfImport(importPath string, capabilities map[string]bool) string {
	const marker = moduleImportPrefix + "internal/capabilities/"
	if !strings.HasPrefix(importPath, marker) {
		return ""
	}
	rest := strings.TrimPrefix(importPath, marker)
	candidate := rest
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		candidate = rest[:slash]
	}
	if candidate == "" {
		return ""
	}
	if !capabilities[candidate] {
		return ""
	}
	return candidate
}
