// Package scan — per-check forward-prevention gate for the
// `player_client=` literal centralization.
//
// scan/percheck_player_client.go owns the Go migration of
// scripts/ci-architectural-checks.sh::Check N (player_client=
// centralization gate). Phase 2 of PR-ARCHCHECK-GO-MIGRATION-
// PHASE-1 (deadline 2026-08-15) ships this scanner alongside
// the original shell check, which is RETAINED as a transitional
// baseline per godlike/08 §"Zero-baseline rule".
//
// Rationale (godlike/06 SSOT + godlike/07 NO-FAKE-AVAILABILITY):
// the `player_client=` literal is the canonical yt-dlp
// --extractor-args argument centralized in
// internal/platform/ytdlp/cmd_builder.go by commit
// f3f1ee90 (the web,android policy). Pre-PR-PLAYER-CLIENT-DRIFT-
// FIX (2026-07-06), the literal had drifted to
// internal/infrastructure/youtube/metadata.go:95 with the
// REVERSED order (android,web) — a godlike/06 SSOT violation
// where production code re-declared a centralized literal, and
// a godlike/07 NO-FAKE-AVAILABILITY regression where the drift
// surfaced only as a wrong-duration symptom on some videos, not
// as a build failure.
//
// This check is the forward-prevention gate: future drift
// surfaces as a CI build failure (--strict mode exit 1), not
// as a silent production symptom.
//
// Excluded files (mirrors the shell check semantics):
//   - internal/platform/ytdlp/cmd_builder.go: the
//     canonical SOLE owner of the literal (per godlike/06
//     SSOT). The literal MUST live here; all other production
//     code routes through ytdlp.BaseArgs().
//   - All *_test.go files: tests legitimately reference the
//     literal for regression guards (cmd_builder_test.go pins
//     the canonical value + NeverWebOnly regression; metadata
//     _test.go has 3 new tests that pin the canonical web,android
//     order). Excluding tests prevents false positives.
//
// Comment-only hits are WARNED (not violation) per the same
// godlike/07 no-fake-availability residue-accounting pattern
// used by percheck_monitor.go (Check 54): descriptive prose
// that mentions the literal is not a real re-declaration, but
// is logged so future drift is visible in CI output every run.
package boundaries

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// playerClientLiteral is the canonical substring the gate
// looks for. The literal is `player_client=` (with the
// trailing `=` so we match the assignment form, not any
// prose mention of "player_client" without a value).
const playerClientLiteral = "player_client="

// playerClientCanonicalRelPath is the repo-relative path
// to the canonical SOLE owner of the literal. Every other
// production Go file MUST route through ytdlp.BaseArgs()
// instead of re-declaring the literal.
const playerClientCanonicalRelPath = "internal/platform/ytdlp/cmd_builder.go"

// playerClientScanNote is the violation Note string. The
// message references the canonical SSOT file + the SSOT
// contract + the PR-PLAYER-CLIENT-DRIFT-FIX precedent so
// future agents reading the CI failure have the full
// context inline.
const playerClientScanNote = "forbidden `player_client=` literal outside canonical SSOT (internal/platform/ytdlp/cmd_builder.go); godlike/06 SSOT requires every consumer to route through ytdlp.BaseArgs() (PR-PLAYER-CLIENT-DRIFT-FIX, 2026-07-06). The canonical command-builder is the SOLE owner of the web,android policy (commit f3f1ee90)."

// playerClientSkipDirs is the standard skip-list for whole-repo
// walks (mirrors the skipDirs pattern in percheck_typeredecl.go
// + percheck_monitor.go: .git + vendor + node_modules + the
// node-scraper frontend + examples + scripts). Entries are
// matched against filepath.Base(path) — i.e., the IMMEDIATE
// directory name — so this map covers top-level skip targets
// only.
var playerClientSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"scripts":      true,
}

// playerClientSkipPathPrefixes covers NESTED path prefixes
// (matched against the repo-relative path with forward
// slashes). The standard `playerClientSkipDirs` map is
// basename-only and misses nested directories like
// `cmd/archcheck/scan` (whose basename is just `scan`).
//
// Why cmd/archcheck/scan is in the skip list: the scanners
// LEGITIMATELY need to contain the literal as the search
// target (e.g., the const `playerClientLiteral = "player_client="`
// in this very file) and as part of the violation Note
// (e.g., `forbidden `player_client=` literal outside canonical
// SSOT...`). Without this exemption, the gate would
// fail-closed on its own source code (`--strict` mode exit 1).
// The same exemption applies to any future per-check scanner
// that needs to reference the literal as the search target.
//
// All other production .go files (cmd/* except cmd/archcheck/scan,
// internal/**, pkg/**, etc.) MUST route through
// ytdlp.BaseArgs() per godlike/06 SSOT and the canonical
// f3f1ee90 centralization in cmd_builder.go.
var playerClientSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// ScanPlayerClientCentralization walks every production .go
// file under <root>/ (excluding *_test.go + the canonical
// cmd_builder.go SSOT) and emits an error-severity violation
// for any file containing the literal substring
// "player_client=".
//
// Severity is `error` (forward-prevention gate; the runner
// --strict mode promotes to ExitViolations). For non-strict
// mode, the runner still prints the report; the exit code
// remains 0 unless --strict is on.
//
// Comment-only hits are logged as warnings via r.Warnings
// (godlike/07 no-fake-availability residue accounting) but
// do NOT contribute to the hard-fail set.
func ScanPlayerClientCentralization(root string, pol *policy.Policy, r *report.Report) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if playerClientSkipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			// Nested-path skip check: matches full repo-relative
			// path prefixes like "cmd/archcheck/scan" (whose
			// basename is just "scan", missed by the basename
			// map above). Computed here so the check is O(prefix
			// count) per dir rather than O(basename) per dir.
			rel, _ := filepath.Rel(root, path)
			relSlash := filepath.ToSlash(rel)
			for _, prefix := range playerClientSkipPathPrefixes {
				if relSlash == prefix || strings.HasPrefix(relSlash, prefix+"/") {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Exclude test files (regression guards legitimately
		// reference the literal for invariant pinning).
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)
		// Exclude the canonical SSOT file (the literal MUST
		// live here; this gate ensures no other file re-declares
		// it).
		if relSlash == playerClientCanonicalRelPath {
			return nil
		}
		scanPlayerClientFile(path, relSlash, r)
		return nil
	})
}

// scanPlayerClientFile reads a single .go file line-by-line
// and emits violations / warnings per the gate contract.
// See ScanPlayerClientCentralization for the full semantics.
func scanPlayerClientFile(path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentCount := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if !strings.Contains(line, playerClientLiteral) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		// Bucket 1: full-line `//`-prefixed comment (descriptive
		// prose, not a real re-declaration). Logged as warning
		// per godlike/07 residue accounting, NOT surfaced as a
		// violation.
		if strings.HasPrefix(trimmed, "//") {
			commentCount++
			continue
		}
		// Bucket 2: production code containing the literal. This
		// is the hard-fail class — emit a violation.
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        lineNo,
			Rule:        "percheck_player_client_centralization",
			Severity:    string(report.SeverityError),
			MatchedRule: "player_client_centralization_gate",
			Note:        playerClientScanNote,
		})
	}

	// WARN accounting (godlike/07 no-fake-availability residue
	// accounting): comment-only hits are logged so future drift
	// is visible in CI output every run. They do NOT contribute
	// to the hard-fail set.
	if commentCount > 0 {
		r.Warnings = append(r.Warnings, "Check N (player_client=): "+strconv.Itoa(commentCount)+
			" comment-only reference(s) in "+relPath+
			" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}
