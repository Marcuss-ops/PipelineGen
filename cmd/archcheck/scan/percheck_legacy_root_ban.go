// Package scan — Wave-25 forward-prevention gate for legacy-root
// imports.
//
// scan/percheck_legacy_root_ban.go enforces godlike/07 ZERO_LEGACY
// POLICY §"No new imports of legacy roots" for the canonical
// deprecation targets enumerated in the godlike/07 retirement plan:
// internal/transcription (P1-4 step 1, retired 2026-07-30),
// internal/youtube (P1-4 step 2, retired 2026-07-30),
// internal/scriptgeneration (P1-4 step 3, retired 2026-07-30), and
// internal/domain/job (P1-7 retired atomic cutover 2026-07-30 —
// the back-compat alias module backed by the canonical kernel/job
// tree). The kernel/job canonical destination is exempted from the
// ban because every operational import path targets
// `internal/kernel/job` directly.
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
// All four Wave-25 retired legacy roots are live. P1-4 step 3
// (internal/scriptgeneration) is the final retirement closure:
// the legacy root was git-mv'd to internal/capabilities/scripts/.
// Its SQLite adapter remains under internal/infrastructure/database/sqlite/scripts/legacy/
// because that path is infrastructure-owned. The capability cutover preserves the
// scriptgeneration package API while removing the application legacy facade.
var legacyRootImportBanned = []string{
	"github.com/Marcuss-ops/PipelineGen/internal/transcription",
	"github.com/Marcuss-ops/PipelineGen/internal/youtube",
	"github.com/Marcuss-ops/PipelineGen/internal/scriptgeneration",
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job",
}

// selfSkipBaseName is the file basename of the gate's own source
// file (this file). The gate's banned-path declarations are verbatim
// String constants that would otherwise self-flag as violations on
// every hard-pinned run; without this skip the scanner would emit a
// percheck_legacy_root_ban violation for the gate's own ban list
// (zero-violations goal is impossible by construction). The skip
// keys on basename (NOT full path) so accidental file-tree
// relocations inside cmd/archcheck/scan/ do not silently re-trigger
// the loop. The skip mirrors the canonical `cmd/archcheck/scan`
// prefix exemption used by percheck_api_policy_literals (see the
// apiPolicyLiteralsSkipPathPrefixes pattern) but is filename-only
// because the legacy-root-ban pattern is data, not a path prefix.
//
// P1-7 retirement (2026-07-30): this self-skip is REQUIRED once
// `percheck_legacy_root_ban` is added to `hard_gates` in
// architecture/policy.yaml. Without it, every CI run after the
// promotion produces a fatal violation against this very file. With
// it, the string-data inside `legacyRootImportBanned` is preserved
// as the canonical banned-list declaration and only OPERATIONAL
// imports (in OTHER files) are flagged.
const selfSkipBaseName = "percheck_legacy_root_ban.go"

// ScanLegacyRootImportBan walks <root> for non-test .go files and
// reports any line whose text contains a substring in
// legacyRootImportBanned. Symmetric to percheck_api_infrastructure_imports
// but without an allowlist (deleted roots admit no exceptions).
//
// Promotion to a Wave-25 hard gate via the runner's pre-exit
// evaluation pass mirrors the existing percheck_api_infrastructure
// pattern: rule id `percheck_legacy_root_ban` is in the canonical
// hard_gates list once the P1-7 cutover has eliminated all backwards-
// compatible consumers of `internal/domain/job`. Demotion requires an
// SSOT-marker in architecture/current.yaml per godlike/08 (no
// in-tree override).
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
		// Self-skip: this file contains the banned-path
		// declarations as String constants; without this guard
		// the hard-promoted gate would self-violate on its
		// own data, producing zero-violations by construction
		// impossible. Per godlike/07 NO-OPERATOR-SURPRISE the
		// rule must be hard-pin-able alongside its own
		// declaration site. The skip keys on the basename
		// (NOT the full path) so accidental file-tree
		// relocations inside cmd/archcheck/scan/ do not
		// silently re-trigger the self-flag loop.
		if filepath.Base(relSlash) == selfSkipBaseName {
			return nil
		}
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
