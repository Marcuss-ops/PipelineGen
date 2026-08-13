// Package scan — Check 75: typed metadata registry (Wave 5, July 2026).
//
// scan/percheck_metadataregistry.go owns the forward-prevention gate
// for typed metadata. Domain types SHOULD NOT use raw
// `map[string]any` for metadata fields; instead they should use the
// typed metadata registry (internal/application/assets/delivery/registry.go
// and related typed registries). This gate flags new occurrences of
// `map[string]any` in internal/domain/** so the codebase migrates
// toward typed metadata structs.
//
// Allowlist:
//   - internal/domain types that already carry map[string]any are
//     grandfathered until their owning wave retires them.
//   - *_test.go : tests may use map[string]any for fixtures.
//
// Pattern anchors:
//
//	map\[string\]any  — raw metadata map type
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// metadataAllowlist maps file paths (repo-relative) to a boolean
// indicating they are grandfathered. Empty by default; populate as
// the migration proceeds.
var metadataAllowlist = map[string]bool{
	// Grandfathered domain files can be added here with owner + deadline.
	"internal/kernel/asset/asset_accessors.go":                 true, // owner: platform-asset-metadata, deadline: 2026-08-15
	"internal/kernel/asset/asset_types.go":                     true, // owner: platform-asset-metadata, deadline: 2026-08-15
	"internal/kernel/asset/metadata_helpers.go":                true, // owner: platform-asset-metadata, deadline: 2026-08-15
	"internal/kernel/asset/processor.go":                       true, // owner: platform-asset-metadata, deadline: 2026-08-15
	"internal/kernel/asset/scoring.go":                         true, // owner: platform-asset-metadata, deadline: 2026-08-15
	"internal/kernel/asset/location_resolver.go":               true, // owner: platform-asset-metadata, deadline: 2026-08-15
	"internal/domain/finalization/types_published_artifact.go": true, // owner: platform-finalization, deadline: 2026-08-15
	"internal/domain/finalization/types_verified_artifact.go":  true, // owner: platform-finalization, deadline: 2026-08-15
	"internal/domain/remote/staged_artifact_reference.go":      true, // owner: platform-finalization, deadline: 2026-08-15
	"internal/domain/script/generation_errors.go":              true, // owner: platform-script-domain, deadline: 2026-08-15
	"internal/domain/script/generation_result.go":              true, // owner: platform-script-domain, deadline: 2026-08-15
	"internal/domain/script/narrative_clip_view.go":            true, // owner: platform-script-domain, deadline: 2026-08-15
}

// ScanMetadataRegistry walks <root>/internal/domain/** for non-test
// .go files and flags any line containing `map[string]any` that is
// not in the allowlist.
func ScanMetadataRegistry(root string, pol *policy.Policy, r *report.Report) {
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}

	dir := filepath.Join(root, "internal/domain")
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)
		if metadataAllowlist[relSlash] {
			return nil
		}
		scanMetadataRegistryFile(root, path, relSlash, r)
		return nil
	})
}

func scanMetadataRegistryFile(root, path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if !strings.Contains(line, "map[string]any") {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        lineNum,
			Rule:        "percheck_metadata_registry",
			Severity:    string(report.SeverityWarn),
			MatchedRule: "metadata_map_string_any",
			Note:        "raw `map[string]any` metadata field in domain type — migrate to the typed metadata registry (internal/application/assets/delivery/registry.go) or add to percheck_metadataregistry.go allowlist with owner + deadline",
		})
	}
}
