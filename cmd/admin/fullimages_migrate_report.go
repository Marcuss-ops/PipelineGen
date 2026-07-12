// cmd/admin/fullimages_migrate_report.go — JSON reporting half of the
// fullimages-migrate CLI (LEGACY-CLEANUP-5-ITEM-ORCHESTRATION Item 5,
// Option B verdict, shipped 2026-07-10).
//
// Split rationale (Commit F, July 2026): the canonical fullimages-migrate
// CLI (fullimages_migrate.go) owns the operator-facing migration
// orchestration — entry point (runFullImagesMigrate), the canonical
// per-class pattern table (fullimagesMigratePatterns var +
// fullimagesMigratePattern type), the byte-precise scan
// (scanFileForOldPatterns), the CSV parsing helper (splitExtsCSV),
// and the text-replacement apply path (applyFullimagesMigrate, which
// tight-couples with scanFileForOldPatterns via the patterns table).
//
// This sibling owns the 2 output-format builders + the 5 JSON types
// used by the canonical automation-harness schema:
//
//   - fullimagesMigrateJSONReport   — top-level JSON envelope
//
//   - fullimagesMigrateJSONMeta     — meta block (target_dir, exts, mode, timestamp)
//
//   - fullimagesMigrateJSONPattern  — per-pattern class block (class, old, new, description)
//
//   - fullimagesMigrateJSONTotals   — globals (files_with_hits, total_matches)
//
//   - fullimagesMigrateJSONFileResult — per-file result (path, total_matches, hits{})
//
//   - buildFullimagesMigrateReport    — human-readable strings.Builder renderer
//
//   - buildFullimagesMigrateJSONReport — automation-harness JSON composer
//
// godlike/06 SSOT (one canonical owner per fact):
//   - The JSON schema is the canonical automation-harness contract;
//     any schema addition is ADDITIVE-ONLY (add new fields; never
//     rename or repurpose existing fields) per the schema-extensibility
//     contract documented on fullimagesMigrateJSONReport.
//   - The per-class ordering of fileHits is STABLE: both build
//     functions iterate patterns (the canonical order in
//     fullimagesMigratePatterns, which stays in the orchestrator
//     file) to derive per-file hits. The orchestrator file remains
//     the owner of the canonical order so the same ordering drives
//     both output formats.
//
// godlike/07 NO-FAKE-AVAILABILITY:
//   - The 2 build functions are pure (no side effects); the schema is
//     invariant regardless of operator cwd or filesystem state.
//   - Both build functions surface a "no data → omit" idiom for the
//     per-class map and warnings slice. Downstream consumers MUST
//     treat the absence of `per_class_totals` / `warnings` as a
//     canonical signal that the dimension is empty (not a typo).
//
// Sibling constraint (Commit F user spec): the variable
// fullimagesMigratePatterns and the function scanFileForOldPatterns
// stay in the orchestrator file because they couple tightly with
// applyFullimagesMigrate (the text-replacement writer). Promoting
// them would force a typed-port interface with no second consumer
// (dead-interface anti-pattern per the PipelineGen architecture).
//
// The non-JSON type fullimagesMigratePattern (the input data-shape
// to the report builders) stays in the orchestrator file too because
// it is the type of the canonical patterns table referenced by the
// variable fullimagesMigratePatterns. The report file references it
// across the package boundary via intra-package visibility (same
// `package main`).
package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// fullimagesMigrateJSONReport is the canonical JSON output schema
// (godlike/06 SSOT — automation-harness surface).
//
// godlike/07 NO-FAKE-AVAILABILITY: the schema is stable, additive-only,
// and is the single canonical contract for CI pipelines + monitoring
// scrapers that consume the migration report. New fields MAY be added
// (additive); existing fields MUST NOT be renamed or repurposed.
//
//	{
//	  "meta": {"target_dir": "...", "exts": [".sh", ".py"],
//	//          "mode": "dry-run"|"apply", "timestamp": "RFC3339"},
//	  "patterns": [{"class", "old", "new", "description"}],
//	  "totals": {"files_with_hits": N, "total_matches": N},
//	  "per_class_totals": {"ClassName": N},
//	  "files": [{"path", "total_matches", "hits": {"ClassName": N}}],
//	  "warnings": ["cannot access X: Y (skipped)"],
//	  "applied_files_count": 0   // only meaningful when mode=="apply"
//	}
type fullimagesMigrateJSONReport struct {
	Meta              fullimagesMigrateJSONMeta         `json:"meta"`
	Patterns          []fullimagesMigrateJSONPattern    `json:"patterns"`
	Totals            fullimagesMigrateJSONTotals       `json:"totals"`
	PerClassTotals    map[string]int                    `json:"per_class_totals"`
	Files             []fullimagesMigrateJSONFileResult `json:"files"`
	Warnings          []string                          `json:"warnings"`
	AppliedFilesCount int                               `json:"applied_files_count"`
}

type fullimagesMigrateJSONMeta struct {
	TargetDir string    `json:"target_dir"`
	Exts      []string  `json:"exts"`
	Mode      string    `json:"mode"` // "dry-run" or "apply"
	Timestamp time.Time `json:"timestamp"`
}

type fullimagesMigrateJSONPattern struct {
	Class       string `json:"class"`
	Old         string `json:"old"`
	New         string `json:"new"`
	Description string `json:"description"`
}

type fullimagesMigrateJSONTotals struct {
	FilesWithHits int `json:"files_with_hits"`
	TotalMatches  int `json:"total_matches"`
}

type fullimagesMigrateJSONFileResult struct {
	Path         string         `json:"path"`
	TotalMatches int            `json:"total_matches"`
	Hits         map[string]int `json:"hits"`
}

// buildFullimagesMigrateReport renders the canonical human-readable
// dry-run / apply-explanation report. Pure function (no side effects)
// per godlike/06 SSOT testability.
func buildFullimagesMigrateReport(rootDir string, fileHits map[string]map[string]int, patterns []fullimagesMigratePattern) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Target directory: %s\n", rootDir)
	fmt.Fprintf(&b, "Patterns scanned: %d\n\n", len(patterns))

	if len(fileHits) == 0 {
		b.WriteString("No old patterns found. The codebase is already migrated.\n")
		return b.String()
	}

	// Sort file paths for stable, diff-friendly output (godlike/06 SSOT).
	paths := make([]string, 0, len(fileHits))
	for p := range fileHits {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	totalFiles := len(paths)
	totalHits := 0
	for _, hits := range fileHits {
		for _, n := range hits {
			totalHits += n
		}
	}

	fmt.Fprintf(&b, "Files with old patterns: %d\n", totalFiles)
	fmt.Fprintf(&b, "Total pattern matches:    %d\n\n", totalHits)

	// Pattern-class summary
	classTotals := make(map[string]int)
	for _, hits := range fileHits {
		for class, n := range hits {
			classTotals[class] += n
		}
	}
	b.WriteString("Per-class summary:\n")
	for _, p := range patterns {
		if n, ok := classTotals[p.Class]; ok && n > 0 {
			fmt.Fprintf(&b, "  %-12s  %-30s  %4d match(es)\n", p.Class, p.Filename, n)
		}
	}
	b.WriteString("\nPer-file details:\n")
	for _, path := range paths {
		hits := fileHits[path]
		fmt.Fprintf(&b, "  %s\n", path)
		// Stable per-class ordering
		for _, p := range patterns {
			if n, ok := hits[p.Class]; ok && n > 0 {
				fmt.Fprintf(&b, "    %s: %d  (%q → %q)\n", p.Class, n, p.Old, p.New)
			}
		}
	}
	return b.String()
}

// buildFullimagesMigrateJSONReport composes the canonical JSON report
// (godlike/06 SSOT — automation-harness surface). Pure function (no
// side effects) so it can be tested in isolation. The schema is
// stable, additive-only (see the type doc above).
func buildFullimagesMigrateJSONReport(
	rootDir string,
	exts []string,
	apply bool,
	fileHits map[string]map[string]int,
	patterns []fullimagesMigratePattern,
	warnings []string,
	appliedCount int,
) fullimagesMigrateJSONReport {
	mode := "dry-run"
	if apply {
		mode = "apply"
	}

	// Patterns: surface the canonical table verbatim (additive; future
	// patterns extend the table, never replace).
	patOut := make([]fullimagesMigrateJSONPattern, 0, len(patterns))
	for _, p := range patterns {
		patOut = append(patOut, fullimagesMigrateJSONPattern{
			Class:       p.Class,
			Old:         p.Old,
			New:         p.New,
			Description: p.Filename,
		})
	}

	// Files: sorted by path for stable, diff-friendly output.
	paths := make([]string, 0, len(fileHits))
	for p := range fileHits {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	filesOut := make([]fullimagesMigrateJSONFileResult, 0, len(paths))
	perClass := make(map[string]int)
	totalMatches := 0
	for _, path := range paths {
		hits := fileHits[path]
		fileTotal := 0
		// Stable per-class ordering (mirrors the human-readable path).
		for _, p := range patterns {
			if n, ok := hits[p.Class]; ok && n > 0 {
				fileTotal += n
				perClass[p.Class] += n
			}
		}
		totalMatches += fileTotal
		filesOut = append(filesOut, fullimagesMigrateJSONFileResult{
			Path:         path,
			TotalMatches: fileTotal,
			Hits:         hits,
		})
	}

	// When no files match, omit the per-class map entirely (empty
	// object is the canonical "no hits" signal for downstream consumers).
	var perClassOut map[string]int
	if len(perClass) > 0 {
		perClassOut = perClass
	}

	// When no warnings, omit the warnings array entirely (empty array
	// would be noise for the canonical happy-path consumers).
	var warningsOut []string
	if len(warnings) > 0 {
		warningsOut = warnings
	}

	return fullimagesMigrateJSONReport{
		Meta: fullimagesMigrateJSONMeta{
			TargetDir: rootDir,
			Exts:      exts,
			Mode:      mode,
			Timestamp: time.Now().UTC(),
		},
		Patterns:          patOut,
		Totals:            fullimagesMigrateJSONTotals{FilesWithHits: len(filesOut), TotalMatches: totalMatches},
		PerClassTotals:    perClassOut,
		Files:             filesOut,
		Warnings:          warningsOut,
		AppliedFilesCount: appliedCount,
	}
}
