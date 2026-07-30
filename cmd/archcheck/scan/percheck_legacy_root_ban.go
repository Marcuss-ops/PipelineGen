// Package scan — Wave-25 forward-prevention gate for legacy-root
// imports.
//
// scan/percheck_legacy_root_ban.go enforces godlike/07 ZERO_LEGACY
// POLICY §"No new imports of legacy roots" for the canonical
// deprecation targets enumerated in the godlike/07 retirement plan:
// internal/transcription (P1-4 step 1, retired 2026-07-30),
// internal/youtube (P1-4 step 2, retired 2026-07-30),
// internal/scriptgeneration (P1-4 step 3, planned).
//
// The gate is a hard-coded ban list: any *.go file under <root>
// whose contents include a canonical import path substring in the
// banned list is reported as a SeverityError violation. _test.go
// files, .git/, vendor/, node-scraper/, examples/, scripts/, docs/,
// and testdata/ are skipped (fixture / residency carve-out,
// mirrors percheck_api_infrastructure_imports skipDirs).
//
// No allowlist: a deleted root has zero legal exceptions in the
// post-cutover tree. Operators who need to grep historical callers
// can use `git log --diff-filter=D -- internal/<root>/*` after the
// archive snapshot is taken; this percheck is a forward-prevention
// gate, not a historical-audit surface.
//
// godlike/06 SSOT: this percheck is the SOLE canonical forward-
// prevention surface for legacy-root reverse-reimport. The disable-
// for-a-merge escape hatch lives ONLY in cmd/archcheck/checks.go
// (comment out the entry there), not on the rule itself.
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// legacyRootImportBanned is the list of full canonical import
// path substrings that the gate flags. Anchored on the canonical
// GitHub import path so shorthand aliases like
// transc "internal/transcription" (a hypothetical misuse) are NOT
// caught here — per godlike/06, shorthand aliases for already-dead
// roots are themselves SSOT violations caught elsewhere; this
// percheck enforces the import-path SSOT.
//
// P1-4 steps 2 + 3 are commented out and uncomment as each
// retirement lands. Step 1 (internal/transcription) is live.
var legacyRootImportBanned = []string{
	"github.com/Marcuss-ops/PipelineGen/internal/transcription",
	"github.com/Marcuss-ops/PipelineGen/internal/youtube",
	// "github.com/Marcuss-ops/PipelineGen/internal/scriptgeneration", // P1-4 step 3 — activate when step 3 lands.
}

// ScanLegacyRootImportBan walks <root> for non-test .go files and
// reports any line whose text contains a substring in
// legacyRootImportBanned. Symmetric to percheck_api_infrastructure_imports
// but without an allowlist (deleted roots admit no exceptions).
//
// Promotion to a Wave-25 hard gate via the runner's pre-exit
// evaluation pass mirrors the existing percheck_api_infrastructure
// pattern: rule id `percheck_legacy_root_ban` is in the canonical
// hard_gates list only once P1-4 step 2 lands (so step 1 stays a
// soft Phase-0 report for the first push to give downstream ops a
// chance to update). Demotion requires an SSOT-marker in
// architecture/current.yaml per godlike/08 (no in-tree override).
func ScanLegacyRootImportBan(root string, _ *policy.Policy, r *report.Report) {
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
		"docs": true, "testdata": true,
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
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		lineNum := 0
		for sc.Scan() {
			lineNum++
			text := sc.Text()
			for _, banned := range legacyRootImportBanned {
				if strings.Contains(text, banned) {
					r.Violations = append(r.Violations, report.Violation{
						File:        relSlash,
						Line:        lineNum,
						MatchedRule: "legacy_root_import_via_" + banned,
						Rule:        "percheck_legacy_root_ban",
						Severity:    string(report.SeverityError),
						Note:        "forbidden import of deleted legacy root " + banned + " (godlike/07 ZERO_LEGACY_POLICY; P1-4 retirement sequence); route the dependency through the canonical replacement (e.g. internal/application/transcripts for transcription, application/youtube + infrastructure/youtube for the youtube root) and rerun the percheck",
					})
				}
			}
		}
		return nil
	})
}
