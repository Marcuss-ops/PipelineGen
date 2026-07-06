// Package main — routes_yaml_dedup.go contains the manifest deduplication
// logic extracted from generate_routes_yaml.go
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band C #2).
//
// Owns: dedupeManifest.
package main

import (
	"fmt"
	"strings"
)

// dedupeManifest removes duplicate (method, path) pairs, keeping
// the first occurrence (lowest source path lexically, after the
// upstream sort). Routes registered in multiple files surface as
// a single row with the lowest source.
//
// Duplicate detection emits a warning per (method, path) seen >1
// BEFORE the prune so the operator can investigate the source
// files. The canonical first-rule of godlike/06 (one owner per
// fact) means duplicate emitters are almost always a bug.
func dedupeManifest(in []manifestRoute) ([]manifestRoute, []string) {
	counts := map[string][]string{}
	out := make([]manifestRoute, 0, len(in))
	seen := map[string]bool{}
	for _, r := range in {
		key := r.Method + " " + r.Path
		counts[key] = append(counts[key], r.Source)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	var warnings []string
	for key, srcs := range counts {
		if len(srcs) > 1 {
			warnings = append(warnings, fmt.Sprintf("duplicate route %q registered from multiple files: %s — investigate (intentional mirror vs accidental duplication?)",
				key, strings.Join(srcs, ", ")))
		}
	}
	return out, warnings
}
