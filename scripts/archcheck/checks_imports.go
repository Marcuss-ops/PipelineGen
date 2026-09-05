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

	"gopkg.in/yaml.v3"
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
//
// Classification granularity: when architecture/capability_macro_map.yaml
// exists (the package-to-macro-capability ownership SSOT published by Wave 24
// P1-CAPABILITY-OWNERSHIP), edges are aggregated to the macro owner first, so
// imports between sibling packages of the same macro capability are not
// reported. Residual macro-cross edges are checked against
// architecture/capability_import_allowlist.yaml (the tracked-decoupling
// ledger); a macro-cross edge that is not allowlisted is a violation, and an
// allowlist entry whose edge no longer exists is a stale-ledger violation.
func checkCrossCapabilityImport() (map[string]int, []string) {
	return checkCrossCapabilityImportAt(".")
}

func checkCrossCapabilityImportAt(root string) (map[string]int, []string) {
	stats := map[string]int{"actual": 0, "macro": 0, "allowlisted": 0, "violations": 0}
	imports, err := scanProductionImports(root)
	if err != nil {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkCrossCapabilityImport: scan: %v", err)}
	}

	capabilities := capabilityNamesAt(root)
	// dirPair -> [exemplar edges] at the raw on-disk granularity.
	dirPairs := make(map[string][]string)
	// macroPair -> [exemplar edges] after package->macro aggregation.
	macroPairs := make(map[string][]string)
	for _, edge := range imports {
		sourceCapability := capabilityOfFile(edge.file, capabilities)
		importCapability := capabilityOfImport(edge.importPath, capabilities)
		if sourceCapability == "" || importCapability == "" || sourceCapability == importCapability {
			continue
		}
		dirPair := sourceCapability + "->" + importCapability
		dirPairs[dirPair] = append(dirPairs[dirPair], edge.file+" -> "+edge.importPath)

		sourceMacro := sourceCapability
		importMacro := importCapability
		if dirToMacro, ok := loadMacroOwners(root); ok {
			sourceMacro = macroOwnerOf(dirToMacro, sourceCapability)
			importMacro = macroOwnerOf(dirToMacro, importCapability)
		}
		if sourceMacro == importMacro {
			continue
		}
		macroPair := sourceMacro + "->" + importMacro
		macroPairs[macroPair] = append(macroPairs[macroPair], edge.file+" -> "+edge.importPath)
	}

	dirPairNames := make([]string, 0, len(dirPairs))
	for pair := range dirPairs {
		dirPairNames = append(dirPairNames, pair)
	}
	sort.Strings(dirPairNames)

	// Stale-ledger detection requires both the macro map and the allowlist.
	_, macroMapPresent := loadMacroOwners(root)
	allow, allowPresent := loadCrossCapabilityAllowlist(root)

	macroPairNames := make([]string, 0, len(macroPairs))
	for pair := range macroPairs {
		macroPairNames = append(macroPairNames, pair)
	}
	sort.Strings(macroPairNames)

	violations := make([]string, 0, len(macroPairNames))
	allowlisted := 0
	for _, pair := range macroPairNames {
		edges := uniqueStrings(macroPairs[pair])
		if allowPresent && allow[pair] {
			allowlisted++
			continue
		}
		violations = append(violations, fmt.Sprintf("cross-capability import %s: %s", pair, edges[0]))
	}
	if macroMapPresent && allowPresent {
		present := make(map[string]bool, len(macroPairs))
		for pair := range macroPairs {
			present[pair] = true
		}
		for pair := range allow {
			if !present[pair] {
				violations = append(violations, fmt.Sprintf("cross-capability allowlist entry no longer present (stale ledger): %s", pair))
			}
		}
	}
	sort.Strings(violations)

	stats["actual"] = len(dirPairNames)
	stats["macro"] = len(macroPairNames)
	stats["allowlisted"] = allowlisted
	stats["violations"] = len(violations)
	return stats, violations
}

// macroOwnerOf resolves a capability package dir to its macro owner. Packages
// without a declared macro owner keep their own name (identity fallback) so a
// missing map entry can never silently bless a cross edge.
func macroOwnerOf(dirToMacro map[string]string, dir string) string {
	if owner, ok := dirToMacro[dir]; ok && owner != "" {
		return owner
	}
	return dir
}

const macroOwnersPath = "architecture/capability_macro_map.yaml"

// loadMacroOwners reads the package-to-macro-capability ownership map. The
// second return value reports whether the file exists (absent file => identity
// fallback, e.g. synthetic check roots in unit tests).
func loadMacroOwners(root string) (map[string]string, bool) {
	doc := struct {
		Map map[string][]string `yaml:"macro_capability_map"`
	}{}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(macroOwnersPath)))
	if err != nil {
		return nil, false
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	dirToMacro := make(map[string]string)
	for macro, dirs := range doc.Map {
		for _, dir := range dirs {
			dirToMacro[dir] = macro
		}
	}
	return dirToMacro, true
}

const crossCapabilityAllowlistPath = "architecture/capability_import_allowlist.yaml"

// loadCrossCapabilityAllowlist reads the macro-cross decoupling ledger. The
// second return value reports whether the file exists.
func loadCrossCapabilityAllowlist(root string) (map[string]bool, bool) {
	doc := struct {
		Allow []struct {
			Source string `yaml:"source"`
			Target string `yaml:"target"`
		} `yaml:"allowlist"`
	}{}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(crossCapabilityAllowlistPath)))
	if err != nil {
		return nil, false
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, false
	}
	allow := make(map[string]bool)
	for _, entry := range doc.Allow {
		if entry.Source != "" && entry.Target != "" {
			allow[entry.Source+"->"+entry.Target] = true
		}
	}
	return allow, true
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
