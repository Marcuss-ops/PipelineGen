// Package main is the Blocco C2-E pre-step generator (June 2026):
//
//	scans the canonical HTTP capability surface (internal/api/**/RegisterRoutes
//	method bodies + nesting through api.NewRouteModule closures) and emits
//	`architecture/routes.yaml`, the structured manifest that the C2-E gate
//	(`scripts/archcheck/gates/gate_c2_route_manifest.go`) compares against
//	`docs/api/ACTIVE_API_GENERATED.md` for the route ≡ manifest ≡ docs
//	invariant.
//
// LONG-FILES-DECOMPOSITION-2026-07-06 Band C #2: types → routes_yaml_types.go,
// file walker → routes_yaml_discovery.go, AST engine → routes_yaml_ast.go,
// dedup → routes_yaml_dedup.go. This file retains main() only.
//
// ── Usage ─────────────────────────────────────────────────────────────
//
//	go run ./scripts/admin/generate_routes_yaml.go [root_dir] [output_path]
//
// root_dir defaults to "."; output_path defaults to "architecture/routes.yaml".
// Exit codes: 0 = success, 2 = invocation error, 3 = zero-routes
// sentinel (the AST scanner is broken, NOT "no API exists" — the
// latter is impossible in this codebase).
package main

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

func main() {
	var root, outputPath string
	flag.StringVar(&root, "root", ".", "repo root (defaults to \".\")")
	flag.StringVar(&outputPath, "output", "architecture/routes.yaml", "output YAML path (defaults to architecture/routes.yaml)")
	flag.Parse()

	if flag.NArg() >= 1 {
		root = flag.Arg(0)
	}
	if flag.NArg() >= 2 {
		outputPath = flag.Arg(1)
	}

	fset := token.NewFileSet()
	files, err := discoverAPIFiles(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "C2-E pre-step: discover API files: %v\n", err)
		os.Exit(2)
	}
	sort.Strings(files)

	var routes []manifestRoute
	var warnings []string
	for _, path := range files {
		relPath := relSlashed(root, path)
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			warnings = append(warnings, fmt.Sprintf("parse error %s: %v", relPath, parseErr))
			continue
		}
		lineRoutes, lineWarnings := inspectAPIFile(fset, file, relPath)
		routes = append(routes, lineRoutes...)
		warnings = append(warnings, lineWarnings...)
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Method != routes[j].Method {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Path < routes[j].Path
	})

	// De-duplicate (same method+path may appear in multiple files).
	// First-source-wins; duplicate-detect warnings surface BEFORE the
	// prune so operators can investigate.
	var dupWarnings []string
	routes, dupWarnings = dedupeManifest(routes)
	warnings = append(warnings, dupWarnings...)

	if len(routes) == 0 {
		fmt.Fprintf(os.Stderr, "C2-E pre-step: 0 routes detected — sentinel error, inspect walker (probably a parse-fatal cascade)\n")
		os.Exit(3)
	}

	doc := manifestDocument{Routes: routes}
	yamlData, err := yaml.Marshal(doc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "C2-E pre-step: marshal YAML: %v\n", err)
		os.Exit(2)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "C2-E pre-step: mkdir %s: %v\n", filepath.Dir(outputPath), err)
		os.Exit(2)
	}
	if err := os.WriteFile(outputPath, yamlData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "C2-E pre-step: write %s: %v\n", outputPath, err)
		os.Exit(2)
	}

	fmt.Fprintf(os.Stderr, "C2-E pre-step: wrote %d route(s) to %s (warnings=%d)\n", len(routes), outputPath, len(warnings))
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "  WARN: %s\n", w)
	}
}
