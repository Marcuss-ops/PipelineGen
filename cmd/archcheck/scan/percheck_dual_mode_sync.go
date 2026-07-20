// Package scan — per-check forward-prevention gate for the
// dual-mode (sync/async) GenerateResponse helper surface.
//
// scan/percheck_dual_mode_sync.go is the canonical SSOT scanner
// for the post-PR-morti-sync (July 2026) wire-shape contract:
// GenerateResponse is async-only (6 fields: OK + JobID + Status
// + StatusURL + DocTitle + CurrentStage). syncSingle and syncMulti helpers
// (both zombie after the slim) MUST stay retired.
//
// Rationale (godlike/06 SSOT + godlike/07 NO-FAKE-AVAILABILITY):
// pre-PR-morti-sync, GenerateResponse carried 14 dead sync-only
// fields + 2 tombstone helpers (syncSingle + syncMulti). A
// cross-package rg audit confirmed ZERO production or test
// callers; the helper definitions existed but were dead code,
// the field declarations existed but were never written. The
// dual-mode surface was a fabrication risk (godlike/07): a
// future contributor could write resp.syncSingle() believing
// it was the canonical sync-fast-path, then ship a wire-shape
// that mismatched every operator-facing chrome test.
//
// This scanner is the forward-prevention seam: re-introduction
// of any of the 4 literal probes (2 calls + 2 method
// definitions) surfaces as a CI build failure (--strict mode
// exit 1), not a silent production wire-shape drift.
//
// Companion gate: the struct field count is locked to 6 by
// internal/api/script/response_test.go::TestGenerateRespons
// e_FieldCountLock. A future contributor adding a sync field
// would surface as a unit-test failure here AND as a CI
// archcheck failure in the new check; both gates are
// load-bearing and either one alone would be insufficient.
//
// Pattern (mostly mirrors percheck_player_client.go +
// percheck_monitor.go): the scanner walks every .go file under
// <root>/ excluding the standard whole-repo skip-dirs. The
// intentional DIVERGENCE from the sister-percheck precedent:
// ALL files (including *_test.go) are scanned — comment-only
// hits generate WARNs (godlike/07 residue accounting), real-
// code hits generate Violations. This divergence exists so
// test files that legitimately pin invariant contracts (e.g.
// response_test.go's TestGenerateResponse_FieldCountLock)
// surface as a load-bearing forward-prevention signal: any
// future regression that re-introduces a .syncSingle( or
// .syncMulti( call surface in test code fails the gate
// immediately. The dual-mode-sync-specific production-code
// exemptions are listed in dualModeSyncAllowlistRelative
// below.
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// dualModeSyncProbes is the canonical substring list this gate
// looks for. Each entry is a unique signature that any
// re-introduction of the dual-mode surface WOULD touch.
//
// The 2 .syncSingle(...) / .syncMulti(...) entries catch the
// CONSUMER side (caller locations in production code).
// The 2 func (r *GenerateResponse) syncSingle/syncMulti entries
// catch the PRODUCER side (helper re-introduction on the
// canonical struct).
//
// Re-introducing any of these is a godlike/06 SSOT violation
// (one-canonical-owner-per-fact: the async-only surface is
// owned by async() + the 5-field struct; resurrecting the sync
// helpers re-creates the dual-mode surface).
var dualModeSyncProbes = []string{
	".syncSingle(",
	".syncMulti(",
	"func (r *GenerateResponse) syncSingle",
	"func (r *GenerateResponse) syncMulti",
}

// dualModeSyncAllowlistRelative is the canonical repo-relative
// path allow-list where the dual-mode-sync literals may
// legitimately appear as production code (e.g. scanner self-
// reference). Test files (`*_test.go`) are also walked by the
// scanner (in contrast to percheck_player_client.go +
// percheck_monitor.go precedent) — comment-only probe hits in
// test files generate WARN instead of VIOLATION, providing godlike/07
// residue accounting for the forward-prevention gate without
// excluding legitimate test-side regression guards.
//
// Why cmd/archcheck/scan is the only allow-list entry: the
// scanner legitimately contains the literals as the search
// target (`var dualModeSyncProbes = []string{".syncSingle("}`)
// and as part of the violation Note. Without this exemption
// the gate would fail-closed on its own source code
// (`--strict` mode exit 1 on the scanner's own file).
//
// Future additional exemptions MUST be added to this map
// explicitly (no implicit allow-list growth); each new entry
// requires (a) a one-line rationale in the matching goddoc,
// (b) a regression test in percheck_dual_mode_sync_test.go,
// (c) the standard code-reviewer-minimax-m3 SHIP gate.
var dualModeSyncAllowlistRelative = map[string]bool{
	// The scanner self-exemption is matched as a path PREFIX
	// (not exact-equality) by dualModeSyncAllowlistMatch below:
	// it accepts any file under cmd/archcheck/scan/. This is
	// the percheck_player_client.go precedent (see the
	// playerClientSkipPathPrefixes comment block for the
	// nested-prefix rationale).
	"cmd/archcheck/scan": true,
}

// dualModeSyncSkipDirs is the standard whole-repo skip-list for
// tree walks (mirrors percheck_player_client.go +
// percheck_monitor.go + percheck_typeredecl.go):
//   - .git: git internals (always skipped)
//   - vendor: vendored deps (if present)
//   - node_modules: npm-installed deps (node-scraper / future
//     frontend)
//   - node-scraper: the Node.js artlist scraper
//     (archcheck is a Go-only gate; .js files would never
//     match the probe substrings but directory-level skip is
//     cheaper + matches user expectations)
//   - examples: third-party integration demos
//     (examples/worker_integration/main.go is documented to
//     hit the 410-Gone legacy route, NOT a sync helper call)
//   - scripts: shell/archived-shell assets (Phase 1 mirrored
//     this from the shell Check N skip list)
//
// Entries are matched against filepath.Base(path). Standard
// basename-only match; nested subdirectories of these
// directories are skipped automatically by filepath.SkipDir.
var dualModeSyncSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"scripts":      true,
}

// dualModeSyncAllowlistMatch decides whether the given
// repo-relative path is exempt from the gate. Returns true
// (skip the file) when the path matches an entry in
// dualModeSyncAllowlistRelative via EITHER exact-equality OR
// prefix-match (the latter handles nested directories like
// `cmd/archcheck/scan/percheck_dual_mode_sync.go` whose basename
// is just `percheck_dual_mode_sync.go`).
//
// godlike/06 SSOT (one canonical owner per fact): the
// allow-list is the single source of truth for path exemptions;
// adding entries here is the ONLY way to extend the exemption
// surface. Test files (`*_test.go`) are NOT excluded at the
// walker level — they are scanned + comment-only hits generate
// WARNs (godlike/07 residue accounting) + real-code hits
// generate Violations. The only legitimate _test.go carry-
// forward surface today is response_test.go whose comment-only
// references are documented + non-fatal.
func dualModeSyncAllowlistMatch(relSlash string) bool {
	for allowedPath := range dualModeSyncAllowlistRelative {
		if relSlash == allowedPath ||
			strings.HasPrefix(relSlash, allowedPath+"/") {
			return true
		}
	}
	return false
}

// ScanDualModeSync is the canonical entry-point for the
// dual-mode-sync forward-prevention gate. Wired into
// cmd/archcheck/runner.go::DefaultChecks as
// "percheck_dual_mode_sync" (added next to
// percheck_player_client_centralization to group the
// forward-prevention seams).
//
// The function signature matches the canonical CheckSpec
// closure contract:
//
//	func(root string, pol *policy.Policy, r *report.Report)
//
// No productionOnly plumbing is required (the canonical
// async-only wire shape is unconditional — no producer/consumer
// split exists in the gate).
func ScanDualModeSync(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // policy is unused; gate is text-substring based.
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if dualModeSyncSkipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(root, path)
			relSlash := filepath.ToSlash(rel)
			if dualModeSyncAllowlistMatch(relSlash) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)
		if dualModeSyncAllowlistMatch(relSlash) {
			return nil
		}
		scanDualModeSyncFile(path, relSlash, r)
		return nil
	})
}

// scanDualModeSyncFile reads a single .go file line-by-line and
// emits one violation per matching PRODUCTION-CODE line
// (comment-only hits are counted + WARNed but do not fail,
// mirroring percheck_player_client.go godlike/07 residue-
// accounting semantics).
//
// The match strategy is substring-based (the 4 probe strings
// are unique enough that false positives on adjacent code are
// negligible). The first matching probe wins; we emit one
// violation per matching line (NOT one per file) so the CI
// log shows every offender line for fast remediation.
func scanDualModeSyncFile(path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentHits := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		matched := ""
		for _, probe := range dualModeSyncProbes {
			if strings.Contains(line, probe) {
				matched = probe
				break
			}
		}
		if matched == "" {
			continue
		}
		// Comment-only bucket: descriptive prose that mentions
		// the helper name, NOT a real call site. godlike/07
		// residue-accounting pattern (mirrors Check 54 +
		// Check N): log a WARN-equivalent entry so future
		// readers see the residue but do NOT contribute to
		// the hard-fail set.
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			commentHits++
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        lineNo,
			Rule:        "percheck_dual_mode_sync",
			Severity:    string(report.SeverityError),
			MatchedRule: "dual_mode_sync_gate",
			Note: dualModeSyncViolationNote +
				" Detected offending literal: " + matched,
		})
	}
	if commentHits > 0 {
		// godlike/07 no-fake-availability residue accounting:
		// WARN the comment-only hits so future drift is visible
		// in CI without contributing to the hard-fail set.
		r.Warnings = append(r.Warnings,
			"Check dual_mode_sync: "+strconv.Itoa(commentHits)+
				" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability). "+
				"If a comment mentions a sync-helper-by-name AND there is no actual call site, consider rewording to avoid the substring probe.")
	}
}

// dualModeSyncViolationNote is the violation Note string. The
// message references the canonical async-only contract + the
// PR-morti-sync precedent + the response_test.go companion
// gate so future agents reading the CI failure have the full
// migration context inline.
const dualModeSyncViolationNote = "forbidden dual-mode (sync-async) GenerateResponse helper re-introduction. Post-PR-morti-sync (July 2026): GenerateResponse is async-only (6 fields: OK + JobID + Status + StatusURL + DocTitle + CurrentStage). syncSingle and syncMulti helpers were RETIRED with zero production or test callers (cross-package rg audit confirmed). Re-introducing them reverses godlike/06 SSOT (the async() helper is the SOLE canonical path; the 6-field struct is the wire-shape contract) and re-creates the dual-mode surface that godlike/07 NO-FAKE-AVAILABILITY explicitly retired. The struct field count is locked to 6 by internal/api/script/response_test.go::TestGenerateResponse_FieldCountLock; a future contributor who bumps the count MUST also revisit this gate."
