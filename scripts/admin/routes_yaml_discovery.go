// Package main — routes_yaml_discovery.go contains the file walker
// extracted from generate_routes_yaml.go
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band C #2).
//
// Owns: discoverAPIFiles, relSlashed.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// discoverAPIFiles returns every production .go file under
// internal/api/**, excluding *_test.go and generated/ subtrees.
// The walker mirrors the C2-A / C2-C gate walker's scope (production-
// code-only) so the generator's output matches the surface that
// actually reaches the gin.Engine.
func discoverAPIFiles(root string) ([]string, error) {
	apiDir := filepath.Join(root, "internal", "api")
	if _, statErr := os.Stat(apiDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, fmt.Errorf("internal/api not found under %s", root)
		}
		return nil, statErr
	}
	var files []string
	walkErr := filepath.Walk(apiDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info == nil {
			return nil
		}
		if info.IsDir() {
			basename := filepath.Base(path)
			if basename == "generated" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return files, nil
}

// relSlashed normalizes a path to repo-relative, forward-slash form
// for stable YAML `source:` field output.
func relSlashed(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}
