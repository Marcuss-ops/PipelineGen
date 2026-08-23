// Package scan — legacy hotspot growth ratchet (Cleanup Day 2026-08-23).
//
// Registered hotspots under migration-only legacy roots must never gain new
// production files. Any count above the committed baseline is a hard violation
// regardless of the global max-files-per-package cap. This is a separate scan
// from ScanPackagesForMode because that scanner only checks growth for packages
// that already exceed the global cap (65). Most legacy hotspots sit well under
// 65 files, so their growth was invisible until this ratchet was introduced.
package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const legacyHotspotRatchetRule = "percheck_legacy_hotspot_growth"

// ScanLegacyHotspotGrowth enforces a non-increasing file-count ratchet for every
// hotspot registered in package_hotspots.json that lives under a legacy
// migration root. Growth is a hard error regardless of whether the package
// exceeds the 65-file emergency ceiling.
func ScanLegacyHotspotGrowth(root string, pol *policy.Policy, r *report.Report) {
	registry, err := loadPackageHotspotRegistry(root)
	if err != nil {
		// The registry loader already emits its own violation; do not
		// duplicate it here.
		return
	}
	if registry == nil || len(registry.Hotspots) == 0 {
		return
	}
	if len(pol.LegacyInternalRoots) == 0 {
		return
	}

	// Build a lookup: package slug → baseline
	hotspots := map[string]packageHotspot{}
	for _, h := range registry.Hotspots {
		hotspots[filepath.ToSlash(h.Path)] = h
	}

	// Count production .go files per directory.
	pkgCounts := map[string]int{}
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
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
		dir := filepath.Dir(path)
		relDir, relErr := filepath.Rel(root, dir)
		if relErr != nil {
			return nil
		}
		pkgCounts[filepath.ToSlash(relDir)]++
		return nil
	})

	legacySet := make(map[string]bool, len(pol.LegacyInternalRoots))
	for _, lr := range pol.LegacyInternalRoots {
		legacySet["internal/"+strings.Trim(lr, "/")] = true
	}

	// Check every registered hotspot that lives in a legacy root.
	for pkg, count := range pkgCounts {
		h, registered := hotspots[pkg]
		if !registered {
			continue
		}
		if !isUnderLegacyRoot(pkg, legacySet) {
			continue
		}
		if count > h.BaselineFiles {
			r.Violations = append(r.Violations, report.Violation{
				Package:      pkg,
				ActualCount:  count,
				AllowedCount: h.BaselineFiles,
				MatchedRule:  "legacy_hotspot_growth",
				Rule:         legacyHotspotRatchetRule,
				Severity:     "error",
				Note: fmt.Sprintf(
					"legacy hotspot %s grew from %d to %d production files; owner=%s deadline=%s — legacy files must never increase after Cleanup Day",
					pkg, h.BaselineFiles, count, h.Owner, h.Deadline,
				),
			})
		}
	}
}

// isUnderLegacyRoot returns true when the package slug is directly under a
// registered legacy migration root.
func isUnderLegacyRoot(pkg string, legacySet map[string]bool) bool {
	for legacy := range legacySet {
		if pkg == legacy || strings.HasPrefix(pkg, legacy+"/") {
			return true
		}
	}
	return false
}

