// Package scan — registered hotspot growth ratchet (Cleanup Day 2026-08-23;
// generalized 2026-08-26 to cover the whole carry-forward debt surface).
//
// Every hotspot registered in package_hotspots.json must never gain new
// production files. Any count above the committed baseline is a hard violation
// regardless of the global max-files-per-package cap. This is a separate scan
// from ScanPackagesForMode because that scanner only checks growth for packages
// that already exceed the global cap (65). Most registered hotspots sit well
// under 65 files (e.g. stockpipeline at 63 vs its 63-file baseline), so their
// growth was invisible until this ratchet fired on the full registry.
//
// After the legacy roots were deleted (2026-08-25) the ratchet no longer filters
// by legacy-root membership: the registered hotspot registry is itself the
// carry-forward debt surface, so growth above any committed baseline fails
// closed.
package structure

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
// hotspot registered in package_hotspots.json. Growth is a hard error regardless
// of whether the package exceeds the 65-file emergency ceiling and regardless of
// which internal root the package lives under.
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

	// Check every registered hotspot.
	for pkg, count := range pkgCounts {
		h, registered := hotspots[pkg]
		if !registered {
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
					"registered hotspot %s grew from %d to %d production files; owner=%s deadline=%s — carry-forward debt must never increase",
					pkg, h.BaselineFiles, count, h.Owner, h.Deadline,
				),
			})
		}
	}
}
