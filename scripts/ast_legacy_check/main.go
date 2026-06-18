// Command ast_legacy_check reports true Go-AST references to the legacy
// internal/media/models.MediaAsset type. Comments, strings, and unrelated
// packages named models are ignored.
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

const targetImportPath = "github.com/Marcuss-ops/PipelineGen/internal/media/models"

type Finding struct {
	Package string `json:"package"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Kind    string `json:"kind"`
	Snippet string `json:"snippet"`
}

type allowList map[string]struct{}

func main() {
	var (
		root         string
		allowPath    string
		includeTests bool
		jsonOut      bool
	)
	flag.StringVar(&root, "root", "./internal", "directory tree to scan")
	flag.StringVar(&allowPath, "allowlist", "./docs/migrations/mediaasset-legacy-allowlist.txt", "allowlist file")
	flag.BoolVar(&includeTests, "include-tests", true, "include _test.go files")
	flag.BoolVar(&jsonOut, "json", true, "emit JSON findings")
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
		for _, finding := range findings {
			fmt.Fprintf(os.Stderr, "%s:%d %s %s\n", finding.File, finding.Line, finding.Kind, finding.Snippet)
		}
	}

	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d non-allowlisted legacy reference(s) found.\n", len(findings))
		os.Exit(1)
	}
}

func loadAllowList(path string) (allowList, error) {
	allowed := make(allowList)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return allowed, nil
		}
		return nil, err
	}
	for lineNo, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "/") {
			return nil, fmt.Errorf("%s:%d: allowlist entry %q must contain a directory", path, lineNo+1, line)
		}
		allowed[filepath.ToSlash(line)] = struct{}{}
	}
	return allowed, nil
}

func walk(root string, allowed allowList, includeTests bool) ([]Finding, error) {
	var findings []Finding
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if !includeTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel := filepath.ToSlash(mustRel(path))
		if isAllowlisted(rel, allowed) {
			return nil
		}

		fset := token.NewFileSet()
		tree, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}

		aliases, dotImported := legacyImportAliases(tree)
		if len(aliases) == 0 && !dotImported {
			return nil
		}

		ast.Inspect(tree, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.SelectorExpr:
				identifier, ok := value.X.(*ast.Ident)
				if !ok || value.Sel == nil || value.Sel.Name != "MediaAsset" || !aliases[identifier.Name] {
					return true
				}
				pos := fset.Position(value.Pos())
				findings = append(findings, Finding{
					Package: targetImportPath,
					File:    filepath.ToSlash(pos.Filename),
					Line:    pos.Line,
					Kind:    "selector",
					Snippet: identifier.Name + ".MediaAsset",
				})
			case *ast.Ident:
				if !dotImported || value.Name != "MediaAsset" {
					return true
				}
				pos := fset.Position(value.Pos())
				findings = append(findings, Finding{
					Package: targetImportPath,
					File:    filepath.ToSlash(pos.Filename),
					Line:    pos.Line,
					Kind:    "dot-import",
					Snippet: "MediaAsset",
				})
			}
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

func legacyImportAliases(tree *ast.File) (map[string]bool, bool) {
	aliases := make(map[string]bool)
	dotImported := false
	for _, imp := range tree.Imports {
		if imp.Path == nil || !isLegacyImport(strings.Trim(imp.Path.Value, "\"")) {
			continue
		}
		if imp.Name == nil {
			aliases["models"] = true
			continue
		}
		switch imp.Name.Name {
		case ".":
			dotImported = true
		case "_":
			// Blank imports cannot reference MediaAsset.
		default:
			aliases[imp.Name.Name] = true
		}
	}
	return aliases, dotImported
}

func isLegacyImport(importPath string) bool {
	return importPath == targetImportPath || importPath == "internal/media/models" || strings.HasSuffix(importPath, "/internal/media/models")
}

func mustRel(path string) string {
	rel, err := filepath.Rel(".", path)
	if err != nil {
		return path
	}
	return rel
}

func isAllowlisted(path string, allowed allowList) bool {
	for entry := range allowed {
		if path == entry || strings.HasSuffix(path, "/"+entry) {
			return true
		}
	}
	return false
}
