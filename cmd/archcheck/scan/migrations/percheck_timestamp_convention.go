// Package scan — percheck_timestamp_convention.go: forward-prevention gate for
// the timestamp format SSOT (docs/migrations/TIMESTAMP_CONVENTION.md).
//
// WHY THIS GATE EXISTS. Timestamps were written in two different string
// formats in the same column: RFC 3339 UTC by the Go writers and
// `datetime('now')` (YYYY-MM-DD HH:MM:SS, no T, no Z) by 62 migration DEFAULTs,
// while 31 declarations used `DEFAULT ”` as "no value" — a non-time that
// still sorts before every real timestamp. Mixed formats break every
// lexicographic comparison (`next_attempt_at > ...`, lease expiry scans) and
// every RFC 3339 parse. The convention freezes one format; this gate enforces
// it on new migrations.
//
// WHAT IT CHECKS. All error-severity, all fail-closed:
//
//  1. DOC LOCKSTEP — docs/migrations/TIMESTAMP_CONVENTION.md must exist and
//     declare the canonical default expression
//     (strftime('%Y-%m-%dT%H:%M:%fZ','now')) exactly as pinned below.
//  2. MIXED-FORMAT DEFAULT — `datetime('now')` in a migration file outside the
//     historical allowlist is a violation (canonical replacement:
//     DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))).
//  3. EMPTY-STRING DEFAULT — a timestamp column (*_at / *_expiry / *_until)
//     declared with DEFAULT ” outside the allowlist is a violation (absence
//     is NULL, not ”).
//  4. ALLOWLIST RATCHET — docs/migrations/timestamp-convention-allowlist.txt is
//     closed and shrink-only: an entry whose file is gone or no longer offends
//     is a stale-entry violation (remove it in the same change that cleans the
//     file).
//
// matched rule_id: `percheck_timestamp_convention`.
package migrations

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/statematrix"
)

const (
	timestampConventionRule   = "percheck_timestamp_convention"
	timestampConventionDoc    = "docs/migrations/TIMESTAMP_CONVENTION.md"
	timestampConventionAllow  = "docs/migrations/timestamp-convention-allowlist.txt"
	canonicalTimestampDefault = "strftime('%Y-%m-%dT%H:%M:%fZ','now')"
	mixedFormatDefault        = "datetime('now')"
)

// isTimestampColumn reports whether a column name is a timestamp column under
// the convention: it ends in _at, _expiry or _until.
func isTimestampColumn(name string) bool {
	return strings.HasSuffix(name, "_at") ||
		strings.HasSuffix(name, "_expiry") ||
		strings.HasSuffix(name, "_until")
}

// ScanTimestampConvention runs the four legs described in the package comment.
func ScanTimestampConvention(root string, _ *policy.Policy, r *report.Report) {
	if !scanTimestampDocLockstep(root, r) {
		return
	}
	allow := loadTimestampAllowlist(root, r)
	seenAllow := map[string]bool{}
	for _, dir := range stateMigrationRoots {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			timestampViolation(r, dir, 0, "timestamp_dir_unreadable", err.Error())
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			rel := filepath.ToSlash(filepath.Join(dir, e.Name()))
			body, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				timestampViolation(r, rel, 0, "timestamp_file_unreadable", err.Error())
				continue
			}
			hits := timestampOffenses(string(body))
			if allow[rel] {
				seenAllow[rel] = true
				if len(hits) == 0 {
					timestampViolation(r, timestampConventionAllow, 0, "timestamp_allowlist_stale",
						"entry "+rel+" no longer offends the timestamp convention; drop the entry")
				}
				continue
			}
			for _, hit := range hits {
				timestampViolation(r, rel, hit.line, hit.rule, hit.note)
			}
		}
	}
	for name := range allow {
		if !seenAllow[name] {
			timestampViolation(r, timestampConventionAllow, 0, "timestamp_allowlist_stale",
				"entry "+name+" has no matching migration file in the scanned roots")
		}
	}
}

// timestampOffense is one detected convention violation inside a migration.
type timestampOffense struct {
	line int
	rule string
	note string
}

// timestampOffenses scans one SQL file for the two banned shapes.
func timestampOffenses(sql string) []timestampOffense {
	var hits []timestampOffense
	// Leg 2: mixed-format DEFAULT, tracked per line after comment stripping.
	lines := strings.Split(sql, "\n")
	for i, raw := range lines {
		line := raw
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		if strings.Contains(line, mixedFormatDefault) {
			hits = append(hits, timestampOffense{
				line: i + 1,
				rule: "timestamp_mixed_format_default",
				note: "uses " + mixedFormatDefault + " (YYYY-MM-DD HH:MM:SS) instead of the canonical RFC 3339 UTC form; see " + timestampConventionDoc + " — use DEFAULT (" + canonicalTimestampDefault + ")",
			})
		}
	}
	// Leg 3: DEFAULT '' on a timestamp column.
	for _, def := range statematrix.ExtractColumns(sql) {
		if !isTimestampColumn(def.Name) || !def.HasDefault || def.Default != "" {
			continue
		}
		hits = append(hits, timestampOffense{
			line: 0,
			rule: "timestamp_empty_string_default",
			note: "column " + def.Table + "." + def.Name + " declares DEFAULT '' — an empty string is not a time; absence is NULL (see " + timestampConventionDoc + ")",
		})
	}
	return hits
}

// scanTimestampDocLockstep enforces leg 1: the convention doc exists and pins
// the exact canonical expression this gate enforces. Returns false when the
// doc is missing/unusable so dependent legs are skipped (fail-closed).
func scanTimestampDocLockstep(root string, r *report.Report) bool {
	rel := timestampConventionDoc
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		timestampViolation(r, rel, 0, "timestamp_doc_missing",
			"cannot open "+rel+": "+err.Error())
		return false
	}
	if !strings.Contains(string(body), canonicalTimestampDefault) {
		timestampViolation(r, rel, 0, "timestamp_doc_drift",
			"the doc does not declare the canonical default expression "+canonicalTimestampDefault+" pinned by this gate; doc and gate must move together")
		return false
	}
	return true
}

// loadTimestampAllowlist reads the closed historical allowlist: one
// repo-relative migration path per line, `#` comments allowed.
func loadTimestampAllowlist(root string, r *report.Report) map[string]bool {
	path := filepath.Join(root, filepath.FromSlash(timestampConventionAllow))
	body, err := os.ReadFile(path)
	if err != nil {
		timestampViolation(r, timestampConventionAllow, 0, "timestamp_allowlist_unreadable", err.Error())
		return map[string]bool{}
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}

// timestampViolation emits one SeverityError violation under this rule.
func timestampViolation(r *report.Report, file string, line int, matched, note string) {
	r.Violations = append(r.Violations, report.Violation{
		Package:     "migrations",
		File:        file,
		Line:        line,
		Rule:        timestampConventionRule,
		Severity:    string(report.SeverityError),
		MatchedRule: matched,
		Note:        note + " (line " + strconv.Itoa(line) + ")",
	})
}
