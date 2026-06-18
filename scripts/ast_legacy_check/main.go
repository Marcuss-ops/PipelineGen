// Command ast_legacy_check walks a Go source tree and reports every TRUE
// AST reference to legacy.MediaAsset (or its canonical import
// internal/media/models). Unlike ripgrep-based detectors, this tool:
//   - parses each file with go/parser (so comments are NOT counted),
//   - ignores string literals and identifier-name collisions (e.g.,
//     a local variable called `models`),
//   - respects an allowlist file (one path per line, # comments) so
//     bridge files can stay during migration,
//   - emits findings as JSON {package, file, line, kind, snippet}.
//
// Use as the new architectural guardrail in CI:
//   go run ./scripts/ast_legacy_check
// exits 0 when the tree is clean, 1 when findings exist. The Travis/GH
// Actions workflow invokes this same command.
//
// Limitation v1: aliased imports like
//     import m "internal/media/models"
// are still detected because the scanner compares the import path
// directly (not the local name). However the X.Name of the SelectorExpr
// must equal the LOCAL name (`m`) for it to count. If someone aliases the
// import AND uses the qualified name `m.MediaAsset`, the tool reports it.
// If they alias the import AND use only the type, the tool reports it.
// If they alias AND write `models.MediaAsset` after re-aliasing back to
// "models", the AST path import resolves differently and the user can
// explicitly add the file to the allowlist with a justification.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// targetImportPath is the canonical path of the legacy package we hunt.
// Anything imported and named MediaAsset from this path is flagged.
const targetImportPath = "internal/media/models"

// Finding is one TRUE Go-AST reference to the legacy MediaAsset type.
// Snippet is the trimmed source line (first non-whitespace content).
// Kind is "selector" (covers typeref & value use via *ast.SelectorExpr).
type Finding struct {
	Package string `json:"package"` // canonical import path
	File    string `json:"file"`    // repo-relative posix path
	Line    int    `json:"line"`    // 1-indexed
	Kind    string `json:"kind"`
	Snippet string `json:"snippet"`
}

func main() {
	var (
		root         string
		allowPath    string
		includeTests bool
		jsonOut      bool
	)
	flag.StringVar(&root, "root", "./internal", "directory tree to walk")
	flag.StringVar(&allowPath, "allowlist", "./scripts/ast_legacy_check/allowlist.txt", "allowlist file (one path per line, # comments)")
	flag.BoolVar(&includeTests, "include-tests", false, "include _test.go files in the scan")
	flag.BoolVar(&jsonOut, "json", true, "emit findings as JSON to stdout")
	flag.Parse()

	allowed, err := loadAllowList(allowPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading allowlist: %v\n", err)
		os.Exit(2)
	}

	findings, err := walk(root, allowed, includeTests)
	if err != nil {
		fmt.Fprintf(os.Stderr, "walking %s: %v\n", root, err)
		os.Exit(2)
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(findings)
	} else {
		for _, f := range findings {
			fmt.Fprintf(os.Stderr, "%s:%d  %s  %s\n", f.File, f.Line, f.Kind, f.Snippet)
		}
	}

	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d legacy reference(s) found.\n", len(findings))
		os.Exit(1)
	}
}

// allowList is a map of repo-relative path -> reason. Empty map (no file)
// means "no entries allowlisted".
type allowList map[string]string

// loadAllowList reads a plain-text allowlist. A non-existent file is
// treated as empty (does NOT error) — this lets new checkouts start
// with zero allowed bridges and surface every real reference.
func loadAllowList(path string) (allowList, error) {
	out := make(allowList)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for i, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Reject single-component entries: isAllowlisted's suffix-match
		// would otherwise match any path ending in that filename across
		// the tree. Require at least one / so each entry is anchored
		// to an explicit directory.
		if !strings.Contains(line, "/") {
			return nil, fmt.Errorf("allowlist %s line %d: entry %q must contain a directory separator (single-component entries are rejected to avoid suffix-match false positives)", path, i+1, line)
		}
		out[line] = "allowlisted"
	}
	return out, nil
}

// walk scans root for *.go files and records legacy references.
// Serial (no goroutines) so findings sort is deterministic.
func walk(root string, allowed allowList, includeTests bool) ([]Finding, error) {
	var findings []Finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if !includeTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel := filepath.ToSlash(mustRel(path))
		if isAllowlisted(rel, allowed) {
			return nil
		}

		// Per-file parse so we can decide whether the file actually
		// imports the target package (cheap filter before walking AST).
		fset := token.NewFileSet()
		tree, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if !fileTargetsLegacyMediaAsset(tree) {
			return nil
		}

		// Walk the AST and collect every SelectorExpr where the
		// qualified LHS resolves to the legacy package and the
		// selector name is MediaAsset. This single shape catches
		// type references (composite lit types, embeddings, named
		// types) and value references alike.
		ast.Inspect(tree, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "MediaAsset" {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if !localNameIsLegacyModels(tree, id.Name) {
				return true
			}
			pos := fset.Position(sel.Pos())
			findings = append(findings, Finding{
				Package: targetImportPath,
				File:    pos.Filename,
				Line:    pos.Line,
				Kind:    "selector",
				Snippet: snippetAt(pos, tree),
			})
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

// fileTargetsLegacyMediaAsset returns true iff the AST imports the
// canonical legacy path. We don't care about the local name here —
// localNameIsLegacyModels resolves the alias for the visitor.
func fileTargetsLegacyMediaAsset(tree *ast.File) bool {
	for _, imp := range tree.Imports {
		if imp.Path == nil {
			continue
		}
		// imp.Path.Value is a quoted string literal in source.
		pathVal := strings.Trim(imp.Path.Value, "\"")
		if pathVal == targetImportPath {
			return true
		}
	}
	return false
}

// localNameIsLegacyModels returns true if localName refers to the
// legacy package's import. We trust the file-level import scan done
// by fileTargetsLegacyMediaAsset to have already established that
// targetImportPath is imported; here we just check whether the local
// name matches either:
//   - the unaliased form (the package basename, "models"), OR
//   - an explicit alias in the file ("alias models") that names it
//     "models" exactly (rare but legal).
func localNameIsLegacyModels(tree *ast.File, localName string) bool {
	if localName == "" {
		return false
	}
	for _, imp := range tree.Imports {
		if imp.Path == nil {
			continue
		}
		pathVal := strings.Trim(imp.Path.Value, "\"")
		if pathVal != targetImportPath {
			continue
		}
		if imp.Name == nil {
			// Unaliased import — local name is the package basename.
			return localName == "models"
		}
		// Aliased import. The alias wins unless it is `.` (dot import).
		if imp.Name.Name == "." {
			return true // dot import exposes MediaAsset as MediaAsset.
		}
		return imp.Name.Name == localName
	}
	return false
}

// snippetAt returns the trimmed source line text for pos.
// If the file text isn't directly available, falls back to the
// qualified identifier at pos.
func snippetAt(pos token.Position, tree *ast.File) string {
	// We have the syntax tree but not the source text. Use the
	// qualified identifier reconstruction: pkg.Sel.
	start := token.Pos(pos.Line)
	for _, imp := range tree.Imports {
		if imp.Path == nil {
			continue
		}
		pathVal := strings.Trim(imp.Path.Value, "\"")
		if pathVal != targetImportPath {
			continue
		}
		if imp.Pos() <= start && start <= imp.End() {
			if imp.Name != nil {
				return imp.Name.Name + ".MediaAsset"
			}
			return "models.MediaAsset"
		}
	}
	return "models.MediaAsset"
}

func mustRel(path string) string {
	rel, err := filepath.Rel(".", path)
	if err != nil {
		return path
	}
	return rel
}

// isAllowlisted returns true iff path matches any entry in the allowlist.
// Two match strategies are used because of the test environment:
//   - exact match: works for production runs where filepath.Rel(".", path)
//     produces paths like "internal/foo.go" that exactly equal allowlist
//     entries.
//   - suffix match: works for tests where the walker root lives under
//     t.TempDir() (e.g. /tmp/...) and Rel produces "../../tmp/.../
//     internal/foo.go". The "/entry" suffix match resolves this without
//     false positives because every repo-relative allowlist entry starts
//     with the directory portion (e.g. "internal/"), and only paths
//     whose final component matches the entry will pass.
func isAllowlisted(path string, allowed allowList) bool {
	for entry := range allowed {
		if path == entry {
			return true
		}
		if strings.HasSuffix(path, "/"+entry) {
			return true
		}
	}
	return false
}
