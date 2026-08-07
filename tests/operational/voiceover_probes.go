// Package operational — voiceover_probes.go
//
// DB probe surface of the voiceover smoke harness: the 4 Probe* methods
// (Jobs / Voiceovers / MediaAssets / OutboxEvents) plus the sqlite3 CLI
// shell-out that executes them. Extracted from voiceover_harness.go on
// 2026-08-07 to satisfy the archcheck-strict 600-line cap
// (architecture/policy.yaml#max_lines_per_file_strict).
//
// The shell-out contract matches bash lib/common.sh::sqlite_q
// (`-separator '|'`). Every Probe* returns
// (nil, ErrSqliteBinaryMissing) when the sqlite3 binary is absent from
// PATH; callers MUST use errors.Is to distinguish that typed sentinel
// from a query that ran and returned zero rows.
package operational

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ProbeJobs runs `SELECT id, type, status, parent_id, created_at
// FROM jobs WHERE id = ? OR parent_id = ? ORDER BY created_at` and
// returns the pipe-separated rows (matches bash lib/common.sh::sqlite_q
// `-separator '|'` convention).
//
// Returns (nil, ErrSqliteBinaryMissing) if the sqlite3 binary is
// absent from PATH. Callers MUST use errors.Is to detect this case
// (NOT a nil-error + empty-slice check) so the typed-sentinel
// contract is preserved across future refactors.
func (h *VoiceoverHarness) ProbeJobs(jobOrParentID string) ([]string, error) {
	if h.sqliteBin == "" {
		return nil, ErrSqliteBinaryMissing
	}
	// Canonical: id matches OR parent_id matches. Bind the value as a
	// quoted SQL literal (defensive: caller-supplied values may contain
	// single quotes; sqlite3 has no parameter binding, so escape).
	q := fmt.Sprintf(
		`SELECT id, type, status, COALESCE(parent_id,''), created_at `+
			`FROM jobs WHERE id = '%s' OR parent_id = '%s' `+
			`ORDER BY created_at`,
		sqlEscape(jobOrParentID), sqlEscape(jobOrParentID),
	)
	return h.runSQLiteQuery(q, "jobs:"+jobOrParentID)
}

// ProbeVoiceovers runs `SELECT id, drive_file_id, status, language,
// parent_job_id FROM voiceovers WHERE parent_job_id = ?`.
//
// Returns (nil, ErrSqliteBinaryMissing) when sqlite3 is absent
// (see ProbeJobs for the typed-sentinel contract).
func (h *VoiceoverHarness) ProbeVoiceovers(parentID string) ([]string, error) {
	if h.sqliteBin == "" {
		return nil, ErrSqliteBinaryMissing
	}
	q := fmt.Sprintf(
		`SELECT id, COALESCE(drive_file_id,''), status, COALESCE(language,''), parent_job_id `+
			`FROM voiceovers WHERE parent_job_id = '%s'`,
		sqlEscape(parentID),
	)
	return h.runSQLiteQuery(q, "voiceovers:"+parentID)
}

// ProbeMediaAssets runs `SELECT id, drive_file_id, status, source_url
// FROM media_assets WHERE source_job_id = ?`.
//
// Returns (nil, ErrSqliteBinaryMissing) when sqlite3 is absent
// (see ProbeJobs for the typed-sentinel contract).
func (h *VoiceoverHarness) ProbeMediaAssets(parentID string) ([]string, error) {
	if h.sqliteBin == "" {
		return nil, ErrSqliteBinaryMissing
	}
	q := fmt.Sprintf(
		`SELECT id, COALESCE(drive_file_id,''), status, COALESCE(source_url,'') `+
			`FROM media_assets WHERE source_job_id = '%s'`,
		sqlEscape(parentID),
	)
	return h.runSQLiteQuery(q, "media_assets:"+parentID)
}

// ProbeOutboxEvents runs `SELECT id, event_type, status, payload
// FROM outbox_events WHERE source_job_id = ?`.
//
// Returns (nil, ErrSqliteBinaryMissing) when sqlite3 is absent
// (see ProbeJobs for the typed-sentinel contract).
func (h *VoiceoverHarness) ProbeOutboxEvents(parentID string) ([]string, error) {
	if h.sqliteBin == "" {
		return nil, ErrSqliteBinaryMissing
	}
	q := fmt.Sprintf(
		`SELECT id, event_type, status, COALESCE(payload,'') `+
			`FROM outbox_events WHERE source_job_id = '%s'`,
		sqlEscape(parentID),
	)
	return h.runSQLiteQuery(q, "outbox_events:"+parentID)
}

// runSQLiteQuery shells out to the sqlite3 binary and records the
// probe into the report. The first row is stored in the report as
// `Sample` (truncated to 200 chars; offline forensics) and the full
// row slice is returned to the caller for in-test assertions.
//
// SECURITY NOTE: the Sample preview may surface `drive_file_id` (from
// media_assets/voiceovers) and `payload` contents (from outbox_events).
// drive_file_id is a Google Drive resource ID — not secret-classified
// today, but operationally sensitive (anyone with the ID can access
// the file if the Drive ACL is permissive). The 200-char truncation
// limits the surface; operators with stricter PII requirements
// should review the generated report before sharing the JSON. The
// future `WithRedactedProbes` option will replace this with a
// column-driven allowlist (forward-pointer, not in this commit).
func (h *VoiceoverHarness) runSQLiteQuery(query, label string) ([]string, error) {
	cmd := exec.Command(h.sqliteBin, "-separator", "|", h.dbPath, query)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("voiceover harness: sqlite3 %s: %w (stderr: %s)",
			label, err, strings.TrimSpace(stderr.String()))
	}

	out := strings.TrimRight(stdout.String(), "\n")
	var rows []string
	if out != "" {
		rows = strings.Split(out, "\n")
	}

	h.recordDBProbe(label, query, rows)
	return rows, nil
}

// sqlEscape doubles single quotes for safe inlining into a sqlite3
// query string. Mirrors the defensive pattern in bash smokes
// (printf '%s' "$value" into a heredoc). NOT a full SQL-injection
// defence — these queries are operator-controlled, not user input.
func sqlEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
