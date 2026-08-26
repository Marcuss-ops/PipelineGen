// Package scan — per-check forward-prevention gate that bans
// direct SQL writes to media_assets, asset_locations, and
// outbox_events from the finalizer package
// (PR-ASSET-COMMITTER-CENTRALIZE, July 2026).
//
// godlike/06 SSOT: the canonical owner of the asset commit
// boundary is `persistence.AssetCommitter` (implemented by
// `SQLiteAssetCommitter` at
// `internal/platform/sqlite/assets/asset_committer.go`).
// Every code path that durably creates or updates a media_assets
// row, an asset_locations row, or the canonical
// asset.index.requested outbox event MUST route through this port.
//
// The `internal/capabilities/assets/finalizer` package is the
// "processor" in scope: it hosts the `AssetTxFinalizer` that
// orchestrates the asset commit per-PublishedArtifact. The
// `AssetTxFinalizer` has a `WithCommitter` method that delegates
// the media_assets + primary asset_locations + outbox writes to
// the canonical `persistence.AssetCommitter`; a legacy
// `finalizeLegacy` path is retained for backward compat but is
// REMOVED in a follow-up cleanup wave.
//
// This gate is the forward-prevention fence that bans any
// re-introduction of direct SQL writes in the finalizer package
// (godlike/06 SSOT: one canonical owner per fact). The check
// inspects the finalizer package and emits a violation for any
// non-canonical, non-test SQL write to the three canonical
// tables. The canonical AssetCommitter is exempt (it IS the
// SSOT); the test files (`*_test.go`) are exempt (regression-
// guard surface).
//
// The companion `percheck_finalizer_no_direct_sql_test.go` pins
// (a) "legacy SQL trip" (legacy file emits violation),
// (b) "canonical committer is exempt" (no violation), and
// (c) "comment-only is residue-accounted" (no violation, WARN).
//
// scanner policy (mirrors percheck_mediatransformer_no_infra_fields.go):
//   - skip file basenames `.git`, `vendor`, `node_modules`,
//     `node-scraper`, `examples`, `archivist`, `docs`, `data`.
//   - skip `_test.go` files (regression-guard surface).
//   - skip `cmd/archcheck/scan/**` (this scanner file + sibling
//     scanners reference the canonical literals).
//   - allow the canonical SOLE owner
//     (internal/platform/sqlite/assets/asset_committer.go)
//     — the gate inspects the finalizer package but the SSOT is
//     elsewhere.
//   - comment-only references to media_assets / asset_locations /
//     outbox_events are WARNed (residue accounting, godlike/07).
//
// matched rule_id: `percheck_finalizer_no_direct_sql`.
package boundaries

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// finalizerNoSQLCanonicalOwner is the canonical SOLE owner of the
// SQL that writes media_assets + asset_locations + outbox_events.
// The gate inspects the finalizer package but the inspection is
// read-only — the SSOT lives here.
const finalizerNoSQLCanonicalOwner = "internal/platform/sqlite/assets/asset_committer.go"

// finalizerNoSQLScanRoot is the package scanned by the gate.
// Every Go file under this root (except tests + the canonical
// SSOT) is inspected for forbidden SQL patterns.
const finalizerNoSQLScanRoot = "internal/capabilities/assets/finalizer"

// finalizerNoSQLForbiddenPatterns lists the SQL patterns that
// the gate bans from the finalizer package. The match is
// case-insensitive and substring-based (so it catches both
// `INSERT INTO media_assets` and `insert into media_assets`).
//
// `media_assets`     — canonical asset row.
// `asset_locations`  — canonical location row.
// `outbox_events`    — canonical outbox row (asset.index.requested
//
//	and friends).
var finalizerNoSQLForbiddenPatterns = []string{
	"INSERT INTO media_assets",
	"INSERT INTO asset_locations",
	"INSERT INTO outbox_events",
	"insert into media_assets",
	"insert into asset_locations",
	"insert into outbox_events",
}

// finalizerNoSQLLineRe matches a Go line that contains any of
// the forbidden SQL patterns. It is intentionally a substring
// match (not a full statement parse) because the goal is
// forward-prevention, not syntactic validation.
var finalizerNoSQLLineRe = regexp.MustCompile(
	`(?i)\b(INSERT\s+INTO)\s+(media_assets|asset_locations|outbox_events)\b`,
)

// finalizerNoSQLRule is the rule-family id the scanner emits.
// Mirrors percheck_mediatransformer_no_infra_fields.go
// MatchedRule naming.
const finalizerNoSQLRule = "percheck_finalizer_no_direct_sql"

// finalizerNoSQLNote is the violation Note string for
// forbidden SQL writes. The message references the canonical
// SOLE owner + the forward-prevention gate so the operator sees
// the migration path inline.
const finalizerNoSQLNote = "forbidden direct SQL write to media_assets / asset_locations / outbox_events from the finalizer package (PR-ASSET-COMMITTER-CENTRALIZE, July 2026): the canonical owner of these writes is persistence.AssetCommitter (implemented by SQLiteAssetCommitter at internal/platform/sqlite/assets/asset_committer.go). Route every asset commit through AssetTxFinalizer.WithCommitter(committer) or a higher-level AssetCommitter caller. The legacy finalizeLegacy path is retired; the canonical finalizeWithCommitter is the single point of write"

// finalizerNoSQLSkipDirs mirrors percheck_mediatransformer_no_infra_fields.go's
// standard skip-dir set.
var finalizerNoSQLSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// finalizerNoSQLSkipPathPrefixes is the scan's own package
// exemption — this file declares regex literals matching the
// forbidden substrings (false-positive exemption).
var finalizerNoSQLSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// finalizerNoSQLWarn is the WARN-bucket emitter for
// residue-accounting. Mirrors mediaTransformerWarn.
func finalizerNoSQLWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, finalizerNoSQLRule+" "+label+" "+msg)
}

// ScanFinalizerNoDirectSQL walks the finalizer package and
// inspects every non-test Go file for direct SQL writes to
// media_assets, asset_locations, and outbox_events. Each
// forbidden write emits a violation; comment-only references
// are residue-accounted (WARNed, not violated) per godlike/07.
//
// The canonical AssetCommitter file
// (`internal/platform/sqlite/assets/asset_committer.go`)
// is exempt — it IS the SSOT. The gate does not inspect that
// file; the inspection target is the finalizer package only.
//
// In the current state (post-PR-ASSET-COMMITTER-CENTRALIZE) the
// gate WILL trip on the legacy `finalizeLegacy` path and its
// sibling SQL helpers (`upsertMediaAsset`, `insertOutboxEvent`,
// `upsertAssetLocation`). The violations are EXPECTED and
// documented as forward-pointers to the removal step; the gate
// passes once the legacy path is deleted and the constructor
// is changed to require a wired `persistence.AssetCommitter`.
func ScanFinalizerNoDirectSQL(root string, pol *policy.Policy, r *report.Report) {
	_ = pol
	scanRoot := filepath.Join(root, finalizerNoSQLScanRoot)

	// Walk the finalizer package.
	err := filepath.Walk(scanRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			// Skip standard excluded dirs even if they appear
			// inside the package (defense-in-depth).
			base := filepath.Base(path)
			if finalizerNoSQLSkipDirs[base] {
				return filepath.SkipDir
			}
			return nil
		}
		// Only inspect .go files.
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip test files (regression-guard surface).
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Inspect the file.
		inspectFinalizerNoSQLFile(root, path, r)
		return nil
	})
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/capabilities/assets/finalizer",
			File:        finalizerNoSQLScanRoot,
			Line:        0,
			Rule:        finalizerNoSQLRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "scan_root_unreadable",
			Note:        finalizerNoSQLNote + " | cannot walk scan root: " + err.Error(),
		})
	}
}

// inspectFinalizerNoSQLFile opens a single Go file and
// scans each line for forbidden SQL patterns. The canonical
// AssetCommitter file is exempt (it IS the SSOT).
func inspectFinalizerNoSQLFile(root, absPath string, r *report.Report) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	// Normalize to forward slashes so the emitted File field and
	// the canonical-path comparisons are platform-independent.
	relPath = filepath.ToSlash(relPath)
	// Defense-in-depth: skip the canonical SSOT (it lives in a
	// different package but the guard makes the intent explicit).
	if relPath == finalizerNoSQLCanonicalOwner {
		return
	}
	// Skip the scan's own package (false-positive exemption for
	// regex literals that match the forbidden substrings).
	for _, prefix := range finalizerNoSQLSkipPathPrefixes {
		if strings.HasPrefix(relPath, prefix) {
			return
		}
	}

	f, err := os.Open(absPath)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/capabilities/assets/finalizer",
			File:        relPath,
			Line:        0,
			Rule:        finalizerNoSQLRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "file_unreadable",
			Note:        finalizerNoSQLNote + " | cannot open file: " + err.Error(),
		})
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)

		// Comment-only line: residue-accounted.
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			if hasAnyFinalizerPattern(line) {
				commentOnly++
			}
			continue
		}

		// Match the forbidden SQL pattern.
		m := finalizerNoSQLLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// Extract the table name from the regex match.
		table := strings.ToLower(strings.TrimSpace(m[2]))
		r.Violations = append(r.Violations, report.Violation{
			Package: "internal/capabilities/assets/finalizer",
			File:    relPath,
			Line:    lineNo,
			Rule:    finalizerNoSQLRule,
			Severity: func() string {
				// The gate emits VIOLATION so a re-introduction
				// of direct SQL writes trips the build. The
				// current violations (legacy finalizeLegacy +
				// sibling SQL helpers) are EXPECTED and
				// documented as forward-pointers to the removal
				// step.
				return string(report.SeverityError)
			}(),
			MatchedRule: "forbidden_sql_" + table,
			Note: finalizerNoSQLNote +
				" | file: " + relPath +
				" | matched table: " + table +
				" | route through persistence.AssetCommitter (canonical SSOT: " + finalizerNoSQLCanonicalOwner + ")",
		})
	}
	if commentOnly > 0 {
		finalizerNoSQLWarn(r, "forbidden-sql:",
			"comment-only reference(s) to forbidden SQL patterns in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// hasAnyFinalizerPattern returns true if `s` contains any of
// the forbidden SQL patterns (case-insensitive).
func hasAnyFinalizerPattern(s string) bool {
	lower := strings.ToLower(s)
	for _, p := range finalizerNoSQLForbiddenPatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}
